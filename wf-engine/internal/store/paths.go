package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	StateDirEnv       = "FISHYUME_STATE_DIR"
	LegacyStateDirEnv = "WF_STATE_DIR"
)

func StateRoot() (string, error) {
	override := os.Getenv(StateDirEnv)
	if override == "" {
		override = os.Getenv(LegacyStateDirEnv)
	}
	if override != "" {
		root, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve %s path %q: %w", StateDirEnv, override, err)
		}
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for workflow state: %w", err)
	}
	switch runtime.GOOS {
	case "windows":
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, "fishyume"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "fishyume"), nil
	default:
		if root := os.Getenv("XDG_STATE_HOME"); root != "" {
			return filepath.Join(root, "fishyume"), nil
		}
		return filepath.Join(home, ".local", "state", "fishyume"), nil
	}
}

// LegacyStateRoot returns the former default root for read-only compatibility lookup.
func LegacyStateRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for legacy workflow state: %w", err)
	}
	switch runtime.GOOS {
	case "windows":
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, "wf"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "wf"), nil
	default:
		if root := os.Getenv("XDG_STATE_HOME"); root != "" {
			return filepath.Join(root, "wf"), nil
		}
		return filepath.Join(home, ".local", "state", "wf"), nil
	}
}
