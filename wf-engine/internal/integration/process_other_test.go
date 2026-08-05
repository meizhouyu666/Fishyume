//go:build !windows && !linux

package integration_test

func processAlive(int) (bool, error) { return false, nil }
