//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func withLeaseGuard(leasePath string, action func() error) error {
	return withFileLock(leasePath+".guard", action)
}

func withFileLock(lockPath string, action func() error) error {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open file lock %q: %w", lockPath, err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock file %q: %w", lockPath, err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return action()
}
