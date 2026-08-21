//go:build windows

package codexprocess

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isTransientFileAccess(err error) bool {
	err = unwrapPathError(err)
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
