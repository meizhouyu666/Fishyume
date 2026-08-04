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
	guardPath := leasePath + ".guard"
	file, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lease guard %q: %w", guardPath, err)
	}
	defer file.Close()
	var overlapped syscall.Overlapped
	const lockFileExclusiveLock = 0x2
	result, _, callErr := lockFileExW.Call(file.Fd(), lockFileExclusiveLock, 0, 1, 0, uintptr(unsafePointer(&overlapped)))
	if result == 0 {
		return fmt.Errorf("lock lease guard %q: %w", guardPath, callErr)
	}
	defer func() { _, _, _ = unlockFileExW.Call(file.Fd(), 0, 1, 0, uintptr(unsafePointer(&overlapped))) }()
	return action()
}
