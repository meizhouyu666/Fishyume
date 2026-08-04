package ccpanes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ControlPathEnv     = "WF_CCPANES_CTL"
	ProfileIDEnv       = "FISHYUME_CCPANES_PROFILE_ID"
	LegacyProfileIDEnv = "WF_CCPANES_PROFILE_ID"
)

func ResolveProfileID() (string, error) {
	if configured, ok := os.LookupEnv(ProfileIDEnv); ok {
		return validateProfileID(ProfileIDEnv, configured)
	}
	if configured, ok := os.LookupEnv(LegacyProfileIDEnv); ok {
		return validateProfileID(LegacyProfileIDEnv, configured)
	}
	return "", fmt.Errorf("Fishyume requires a dedicated non-interactive CC-Panes launch profile; create one in CC-Panes and set %s to its exact profile ID", ProfileIDEnv)
}

func validateProfileID(name, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is invalid: set it to the exact non-empty CC-Panes profile ID", name)
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s is invalid: the CC-Panes profile ID must not contain leading or trailing whitespace", name)
	}
	return value, nil
}

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
