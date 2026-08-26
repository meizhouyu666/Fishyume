package codexprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ManagedProcess is provider-neutral process evidence produced by the durable
// supervisor used by process-backed Session Drivers.
type ManagedProcess struct {
	Supervisor processRef `json:"supervisor"`
	Child      processRef `json:"child"`
}

type ManagedProcessState string

const (
	ManagedProcessActive ManagedProcessState = "active"
	ManagedProcessExited ManagedProcessState = "exited"
	ManagedProcessLost   ManagedProcessState = "lost"
)

type ManagedProcessObservation struct {
	State    ManagedProcessState
	ExitCode int
}

type ManagedProcessRequest struct {
	ExecutionID, SupervisorExecutable, Executable, Workspace string
	Args, Env                                                []string
	Prompt                                                   string
	ConfigPath, ReadyPath, StdoutPath, StderrPath, ExitPath  string
	StartupTimeout, PollInterval                             time.Duration
	MaxOutputBytes, MaxStderrBytes                           int64
}

const managedProcessExitEvidenceAttempts = 25

func LaunchManagedProcess(ctx context.Context, request ManagedProcessRequest) (ManagedProcess, error) {
	config := supervisorConfig{ExecutionID: request.ExecutionID, Executable: request.Executable, Workspace: request.Workspace, Args: request.Args, Env: request.Env, EventsPath: request.StdoutPath, StderrPath: request.StderrPath, ReadyPath: request.ReadyPath, ExitPath: request.ExitPath, MaxEventBytes: request.MaxOutputBytes, MaxStderrBytes: request.MaxStderrBytes}
	if err := writeJSONExclusive(request.ConfigPath, config); err != nil {
		return ManagedProcess{}, err
	}
	command := exec.Command(request.SupervisorExecutable, "direct-supervisor", request.ConfigPath)
	configureBackgroundCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return ManagedProcess{}, err
	}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return ManagedProcess{}, err
	}
	go func() { _ = command.Wait() }()
	if _, err := io.WriteString(stdin, request.Prompt); err != nil {
		_ = stdin.Close()
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return ManagedProcess{}, err
	}
	if err := stdin.Close(); err != nil {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return ManagedProcess{}, err
	}
	identity, exists, err := currentProcessIdentity(command.Process.Pid)
	if err != nil || !exists {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return ManagedProcess{}, fmt.Errorf("identify process supervisor: %w", errors.Join(err, errProcessDisappeared))
	}
	if request.StartupTimeout <= 0 {
		request.StartupTimeout = 10 * time.Second
	}
	if request.PollInterval <= 0 {
		request.PollInterval = 25 * time.Millisecond
	}
	deadline := time.Now().Add(request.StartupTimeout)
	for {
		var ready readyRecord
		if err := readJSONFile(request.ReadyPath, 64*1024, &ready); err == nil {
			if ready.ExecutionID != request.ExecutionID || !sameExecutable(ready.Child.Executable, request.Executable) {
				_, _, _ = cancelProcessRefs(context.Background(), ready.Child, processRef{PID: command.Process.Pid, GroupID: identity.GroupID, Fingerprint: identity.Fingerprint, Executable: identity.Executable}, request.PollInterval, "Agent Session startup")
				return ManagedProcess{}, fmt.Errorf("process supervisor returned mismatched child identity")
			}
			return ManagedProcess{Supervisor: processRef{PID: command.Process.Pid, GroupID: identity.GroupID, Fingerprint: identity.Fingerprint, Executable: identity.Executable}, Child: ready.Child}, nil
		} else if !os.IsNotExist(unwrapPathError(err)) && !isTransientFileAccess(err) {
			return ManagedProcess{}, err
		}
		if time.Now().After(deadline) {
			_ = terminateProcessTree(context.Background(), processRef{PID: command.Process.Pid, GroupID: identity.GroupID, Fingerprint: identity.Fingerprint, Executable: identity.Executable})
			return ManagedProcess{}, fmt.Errorf("process supervisor did not become ready within %s", request.StartupTimeout)
		}
		select {
		case <-ctx.Done():
			return ManagedProcess{}, ctx.Err()
		case <-time.After(request.PollInterval):
		}
	}
}

func ObserveManagedProcess(process ManagedProcess, exitPath string) (ManagedProcessObservation, error) {
	readExit := func() (*ManagedProcessObservation, error) {
		var exit exitRecord
		if err := readJSONFile(exitPath, 64*1024, &exit); err == nil {
			if exit.ChildPID != process.Child.PID || exit.Fingerprint != process.Child.Fingerprint {
				result := ManagedProcessObservation{State: ManagedProcessLost}
				return &result, nil
			}
			result := ManagedProcessObservation{State: ManagedProcessExited, ExitCode: exit.ExitCode}
			return &result, nil
		} else if !os.IsNotExist(unwrapPathError(err)) && !isTransientFileAccess(err) {
			return nil, err
		}
		return nil, nil
	}
	if result, err := readExit(); result != nil || err != nil {
		if err != nil {
			return ManagedProcessObservation{}, err
		}
		return *result, nil
	}
	waitExit := func() (*ManagedProcessObservation, error) {
		for attempt := 0; attempt < managedProcessExitEvidenceAttempts; attempt++ {
			time.Sleep(10 * time.Millisecond)
			if result, err := readExit(); result != nil || err != nil {
				return result, err
			}
		}
		return nil, nil
	}
	child, err := inspectProcessRef(process.Child)
	if err != nil {
		return ManagedProcessObservation{}, err
	}
	if child == processMatched {
		return ManagedProcessObservation{State: ManagedProcessActive}, nil
	}
	if child == processMismatched {
		if result, err := waitExit(); result != nil || err != nil {
			if err != nil {
				return ManagedProcessObservation{}, err
			}
			return *result, nil
		}
		return ManagedProcessObservation{State: ManagedProcessLost}, nil
	}
	supervisor, err := inspectProcessRef(process.Supervisor)
	if err != nil {
		return ManagedProcessObservation{}, err
	}
	if supervisor == processMatched {
		return ManagedProcessObservation{State: ManagedProcessActive}, nil
	}
	// The supervisor writes exit evidence atomically after Wait returns. A very
	// short process can disappear just before that rename becomes visible.
	if result, err := waitExit(); result != nil || err != nil {
		if err != nil {
			return ManagedProcessObservation{}, err
		}
		return *result, nil
	}
	return ManagedProcessObservation{State: ManagedProcessLost}, nil
}

func CancelManagedProcess(ctx context.Context, process ManagedProcess, pollInterval time.Duration) (bool, string, error) {
	return cancelProcessRefs(ctx, process.Child, process.Supervisor, pollInterval, "Agent Session")
}

func ResolveSupervisorExecutable(override string) (string, error) {
	if override == "" {
		var err error
		override, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(override)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		return "", fmt.Errorf("process supervisor executable is unavailable at %s", abs)
	}
	return filepath.Clean(abs), nil
}
