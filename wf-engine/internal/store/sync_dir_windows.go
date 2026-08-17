//go:build windows

package store

// MoveFileEx with MOVEFILE_WRITE_THROUGH provides the required replacement
// durability on Windows. Directories cannot be opened for Sync there.
func syncDirectory(string) error { return nil }
