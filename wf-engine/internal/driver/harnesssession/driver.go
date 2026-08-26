package harnesssession

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/teamcontract"
)

const recordVersion = 1

type Config struct {
	StateRoot, Executable, SupervisorExecutable string
	Catalog                                     routing.CapabilityCatalogV1
	PollInterval, StartupTimeout                time.Duration
}

type Driver struct {
	name, executableName string
	config               Config
	routes               map[string]routing.ModelCapabilityV1
	targets              []string
	mu                   sync.Mutex
	locks                map[string]*sync.Mutex
}

type lifecycle string

const (
	active lifecycle = "active"
	parked lifecycle = "parked"
	closed lifecycle = "closed"
	lost   lifecycle = "lost"
)

type record struct {
	SchemaVersion  int                           `json:"schemaVersion"`
	Driver         string                        `json:"driver"`
	HandleID       string                        `json:"handleId"`
	Identity       sessiondriver.SessionIdentity `json:"identity"`
	Workspace      string                        `json:"workspace"`
	Target         string                        `json:"target"`
	ModelID        string                        `json:"modelId"`
	Model          string                        `json:"model"`
	Executable     string                        `json:"executable"`
	ExecutableHash string                        `json:"executableHash"`
	ExternalID     string                        `json:"externalId,omitempty"`
	State          lifecycle                     `json:"state"`
	Revision       uint64                        `json:"revision"`
	LastTurnID     string                        `json:"lastTurnId,omitempty"`
	LastTurnState  sessiondriver.TurnState       `json:"lastTurnState,omitempty"`
	LastMaxOutput  int                           `json:"lastMaxOutput,omitempty"`
	LastOutput     string                        `json:"lastOutput,omitempty"`
	LastDiagnostic string                        `json:"lastDiagnostic,omitempty"`
	Process        codexprocess.ManagedProcess   `json:"process,omitempty"`
	UpdatedAt      time.Time                     `json:"updatedAt"`
}

func NewClaude(config Config) (*Driver, error)   { return newDriver("claude", "claude", config) }
func NewOpenCode(config Config) (*Driver, error) { return newDriver("opencode", "opencode", config) }

func newDriver(name, executable string, config Config) (*Driver, error) {
	if !filepath.IsAbs(config.StateRoot) {
		return nil, fmt.Errorf("%s Session state root must be absolute", name)
	}
	catalog := routing.CanonicalCatalogV1(config.Catalog)
	if err := routing.ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	routes, targets := map[string]routing.ModelCapabilityV1{}, []string{}
	seen := map[string]bool{}
	for _, model := range catalog.Models {
		if model.Target.Driver != name {
			continue
		}
		routes[model.ID] = model
		if !seen[model.Target.Provider] {
			seen[model.Target.Provider] = true
			targets = append(targets, model.Target.Provider)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("%s has no routes in the trusted Team catalog", name)
	}
	sort.Strings(targets)
	if config.PollInterval <= 0 {
		config.PollInterval = 50 * time.Millisecond
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 15 * time.Second
	}
	driver := &Driver{name: name, executableName: executable, config: config, routes: routes, targets: targets, locks: map[string]*sync.Mutex{}}
	if _, _, err := driver.discover(); err != nil {
		return nil, err
	}
	return driver, nil
}

func (d *Driver) Name() string { return d.name }
func (d *Driver) Capabilities() sessiondriver.DriverCapabilities {
	return sessiondriver.DriverCapabilities{Targets: append([]string(nil), d.targets...), SupportsResume: true, SupportsPark: true, SupportsRecovery: true, SupportsDirectedInput: true, SupportsConfirmedCancel: true, MaxConcurrentTurns: 1}
}

func (d *Driver) StartSession(_ context.Context, request sessiondriver.StartSessionRequest) (*sessiondriver.SessionHandle, error) {
	if err := sessiondriver.ValidateStartSessionRequest(request); err != nil {
		return nil, err
	}
	route, ok := d.routes[request.ModelID]
	if !ok || route.Target.Provider != request.Target {
		return nil, fmt.Errorf("%s route %q is not trusted for target %q", d.name, request.ModelID, request.Target)
	}
	workspace, err := canonicalDirectory(request.Workspace)
	if err != nil {
		return nil, err
	}
	executable, hash, err := d.discover()
	if err != nil {
		return nil, err
	}
	id, err := randomID("session")
	if err != nil {
		return nil, err
	}
	external := ""
	if d.name == "claude" {
		external, err = uuid()
		if err != nil {
			return nil, err
		}
	}
	r := record{SchemaVersion: recordVersion, Driver: d.name, HandleID: id, Identity: request.Identity, Workspace: workspace, Target: request.Target, ModelID: request.ModelID, Model: route.Target.Model, Executable: executable, ExecutableHash: hash, ExternalID: external, State: active, Revision: 1, UpdatedAt: time.Now().UTC()}
	if err := d.writeInitial(r); err != nil {
		return nil, err
	}
	return d.sessionHandle(r)
}

func (d *Driver) StartTurn(ctx context.Context, handle sessiondriver.SessionHandle, request sessiondriver.StartTurnRequest) (*sessiondriver.StartTurnResult, error) {
	if err := sessiondriver.ValidateStartTurnRequest(request); err != nil {
		return nil, err
	}
	lock := d.lock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	r, err := d.require(handle)
	if err != nil {
		return nil, err
	}
	if r.Identity.Generation != request.Identity.ExpectedSessionGeneration {
		return nil, sessiondriver.Conflict("session generation changed")
	}
	if r.LastTurnID == request.Identity.TurnID {
		return d.turnResult(r)
	}
	if r.State != active || r.LastTurnState == sessiondriver.TurnActive || r.LastTurnState == sessiondriver.TurnDispatching {
		return nil, sessiondriver.Conflict("session is not ready for a new turn")
	}
	r.LastTurnID, r.LastTurnState, r.LastMaxOutput = request.Identity.TurnID, sessiondriver.TurnDispatching, request.MaxOutputBytes
	r.LastOutput, r.LastDiagnostic = "", ""
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	if err := d.write(r); err != nil {
		return nil, err
	}
	dir, err := d.turnDir(r)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	args, env := d.command(r)
	supervisor, err := codexprocess.ResolveSupervisorExecutable(d.config.SupervisorExecutable)
	if err != nil {
		return nil, err
	}
	p := func(name string) string { return filepath.Join(dir, name) }
	managed, err := codexprocess.LaunchManagedProcess(ctx, codexprocess.ManagedProcessRequest{ExecutionID: r.HandleID + "-" + r.LastTurnID, SupervisorExecutable: supervisor, Executable: r.Executable, Workspace: r.Workspace, Args: args, Env: env, Prompt: sessionPrompt(request.Prompt), ConfigPath: p("supervisor.json"), ReadyPath: p("ready.json"), StdoutPath: p("stdout.jsonl"), StderrPath: p("stderr.log"), ExitPath: p("exit.json"), StartupTimeout: d.config.StartupTimeout, PollInterval: d.config.PollInterval, MaxOutputBytes: 1024 * 1024, MaxStderrBytes: 256 * 1024})
	if err != nil {
		r.LastTurnState, r.LastDiagnostic, r.Revision, r.UpdatedAt = sessiondriver.TurnFailed, bounded(err.Error(), sessiondriver.MaxDiagnosticBytes), r.Revision+1, time.Now().UTC()
		_ = d.write(r)
		return nil, err
	}
	r.Process, r.LastTurnState, r.Revision, r.UpdatedAt = managed, sessiondriver.TurnActive, r.Revision+1, time.Now().UTC()
	if err := d.write(r); err != nil {
		_, _, _ = codexprocess.CancelManagedProcess(context.Background(), managed, d.config.PollInterval)
		return nil, err
	}
	return d.turnResult(r)
}

func (d *Driver) ObserveTurn(_ context.Context, handle sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.TurnObservation, error) {
	lock := d.lock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	r, err := d.require(handle)
	if err != nil {
		return nil, err
	}
	if turn.ID != r.LastTurnID || turn.SessionID != r.HandleID {
		return nil, sessiondriver.Conflict("turn handle does not match session")
	}
	if r.LastTurnState == sessiondriver.TurnActive || r.LastTurnState == sessiondriver.TurnDispatching {
		dir, _ := d.turnDir(r)
		observed, err := codexprocess.ObserveManagedProcess(r.Process, filepath.Join(dir, "exit.json"))
		if err != nil {
			return nil, err
		}
		switch observed.State {
		case codexprocess.ManagedProcessActive:
			return d.observation(r, "", "")
		case codexprocess.ManagedProcessLost:
			r.LastTurnState, r.State = sessiondriver.TurnLost, lost
			r.LastDiagnostic = "managed Agent process identity was lost"
		case codexprocess.ManagedProcessExited:
			output, external, parseErr := d.parse(filepath.Join(dir, "stdout.jsonl"), r.LastMaxOutput)
			if parseErr != nil || observed.ExitCode != 0 {
				r.LastTurnState = sessiondriver.TurnFailed
				if parseErr != nil {
					r.LastDiagnostic = parseErr.Error()
					return d.finishObservation(r, "", parseErr.Error())
				}
				r.LastDiagnostic = fmt.Sprintf("%s exited with code %d", d.name, observed.ExitCode)
				return d.finishObservation(r, "", fmt.Sprintf("%s exited with code %d", d.name, observed.ExitCode))
			}
			if r.ExternalID == "" {
				r.ExternalID = external
			} else if external != "" && external != r.ExternalID {
				r.LastTurnState, r.State = sessiondriver.TurnLost, lost
				return d.finishObservation(r, "", "Agent session identity changed")
			}
			r.LastTurnState = sessiondriver.TurnResponded
			r.LastOutput = output
			return d.finishObservation(r, output, "")
		}
		return d.finishObservation(r, "", "managed Agent process identity was lost")
	}
	return d.observation(r, "", "")
}

func (d *Driver) ParkSession(_ context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	return d.setLifecycle(handle, active, parked)
}
func (d *Driver) ResumeSession(_ context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	lock := d.lock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	r, err := d.require(handle)
	if err != nil {
		return nil, err
	}
	if r.State == lost {
		return nil, sessiondriver.Lost("%s session is lost", d.name)
	}
	if r.State != parked && r.State != active {
		return nil, sessiondriver.Conflict("session cannot be resumed")
	}
	path, hash, err := d.discover()
	if err != nil {
		return nil, err
	}
	if path != r.Executable || hash != r.ExecutableHash {
		return nil, sessiondriver.Lost("%s executable identity changed", d.name)
	}
	if r.State == parked {
		r.State, r.Revision, r.UpdatedAt = active, r.Revision+1, time.Now().UTC()
		if err := d.write(r); err != nil {
			return nil, err
		}
	}
	return d.sessionHandle(r)
}

func (d *Driver) CancelTurn(ctx context.Context, handle sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.CancelTurnResult, error) {
	lock := d.lock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	r, err := d.require(handle)
	if err != nil {
		return nil, err
	}
	if turn.ID != r.LastTurnID || r.LastTurnState != sessiondriver.TurnActive {
		return nil, sessiondriver.Conflict("only the exact active turn can be cancelled")
	}
	confirmed, diagnostic, err := codexprocess.CancelManagedProcess(ctx, r.Process, d.config.PollInterval)
	if err != nil {
		return nil, err
	}
	state := sessiondriver.CancelNotConfirmed
	if confirmed {
		dir, pathErr := d.turnDir(r)
		if pathErr != nil {
			return nil, pathErr
		}
		exit, observeErr := codexprocess.ObserveManagedProcess(r.Process, filepath.Join(dir, "exit.json"))
		if observeErr != nil {
			return nil, observeErr
		}
		if exit.State == codexprocess.ManagedProcessExited && exit.ExitCode != 0 {
			state, r.LastTurnState = sessiondriver.CancelConfirmed, sessiondriver.TurnInterrupted
			r.Revision++
			r.UpdatedAt = time.Now().UTC()
			if err := d.write(r); err != nil {
				return nil, err
			}
		} else {
			diagnostic = "Agent process stopped without signed interruption evidence"
		}
	}
	sh, _ := d.sessionHandle(r)
	th := d.turnHandle(r)
	result := &sessiondriver.CancelTurnResult{Session: *sh, Turn: th, State: state, Diagnostic: diagnostic}
	return result, sessiondriver.ValidateCancelTurnResult(*result)
}

func (d *Driver) CloseSession(_ context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	return d.setLifecycle(handle, parked, closed)
}

func (d *Driver) setLifecycle(handle sessiondriver.SessionHandle, from, to lifecycle) (*sessiondriver.SessionHandle, error) {
	lock := d.lock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	r, err := d.require(handle)
	if err != nil {
		return nil, err
	}
	if r.State == to {
		return d.sessionHandle(r)
	}
	if r.State != from || r.LastTurnState == sessiondriver.TurnActive || r.LastTurnState == sessiondriver.TurnDispatching {
		return nil, sessiondriver.Conflict("session lifecycle transition is not allowed")
	}
	r.State, r.Revision, r.UpdatedAt = to, r.Revision+1, time.Now().UTC()
	if err := d.write(r); err != nil {
		return nil, err
	}
	return d.sessionHandle(r)
}

func (d *Driver) finishObservation(r record, output, diagnostic string) (*sessiondriver.TurnObservation, error) {
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	if err := d.write(r); err != nil {
		return nil, err
	}
	return d.observation(r, output, diagnostic)
}
func (d *Driver) observation(r record, output, diagnostic string) (*sessiondriver.TurnObservation, error) {
	sh, err := d.sessionHandle(r)
	if err != nil {
		return nil, err
	}
	result := &sessiondriver.TurnObservation{Session: *sh, Turn: d.turnHandle(r), State: r.LastTurnState, Diagnostic: bounded(diagnostic, sessiondriver.MaxDiagnosticBytes)}
	if r.LastTurnState == sessiondriver.TurnResponded {
		if output == "" {
			output = r.LastOutput
		}
		result.Output, err = contribution(output)
		if err != nil {
			return nil, err
		}
	}
	return result, sessiondriver.ValidateTurnObservation(*result)
}
func (d *Driver) turnResult(r record) (*sessiondriver.StartTurnResult, error) {
	sh, err := d.sessionHandle(r)
	if err != nil {
		return nil, err
	}
	return &sessiondriver.StartTurnResult{Session: *sh, Turn: d.turnHandle(r)}, nil
}
func (d *Driver) sessionHandle(r record) (*sessiondriver.SessionHandle, error) {
	data, _ := json.Marshal(map[string]any{"workspace": r.Workspace, "modelId": r.ModelID, "externalId": r.ExternalID, "state": r.State})
	h := &sessiondriver.SessionHandle{Driver: d.name, Target: r.Target, SchemaVersion: recordVersion, ID: r.HandleID, Generation: r.Identity.Generation, Revision: r.Revision, Data: data}
	return h, sessiondriver.ValidateSessionHandle(*h)
}
func (d *Driver) turnHandle(r record) sessiondriver.TurnHandle {
	data, _ := json.Marshal(map[string]string{"logicalTurnId": r.LastTurnID})
	return sessiondriver.TurnHandle{Driver: d.name, Target: r.Target, SchemaVersion: recordVersion, ID: r.LastTurnID, SessionID: r.HandleID, SessionGeneration: r.Identity.Generation, Data: data}
}

func (d *Driver) command(r record) ([]string, []string) {
	if d.name == "claude" {
		args := []string{"--print", "--output-format", "json", "--safe-mode", "--permission-mode", "dontAsk", "--tools", "Read,Glob,Grep", "--disallowed-tools", "Bash,Edit,Write,WebFetch,WebSearch,Task", "--strict-mcp-config", "--no-chrome", "--disable-slash-commands", "--model", r.Model}
		if r.ExternalID != "" && r.LastTurnID != "" && len(r.Process.Child.Executable) > 0 {
			args = append(args, "--resume", r.ExternalID)
		} else {
			args = append(args, "--session-id", r.ExternalID)
		}
		return args, nil
	}
	args := []string{"--pure", "run", "--format", "json", "--model", r.Model, "--agent", "fishyume-readonly", "--title", "Fishyume " + r.HandleID}
	if r.ExternalID != "" {
		args = append(args, "--session", r.ExternalID)
	}
	policy := `{"agent":{"fishyume-readonly":{"description":"Fishyume read-only Team worker","mode":"primary","permission":{"*":"deny","read":"allow","glob":"allow","grep":"allow"}}}}`
	return args, []string{"OPENCODE_CONFIG_CONTENT=" + policy}
}

func (d *Driver) parse(path string, limit int) (string, string, error) {
	data, err := readBounded(path, 1024*1024)
	if err != nil {
		return "", "", err
	}
	if d.name == "claude" {
		var v struct {
			Result    string `json:"result"`
			SessionID string `json:"session_id"`
			IsError   bool   `json:"is_error"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return "", "", err
		}
		if v.IsError || strings.TrimSpace(v.Result) == "" || v.SessionID == "" {
			return "", "", fmt.Errorf("Claude returned no successful result")
		}
		return bounded(v.Result, limit), v.SessionID, nil
	}
	return parseOpenCode(data, limit)
}

func parseOpenCode(data []byte, limit int) (string, string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var texts []string
	sessionID := ""
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) != nil {
			continue
		}
		if id, _ := value["sessionID"].(string); id != "" {
			sessionID = id
		}
		if id, _ := value["sessionId"].(string); id != "" {
			sessionID = id
		}
		part, _ := value["part"].(map[string]any)
		if id, _ := part["sessionID"].(string); id != "" {
			sessionID = id
		}
		if value["type"] == "text" {
			if text, _ := part["text"].(string); text != "" {
				texts = append(texts, text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	output := strings.TrimSpace(strings.Join(texts, ""))
	if output == "" || sessionID == "" {
		return "", "", fmt.Errorf("OpenCode returned no text or Session identity")
	}
	return bounded(output, limit), sessionID, nil
}

func (d *Driver) require(handle sessiondriver.SessionHandle) (record, error) {
	if err := sessiondriver.ValidateSessionHandle(handle); err != nil {
		return record{}, err
	}
	if handle.Driver != d.name {
		return record{}, sessiondriver.Conflict("Driver binding changed")
	}
	r, err := d.read(handle.ID)
	if err != nil {
		return record{}, err
	}
	if r.Revision != handle.Revision || r.Target != handle.Target || r.Identity.Generation != handle.Generation {
		return record{}, sessiondriver.Conflict("session handle is stale")
	}
	return r, nil
}
func (d *Driver) lock(id string) *sync.Mutex {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locks[id] == nil {
		d.locks[id] = &sync.Mutex{}
	}
	return d.locks[id]
}
func (d *Driver) recordPath(id string) string {
	return filepath.Join(d.config.StateRoot, "sessions", d.name, id, "state.json")
}
func (d *Driver) turnDir(r record) (string, error) {
	if !safe(r.HandleID) || !safe(r.LastTurnID) {
		return "", fmt.Errorf("unsafe Session identity")
	}
	return filepath.Join(d.config.StateRoot, "sessions", d.name, r.HandleID, "turns", r.LastTurnID), nil
}
func (d *Driver) read(id string) (record, error) {
	data, err := readBounded(d.recordPath(id), 256*1024)
	if os.IsNotExist(err) {
		return record{}, sessiondriver.Lost("%s session is not registered", d.name)
	}
	if err != nil {
		return record{}, err
	}
	var r record
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return record{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return record{}, fmt.Errorf("%s Session record contains trailing data", d.name)
	}
	if r.SchemaVersion != recordVersion || r.Driver != d.name || r.HandleID != id || r.Revision == 0 {
		return record{}, fmt.Errorf("invalid %s Session record", d.name)
	}
	return r, nil
}
func (d *Driver) writeInitial(r record) error {
	path := d.recordPath(r.HandleID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
func (d *Driver) write(r record) error {
	path := d.recordPath(r.HandleID)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_ = tmp.Chmod(0o600)
	if _, err = tmp.Write(append(data, '\n')); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (d *Driver) discover() (string, string, error) {
	name := strings.TrimSpace(d.config.Executable)
	if name == "" {
		name = strings.TrimSpace(os.Getenv("FISHYUME_" + strings.ToUpper(d.name) + "_PATH"))
	}
	if name == "" {
		name = d.executableName
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", "", fmt.Errorf("%s CLI was not found: %w", d.name, err)
	}
	path, _ = filepath.Abs(path)
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	if harnessShim(path) {
		if native := nativeHarness(d.name, path); native != "" {
			path = native
		} else {
			return "", "", fmt.Errorf("%s CLI path is a script shim; set FISHYUME_%s_PATH to the native executable", d.name, strings.ToUpper(d.name))
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", "", err
	}
	return filepath.Clean(path), hex.EncodeToString(hash.Sum(nil)), nil
}
func harnessShim(path string) bool {
	if runtime.GOOS == "windows" {
		return !strings.EqualFold(filepath.Ext(path), ".exe")
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	prefix := make([]byte, 2)
	_, _ = file.Read(prefix)
	return string(prefix) == "#!"
}

func nativeHarness(name, shim string) string {
	base := filepath.Dir(shim)
	candidates := []string{}
	binary := name
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	architecture := runtime.GOARCH
	if architecture == "amd64" {
		architecture = "x64"
	}
	var suffixes []string
	if name == "claude" {
		platform := runtime.GOOS
		if platform == "windows" {
			platform = "win32"
		}
		packageName := "claude-code-" + platform + "-" + architecture
		suffixes = []string{
			filepath.Join("node_modules", "@anthropic-ai", "claude-code", "node_modules", "@anthropic-ai", packageName, binary),
			filepath.Join("node_modules", "@anthropic-ai", packageName, binary),
			filepath.Join("node_modules", "@anthropic-ai", packageName, "bin", binary),
			// The top-level bin/claude.exe may be an old wrapper after npm
			// repairs. Use it only when the platform optional package is absent.
			filepath.Join("node_modules", "@anthropic-ai", "claude-code", "bin", binary),
		}
	} else {
		packageName := "opencode-" + runtime.GOOS + "-" + architecture
		suffixes = []string{
			filepath.Join("node_modules", "opencode-ai", "node_modules", packageName, "bin", binary),
			filepath.Join("node_modules", "opencode-ai", "node_modules", packageName+"-baseline", "bin", binary),
			filepath.Join("node_modules", packageName, "bin", binary),
			filepath.Join("node_modules", packageName+"-baseline", "bin", binary),
		}
	}
	for directory := base; ; directory = filepath.Dir(directory) {
		for _, suffix := range suffixes {
			candidates = append(candidates, filepath.Join(directory, suffix))
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			abs, _ := filepath.Abs(candidate)
			return abs
		}
	}
	return ""
}

func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory")
	}
	return filepath.Clean(resolved), nil
}
func randomID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b), nil
}
func uuid() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
func safe(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func readBounded(path string, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max || !utf8.Valid(data) {
		return nil, fmt.Errorf("file is invalid or exceeds %d bytes", max)
	}
	return data, nil
}
func bounded(value string, max int) string {
	data := []byte(value)
	if len(data) <= max {
		return value
	}
	data = data[:max]
	for !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data)
}
func contribution(content string) (string, error) {
	data, err := json.Marshal(teamcontract.ContributionV1{SchemaVersion: teamcontract.SchemaVersion, Status: teamcontract.ContributionCompleted, ContentMarkdown: content})
	return string(data), err
}
func sessionPrompt(prompt string) string {
	return prompt + "\n\nFishyume Team contribution protocol:\nReturn only the public Markdown contribution. Do not modify files, run shell commands, use the network, delegate work, or include hidden reasoning."
}

var _ sessiondriver.Driver = (*Driver)(nil)
