//go:build !windows

package controlplane

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type ownerLock struct{ file *os.File }

func acquireOwnerLock(path string) (*ownerLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
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
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
