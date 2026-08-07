package store

import (
	"os"
	"sync"
	"testing"
	"time"
)

func TestCancellationRequestIsAtomicAndIdempotent(t *testing.T) {
	state := New(t.TempDir())
	const runID = "run-cancel-request"
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	requests := make(chan CancelRequest, 8)
	errorsChannel := make(chan error, 8)
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			request, err := state.RequestCancellation(runID, time.Unix(10, 0))
			requests <- request
			errorsChannel <- err
		}()
	}
	group.Wait()
	close(requests)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for request := range requests {
		if id == "" {
			id = request.ID
		}
		if request.ID != id {
			t.Fatalf("request ids differ: %q and %q", id, request.ID)
		}
	}
}

func TestCancellationResponseIsNotVisibleBeforeRequestCleanup(t *testing.T) {
	state := New(t.TempDir())
	const runID = "run-cancel-response-visibility"
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	request, err := state.RequestCancellation(runID, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	release := make(chan struct{})
	resolved := make(chan error, 1)
	go func() {
		resolved <- withLeaseGuard(state.CancelRequestPath(runID), func() error {
			response := CancelResponse{RequestID: request.ID, Status: CancelResponseCompleted, UpdatedAt: time.Unix(11, 0)}
			if err := state.writeJSON(state.CancelResponsePath(runID), response); err != nil {
				return err
			}
			close(written)
			<-release
			return os.Remove(state.CancelRequestPath(runID))
		})
	}()
	<-written

	readStarted := make(chan struct{})
	read := make(chan error, 1)
	go func() {
		close(readStarted)
		_, err := state.ReadCancellationResponse(runID, request.ID)
		read <- err
	}()
	<-readStarted
	select {
	case err := <-read:
		t.Fatalf("response became visible before request cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-resolved; err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state.CancelRequestPath(runID)); !os.IsNotExist(err) {
		t.Fatalf("resolved request still exists: %v", err)
	}
}

func TestCancellationResponseCleansRequestAndRetryReplacesStaleResponse(t *testing.T) {
	state := New(t.TempDir())
	const runID = "run-cancel-retry"
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	first, err := state.RequestCancellation(runID, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	response := CancelResponse{RequestID: first.ID, Status: CancelResponseFailed, Message: "kill failed", UpdatedAt: time.Unix(11, 0)}
	if err := state.ResolveCancellation(runID, response); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state.CancelRequestPath(runID)); !os.IsNotExist(err) {
		t.Fatalf("resolved request still exists: %v", err)
	}
	if stored, err := state.ReadCancellationResponse(runID, first.ID); err != nil || stored.Status != CancelResponseFailed {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	second, err := state.RequestCancellation(runID, time.Unix(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("retry reused resolved request id %q", first.ID)
	}
	if _, err := os.Stat(state.CancelResponsePath(runID)); !os.IsNotExist(err) {
		t.Fatalf("stale response was not removed for retry: %v", err)
	}
}
