//go:build !windows

package directcli

func isTransientFileAccess(error) bool { return false }
