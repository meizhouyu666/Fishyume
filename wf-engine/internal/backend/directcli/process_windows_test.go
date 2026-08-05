//go:build windows

package directcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"wf.local/wf-engine/internal/backend"
)

func TestWindowsAccessDeniedIsAnUntrustedProcessIdentity(t *testing.T) {
	identity, exists, err := inaccessibleProcessIdentity(windows.ERROR_ACCESS_DENIED)
	if err != nil || !exists || identity.Fingerprint != "inaccessible" {
		t.Fatalf("identity=%+v exists=%t err=%v", identity, exists, err)
	}
	_, exists, err = inaccessibleProcessIdentity(errors.New("fixture"))
	if err == nil || exists {
		t.Fatalf("unexpected non-access error classification: exists=%t err=%v", exists, err)
	}
}

func TestWindowsSharingViolationsAreTransient(t *testing.T) {
	for _, err := range []error{
		&os.PathError{Op: "open", Path: "ready.json", Err: windows.ERROR_SHARING_VIOLATION},
		&os.PathError{Op: "open", Path: "ready.json", Err: windows.ERROR_LOCK_VIOLATION},
		&os.PathError{Op: "open", Path: "ready.json", Err: windows.ERROR_ACCESS_DENIED},
	} {
		if !isTransientFileAccess(err) {
			t.Fatalf("expected transient file access: %v", err)
		}
	}
	if isTransientFileAccess(os.ErrNotExist) {
		t.Fatal("missing file is not a Windows sharing violation")
	}
}

func TestWindowsLockedExitRecordRemainsResultPending(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "terminal-succeeded")
	_, paths, err := candidate.decodeHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(paths.Exit)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var locked windows.Handle
	for {
		locked, err = windows.CreateFile(path, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock Direct exit record: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		if locked != 0 {
			_ = windows.CloseHandle(locked)
		}
	}()

	observation, err := candidate.Observe(context.Background(), handle)
	if err != nil || observation.State != backend.ObservationResultPending || observation.Result != nil || !strings.Contains(observation.Diagnostic, "temporarily inaccessible") {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if err := windows.CloseHandle(locked); err != nil {
		t.Fatal(err)
	}
	locked = 0
	awaitObservation(t, candidate, handle, func(value *backend.ExecutionObservation) bool {
		return value.State == backend.ObservationTerminal && value.Result != nil && value.Result.Status == "succeeded"
	})
}

func TestWindowsWaitReadyRetriesSharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct-ready.json")
	want := readyRecord{
		ExecutionID: "direct-fixture",
		Child:       processRef{PID: 42, Fingerprint: "fixture", Executable: `C:\fixture\codex.exe`},
		StartedAt:   time.Now().UTC(),
	}
	if err := writeJSONAtomic(path, want); err != nil {
		t.Fatal(err)
	}
	windowsPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := windows.CreateFile(windowsPath, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = windows.CloseHandle(locked)
		close(released)
	}()

	candidate := New(Config{StateRoot: t.TempDir(), StartupTimeout: time.Second, PollInterval: 5 * time.Millisecond})
	got, err := candidate.waitReady(context.Background(), path, want.ExecutionID)
	<-released
	if err != nil || got.ExecutionID != want.ExecutionID || got.Child.Fingerprint != want.Child.Fingerprint {
		t.Fatalf("ready=%+v err=%v", got, err)
	}
}
