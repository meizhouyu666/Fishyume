//go:build windows

package integration_test

import (
	"errors"

	"golang.org/x/sys/windows"
)

func processAlive(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true, nil
		}
		return false, err
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, err
	}
	switch status {
	case windows.WAIT_OBJECT_0:
		return false, nil
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	default:
		return false, errors.New("unexpected process wait status")
	}
}
