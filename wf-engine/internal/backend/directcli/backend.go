package directcli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/backend"
)

const (
	handleSchemaVersion = 1
	defaultResultBytes  = 64 * 1024
	maxResultBytes      = 4 * 1024 * 1024
	defaultEventBytes   = 2 * 1024 * 1024
	defaultStderrBytes  = 1 * 1024 * 1024
	maxOutputBytes      = 64 * 1024
)

type Config struct {
	StateRoot            string
	Executable           string
	SupervisorExecutable string
	Sandbox              string
	StartupTimeout       time.Duration
	PollInterval         time.Duration
	MaxEventBytes        int64
	MaxStderrBytes       int64
}

type Backend struct {
	config      Config
	discoveryMu sync.Mutex
	discovery   *discoveryCache
}

func New(config Config) *Backend {
	config.StateRoot = filepath.Clean(config.StateRoot)
	if config.Sandbox == "" {
		config.Sandbox = strings.TrimSpace(os.Getenv("FISHYUME_DIRECT_SANDBOX"))
	}
	if config.Sandbox == "" {
		config.Sandbox = "workspace-write"
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 10 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 25 * time.Millisecond
	}
	if config.MaxEventBytes <= 0 {
		config.MaxEventBytes = defaultEventBytes
	}
	if config.MaxStderrBytes <= 0 {
		config.MaxStderrBytes = defaultStderrBytes
	}
	return &Backend{config: config}
}

func (*Backend) Name() string { return "direct" }

func (*Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true}
}

func (b *Backend) Doctor(ctx context.Context, request backend.DoctorRequest) backend.DoctorReport {
	report := backend.DoctorReport{Backend: b.Name()}
	addError := func(name string, err error) {
		report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: name, Status: backend.DiagnosticError, Message: err.Error()})
	}
	if !processPlatformSupported() {
		addError("platform", fmt.Errorf("Direct Backend process recovery is not implemented on this platform"))
		return report
	}
	if request.Tool != "" && request.Tool != "codex" {
		addError("tool", fmt.Errorf("Direct Backend supports tool codex, not %q", request.Tool))
		return report
	}
	if request.Runtime != "" && request.Runtime != "local" {
		addError("runtime", fmt.Errorf("Direct Backend supports runtime local, not %q", request.Runtime))
		return report
	}
	if err := validateSandbox(b.config.Sandbox); err != nil {
		addError("sandbox", err)
		return report
	}
	discovered, err := b.discoverExecutable()
	if err != nil {
		addError("executable", err)
		return report
	}
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(versionCtx, discovered.Path, "--version")
	configureBackgroundCommand(command)
	output, err := command.Output()
	if err != nil {
		addError("executable", fmt.Errorf("run %s --version: %w", discovered.Path, err))
		return report
	}
	version := strings.TrimSpace(string(bytes.TrimSpace(output)))
	if version == "" {
		version = "version command succeeded"
	}
	report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "executable", Status: backend.DiagnosticOK, Message: discovered.Path + " (" + version + ")"})
	if strings.TrimSpace(b.config.StateRoot) == "" || !filepath.IsAbs(b.config.StateRoot) {
		addError("state", fmt.Errorf("Direct Backend state root must be an absolute path"))
		return report
	}
	report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "state", Status: backend.DiagnosticOK, Message: "Direct execution state is isolated under " + b.config.StateRoot})
	if request.Workspace != "" {
		workspace, err := canonicalDirectory(request.Workspace)
		if err != nil {
			addError("workspace", err)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "workspace", Status: backend.DiagnosticOK, Message: "workspace is available at " + workspace})
	}
	report.Ready = true
	return report
}

func (b *Backend) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	if !processPlatformSupported() {
		return nil, fmt.Errorf("Direct Backend process recovery is not implemented on this platform")
	}
	if err := backend.ValidateAgentExecutionSpec(spec); err != nil {
		return nil, err
	}
	if spec.Tool != "codex" || spec.Runtime != "local" {
		return nil, fmt.Errorf("Direct Backend supports only codex + local, got %s + %s", spec.Tool, spec.Runtime)
	}
	if err := validateSandbox(b.config.Sandbox); err != nil {
		return nil, err
	}
	workspace, err := canonicalDirectory(spec.Workspace)
	if err != nil {
		return nil, err
	}
	discovered, err := b.discoverExecutable()
	if err != nil {
		return nil, err
	}
	supervisor, err := b.supervisorExecutable()
	if err != nil {
		return nil, err
	}
	executionID, err := randomID("direct")
	if err != nil {
		return nil, err
	}
	relativeAttempt := filepath.Join("runs", spec.RunID, "nodes", spec.NodeID, "attempts", fmt.Sprintf("%d", spec.Attempt))
	attemptDir, err := b.resolveRelativePath(relativeAttempt)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(attemptDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Direct Attempt directory: %w", err)
	}
	paths := artifactPaths(attemptDir)
	for _, path := range []string{paths.Config, paths.Ready, paths.Events, paths.Stderr, paths.Schema, paths.Result, paths.Exit} {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("Direct Attempt artifact already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect Direct Attempt artifact %s: %w", path, err)
		}
	}
	resultBytes := spec.ResultContract.MaxBytes
	if resultBytes <= 0 {
		resultBytes = defaultResultBytes
	}
	if resultBytes > maxResultBytes {
		return nil, fmt.Errorf("Direct result limit %d exceeds %d bytes", resultBytes, maxResultBytes)
	}
	schema := resultSchema(spec, executionID)
	if err := writeJSONExclusive(paths.Schema, schema); err != nil {
		return nil, err
	}
	config := supervisorConfig{
		ExecutionID: executionID, Executable: discovered.Path, Workspace: workspace,
		Args:       []string{"exec", "--ephemeral", "--sandbox", b.config.Sandbox, "--json", "--color", "never", "--output-schema", paths.Schema, "--output-last-message", paths.Result, "-C", workspace, "-"},
		EventsPath: paths.Events, StderrPath: paths.Stderr, ReadyPath: paths.Ready, ExitPath: paths.Exit,
		MaxEventBytes: b.config.MaxEventBytes, MaxStderrBytes: b.config.MaxStderrBytes,
	}
	if err := writeJSONExclusive(paths.Config, config); err != nil {
		return nil, err
	}
	prompt := completionPrompt(spec, executionID)
	command := exec.Command(supervisor, "direct-supervisor", paths.Config)
	configureBackgroundCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Direct supervisor prompt pipe: %w", err)
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start Direct supervisor: %w", err)
	}
	go func() { _ = command.Wait() }()
	if _, err := io.WriteString(stdin, prompt); err != nil {
		_ = stdin.Close()
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, fmt.Errorf("send Direct supervisor prompt: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, fmt.Errorf("close Direct supervisor prompt: %w", err)
	}
	supervisorIdentity, exists, err := currentProcessIdentity(command.Process.Pid)
	if err != nil || !exists {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, fmt.Errorf("identify Direct supervisor process: %w", errors.Join(err, errProcessDisappeared))
	}
	ready, err := b.waitReady(ctx, paths.Ready, executionID)
	if err != nil {
		_ = terminateProcessTree(context.Background(), processRef{PID: command.Process.Pid, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable})
		return nil, err
	}
	if !sameExecutable(ready.Child.Executable, discovered.Path) {
		_ = terminateProcessTree(context.Background(), processRef{PID: command.Process.Pid, GroupID: supervisorIdentity.GroupID, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable})
		return nil, fmt.Errorf("Direct supervisor launched unexpected executable %s", ready.Child.Executable)
	}
	handleData := handleData{
		ExecutionID: executionID, RunID: spec.RunID, NodeID: spec.NodeID, Attempt: spec.Attempt,
		AttemptDir: filepath.ToSlash(relativeAttempt), Workspace: workspace, ResultMaxBytes: resultBytes,
		AgentExecutable: discovered.Path, AgentExecutableSHA256: discovered.SHA256,
		Supervisor: processRef{PID: command.Process.Pid, GroupID: supervisorIdentity.GroupID, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable},
		Child:      ready.Child, StartedAt: ready.StartedAt,
	}
	handle, err := encodeHandle(handleData)
	if err != nil {
		_ = terminateProcessTree(context.Background(), handleData.Supervisor)
		return nil, err
	}
	return handle, nil
}

func (b *Backend) Observe(_ context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	data, paths, err := b.decodeHandle(handle)
	if err != nil {
		return nil, err
	}
	events, err := readEventEvidence(paths.Events)
	if err != nil {
		if isTransientFileAccess(err) {
			return transientArtifactObservation("event log", err), nil
		}
		return nil, err
	}
	exit, exitExists, err := readExitRecord(paths.Exit, data)
	if err != nil {
		if isTransientFileAccess(err) {
			return transientArtifactObservation("exit record", err), nil
		}
		return invalidResultObservation(err.Error()), nil
	}
	if events.Completed || exitExists {
		return completedObservation(paths, data, events, exit, exitExists), nil
	}
	evidence, err := inspectExecution(data, paths)
	if err != nil {
		return nil, err
	}
	if evidence.Active {
		if events.WaitingInput {
			return &backend.ExecutionObservation{State: backend.ObservationWaitingInput, Diagnostic: "Codex CLI reported waiting for input"}, nil
		}
		return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
	}
	if evidence.IdentityMismatch {
		return &backend.ExecutionObservation{State: backend.ObservationLost, Diagnostic: "Direct process identity no longer matches the persisted handle"}, nil
	}
	return completedObservation(paths, data, events, exitRecord{}, false), nil
}

func completedObservation(paths artifactSet, data handleData, events eventEvidence, exit exitRecord, exitExists bool) *backend.ExecutionObservation {
	result, resultExists, err := readResult(paths.Result, data)
	if err != nil {
		if isTransientFileAccess(err) {
			return transientArtifactObservation("structured result", err)
		}
		return invalidResultObservation(err.Error())
	}
	if resultExists {
		if exitExists {
			if exit.ResultSHA256 == "" {
				return invalidResultObservation("Direct result appeared after the supervisor recorded process exit")
			}
			currentHash, hashErr := executableSHA256(paths.Result)
			if hashErr != nil && isTransientFileAccess(hashErr) {
				return transientArtifactObservation("structured result integrity check", hashErr)
			}
			if hashErr != nil || currentHash != exit.ResultSHA256 {
				return invalidResultObservation("Direct structured result changed after process exit")
			}
		}
		if exitExists && exit.ExitCode != 0 && result.Status == "succeeded" {
			return invalidResultObservation(fmt.Sprintf("Codex CLI exited with code %d but reported succeeded", exit.ExitCode))
		}
		if result.Usage.InputTokensEstimated == 0 {
			result.Usage.InputTokensEstimated = events.InputTokens
		}
		if result.Usage.OutputTokensEstimated == 0 {
			result.Usage.OutputTokensEstimated = events.OutputTokens
		}
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: result}
	}
	if exitExists && exit.ResultSHA256 != "" {
		return invalidResultObservation("Direct structured result recorded at process exit is now missing")
	}
	if exitExists && exit.ExitCode != 0 {
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "failed", Summary: fmt.Sprintf("Codex CLI exited with code %d without a structured result", exit.ExitCode)}}
	}
	return &backend.ExecutionObservation{State: backend.ObservationResultPending, Diagnostic: "Codex CLI finished without a structured result"}
}

func transientArtifactObservation(name string, err error) *backend.ExecutionObservation {
	return &backend.ExecutionObservation{
		State:      backend.ObservationResultPending,
		Diagnostic: fmt.Sprintf("Direct %s is temporarily inaccessible: %v", name, err),
	}
}

func (b *Backend) Output(_ context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	_, paths, err := b.decodeHandle(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 200
	}
	if lines > 1000 {
		lines = 1000
	}
	events, err := tailLines(paths.Events, lines, maxOutputBytes/2)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	stderr, stderrErr := tailLines(paths.Stderr, lines, maxOutputBytes/2)
	if stderrErr != nil && !os.IsNotExist(stderrErr) {
		return "", stderrErr
	}
	parts := make([]string, 0, 2)
	if strings.TrimSpace(events) != "" {
		parts = append(parts, "events:\n"+events)
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	output := strings.Join(parts, "\n")
	if len(output) > maxOutputBytes {
		output = output[len(output)-maxOutputBytes:]
	}
	return output, nil
}

func (b *Backend) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	data, _, err := b.decodeHandle(handle)
	if err != nil {
		return nil, err
	}
	refs := []processRef{data.Supervisor, data.Child}
	active := make([]processRef, 0, len(refs))
	mismatchedPID := 0
	for _, ref := range refs {
		status, err := inspectProcessRef(ref)
		if err != nil {
			return nil, err
		}
		switch status {
		case processMatched:
			active = append(active, ref)
		case processMismatched:
			if mismatchedPID == 0 {
				mismatchedPID = ref.PID
			}
		}
	}
	if len(active) == 0 {
		if mismatchedPID != 0 {
			return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: fmt.Sprintf("PID %d no longer matches the Direct execution identity", mismatchedPID)}, nil
		}
		return &backend.CancelResult{State: backend.CancelConfirmed, Diagnostic: "Direct execution is already stopped"}, nil
	}
	root := active[0]
	if err := terminateProcessTree(ctx, root); err != nil {
		return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: err.Error()}, nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		remaining := false
		for _, ref := range active {
			status, err := inspectProcessRef(ref)
			if err != nil {
				return nil, err
			}
			if status == processMatched {
				remaining = true
			}
		}
		if !remaining {
			if mismatchedPID != 0 {
				return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: fmt.Sprintf("stopped matching Direct processes, but PID %d no longer matches the execution identity", mismatchedPID)}, nil
			}
			return &backend.CancelResult{State: backend.CancelConfirmed}, nil
		}
		if time.Now().After(deadline) {
			return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: "Direct process tree remained active after termination"}, nil
		}
		select {
		case <-ctx.Done():
			return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: ctx.Err().Error()}, nil
		case <-time.After(b.config.PollInterval):
		}
	}
}

func (b *Backend) waitReady(ctx context.Context, path, executionID string) (readyRecord, error) {
	deadline := time.Now().Add(b.config.StartupTimeout)
	var lastTransientErr error
	for {
		var ready readyRecord
		if err := readJSONFile(path, 64*1024, &ready); err == nil {
			if ready.ExecutionID != executionID || ready.Child.PID <= 0 || ready.Child.Fingerprint == "" {
				return readyRecord{}, fmt.Errorf("Direct supervisor ready record has the wrong execution identity")
			}
			return ready, nil
		} else if os.IsNotExist(unwrapPathError(err)) {
			lastTransientErr = nil
		} else if isTransientFileAccess(err) {
			lastTransientErr = err
		} else {
			return readyRecord{}, err
		}
		if time.Now().After(deadline) {
			if lastTransientErr != nil {
				return readyRecord{}, fmt.Errorf("Direct supervisor ready record remained inaccessible within %s: %w", b.config.StartupTimeout, lastTransientErr)
			}
			return readyRecord{}, fmt.Errorf("Direct supervisor did not become ready within %s", b.config.StartupTimeout)
		}
		select {
		case <-ctx.Done():
			return readyRecord{}, ctx.Err()
		case <-time.After(b.config.PollInterval):
		}
	}
}

func readResult(path string, data handleData) (*backend.AgentResult, bool, error) {
	var envelope resultEnvelope
	err := readJSONFile(path, int64(data.ResultMaxBytes)+16*1024, &envelope)
	if err != nil {
		if os.IsNotExist(unwrapPathError(err)) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read Direct structured result: %w", err)
	}
	if envelope.ExecutionID != data.ExecutionID || envelope.RunID != data.RunID || envelope.NodeID != data.NodeID || envelope.Attempt != data.Attempt {
		return nil, true, fmt.Errorf("Direct structured result identity does not match the Attempt")
	}
	if err := backend.ValidateAgentResult(envelope.Result); err != nil {
		return nil, true, fmt.Errorf("Direct structured result is invalid: %w", err)
	}
	return &envelope.Result, true, nil
}

func invalidResultObservation(message string) *backend.ExecutionObservation {
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Diagnostic: message, Result: &backend.AgentResult{Status: "invalid_result", Summary: message}}
}

type eventEvidence struct {
	Completed    bool
	WaitingInput bool
	InputTokens  int
	OutputTokens int
}

func readEventEvidence(path string) (eventEvidence, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return eventEvidence{}, nil
	}
	if err != nil {
		return eventEvidence{}, fmt.Errorf("open Direct event log: %w", err)
	}
	defer file.Close()
	var evidence eventEvidence
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event struct {
			Type  string `json:"type"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "turn.completed":
			evidence.Completed = true
			evidence.InputTokens = event.Usage.InputTokens
			evidence.OutputTokens = event.Usage.OutputTokens
		case "turn.waiting_input", "item.waiting_input", "fishyume.waiting_input":
			evidence.WaitingInput = true
		}
	}
	if err := scanner.Err(); err != nil {
		return eventEvidence{}, fmt.Errorf("read Direct event log: %w", err)
	}
	return evidence, nil
}

func readJSONFile(path string, maxBytes int64, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("file is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("file contains multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".direct-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func executableSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value), nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve workspace %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect workspace %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", path)
	}
	return filepath.Clean(resolved), nil
}

func validateSandbox(value string) error {
	switch value {
	case "read-only", "workspace-write", "danger-full-access":
		return nil
	default:
		return fmt.Errorf("unsupported Direct sandbox %q", value)
	}
}

func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

var errProcessDisappeared = errors.New("process disappeared before its identity was captured")
