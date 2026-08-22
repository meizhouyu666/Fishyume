package run

import (
	"context"
	"testing"
	"time"

	"wf.local/wf-engine/internal/store"
)

func TestM69SteadyStateReconciliationDoesNotGrowEventLog(t *testing.T) {
	backendImpl := &aggregateBackend{release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(backendImpl, state)

	cycles := make(chan struct{})
	advance := make(chan struct{})
	service.testHooks.idleReconcileDelay = func(ctx context.Context) error {
		select {
		case cycles <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-advance:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "steady-state"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("first steady-state reconciliation cycle did not start")
	}
	baseline := countRunEvents(t, state, started.ID)
	for cycle := 0; cycle < 64; cycle++ {
		advance <- struct{}{}
		select {
		case <-cycles:
		case <-time.After(3 * time.Second):
			t.Fatalf("steady-state reconciliation cycle %d did not start", cycle+1)
		}
		if got := countRunEvents(t, state, started.ID); got != baseline {
			t.Fatalf("unchanged active observation grew event log at cycle %d: baseline=%d current=%d", cycle+1, baseline, got)
		}
	}

	close(backendImpl.release)
	advance <- struct{}{}
	final := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("steady-state run=%+v", final)
	}
}
