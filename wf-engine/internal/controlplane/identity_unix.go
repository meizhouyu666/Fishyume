//go:build !windows

package controlplane

import (
	"fmt"
	"os"
	"syscall"
)

func currentUserIdentity() (string, error) { return fmt.Sprintf("uid:%d", os.Getuid()), nil }

func secureStateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("state directory %q is not owned by the current user", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	return nil
}
