//go:build !windows && !linux

package codexprocess

import (
	"context"
	"fmt"
	"os/exec"
)

func configureBackgroundCommand(command *exec.Cmd) {}

func processPlatformSupported() bool { return false }

func currentProcessIdentity(int) (processIdentity, bool, error) {
	return processIdentity{}, false, fmt.Errorf("Direct Backend process identity is not implemented on this platform")
}

func terminateProcessTree(context.Context, processRef) error {
	return fmt.Errorf("Direct Backend process termination is not implemented on this platform")
}

func terminateUnverifiedProcess(int) error { return nil }
