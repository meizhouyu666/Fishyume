package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestLeaseExclusionHeartbeatAndStaleTakeover(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-lease"); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)}
	owners := []string{"owner-a", "owner-b", "owner-c"}
	manager := NewLeaseManagerForTest(state, clock, 9*time.Second, func() (string, error) {
		owner := owners[0]
		owners = owners[1:]
		return owner, nil
	})
	first, err := manager.Acquire("run-lease", "resume")
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Acquire("run-lease", "cancel")
	var conflict *LeaseConflictError
	if !errors.As(err, &conflict) || conflict.Current.OwnerID != "owner-a" {
		t.Fatalf("conflict=%v", err)
	}
	clock.now = clock.now.Add(5 * time.Second)
	if err := first.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(10 * time.Second)
	second, err := manager.Acquire("run-lease", "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if second.Record().OwnerID != "owner-c" {
		t.Fatalf("takeover owner=%s", second.Record().OwnerID)
	}
	if err := first.Release(); err == nil {
		t.Fatal("stale owner released replacement lease")
	}
	if _, err := os.Stat(filepath.Join(state.RunDir("run-lease"), "control.lock")); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStaleTakeoverHasSingleOwner(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-race"); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Now().UTC()}
	initial := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return "initial", nil })
	if _, err := initial.Acquire("run-race", "run"); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(2 * time.Second)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := []*Lease{}
	for index := 0; index < 12; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return fmt.Sprintf("owner-%d", index), nil })
			lease, err := manager.Acquire("run-race", "resume")
			if err == nil {
				mu.Lock()
				successes = append(successes, lease)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(successes) != 1 {
		t.Fatalf("successful stale takeovers=%d, want 1", len(successes))
	}
	_ = successes[0].Release()
}

func TestLeaseCrashLikeAbandonmentAndWrongOwnerRelease(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-crash"); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Now().UTC()}
	managerA := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return "a", nil })
	leaseA, err := managerA.Acquire("run-crash", "run")
	if err != nil {
		t.Fatal(err)
	}
	wrong := &Lease{manager: managerA, runID: "run-crash", record: LeaseRecord{OwnerID: "wrong"}}
	if err := wrong.Release(); err == nil {
		t.Fatal("wrong owner released lease")
	}
	clock.now = clock.now.Add(2 * time.Second)
	managerB := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return "b", nil })
	leaseB, err := managerB.Acquire("run-crash", "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := leaseA.Heartbeat(); err == nil {
		t.Fatal("abandoned owner refreshed replacement lease")
	}
	if err := leaseB.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestOldHeartbeatCannotOverwriteOrReleaseReplacement(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-heartbeat-race"); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)}
	oldManager := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return "old", nil })
	oldLease, err := oldManager.Acquire("run-heartbeat-race", "run")
	if err != nil {
		t.Fatal(err)
	}

	heartbeatRead := make(chan struct{})
	continueHeartbeat := make(chan struct{})
	oldManager.beforeHeartbeatGuard = func() {
		close(heartbeatRead)
		<-continueHeartbeat
	}
	heartbeatDone := make(chan error, 1)
	go func() { heartbeatDone <- oldLease.Heartbeat() }()
	<-heartbeatRead

	clock.now = clock.now.Add(2 * time.Second)
	newManager := NewLeaseManagerForTest(state, clock, time.Second, func() (string, error) { return "replacement", nil })
	replacement, err := newManager.Acquire("run-heartbeat-race", "resume")
	if err != nil {
		t.Fatal(err)
	}
	close(continueHeartbeat)
	if err := <-heartbeatDone; err == nil {
		t.Fatal("old heartbeat overwrote replacement lease")
	}

	path := filepath.Join(state.RunDir("run-heartbeat-race"), "control.lock")
	record, err := readLease(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.OwnerID != "replacement" {
		t.Fatalf("owner after stale heartbeat=%q, want replacement", record.OwnerID)
	}
	if err := oldLease.Release(); err == nil {
		t.Fatal("old owner deleted replacement lease")
	}
	record, err = readLease(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.OwnerID != "replacement" {
		t.Fatalf("owner after stale release=%q, want replacement", record.OwnerID)
	}
	if err := replacement.Release(); err != nil {
		t.Fatal(err)
	}
}
