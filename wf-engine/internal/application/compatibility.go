package application

import (
	"context"
	"strings"

	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/workflow"
)

// CompatibilityStatus keeps the protocol-v2 human CLI adapter behind the
// Application Service boundary while clients migrate to RunGet.
func (s *Service) CompatibilityStatus(runID string) (run.StatusView, *Error) {
	view, err := s.core.Status(runID)
	if err != nil {
		return run.StatusView{}, mapCoreError(err, "run not found")
	}
	return view, nil
}

func (s *Service) CompatibilityDetach(runID string) (run.WorkflowSnapshot, *Error) {
	snapshot, err := s.core.Detach(runID)
	if err != nil {
		return run.WorkflowSnapshot{}, mapCoreError(err, "could not detach run")
	}
	return snapshot, nil
}

func (s *Service) CompatibilityResume(ctx context.Context, request run.ResumeRequest) (run.WorkflowSnapshot, *Error) {
	snapshot, err := s.core.Resume(ctx, request)
	if err != nil {
		return run.WorkflowSnapshot{}, mapActionError(err)
	}
	return snapshot, nil
}

func (s *Service) CompatibilityWaitControllers(ctx context.Context) error {
	return s.core.WaitControllers(ctx)
}

func (s *Service) CompatibilityStartWorkflow(ctx context.Context, request run.StartWorkflowRequest, clientRequestID string) (RunStartResponse, *Error) {
	project := strings.TrimSpace(request.Project)
	if project == "" {
		return RunStartResponse{}, NewError(CodeInvalidArgument, "project is required", map[string]any{"path": "$.project"})
	}
	if appErr := validateExternalID("clientRequestId", clientRequestID); appErr != nil {
		return RunStartResponse{}, appErr
	}
	filename := strings.TrimSpace(request.Filename)
	if filename == "" {
		filename = "workflow.yaml"
	}
	normalized, err := workflow.Parse([]byte(request.Content), filename, request.Inputs)
	if err != nil {
		return RunStartResponse{}, NewError(CodeInvalidWorkflow, "workflow is invalid", map[string]any{"issues": []ValidationIssue{{Kind: "static", Path: "$", Code: classifyWorkflowError(err), Message: err.Error()}}})
	}
	driver := strings.TrimSpace(request.Driver)
	if driver == "" {
		driver = strings.TrimSpace(request.Backend)
		if driver == "direct" {
			driver = "codex"
		}
	}
	formal := RunStartRequest{Project: project, Workflow: WorkflowInput{Source: &WorkflowSource{Filename: filename, Content: request.Content}}, Inputs: request.Inputs, Driver: driver, Target: strings.TrimSpace(request.Target), ClientRequestID: clientRequestID}
	return s.runStartNormalized(ctx, formal, project, normalized)
}
