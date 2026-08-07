package directcli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxSupervisorPromptBytes = 256 * 1024

type supervisorConfig struct {
	ExecutionID    string   `json:"executionId"`
	Executable     string   `json:"executable"`
	Workspace      string   `json:"workspace"`
	Args           []string `json:"args"`
	EventsPath     string   `json:"eventsPath"`
	StderrPath     string   `json:"stderrPath"`
	ReadyPath      string   `json:"readyPath"`
	ExitPath       string   `json:"exitPath"`
	MaxEventBytes  int64    `json:"maxEventBytes"`
	MaxStderrBytes int64    `json:"maxStderrBytes"`
}

// RunSupervisor is the hidden process entry point used by the Fishyume Engine
// binary. It owns Codex pipes so bounded logs and exit evidence survive an
// Engine process restart.
func RunSupervisor(configPath string) int {
	var config supervisorConfig
	if err := readJSONFile(configPath, 256*1024, &config); err != nil {
		return 125
	}
	_ = os.Remove(configPath)
	prompt, err := io.ReadAll(io.LimitReader(os.Stdin, maxSupervisorPromptBytes+1))
	if err != nil || len(prompt) > maxSupervisorPromptBytes {
		return 125
	}
	if config.ExecutionID == "" || config.Executable == "" || config.Workspace == "" || len(config.Args) == 0 {
		return 125
	}
	events, err := newBoundedLog(config.EventsPath, config.MaxEventBytes)
	if err != nil {
		return 125
	}
	defer events.Close()
	stderr, err := newBoundedLog(config.StderrPath, config.MaxStderrBytes)
	if err != nil {
		return 125
	}
	defer stderr.Close()
	command := exec.Command(config.Executable, config.Args...)
	command.Dir = config.Workspace
	command.Stdin = bytes.NewReader(prompt)
	command.Stdout = events
	command.Stderr = stderr
	// Keep the Agent and its descendants in a process group separate from the
	// supervisor. Cancellation can then stop the Agent tree first while the
	// supervisor remains alive long enough to reap the child and persist exit
	// evidence. Killing both in one Unix process group can otherwise leave an
	// orphaned zombie that still occupies the persisted child PID.
	configureBackgroundCommand(command)
	if err := command.Start(); err != nil {
		_, _ = stderr.Write([]byte("start Codex CLI: " + err.Error() + "\n"))
		return 125
	}
	identity, exists, err := currentProcessIdentity(command.Process.Pid)
	if err != nil || !exists {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 125
	}
	startedAt := time.Now().UTC()
	ready := readyRecord{ExecutionID: config.ExecutionID, Child: processRef{PID: command.Process.Pid, GroupID: identity.GroupID, Fingerprint: identity.Fingerprint, Executable: identity.Executable}, StartedAt: startedAt}
	if err := writeJSONAtomic(config.ReadyPath, ready); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 125
	}
	err = command.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 125
		}
	}
	_ = events.Close()
	_ = stderr.Close()
	resultHash, _ := executableSHA256(resultPathFromArgs(config.Args))
	record := exitRecord{ExecutionID: config.ExecutionID, ChildPID: command.Process.Pid, Fingerprint: identity.Fingerprint, ExitCode: exitCode, ResultSHA256: resultHash, ExitedAt: time.Now().UTC()}
	if err := writeJSONAtomic(config.ExitPath, record); err != nil && exitCode == 0 {
		exitCode = 125
	}
	return exitCode
}

func resultPathFromArgs(args []string) string {
	for index, value := range args {
		if value == "--output-last-message" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

type boundedLog struct {
	mu       sync.Mutex
	file     *os.File
	maxBytes int64
}

func newBoundedLog(path string, maxBytes int64) (*boundedLog, error) {
	if maxBytes < 64*1024 {
		maxBytes = 64 * 1024
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &boundedLog{file: file, maxBytes: maxBytes}, nil
}

func (w *boundedLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	original := len(data)
	info, err := w.file.Stat()
	if err != nil {
		return 0, err
	}
	if int64(len(data)) >= w.maxBytes {
		data = data[len(data)-int(w.maxBytes):]
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline+1 < len(data) {
			data = data[newline+1:]
		}
		if err := w.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := w.file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := w.file.Write(data); err != nil {
			return 0, err
		}
		return original, nil
	}
	keep := w.maxBytes - int64(len(data))
	if info.Size() > keep {
		start := info.Size() - keep
		if _, err := w.file.Seek(start, io.SeekStart); err != nil {
			return 0, err
		}
		tail, err := io.ReadAll(io.LimitReader(w.file, keep))
		if err != nil {
			return 0, err
		}
		if newline := bytes.IndexByte(tail, '\n'); newline >= 0 && newline+1 < len(tail) {
			tail = tail[newline+1:]
		}
		if err := w.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := w.file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := w.file.Write(tail); err != nil {
			return 0, err
		}
	}
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return 0, err
	}
	if _, err := w.file.Write(data); err != nil {
		return 0, err
	}
	return original, nil
}

func (w *boundedLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func tailLines(path string, lines int, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return "", err
	}
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	parts := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}

func inspectExecution(data handleData, _ artifactSet) (executionEvidence, error) {
	evidence := executionEvidence{}
	for _, ref := range []processRef{data.Supervisor, data.Child} {
		status, err := inspectProcessRef(ref)
		if err != nil {
			return evidence, err
		}
		switch status {
		case processMatched:
			evidence.Active = true
		case processMismatched:
			evidence.IdentityMismatch = true
		}
	}
	return evidence, nil
}

type executionEvidence struct {
	Active           bool
	IdentityMismatch bool
}
