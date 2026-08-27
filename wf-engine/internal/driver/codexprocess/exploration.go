package codexprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/execution"
	"wf.local/wf-engine/internal/explorationdriver"
)

const explorationHandleSchemaVersion = 1

type ExplorationAdapter struct{ process *Backend }

func NewExplorationAdapter(process *Backend) *ExplorationAdapter {
	return &ExplorationAdapter{process: process}
}

func (*ExplorationAdapter) Name() string { return "codex" }

func (*ExplorationAdapter) Capabilities() explorationdriver.DriverCapabilities {
	return explorationdriver.DriverCapabilities{Targets: []string{"local"}, SupportsOutput: true, SupportsRecovery: true, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true}
}

func (a *ExplorationAdapter) Doctor(ctx context.Context, request explorationdriver.DoctorRequest) explorationdriver.DoctorReport {
	report := explorationdriver.DoctorReport{Driver: a.Name()}
	if a == nil || a.process == nil {
		report.Diagnostics = []explorationdriver.Diagnostic{{Name: "adapter", Status: "error", Message: "Codex process adapter is unavailable"}}
		return report
	}
	base := a.process.Doctor(ctx, backend.DoctorRequest{Workspace: request.Workspace, Tool: "codex", Runtime: request.Target})
	report.Ready = base.Ready
	for _, diagnostic := range base.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, explorationdriver.Diagnostic{Name: diagnostic.Name, Status: string(diagnostic.Status), Message: diagnostic.Message})
	}
	return report
}

type explorationHandleData struct {
	ExecutionID           string                              `json:"executionId"`
	Identity              explorationdriver.ExecutionIdentity `json:"identity"`
	ArtifactDir           string                              `json:"artifactDir"`
	Workspace             string                              `json:"workspace"`
	ResultMaxBytes        int                                 `json:"resultMaxBytes"`
	EventMaxBytes         int64                               `json:"eventMaxBytes"`
	AgentExecutable       string                              `json:"agentExecutable"`
	AgentExecutableSHA256 string                              `json:"agentExecutableSha256"`
	Supervisor            processRef                          `json:"supervisor"`
	Child                 processRef                          `json:"child"`
	StartedAt             time.Time                           `json:"startedAt"`
}

func (a *ExplorationAdapter) Start(ctx context.Context, request explorationdriver.StartRequest) (*explorationdriver.ExecutionHandle, error) {
	if a == nil || a.process == nil {
		return nil, fmt.Errorf("Codex process adapter is unavailable")
	}
	if err := explorationdriver.ValidateStartRequest(request); err != nil {
		return nil, err
	}
	if request.Target != "local" {
		return nil, fmt.Errorf("Codex exploration supports only local target")
	}
	if !processPlatformSupported() {
		return nil, fmt.Errorf("Codex process recovery is not implemented on this platform")
	}
	workspace, err := canonicalDirectory(request.Workspace)
	if err != nil {
		return nil, err
	}
	discovered, err := a.process.discoverExecutable()
	if err != nil {
		return nil, err
	}
	supervisor, err := a.process.supervisorExecutable()
	if err != nil {
		return nil, err
	}
	executionID, err := randomID("team")
	if err != nil {
		return nil, err
	}
	relative, err := (execution.ArtifactLocation{Namespace: "teams", OwnerID: request.Identity.TeamID, ResourceKind: "turns", ResourceID: request.Identity.TurnID, GenerationKind: "executions", Generation: 1}).RelativePath()
	if err != nil {
		return nil, fmt.Errorf("resolve Team turn artifact location: %w", err)
	}
	directory, err := a.process.resolveRelativePath(relative)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Team execution directory: %w", err)
	}
	paths := artifactPaths(directory)
	for _, path := range []string{paths.Config, paths.Ready, paths.Events, paths.Stderr, paths.Result, paths.Exit} {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("Team execution artifact already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	resultBytes := request.ResultContract.MaxBytes
	if resultBytes <= 0 || resultBytes > explorationdriver.MaxOutputBytes {
		return nil, fmt.Errorf("Team contribution result limit is invalid")
	}
	args := codexExplorationExecArgs(modelName(request.ModelID), string(explorationdriver.SandboxReadOnly), paths.Result, workspace)
	config := supervisorConfig{ExecutionID: executionID, Executable: discovered.Path, Workspace: workspace, Args: args, EventsPath: paths.Events, StderrPath: paths.Stderr, ReadyPath: paths.Ready, ExitPath: paths.Exit, MaxEventBytes: a.process.config.MaxEventBytes, MaxStderrBytes: a.process.config.MaxStderrBytes}
	if err := writeJSONExclusive(paths.Config, config); err != nil {
		return nil, err
	}
	command := exec.Command(supervisor, "direct-supervisor", paths.Config)
	configureBackgroundCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start Team supervisor: %w", err)
	}
	go func() { _ = command.Wait() }()
	prompt := explorationPrompt(request, executionID)
	if _, err := io.WriteString(stdin, prompt); err != nil {
		_ = stdin.Close()
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, err
	}
	if err := stdin.Close(); err != nil {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, err
	}
	supervisorIdentity, exists, err := currentProcessIdentity(command.Process.Pid)
	if err != nil || !exists {
		_ = terminateUnverifiedProcess(command.Process.Pid)
		return nil, errors.Join(err, errProcessDisappeared)
	}
	ready, err := a.process.waitReady(ctx, paths.Ready, executionID)
	if err != nil {
		_ = terminateProcessTree(context.Background(), processRef{PID: command.Process.Pid, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable})
		return nil, err
	}
	if !sameExecutable(ready.Child.Executable, discovered.Path) {
		_ = terminateProcessTree(context.Background(), processRef{PID: command.Process.Pid, GroupID: supervisorIdentity.GroupID, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable})
		return nil, fmt.Errorf("Team supervisor launched unexpected executable")
	}
	data := explorationHandleData{ExecutionID: executionID, Identity: request.Identity, ArtifactDir: filepath.ToSlash(relative), Workspace: workspace, ResultMaxBytes: resultBytes, EventMaxBytes: a.process.config.MaxEventBytes, AgentExecutable: discovered.Path, AgentExecutableSHA256: discovered.SHA256, Supervisor: processRef{PID: command.Process.Pid, GroupID: supervisorIdentity.GroupID, Fingerprint: supervisorIdentity.Fingerprint, Executable: supervisorIdentity.Executable}, Child: ready.Child, StartedAt: ready.StartedAt}
	encoded, err := json.Marshal(data)
	if err != nil {
		_ = terminateProcessTree(context.Background(), data.Supervisor)
		return nil, err
	}
	handle := &explorationdriver.ExecutionHandle{Driver: a.Name(), Target: request.Target, SchemaVersion: explorationHandleSchemaVersion, ID: executionID, Data: encoded}
	if err := explorationdriver.ValidateExecutionHandle(*handle); err != nil {
		_ = terminateProcessTree(context.Background(), data.Supervisor)
		return nil, err
	}
	return handle, nil
}

func (a *ExplorationAdapter) Observe(_ context.Context, handle explorationdriver.ExecutionHandle) (*explorationdriver.Observation, error) {
	data, paths, err := a.decodeHandle(handle)
	if err != nil {
		return nil, err
	}
	events, err := readEventEvidence(paths.Events, data.EventMaxBytes)
	if err != nil {
		if isTransientFileAccess(err) {
			return &explorationdriver.Observation{State: explorationdriver.ObservationActive, Diagnostic: boundedExplorationDiagnostic(err.Error())}, nil
		}
		return nil, err
	}
	common := data.common()
	exit, exitExists, err := readExitRecord(paths.Exit, common)
	if err != nil {
		return &explorationdriver.Observation{State: explorationdriver.ObservationLost, Diagnostic: boundedExplorationDiagnostic(err.Error())}, nil
	}
	if events.Completed || exitExists {
		diagnostic := events.Failure
		if diagnostic == "" && exitExists && exit.ExitCode != 0 {
			diagnostic = fmt.Sprintf("Codex CLI exited with code %d without a Team contribution", exit.ExitCode)
		}
		if exitExists && exit.ResultSHA256 != "" {
			hash, hashErr := executableSHA256(paths.Result)
			if hashErr != nil || hash != exit.ResultSHA256 {
				return &explorationdriver.Observation{State: explorationdriver.ObservationLost, Diagnostic: "Codex contribution integrity check failed"}, nil
			}
		}
		return &explorationdriver.Observation{State: explorationdriver.ObservationTerminal, Diagnostic: boundedExplorationDiagnostic(diagnostic)}, nil
	}
	evidence, err := inspectExecution(common, paths)
	if err != nil {
		return nil, err
	}
	if evidence.Active {
		return &explorationdriver.Observation{State: explorationdriver.ObservationActive}, nil
	}
	if evidence.IdentityMismatch {
		return &explorationdriver.Observation{State: explorationdriver.ObservationLost, Diagnostic: "Codex process identity no longer matches the Team handle"}, nil
	}
	return &explorationdriver.Observation{State: explorationdriver.ObservationTerminal}, nil
}

func (a *ExplorationAdapter) Output(_ context.Context, handle explorationdriver.ExecutionHandle, maxBytes int) (string, error) {
	data, paths, err := a.decodeHandle(handle)
	if err != nil {
		return "", err
	}
	events, err := readEventEvidence(paths.Events, data.EventMaxBytes)
	if err != nil {
		return "", err
	}
	exit, exitExists, err := readExitRecord(paths.Exit, data.common())
	if err != nil {
		return "", err
	}
	if !events.Completed && !exitExists {
		return "", fmt.Errorf("Codex Team contribution is not terminal")
	}
	if events.Failure != "" {
		return "", fmt.Errorf("Codex Team contribution failed: %s", boundedExplorationDiagnostic(events.Failure))
	}
	if exitExists && exit.ExitCode != 0 {
		return "", fmt.Errorf("Codex CLI exited with code %d without a Team contribution", exit.ExitCode)
	}
	if maxBytes <= 0 || maxBytes > data.ResultMaxBytes {
		maxBytes = data.ResultMaxBytes
	}
	file, err := os.Open(paths.Result)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("Codex completed without a Team contribution")
		}
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxBytes {
		return "", fmt.Errorf("Codex contribution exceeds %d bytes", maxBytes)
	}
	if exitExists {
		if exit.ResultSHA256 == "" {
			return "", fmt.Errorf("Codex contribution appeared after the supervisor recorded process exit")
		}
		hash, hashErr := executableSHA256(paths.Result)
		if hashErr != nil || hash != exit.ResultSHA256 {
			return "", fmt.Errorf("Codex contribution changed after process exit")
		}
	}
	content := string(bytes.TrimSpace(raw))
	if content == "" {
		return "", fmt.Errorf("Codex completed with an empty Team contribution")
	}
	encoded, err := encodeTeamContribution(content)
	if err != nil {
		return "", err
	}
	return encoded, nil
}

func (a *ExplorationAdapter) Cancel(ctx context.Context, handle explorationdriver.ExecutionHandle) (*explorationdriver.CancelResult, error) {
	data, _, err := a.decodeHandle(handle)
	if err != nil {
		return nil, err
	}
	confirmed, diagnostic, err := cancelProcessRefs(ctx, data.Child, data.Supervisor, a.process.config.PollInterval, "Team")
	if err != nil {
		return nil, err
	}
	state := explorationdriver.CancelNotConfirmed
	if confirmed {
		state = explorationdriver.CancelConfirmed
	}
	return &explorationdriver.CancelResult{State: state, Diagnostic: boundedExplorationDiagnostic(diagnostic)}, nil
}

func (a *ExplorationAdapter) decodeHandle(handle explorationdriver.ExecutionHandle) (explorationHandleData, artifactSet, error) {
	if a == nil || a.process == nil {
		return explorationHandleData{}, artifactSet{}, fmt.Errorf("Codex process adapter is unavailable")
	}
	if err := explorationdriver.ValidateExecutionHandle(handle); err != nil {
		return explorationHandleData{}, artifactSet{}, err
	}
	if handle.Driver != a.Name() || handle.Target != "local" || handle.SchemaVersion != explorationHandleSchemaVersion {
		return explorationHandleData{}, artifactSet{}, fmt.Errorf("unsupported Codex exploration handle")
	}
	var data explorationHandleData
	decoder := json.NewDecoder(bytes.NewReader(handle.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return data, artifactSet{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return data, artifactSet{}, fmt.Errorf("multiple JSON values are not allowed in Codex exploration handle")
		}
		return data, artifactSet{}, fmt.Errorf("trailing Codex exploration handle data: %w", err)
	}
	if data.ExecutionID != handle.ID || explorationdriver.ValidateIdentity(data.Identity) != nil || data.AgentExecutableSHA256 == "" || data.ResultMaxBytes < 1 || data.ResultMaxBytes > explorationdriver.MaxOutputBytes {
		return data, artifactSet{}, fmt.Errorf("Codex exploration handle identity is incomplete")
	}
	if data.EventMaxBytes < 0 {
		return data, artifactSet{}, fmt.Errorf("Codex exploration handle event limit is invalid")
	}
	if data.Supervisor.PID <= 0 || data.Child.PID <= 0 || data.Supervisor.Fingerprint == "" || data.Child.Fingerprint == "" || !sameExecutable(data.Child.Executable, data.AgentExecutable) {
		return data, artifactSet{}, fmt.Errorf("Codex exploration process identity is incomplete")
	}
	directory, err := a.process.resolveRelativePath(filepath.FromSlash(data.ArtifactDir))
	if err != nil {
		return data, artifactSet{}, err
	}
	want, err := (execution.ArtifactLocation{Namespace: "teams", OwnerID: data.Identity.TeamID, ResourceKind: "turns", ResourceID: data.Identity.TurnID, GenerationKind: "executions", Generation: 1}).RelativePath()
	if err != nil || filepath.Clean(want) != filepath.Clean(filepath.FromSlash(data.ArtifactDir)) {
		return data, artifactSet{}, fmt.Errorf("Codex exploration artifact identity is invalid")
	}
	return data, artifactPaths(directory), nil
}

func (data explorationHandleData) common() handleData {
	return handleData{ExecutionID: data.ExecutionID, AttemptDir: data.ArtifactDir, Workspace: data.Workspace, ResultMaxBytes: data.ResultMaxBytes, EventMaxBytes: data.EventMaxBytes, AgentExecutable: data.AgentExecutable, AgentExecutableSHA256: data.AgentExecutableSHA256, Supervisor: data.Supervisor, Child: data.Child, StartedAt: data.StartedAt}
}

func codexExplorationExecArgs(model, sandbox, resultPath, workspace string) []string {
	args := []string{"exec", "--ephemeral"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "--sandbox", sandbox, "--json", "--color", "never", "--output-last-message", resultPath, "-C", workspace, "-")
}

func explorationPrompt(request explorationdriver.StartRequest, executionID string) string {
	identity, _ := json.Marshal(map[string]any{"executionId": executionID, "teamId": request.Identity.TeamID, "participantId": request.Identity.ParticipantID, "turnId": request.Identity.TurnID})
	return request.Prompt + "\n\nFishyume Team contribution protocol:\nReturn either a public Markdown answer or a JSON contribution envelope with schemaVersion, status, resultType (report|decision|artifact|data|question), and output. Do not include hidden reasoning. Plain text is wrapped as legacy Markdown for compatibility.\nFISHYUME_TEAM_IDENTITY=" + string(identity)
}

func modelName(modelID string) string {
	parts := strings.Split(modelID, "/")
	if len(parts) == 0 {
		return modelID
	}
	return parts[len(parts)-1]
}

func boundedExplorationDiagnostic(value string) string {
	if len(value) <= explorationdriver.MaxDiagnosticBytes {
		return value
	}
	value = value[:explorationdriver.MaxDiagnosticBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

var _ explorationdriver.Driver = (*ExplorationAdapter)(nil)
