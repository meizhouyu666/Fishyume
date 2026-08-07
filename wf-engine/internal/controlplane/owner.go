package controlplane

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Owner struct {
	root       string
	recordPath string
	lock       *ownerLock
	record     OwnerRecord
}

func AcquireOwner(root, engineVersion string, rpcProtocolVersion int) (*Owner, error) {
	canonical, err := canonicalStateDir(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	if err := secureStateDirectory(canonical); err != nil {
		return nil, err
	}
	lock, err := acquireOwnerLock(filepath.Join(canonical, LockName))
	if err != nil {
		if errors.Is(err, ErrOwnerActive) {
			record, readErr := readOwner(filepath.Join(canonical, MetadataName))
			if readErr != nil {
				return nil, fmt.Errorf("%w; owner metadata unavailable: %v", ErrOwnerActive, readErr)
			}
			return nil, fmt.Errorf("%w: engine=%s protocol=%d owner=%s pid=%d", ErrOwnerActive, record.EngineVersion, record.ProtocolVersion, record.OwnerID, record.PID)
		}
		return nil, err
	}
	ownerID, err := newOwnerID()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	hash, err := StateDirHash(canonical)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	identity, err := currentUserIdentity()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	endpoint, transport, err := endpointFor(canonical, hash, identity)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	record := OwnerRecord{
		ProtocolVersion: ProtocolVersion, RPCProtocolVersion: rpcProtocolVersion, StateSchema: StateSchema,
		EngineVersion: engineVersion, OwnerID: ownerID, StateDirHash: hash, StateDir: canonical,
		Endpoint: endpoint, Transport: transport, PID: os.Getpid(), UserIdentity: identity, CreatedAt: time.Now().UTC(),
	}
	return &Owner{root: canonical, recordPath: filepath.Join(canonical, MetadataName), lock: lock, record: record}, nil
}

func (o *Owner) Record() OwnerRecord { return o.record }

func (o *Owner) Publish() error {
	return writeOwner(o.recordPath, o.record)
}

func (o *Owner) Close() error {
	if o == nil || o.lock == nil {
		return nil
	}
	if current, err := readOwner(o.recordPath); err == nil && current.OwnerID == o.record.OwnerID {
		_ = os.Remove(o.recordPath)
	}
	err := o.lock.Close()
	o.lock = nil
	return err
}
