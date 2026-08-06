package run

import (
	"context"
	"fmt"
	"sync"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/workflow"
)

type launchOutcome struct {
	launch pendingLaunch
	handle *backend.ExecutionHandle
	err    error
}

func (s *Service) scheduleBatch(ctx context.Context, runID string, generation uint64, normalized workflow.Normalized) (bool, bool, error) {
	var launches []pendingLaunch
	progressed, stop := false, false
	point := "node.schedule_batch"
	if _, nodes, err := s.loadRun(runID); err == nil {
		for _, nodeID := range normalized.TopologicalOrder {
			for _, node := range nodes {
				if node.ID == nodeID && (node.Phase == NodePhasePending || node.Phase == NodePhaseReady) {
					if normalized.Document.Nodes[nodeID].Type == "approval" {
						point = "approval.waiting"
					} else {
						point = "agent.prelaunch"
					}
					break
				}
			}
			if point != "node.schedule_batch" {
				break
			}
		}
	}
	err := s.controllerMutation(runID, generation, point, func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		candidate, err := s.registry.Get(runDriver(*run))
		if err != nil {
			return err
		}
		backendLimit, err := backendConcurrencyLimit(candidate)
		if err != nil {
			return err
		}
		effective, err := EffectiveConcurrency(normalized.Document.Execution.MaxConcurrency, backendLimit)
		if err != nil {
			return err
		}
		if run.EffectiveConcurrency != effective {
			run.EffectiveConcurrency = effective
		}
		decision, err := PlanSchedule(normalized, *run, nodes, backendLimit)
		if err != nil {
			return err
		}
		for _, nodeID := range decision.SkipUpstream {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseSkipped, ReasonUpstreamFailed, "upstream failed", s.now().UTC()
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.skipped", node.Diagnostic); err != nil {
				return err
			}
			progressed = true
		}
		for _, nodeID := range decision.SkipConditions {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseSkipped, ReasonConditionFalse, "condition evaluated false", s.now().UTC()
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.skipped", node.Diagnostic); err != nil {
				return err
			}
			progressed = true
		}
		for _, nodeID := range decision.SkipFailurePolicy {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseSkipped, ReasonFailurePolicy, "not scheduled after workflow failure", s.now().UTC()
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.skipped", node.Diagnostic); err != nil {
				return err
			}
			progressed = true
		}
		results := make(map[string]workflow.Result)
		for _, node := range nodes {
			if node.Result != nil {
				results[node.ID] = *node.Result
			}
		}
		for _, nodeID := range decision.ReadyApprovals {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			definition := normalized.Document.Nodes[nodeID]
			template, err := workflow.ParseTemplate(definition.Prompt, normalized.Document.Inputs, ancestorSet(normalized.Document, node.ID))
			if err != nil {
				return err
			}
			prompt, err := template.Render(normalized.Inputs, results)
			if err != nil {
				return err
			}
			now := s.now().UTC()
			node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseWaiting, ReasonApprovalRequired, prompt, now
			run.Summary = prompt
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.approval_required", prompt); err != nil {
				return err
			}
			progressed = true
		}
		for _, nodeID := range decision.ReadyAgents {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			definition := normalized.Document.Nodes[nodeID]
			template, err := workflow.ParseTemplate(definition.Task, normalized.Document.Inputs, ancestorSet(normalized.Document, node.ID))
			if err != nil {
				return err
			}
			renderedTask, err := template.Render(normalized.Inputs, results)
			if err != nil {
				return err
			}
			if len([]byte(renderedTask)) > workflow.MaxPromptBytes {
				return fmt.Errorf("rendered prompt exceeds %d bytes", workflow.MaxPromptBytes)
			}
			driver, target, err := workflow.ResolveAgent(normalized.Document.Defaults, definition)
			if err != nil {
				return err
			}
			number, now := node.CurrentAttempt+1, s.now().UTC()
			ancestorResults := make(map[string]workflow.Result)
			for ancestorID := range ancestorSet(normalized.Document, node.ID) {
				if result, ok := results[ancestorID]; ok {
					ancestorResults[ancestorID] = result
				}
			}
			compiled, err := contextcompiler.Compile(contextcompiler.Input{
				Identity: agent.AttemptIdentity{RunID: run.ID, NodeID: node.ID, Attempt: number}, Workspace: run.Project, Target: target, Task: renderedTask,
				AncestorResults: ancestorResults, RequiredSkills: definition.RequiredSkills,
				Constraints: map[string]string{"interaction": "none", "processMode": "one-shot", "pty": "disabled"}, Budget: map[string]int64{},
				ResultSchema: agentResultContractSchema(), ResultMaxBytes: workflow.MaxResultBytes,
			})
			if err != nil {
				return err
			}
			attempt := AttemptSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: run.ID, NodeID: node.ID, Number: number, Phase: NodePhaseRunning, LaunchState: LaunchPrepared,
				ResolvedDriver: driver, ResolvedTarget: target, Backend: driver, ContextCompilerVersion: compiled.Manifest.CompilerVersion,
				ContextManifest: compiled.Manifest, ContextHash: compiled.Hash, PromptHash: compiled.Hash, StartedAt: now, UpdatedAt: now}
			if err := s.writeAttempt(attempt, true); err != nil {
				return err
			}
			node.Phase, node.Reason, node.Diagnostic, node.Conclusion, node.CurrentAttempt, node.UpdatedAt = NodePhaseRunning, "", "", "", number, now
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			run.Phase, run.Reason, run.Conclusion, run.Summary, run.UpdatedAt = PhaseRunning, "", "", "launching Agents", now
			if err := s.persistRun(run, node, "node.running", "launching Agent attempt"); err != nil {
				return err
			}
			launches = append(launches, pendingLaunch{runID: run.ID, nodeID: node.ID, attempt: number, backend: driver,
				launchSpec: backend.AgentExecutionSpec{RunID: run.ID, NodeID: node.ID, Attempt: number, Workspace: run.Project, Tool: legacyToolForDriver(s.registry, driver), Runtime: target,
					Instructions: compiled.Prompt, RequiredSkills: append([]string(nil), definition.RequiredSkills...), ResultContract: backend.ResultContract{Schema: compiled.Envelope.ResultContract.Schema, MaxBytes: workflow.MaxResultBytes}, Envelope: &compiled.Envelope}})
			progressed = true
		}
		if len(launches) == 0 {
			before := *run
			eventType, shouldStop := aggregateRunState(run, nodes, normalized.Document, s.now().UTC())
			if eventType != "" && aggregateRunStateChanged(before, *run) {
				if err := s.persistRun(run, nil, eventType, run.Summary); err != nil {
					return err
				}
			}
			stop = shouldStop
		}
		return nil
	})
	if err != nil {
		return false, true, err
	}
	if len(launches) == 0 {
		return progressed, stop, nil
	}
	if err := s.startPendingLaunches(ctx, generation, launches); err != nil {
		return progressed, true, err
	}
	return true, false, nil
}

func aggregateConclusion(nodes []NodeSnapshot, doc workflow.Document) (Conclusion, Reason) {
	conclusion, reason := ConclusionSucceeded, Reason("")
	for _, node := range nodes {
		if node.Conclusion == ConclusionFailed {
			return ConclusionFailed, ReasonUpstreamFailed
		}
		if node.Conclusion == ConclusionIndeterminate {
			conclusion = ConclusionIndeterminate
		}
		if node.Conclusion == ConclusionRejected && conclusion == ConclusionSucceeded {
			conclusion = ConclusionRejected
		}
	}
	if conclusion == ConclusionRejected && hasEligibleRejectedBranch(doc, nodes) {
		return ConclusionSucceeded, ""
	}
	return conclusion, reason
}

func (s *Service) startPendingLaunches(ctx context.Context, generation uint64, launches []pendingLaunch) error {
	outcomes := make([]launchOutcome, len(launches))
	var group sync.WaitGroup
	for index, launch := range launches {
		group.Add(1)
		go func(index int, launch pendingLaunch) {
			defer group.Done()
			outcomes[index] = s.startPendingLaunch(ctx, generation, launch)
		}(index, launch)
	}
	group.Wait()
	for _, outcome := range outcomes {
		if outcome.err != nil {
			if outcome.handle != nil && outcome.handle.ID != "" {
				if err := s.waiting(outcome.launch.runID, outcome.launch.nodeID, outcome.launch.attempt, generation, ReasonCompletionMissing, "Agent execution started but Backend post-start setup failed: "+outcome.err.Error()); err != nil {
					return err
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := s.finishIndeterminate(outcome.launch.runID, outcome.launch.nodeID, outcome.launch.attempt, generation, "Backend launch outcome is unknown: "+outcome.err.Error()); err != nil {
				return err
			}
			continue
		}
		if outcome.handle == nil || outcome.handle.ID == "" {
			if err := s.finishIndeterminate(outcome.launch.runID, outcome.launch.nodeID, outcome.launch.attempt, generation, "Backend launch returned no execution handle"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) startPendingLaunch(ctx context.Context, generation uint64, launch pendingLaunch) launchOutcome {
	outcome := launchOutcome{launch: launch}
	if err := s.beginBackendLaunch(launch, generation); err != nil {
		outcome.err = err
		return outcome
	}
	candidate, err := s.registry.Get(launch.backend)
	if err != nil {
		outcome.err = err
		return outcome
	}
	handle, startErr := candidate.Start(ctx, launch.launchSpec)
	if hook := s.testHooks.afterLaunch; hook != nil {
		hook()
	}
	outcome.handle, outcome.err = handle, startErr
	if handle != nil && handle.ID != "" {
		if err := s.persistExecutionHandle(launch, *handle); err != nil {
			outcome.err = errorsJoin(outcome.err, err)
		}
	} else if err := s.markLaunchFinishedWithoutHandle(launch); err != nil {
		outcome.err = errorsJoin(outcome.err, err)
	}
	return outcome
}

func errorsJoin(values ...error) error {
	var result error
	for _, value := range values {
		if value == nil {
			continue
		}
		if result == nil {
			result = value
		} else {
			result = fmt.Errorf("%v; %w", result, value)
		}
	}
	return result
}
