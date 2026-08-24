package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DefaultLeaseTTL = 15 * time.Second

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type LeaseRecord struct {
	OwnerID   string    `json:"ownerId"`
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"createdAt"`
	Heartbeat time.Time `json:"heartbeat"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type LeaseConflictError struct{ Current LeaseRecord }

func (e *LeaseConflictError) Error() string {
	return fmt.Sprintf("run is controlled by %q (pid %d) until %s; wait for expiry or use status", e.Current.Command, e.Current.PID, e.Current.ExpiresAt.Format(time.RFC3339))
}

type LeaseManager struct {
	store                *Store
	clock                Clock
	ttl                  time.Duration
	owner                func() (string, error)
	beforeHeartbeatGuard func()
	processAlive         func(int) (bool, error)
}

func NewLeaseManager(store *Store) *LeaseManager {
	return &LeaseManager{store: store, clock: realClock{}, ttl: DefaultLeaseTTL, owner: newLeaseOwner, processAlive: platformProcessAlive}
}

func NewLeaseManagerForTest(store *Store, clock Clock, ttl time.Duration, owner func() (string, error)) *LeaseManager {
	return &LeaseManager{store: store, clock: clock, ttl: ttl, owner: owner, processAlive: platformProcessAlive}
}

func (m *LeaseManager) Acquire(runID, command string) (*Lease, error) {
	return m.acquire(runID, command, false)
}

// AcquireRecovery may replace an unexpired lease only when its recorded
// process is confirmed dead. A live or unverifiable owner remains a conflict.
func (m *LeaseManager) AcquireRecovery(runID, command string) (*Lease, error) {
	return m.acquire(runID, command, true)
}

func (m *LeaseManager) acquire(runID, command string, recovery bool) (*Lease, error) {
	if err := validateID("run", runID); err != nil {
		return nil, err
	}
	if command == "" {
		return nil, fmt.Errorf("lease command is required")
	}
	ownerID, err := m.owner()
	if err != nil {
		return nil, err
	}
	now := m.clock.Now().UTC()
	record := LeaseRecord{OwnerID: ownerID, PID: os.Getpid(), Command: command, CreatedAt: now, Heartbeat: now, ExpiresAt: now.Add(m.ttl)}
	path := filepath.Join(m.store.RunDir(runID), "control.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lease directory: %w", err)
	}
	var lease *Lease
	err = withLeaseGuard(path, func() error {
		current, readErr := readLease(path)
		if readErr == nil {
			if current.ExpiresAt.After(now) {
				if !recovery {
					return &LeaseConflictError{Current: current}
				}
				alive, aliveErr := m.processAlive(current.PID)
				if aliveErr != nil || alive {
					return &LeaseConflictError{Current: current}
				}
			}
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return fmt.Errorf("remove expired control lease: %w", removeErr)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read existing control lease: %w", readErr)
		}
		if faultErr := m.store.injectFault("lease_acquire", path); faultErr != nil {
			return faultErr
		}
		if createErr := createLeaseExclusive(path, record); createErr != nil {
			return fmt.Errorf("create control lease: %w", createErr)
		}
		lease = &Lease{manager: m, runID: runID, record: record}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

type Lease struct {
	manager *LeaseManager
	runID   string
	record  LeaseRecord
}

func (l *Lease) Record() LeaseRecord { return l.record }

// Owns reports whether the durable control lease is still bound to this
// handle. It never mutates or replaces another owner's lease.
func (l *Lease) Owns() (bool, error) {
	path := filepath.Join(l.manager.store.RunDir(l.runID), "control.lock")
	current, err := readLease(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.OwnerID == l.record.OwnerID && current.ExpiresAt.After(l.manager.clock.Now().UTC()), nil
}

// Bound reports whether the durable control lease still names this handle's
// owner. An expired lease remains bound until another owner replaces it.
func (l *Lease) Bound() (bool, error) {
	path := filepath.Join(l.manager.store.RunDir(l.runID), "control.lock")
	current, err := readLease(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.OwnerID == l.record.OwnerID, nil
}

func (l *Lease) Heartbeat() error {
	path := filepath.Join(l.manager.store.RunDir(l.runID), "control.lock")
	if err := l.manager.store.injectFault("lease_heartbeat", path); err != nil {
		return err
	}
	current, err := readLease(path)
	if err != nil {
		return err
	}
	if current.OwnerID != l.record.OwnerID {
		return fmt.Errorf("control lease is no longer owned by %q", l.record.OwnerID)
	}
	if l.manager.beforeHeartbeatGuard != nil {
		l.manager.beforeHeartbeatGuard()
	}
	return withLeaseGuard(path, func() error {
		current, err := readLease(path)
		if err != nil {
			return err
		}
		if current.OwnerID != l.record.OwnerID {
			return fmt.Errorf("control lease is no longer owned by %q", l.record.OwnerID)
		}
		now := l.manager.clock.Now().UTC()
		updated := l.record
		updated.Heartbeat = now
		updated.ExpiresAt = now.Add(l.manager.ttl)
		if err := l.manager.store.writeJSON(path, updated); err != nil {
			return err
		}
		l.record = updated
		return nil
	})
}

func (l *Lease) KeepAlive(ctx context.Context) <-chan error {
	errorsChannel := make(chan error, 1)
	interval := l.manager.ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		defer close(errorsChannel)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := l.Heartbeat(); err != nil {
					errorsChannel <- err
					return
				}
			}
		}
	}()
	return errorsChannel
}

func (l *Lease) Release() error {
	path := filepath.Join(l.manager.store.RunDir(l.runID), "control.lock")
	return withLeaseGuard(path, func() error {
		current, err := readLease(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.OwnerID != l.record.OwnerID {
			return fmt.Errorf("refusing to release lease owned by %q", current.OwnerID)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("release control lease: %w", err)
		}
		return nil
	})
}

func createLeaseExclusive(path string, record LeaseRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func readLease(path string) (LeaseRecord, error) {
	var record LeaseRecord
	if err := readJSON(path, &record); err != nil {
		return record, err
	}
	if record.OwnerID == "" || record.ExpiresAt.IsZero() {
		return record, fmt.Errorf("control lease is incomplete")
	}
	return record, nil
}

func newLeaseOwner() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate lease owner: %w", err)
	}
	return hex.EncodeToString(value), nil
}
