package ccpanes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const ControlPathEnv = "WF_CCPANES_CTL"

func Discover() (string, error) {
	if configured := os.Getenv(ControlPathEnv); configured != "" {
		path, err := validateExecutable(configured)
		if err != nil {
			return "", fmt.Errorf("%s is invalid: %w", ControlPathEnv, err)
		}
		return path, nil
	}
	for _, name := range []string{"cc-panes-ctl", "cc-panes-ctl.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return filepath.Abs(path)
		}
	}
	if runtime.GOOS == "windows" {
		if path, err := discoverFromRunningProcess(); err == nil && path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("cc-panes-ctl was not found; set %s to the full executable path", ControlPathEnv)
}

func validateExecutable(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory", abs)
	}
	return abs, nil
}
