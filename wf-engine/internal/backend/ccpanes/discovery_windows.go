//go:build windows

package ccpanes

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func discoverFromRunningProcess() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-Process -Name 'cc-panes' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty Path)")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("inspect running cc-panes process: %w", err)
	}
	processPath := strings.TrimSpace(stdout.String())
	if processPath == "" {
		return "", fmt.Errorf("cc-panes.exe is not running")
	}
	candidate := filepath.Join(filepath.Dir(processPath), "binaries", "cc-panes-ctl.exe")
	return validateExecutable(candidate)
}
