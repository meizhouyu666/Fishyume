//go:build windows

package store

import "unsafe"

func unsafePointer[T any](pointer *T) unsafe.Pointer { return unsafe.Pointer(pointer) }
