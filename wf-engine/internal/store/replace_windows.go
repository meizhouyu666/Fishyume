//go:build windows

package store

import (
	"syscall"
	"time"
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	const moveFileReplaceExisting = 0x1
	const moveFileWriteThrough = 0x8
	for attempt := 0; attempt < 20; attempt++ {
		result, _, callErr := moveFileExW.Call(
			uintptr(unsafePointer(sourcePtr)), uintptr(unsafePointer(destinationPtr)),
			uintptr(moveFileReplaceExisting|moveFileWriteThrough),
		)
		if result != 0 {
			return nil
		}
		errno, retryable := callErr.(syscall.Errno)
		if !retryable || (errno != syscall.ERROR_ACCESS_DENIED && errno != syscall.Errno(32)) {
			return callErr
		}
		time.Sleep(2 * time.Millisecond)
	}
	return syscall.ERROR_ACCESS_DENIED
}
