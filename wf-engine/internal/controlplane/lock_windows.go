//go:build windows

package controlplane

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

type ownerLock struct{ file *os.File }

func acquireOwnerLock(path string) (*ownerLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return nil, ErrOwnerActive
		}
		return nil, fmt.Errorf("lock control plane owner: %w", err)
	}
	return &ownerLock{file: file}, nil
}

func (l *ownerLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	overlapped := new(windows.Overlapped)
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, overlapped)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
