//go:build !windows

package controlplane

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func endpointFor(stateDir, stateHash, identity string) (string, string, error) {
	candidate := filepath.Join(stateDir, "control-plane-v1.sock")
	if len(candidate) < 96 {
		return candidate, "unix", nil
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	directory := filepath.Join(base, "fishyume-"+strings.TrimPrefix(identity, "uid:"))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", fmt.Errorf("create control plane socket directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", "", fmt.Errorf("secure control plane socket directory: %w", err)
	}
	return filepath.Join(directory, "cp-"+stateHash[:24]+".sock"), "unix", nil
}

func listenEndpoint(record OwnerRecord) (net.Listener, error) {
	if err := os.Remove(record.Endpoint); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale Unix socket after acquiring owner lock: %w", err)
	}
	listener, err := net.Listen("unix", record.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("listen on Unix socket %q: %w", record.Endpoint, err)
	}
	if err := os.Chmod(record.Endpoint, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(record.Endpoint)
		return nil, fmt.Errorf("secure Unix socket: %w", err)
	}
	return listener, nil
}

func dialEndpoint(endpoint string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, timeout)
}

func cleanupEndpoint(record OwnerRecord) error {
	err := os.Remove(record.Endpoint)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
