package codexprocess

import (
	"path/filepath"
	"testing"
	"time"
)

func TestObserveManagedProcessWaitsForDelayedExitEvidence(t *testing.T) {
	exitPath := filepath.Join(t.TempDir(), "exit.json")
	process := ManagedProcess{
		Supervisor: processRef{PID: 2_000_000_001, Fingerprint: "missing-supervisor", Executable: "missing-supervisor"},
		Child:      processRef{PID: 2_000_000_002, Fingerprint: "finished-child", Executable: "finished-child"},
	}
	written := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		written <- writeJSONAtomic(exitPath, exitRecord{ChildPID: process.Child.PID, Fingerprint: process.Child.Fingerprint, ExitCode: 0, ExitedAt: time.Now().UTC()})
	}()

	observed, err := ObserveManagedProcess(process, exitPath)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := <-written; writeErr != nil {
		t.Fatal(writeErr)
	}
	if observed.State != ManagedProcessExited || observed.ExitCode != 0 {
		t.Fatalf("observation=%+v", observed)
	}
}
