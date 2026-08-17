//go:build windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

var (
	lockFileExW   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	unlockFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
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
	var overlapped syscall.Overlapped
	const lockFileExclusiveLock = 0x2
	result, _, callErr := lockFileExW.Call(file.Fd(), lockFileExclusiveLock, 0, 1, 0, uintptr(unsafePointer(&overlapped)))
	if result == 0 {
		return fmt.Errorf("lock file %q: %w", lockPath, callErr)
	}
	defer func() { _, _, _ = unlockFileExW.Call(file.Fd(), 0, 1, 0, uintptr(unsafePointer(&overlapped))) }()
	return action()
}
