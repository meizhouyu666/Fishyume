//go:build windows

package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func endpointFor(_ string, stateHash, identity string) (string, string, error) {
	userHash := sha256.Sum256([]byte(identity))
	return `\\.\pipe\fishyume-` + hex.EncodeToString(userHash[:6]) + "-" + stateHash[:24] + "-v1", "named-pipe", nil
}

func listenEndpoint(record OwnerRecord) (net.Listener, error) {
	config := &winio.PipeConfig{
		SecurityDescriptor: fmt.Sprintf("D:P(A;;GA;;;%s)", record.UserIdentity),
		MessageMode:        false,
		InputBufferSize:    agentFrameBufferSize,
		OutputBufferSize:   agentFrameBufferSize,
	}
	listener, err := winio.ListenPipe(record.Endpoint, config)
	if err != nil {
		return nil, fmt.Errorf("listen on named pipe %q: %w", record.Endpoint, err)
	}
	return listener, nil
}

func dialEndpoint(endpoint string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(endpoint, &timeout)
}

func cleanupEndpoint(OwnerRecord) error { return nil }
