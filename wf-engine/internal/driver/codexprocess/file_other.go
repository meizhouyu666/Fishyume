//go:build !windows

package codexprocess

func isTransientFileAccess(error) bool { return false }
