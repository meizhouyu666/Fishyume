//go:build windows

package routingconfig

import (
	"syscall"
	"time"
	"unsafe"
)

var routingMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceRoutingFile(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	const replaceExisting = 0x1
	const writeThrough = 0x8
	for attempt := 0; attempt < 20; attempt++ {
		result, _, callErr := routingMoveFileExW.Call(uintptr(unsafe.Pointer(sourcePtr)), uintptr(unsafe.Pointer(destinationPtr)), uintptr(replaceExisting|writeThrough))
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
