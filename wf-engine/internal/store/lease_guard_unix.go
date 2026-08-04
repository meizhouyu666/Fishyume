//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func withLeaseGuard(leasePath string, action func() error) error {
	guardPath := leasePath + ".guard"
	file, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lease guard %q: %w", guardPath, err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock lease guard %q: %w", guardPath, err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return action()
}
