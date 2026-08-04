package run

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type blockingBackend struct{ cancelCalls atomic.Int32 }

func (*blockingBackend) Name() string                 { return "ccpanes" }
func (*blockingBackend) Doctor(context.Context) error { return nil }
func (*blockingBackend) Launch(context.Context, backend.LaunchSpec) (*backend.Session, error) {
	return &backend.Session{ID: "own-session"}, nil
}

type ownershipBackend struct {
	mu                sync.Mutex
	cancelAttempts    []string
	successfulKills   []string
	cancelFailures    int
	cancelEntered     chan struct{}
	cancelEnteredOnce sync.Once
	cancelRelease     chan struct{}
	launchEntered     chan struct{}
	launchEnteredOnce sync.Once
	launchRelease     chan struct{}
	waitCalls         int
}

func (*ownershipBackend) Name() string                 { return "ccpanes" }
func (*ownershipBackend) Doctor(context.Context) error { return nil }
func (b *ownershipBackend) Launch(_ context.Context, spec backend.LaunchSpec) (*backend.Session, error) {
	if b.launchEntered != nil {
		b.launchEnteredOnce.Do(func() { close(b.launchEntered) })
	}
	if b.launchRelease != nil {
		<-b.launchRelease
	}
	return &backend.Session{ID: "session-" + spec.RunID}, nil
}
func (b *ownershipBackend) Wait(ctx context.Context, _ backend.Session) (*backend.BackendResult, error) {
	b.mu.Lock()
	b.waitCalls++
	b.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*ownershipBackend) Output(context.Context, backend.Session, int) (string, error) {
	return "", nil
}
func (b *ownershipBackend) Cancel(_ context.Context, session backend.Session) error {
	b.mu.Lock()
	b.cancelAttempts = append(b.cancelAttempts, session.ID)
	shouldFail := b.cancelFailures > 0
	if shouldFail {
		b.cancelFailures--
	}
	entered := b.cancelEntered
	release := b.cancelRelease
	b.mu.Unlock()
	if entered != nil {
		b.cancelEnteredOnce.Do(func() { close(entered) })
	}
	if release != nil {
		<-release
	}
	if shouldFail {
		return fmt.Errorf("fixture kill failed")
	}
	b.mu.Lock()
	b.successfulKills = append(b.successfulKills, session.ID)
	b.mu.Unlock()
	return nil
}

func (b *ownershipBackend) cancelledSessions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.successfulKills...)
}

func (b *ownershipBackend) attemptedSessions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.cancelAttempts...)
}

type deterministicGate struct {
	entered chan struct{}
	release chan struct{}
}

type killWaitBackend struct {
	waitEntered chan struct{}
	waitRelease chan struct{}
	killEntered chan struct{}
	killRelease chan struct{}
	cancelCalls atomic.Int32
}

func (*killWaitBackend) Name() string                 { return "ccpanes" }
func (*killWaitBackend) Doctor(context.Context) error { return nil }
func (*killWaitBackend) Launch(_ context.Context, spec backend.LaunchSpec) (*backend.Session, error) {
	return &backend.Session{ID: "session-" + spec.RunID}, nil
}
func (b *killWaitBackend) Wait(context.Context, backend.Session) (*backend.BackendResult, error) {
	close(b.waitEntered)
	<-b.waitRelease
	return nil, fmt.Errorf("session wait ended after kill began")
}
func (*killWaitBackend) Output(context.Context, backend.Session, int) (string, error) {
	return "", nil
}
func (b *killWaitBackend) Cancel(context.Context, backend.Session) error {
	b.cancelCalls.Add(1)
	close(b.killEntered)
	close(b.waitRelease)
	<-b.killRelease
	return nil
}

func newDeterministicGate() *deterministicGate {
	return &deterministicGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *deterministicGate) wait() {
	close(g.entered)
	<-g.release
}

func receive(t *testing.T, channel <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestControlStateWinsBeforeDispatchTransition(t *testing.T) {
	for _, control := range []string{"detach", "cancel"} {
		t.Run(control, func(t *testing.T) {
			b := &ownershipBackend{}
			gate := newDeterministicGate()
			done := make(chan struct{})
			service := NewService(b, store.New(t.TempDir()))
			service.hooks.beforeDispatch = gate.wait
			service.hooks.afterExecute = func() { close(done) }
			started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
			if err != nil {
				t.Fatal(err)
			}
			receive(t, gate.entered, "before-dispatch gate")
			if control == "detach" {
				if _, err := service.Detach(started.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := service.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
			}
			close(gate.release)
			receive(t, done, "execute completion")
			final, err := service.Get(started.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := RunPaused
			if control == "cancel" {
				want = RunCancelled
			}
			if final.Status != want {
				t.Fatalf("final status = %s, want %s", final.Status, want)
			}
			if kills := b.cancelledSessions(); len(kills) != 0 {
				t.Fatalf("pre-dispatch %s killed sessions: %v", control, kills)
			}
		})
	}
}

func TestControlStateWinsAfterLaunchBeforeRunningTransition(t *testing.T) {
	for _, control := range []string{"detach", "cancel"} {
		t.Run(control, func(t *testing.T) {
			b := &ownershipBackend{}
			gate := newDeterministicGate()
			done := make(chan struct{})
			service := NewService(b, store.New(t.TempDir()))
			service.hooks.beforeRunning = gate.wait
			service.hooks.afterExecute = func() { close(done) }
			started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
			if err != nil {
				t.Fatal(err)
			}
			receive(t, gate.entered, "before-running gate")
			if control == "detach" {
				if _, err := service.Detach(started.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := service.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := service.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
			}
			close(gate.release)
			receive(t, done, "execute completion")
			final, err := service.Get(started.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := RunPaused
			if control == "cancel" {
				want = RunCancelled
			}
			if final.Status != want {
				t.Fatalf("final status = %s, want %s", final.Status, want)
			}
			kills := b.cancelledSessions()
			if control == "detach" {
				if len(kills) != 0 {
					t.Fatalf("detach killed sessions: %v", kills)
				}
			} else {
				wantSession := fmt.Sprintf("session-%s", started.ID)
				if len(kills) != 1 || kills[0] != wantSession {
					t.Fatalf("cancelled sessions = %v, want [%s]", kills, wantSession)
				}
			}
		})
	}
}

func TestCancelKillFailureRemainsTruthfulAndRetryable(t *testing.T) {
	b := &ownershipBackend{cancelFailures: 1}
	gate := newDeterministicGate()
	done := make(chan struct{})
	service := NewService(b, store.New(t.TempDir()))
	service.hooks.beforeRunning = gate.wait
	service.hooks.afterExecute = func() { close(done) }
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, gate.entered, "before-running gate")
	failedSnapshot, err := service.Cancel(context.Background(), started.ID)
	if err == nil {
		t.Fatal("expected first kill to fail")
	}
	if failedSnapshot.Status == RunCancelled {
		t.Fatalf("failed kill produced cancelled snapshot: %+v", failedSnapshot)
	}
	if kills := b.cancelledSessions(); len(kills) != 0 {
		t.Fatalf("failed kill recorded success: %v", kills)
	}
	retried, err := service.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != RunCancelled {
		t.Fatalf("retry status = %s, want cancelled", retried.Status)
	}
	if _, err := service.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	wantSession := "session-" + started.ID
	if attempts := b.attemptedSessions(); len(attempts) != 2 || attempts[0] != wantSession || attempts[1] != wantSession {
		t.Fatalf("kill attempts = %v, want two retries for %s", attempts, wantSession)
	}
	if kills := b.cancelledSessions(); len(kills) != 1 || kills[0] != wantSession {
		t.Fatalf("successful kills = %v, want [%s]", kills, wantSession)
	}
	close(gate.release)
	receive(t, done, "execute completion")
	final, _ := service.Get(started.ID)
	if final.Status != RunCancelled {
		t.Fatalf("execute overwrote retry cancellation: %s", final.Status)
	}
}

func TestConcurrentCancelSharesSingleSuccessfulKill(t *testing.T) {
	b := &ownershipBackend{cancelEntered: make(chan struct{}), cancelRelease: make(chan struct{})}
	gate := newDeterministicGate()
	done := make(chan struct{})
	waiting := make(chan struct{})
	var waitOnce sync.Once
	service := NewService(b, store.New(t.TempDir()))
	service.hooks.beforeRunning = gate.wait
	service.hooks.afterExecute = func() { close(done) }
	service.hooks.onCancelWait = func() { waitOnce.Do(func() { close(waiting) }) }
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, gate.entered, "before-running gate")
	type cancelResult struct {
		snapshot RunSnapshot
		err      error
	}
	first := make(chan cancelResult, 1)
	second := make(chan cancelResult, 1)
	go func() {
		snapshot, cancelErr := service.Cancel(context.Background(), started.ID)
		first <- cancelResult{snapshot: snapshot, err: cancelErr}
	}()
	receive(t, b.cancelEntered, "first backend kill")
	go func() {
		snapshot, cancelErr := service.Cancel(context.Background(), started.ID)
		second <- cancelResult{snapshot: snapshot, err: cancelErr}
	}()
	receive(t, waiting, "second cancel waiting on in-flight kill")
	close(b.cancelRelease)
	for index, resultChannel := range []<-chan cancelResult{first, second} {
		result := <-resultChannel
		if result.err != nil || result.snapshot.Status != RunCancelled {
			t.Fatalf("cancel result %d = %+v, err=%v", index, result.snapshot, result.err)
		}
	}
	close(gate.release)
	receive(t, done, "execute completion")
	if attempts := b.attemptedSessions(); len(attempts) != 1 {
		t.Fatalf("backend kill attempts = %v, want exactly one", attempts)
	}
	if kills := b.cancelledSessions(); len(kills) != 1 {
		t.Fatalf("successful kills = %v, want exactly one", kills)
	}
}

func TestLateSessionKillFailureCanBeRetried(t *testing.T) {
	b := &ownershipBackend{
		cancelFailures: 1,
		launchEntered:  make(chan struct{}),
		launchRelease:  make(chan struct{}),
	}
	done := make(chan struct{})
	waiting := make(chan struct{})
	var waitOnce sync.Once
	service := NewService(b, store.New(t.TempDir()))
	service.hooks.afterExecute = func() { close(done) }
	service.hooks.onCancelWait = func() { waitOnce.Do(func() { close(waiting) }) }
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, b.launchEntered, "launch entry")
	first := make(chan error, 1)
	go func() {
		_, cancelErr := service.Cancel(context.Background(), started.ID)
		first <- cancelErr
	}()
	receive(t, waiting, "cancel waiting for late session")
	close(b.launchRelease)
	if err := <-first; err == nil {
		t.Fatal("expected late-session kill failure")
	}
	receive(t, done, "execute completion")
	afterFailure, _ := service.Get(started.ID)
	if afterFailure.Status == RunCancelled {
		t.Fatalf("late kill failure claimed cancellation: %+v", afterFailure)
	}
	retried, err := service.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != RunCancelled {
		t.Fatalf("retry status = %s, want cancelled", retried.Status)
	}
	wantSession := "session-" + started.ID
	if attempts := b.attemptedSessions(); len(attempts) != 2 {
		t.Fatalf("late-session attempts = %v, want two", attempts)
	}
	if kills := b.cancelledSessions(); len(kills) != 1 || kills[0] != wantSession {
		t.Fatalf("late-session successful kills = %v, want [%s]", kills, wantSession)
	}
}

func TestKillInFlightSuppressesWaitFailureEvent(t *testing.T) {
	b := &killWaitBackend{
		waitEntered: make(chan struct{}), waitRelease: make(chan struct{}),
		killEntered: make(chan struct{}), killRelease: make(chan struct{}),
	}
	executeDone := make(chan struct{})
	service := NewService(b, store.New(t.TempDir()))
	service.hooks.afterExecute = func() { close(executeDone) }
	var eventsMu sync.Mutex
	var events []RunEvent
	service.SetEventSink(func(event RunEvent) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	})
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, b.waitEntered, "backend wait entry")
	type cancelResult struct {
		snapshot RunSnapshot
		err      error
	}
	result := make(chan cancelResult, 1)
	go func() {
		snapshot, cancelErr := service.Cancel(context.Background(), started.ID)
		result <- cancelResult{snapshot: snapshot, err: cancelErr}
	}()
	receive(t, b.killEntered, "backend kill entry")
	receive(t, executeDone, "execute exit while kill is in flight")
	eventsMu.Lock()
	beforeKillEvents := append([]RunEvent(nil), events...)
	eventsMu.Unlock()
	for _, event := range beforeKillEvents {
		if event.Status.Terminal() {
			t.Fatalf("execute emitted terminal event while kill was in flight: %+v", beforeKillEvents)
		}
	}
	beforeKillCompletion, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeKillCompletion.Status != RunRunning {
		t.Fatalf("kill in-flight snapshot = %s, want running until kill succeeds", beforeKillCompletion.Status)
	}
	close(b.killRelease)
	cancelled := <-result
	if cancelled.err != nil || cancelled.snapshot.Status != RunCancelled {
		t.Fatalf("cancel result = %+v, err=%v", cancelled.snapshot, cancelled.err)
	}
	final, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != RunCancelled || final.NodeStatus != NodeCancelled {
		t.Fatalf("final snapshot = %+v, want cancelled", final)
	}
	eventsMu.Lock()
	finalEvents := append([]RunEvent(nil), events...)
	eventsMu.Unlock()
	var terminalEvents []RunEvent
	for _, event := range finalEvents {
		if event.Status.Terminal() {
			terminalEvents = append(terminalEvents, event)
		}
	}
	if len(terminalEvents) != 1 || terminalEvents[0].Status != RunCancelled || terminalEvents[0].Type != "run.cancelled" {
		t.Fatalf("terminal events = %+v, all events=%+v", terminalEvents, finalEvents)
	}
	if b.cancelCalls.Load() != 1 {
		t.Fatalf("backend cancel calls = %d, want 1", b.cancelCalls.Load())
	}
}
func (*blockingBackend) Wait(ctx context.Context, _ backend.Session) (*backend.BackendResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*blockingBackend) Output(context.Context, backend.Session, int) (string, error) { return "", nil }
func (b *blockingBackend) Cancel(context.Context, backend.Session) error {
	b.cancelCalls.Add(1)
	return nil
}

func waitRunning(t *testing.T, service *Service, runID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := service.Get(runID)
		if snapshot.Status == RunRunning {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run did not reach running")
}

func TestDetachDoesNotCancelBackendSession(t *testing.T) {
	b := &blockingBackend{}
	service := NewService(b, store.New(t.TempDir()))
	snapshot, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, service, snapshot.ID)
	paused, err := service.Detach(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != RunPaused || b.cancelCalls.Load() != 0 {
		t.Fatalf("detach result=%s cancelCalls=%d", paused.Status, b.cancelCalls.Load())
	}
}

func TestCancelInvokesBackendSessionKill(t *testing.T) {
	b := &blockingBackend{}
	service := NewService(b, store.New(t.TempDir()))
	snapshot, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, service, snapshot.ID)
	cancelled, err := service.Cancel(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != RunCancelled || b.cancelCalls.Load() != 1 {
		t.Fatalf("cancel result=%s cancelCalls=%d", cancelled.Status, b.cancelCalls.Load())
	}
}

func TestCancelAfterDetachStillKillsOwnedSession(t *testing.T) {
	b := &blockingBackend{}
	service := NewService(b, store.New(t.TempDir()))
	snapshot, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, service, snapshot.ID)
	if _, err := service.Detach(snapshot.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.Cancel(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != RunCancelled || b.cancelCalls.Load() != 1 {
		t.Fatalf("cancel result=%s cancelCalls=%d", cancelled.Status, b.cancelCalls.Load())
	}
}
