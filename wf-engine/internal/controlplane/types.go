package controlplane

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wf.local/wf-engine/internal/agent"
)

const (
	ProtocolVersion = 1
	StateSchema     = 3
	MetadataName    = "control-plane.json"
	LockName        = "control-plane.lock"
)

var ErrOwnerActive = errors.New("control plane owner is active")

type OwnerRecord struct {
	ProtocolVersion    int       `json:"protocolVersion"`
	RPCProtocolVersion int       `json:"rpcProtocolVersion"`
	StateSchema        int       `json:"stateSchema"`
	EngineVersion      string    `json:"engineVersion"`
	OwnerID            string    `json:"ownerId"`
	StateDirHash       string    `json:"stateDirHash"`
	StateDir           string    `json:"stateDir"`
	Endpoint           string    `json:"endpoint"`
	Transport          string    `json:"transport"`
	PID                int       `json:"pid"`
	UserIdentity       string    `json:"userIdentity"`
	CreatedAt          time.Time `json:"createdAt"`
}

type HandshakeRequest struct {
	ProtocolVersion    int    `json:"protocolVersion"`
	RPCProtocolVersion int    `json:"rpcProtocolVersion"`
	StateSchema        int    `json:"stateSchema"`
	EngineVersion      string `json:"engineVersion"`
	OwnerID            string `json:"ownerId"`
	StateDirHash       string `json:"stateDirHash"`
}

type HandshakeResponse struct {
	OK            bool               `json:"ok"`
	Handshake     agent.IPCHandshake `json:"handshake"`
	EngineVersion string             `json:"engineVersion"`
	Error         string             `json:"error,omitempty"`
}

func canonicalStateDir(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve state directory %q: %w", root, err)
	}
	return filepath.Clean(absolute), nil
}

func StateDirHash(root string) (string, error) {
	canonical, err := canonicalStateDir(root)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func newOwnerID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate control plane owner identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func validateOwner(record OwnerRecord) error {
	handshake := agent.IPCHandshake{
		ProtocolVersion: record.ProtocolVersion,
		StateSchema:     record.StateSchema,
		OwnerID:         record.OwnerID,
		StateDirHash:    record.StateDirHash,
	}
	if err := agent.ValidateIPCHandshake(handshake); err != nil {
		return err
	}
	if record.RPCProtocolVersion < 1 || strings.TrimSpace(record.EngineVersion) == "" || strings.TrimSpace(record.StateDir) == "" || strings.TrimSpace(record.Endpoint) == "" || strings.TrimSpace(record.Transport) == "" || record.PID <= 0 || strings.TrimSpace(record.UserIdentity) == "" || record.CreatedAt.IsZero() {
		return errors.New("control plane owner record is incomplete")
	}
	return nil
}

func readOwner(path string) (OwnerRecord, error) {
	var record OwnerRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, fmt.Errorf("decode control plane owner record: %w", err)
	}
	if err := validateOwner(record); err != nil {
		return record, err
	}
	return record, nil
}

func writeOwner(path string, record OwnerRecord) error {
	if err := validateOwner(record); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".control-plane-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceOwnerFile(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
