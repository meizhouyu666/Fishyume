package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type runLeaseClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *runLeaseClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *runLeaseClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

func TestHeartbeatFailurePausesRecoversAndReconcilesWithoutDuplicateLaunch(t *testing.T) {
	state := store.New(t.TempDir())
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	service := newHeartbeatTestService(b, state, 60*time.Millisecond, "transient")
	service.testHooks.controllerRecoveryDelay = func(context.Context) error { return nil }

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, service, started.ID)
	state.SetFaultInjectorForTest(failOnce(controlLeaseWrite))
	waitForEventType(t, state, started.ID, "run.recovered")
	state.SetFaultInjectorForTest(nil)

	results <- backend.BackendResult{Status: "succeeded", Summary: "reconciled after heartbeat recovery"}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
	if got := countEventType(t, state, started.ID, "run.paused"); got != 1 {
		t.Fatalf("run.paused events = %d, want 1", got)
	}
	if got := countEventType(t, state, started.ID, "run.recovered"); got != 1 {
		t.Fatalf("run.recovered events = %d, want 1", got)
	}
}

func TestHeartbeatRecoveryRetriesLeaseAcquisition(t *testing.T) {
	state := store.New(t.TempDir())
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	service := newHeartbeatTestService(b, state, 60*time.Millisecond, "retry")
	service.testHooks.controllerRecoveryDelay = func(context.Context) error { return nil }

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, service, started.ID)
	var mu sync.Mutex
	stage := 0
	state.SetFaultInjectorForTest(func(operation, path string) error {
		mu.Lock()
		defer mu.Unlock()
		if stage == 0 && controlLeaseWrite(operation, path) {
			stage = 1
			return fmt.Errorf("heartbeat unavailable")
		}
		if stage == 1 && operation == "lease_acquire" {
			stage = 2
			return fmt.Errorf("recovery lease temporarily unavailable")
		}
		return nil
	})
	waitForEventType(t, state, started.ID, "run.recovered")
	state.SetFaultInjectorForTest(nil)

	results <- backend.BackendResult{Status: "succeeded", Summary: "recovered after lease retry"}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
}

func TestHeartbeatRecoveryRetriesRecoveredEventCommit(t *testing.T) {
	state := store.New(t.TempDir())
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	service := newHeartbeatTestService(b, state, 60*time.Millisecond, "event-retry")
	service.testHooks.controllerRecoveryDelay = func(context.Context) error { return nil }

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, service, started.ID)
	var mu sync.Mutex
	stage := 0
	state.SetFaultInjectorForTest(func(operation, path string) error {
		mu.Lock()
		defer mu.Unlock()
		if stage == 0 && controlLeaseWrite(operation, path) {
			stage = 1
			return fmt.Errorf("heartbeat unavailable")
		}
		if operation == "append_event" {
			switch stage {
			case 1:
				stage = 2 // Allow run.paused.
			case 2:
				stage = 3
				return fmt.Errorf("recovered event temporarily unavailable")
			}
		}
		return nil
	})
	waitForEventType(t, state, started.ID, "run.recovered")
	state.SetFaultInjectorForTest(nil)

	mu.Lock()
	finalStage := stage
	mu.Unlock()
	if finalStage != 3 {
		t.Fatalf("fault injector stage=%d, want 3", finalStage)
	}
	if got := countEventType(t, state, started.ID, "run.paused"); got != 1 {
		t.Fatalf("run.paused events = %d, want 1", got)
	}
	if got := countEventType(t, state, started.ID, "run.recovered"); got != 1 {
		t.Fatalf("run.recovered events = %d, want 1", got)
	}

	results <- backend.BackendResult{Status: "succeeded", Summary: "recovered after event retry"}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
}

func TestHeartbeatRecoveryRecreatesMissingLease(t *testing.T) {
	state := store.New(t.TempDir())
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	service := newHeartbeatTestService(b, state, 60*time.Millisecond, "missing")

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, service, started.ID)
	oldController := service.controller(started.ID)
	var mu sync.Mutex
	removed := false
	state.SetFaultInjectorForTest(func(operation, path string) error {
		mu.Lock()
		defer mu.Unlock()
		if operation == "lease_heartbeat" && !removed {
			removed = true
			return os.Remove(path)
		}
		return nil
	})
	waitForReplacementController(t, service, started.ID, oldController)
	state.SetFaultInjectorForTest(nil)

	results <- backend.BackendResult{Status: "succeeded", Summary: "recovered after missing lease"}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
}

func TestHeartbeatRecoverySurvivesPausePersistenceFaults(t *testing.T) {
	tests := []struct {
		name  string
		match func(string, string) bool
	}{
		{name: "run_snapshot", match: func(operation, path string) bool {
			return operation == "write_json" && filepath.Base(path) == "run.json"
		}},
		{name: "pause_event", match: func(operation, _ string) bool { return operation == "append_event" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := store.New(t.TempDir())
			results := make(chan backend.BackendResult, 1)
			b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
			service := newHeartbeatTestService(b, state, 60*time.Millisecond, "pause-fault")
			service.testHooks.controllerRecoveryDelay = func(context.Context) error { return nil }

			started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
			if err != nil {
				t.Fatal(err)
			}
			waitForActiveAttempt(t, service, started.ID)
			oldController := service.controller(started.ID)
			var mu sync.Mutex
			stage := 0
			state.SetFaultInjectorForTest(func(operation, path string) error {
				mu.Lock()
				defer mu.Unlock()
				if stage == 0 && controlLeaseWrite(operation, path) {
					stage = 1
					return fmt.Errorf("heartbeat unavailable")
				}
				if stage == 1 && test.match(operation, path) {
					stage = 2
					return fmt.Errorf("pause persistence unavailable")
				}
				return nil
			})
			waitForReplacementController(t, service, started.ID, oldController)
			state.SetFaultInjectorForTest(nil)

			results <- backend.BackendResult{Status: "succeeded", Summary: "recovered despite pause persistence fault"}
			final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
			if final.Conclusion != ConclusionSucceeded {
				t.Fatalf("final = %+v", final)
			}
			assertSingleBackendLaunch(t, b)
		})
	}
}

func TestHeartbeatRecoveryExhaustionLeavesExplicitPausedRunRecoverableAfterRestart(t *testing.T) {
	state := store.New(t.TempDir())
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	service := newHeartbeatTestService(b, state, 60*time.Millisecond, "exhaust")
	service.testHooks.controllerRecoveryDelay = func(context.Context) error { return nil }

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, service, started.ID)
	active := service.controller(started.ID)
	var mu sync.Mutex
	heartbeatFailed := false
	state.SetFaultInjectorForTest(func(operation, path string) error {
		mu.Lock()
		defer mu.Unlock()
		if !heartbeatFailed && controlLeaseWrite(operation, path) {
			heartbeatFailed = true
			return fmt.Errorf("heartbeat unavailable")
		}
		if heartbeatFailed && operation == "lease_acquire" {
			return fmt.Errorf("recovery lease unavailable")
		}
		return nil
	})
	select {
	case <-active.done:
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat recovery did not exhaust")
	}
	if active.err == nil || !strings.Contains(active.err.Error(), "recover controller after heartbeat failure") {
		t.Fatalf("controller error = %v", active.err)
	}
	paused, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Phase != PhasePaused || paused.Reason != ReasonControllerDetach {
		t.Fatalf("paused run = %+v", paused)
	}
	state.SetFaultInjectorForTest(nil)

	restarted := newHeartbeatTestService(b, state, time.Second, "restart")
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	results <- backend.BackendResult{Status: "succeeded", Summary: "recovered after restart"}
	final := waitForRun(t, restarted, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
}

func TestHeartbeatOwnershipLossHandsOffWithoutOverwritingReplacement(t *testing.T) {
	state := store.New(t.TempDir())
	clock := &runLeaseClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)}
	results := make(chan backend.BackendResult, 1)
	b := &fakeWorkflowBackend{waitBlock: true, waitReturn: results, observations: map[string][]backend.Observation{}}
	first := NewService(b, state)
	first.leases = store.NewLeaseManagerForTest(state, clock, 600*time.Millisecond, ownerSequence("first"))

	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForActiveAttempt(t, first, started.ID)
	oldController := first.controller(started.ID)
	clock.Advance(time.Second)

	second := NewService(b, state)
	second.leases = store.NewLeaseManagerForTest(state, clock, 600*time.Millisecond, ownerSequence("second"))
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldController.done:
	case <-time.After(3 * time.Second):
		t.Fatal("old controller did not observe replacement ownership")
	}
	newController := second.controller(started.ID)
	if newController == nil {
		t.Fatal("replacement controller is missing")
	}
	if owns, err := newController.lease.Owns(); err != nil || !owns {
		t.Fatalf("replacement ownership = %t, %v", owns, err)
	}
	if got := countEventType(t, state, started.ID, "run.paused"); got != 0 {
		t.Fatalf("old owner emitted %d pause events after handoff", got)
	}

	results <- backend.BackendResult{Status: "succeeded", Summary: "completed by replacement owner"}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final = %+v", final)
	}
	assertSingleBackendLaunch(t, b)
}

func newHeartbeatTestService(b backend.AgentBackend, state *store.Store, ttl time.Duration, ownerPrefix string) *Service {
	service := NewService(b, state)
	service.leases = store.NewLeaseManagerForTest(state, realRunLeaseClock{}, ttl, ownerSequence(ownerPrefix))
	return service
}

type realRunLeaseClock struct{}

func (realRunLeaseClock) Now() time.Time { return time.Now() }

func ownerSequence(prefix string) func() (string, error) {
	var mu sync.Mutex
	next := 0
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		next++
		return fmt.Sprintf("%s-%d", prefix, next), nil
	}
}

func controlLeaseWrite(operation, path string) bool {
	return operation == "write_json" && filepath.Base(path) == "control.lock"
}

func waitForActiveAttempt(t *testing.T, service *Service, runID string) {
	t.Helper()
	waitForRun(t, service, runID, func(run WorkflowSnapshot) bool {
		view, err := service.Status(run.ID)
		return err == nil && view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
}

func waitForEventType(t *testing.T, state *store.Store, runID, eventType string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countEventType(t, state, runID, eventType) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var snapshot WorkflowSnapshot
	_ = state.ReadSnapshot(runID, &snapshot)
	t.Fatalf("event %q was not persisted for run %s: phase=%q reason=%q summary=%q paused=%d recovered=%d", eventType, runID, snapshot.Phase, snapshot.Reason, snapshot.Summary, countEventType(t, state, runID, "run.paused"), countEventType(t, state, runID, "run.recovered"))
}

func waitForReplacementController(t *testing.T, service *Service, runID string, old *controller) *controller {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current := service.controller(runID)
		if current != nil && current != old {
			return current
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("replacement controller was not started for run %s", runID)
	return nil
}

func assertSingleBackendLaunch(t *testing.T, b *fakeWorkflowBackend) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.launches != 1 {
		t.Fatalf("backend launches = %d, want 1", b.launches)
	}
}
