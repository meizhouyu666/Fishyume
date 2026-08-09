package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	CancelResponseCompleted = "completed"
	CancelResponseFailed    = "failed"
)

type CancelRequest struct {
	ID                   string    `json:"id"`
	RequestedAt          time.Time `json:"requestedAt"`
	ExpectedStateVersion *uint64   `json:"expectedStateVersion,omitempty"`
	ActionID             string    `json:"actionId,omitempty"`
	ActionRequestHash    string    `json:"actionRequestHash,omitempty"`
}

type CancelResponse struct {
	RequestID string    `json:"requestId"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Store) CancelRequestPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "cancel.request.json")
}

func (s *Store) CancelResponsePath(runID string) string {
	return filepath.Join(s.RunDir(runID), "cancel.response.json")
}

func (s *Store) RequestCancellation(runID string, requestedAt time.Time) (CancelRequest, error) {
	return s.RequestCancellationWithPrecondition(runID, requestedAt, nil, "", "")
}

func (s *Store) RequestCancellationWithPrecondition(runID string, requestedAt time.Time, expectedStateVersion *uint64, actionID, actionRequestHash string) (CancelRequest, error) {
	if err := validateID("run", runID); err != nil {
		return CancelRequest{}, err
	}
	path := s.CancelRequestPath(runID)
	var request CancelRequest
	err := withLeaseGuard(path, func() error {
		if err := readJSON(path, &request); err == nil {
			if err := validateCancelRequest(request); err != nil {
				return err
			}
			if expectedStateVersion != nil && (request.ActionID != actionID || request.ActionRequestHash != actionRequestHash) {
				return fmt.Errorf("cancellation request is already pending for a different action")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		id, err := newCancelRequestID()
		if err != nil {
			return err
		}
		request = CancelRequest{ID: id, RequestedAt: requestedAt.UTC(), ExpectedStateVersion: expectedStateVersion, ActionID: actionID, ActionRequestHash: actionRequestHash}
		if err := os.Remove(s.CancelResponsePath(runID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale cancel response: %w", err)
		}
		return s.writeJSON(path, request)
	})
	return request, err
}

func (s *Store) ReadCancellationRequest(runID string) (CancelRequest, error) {
	if err := validateID("run", runID); err != nil {
		return CancelRequest{}, err
	}
	var request CancelRequest
	if err := readJSON(s.CancelRequestPath(runID), &request); err != nil {
		return CancelRequest{}, err
	}
	return request, validateCancelRequest(request)
}

func (s *Store) ResolveCancellation(runID string, response CancelResponse) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	if response.RequestID == "" || (response.Status != CancelResponseCompleted && response.Status != CancelResponseFailed) || response.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid cancel response")
	}
	path := s.CancelRequestPath(runID)
	return withLeaseGuard(path, func() error {
		if err := s.writeJSON(s.CancelResponsePath(runID), response); err != nil {
			return err
		}
		var request CancelRequest
		if err := readJSON(path, &request); err == nil {
			if request.ID != response.RequestID {
				return fmt.Errorf("cancel request changed from %q to %q", response.RequestID, request.ID)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove resolved cancel request: %w", err)
		}
		return nil
	})
}

func (s *Store) ReadCancellationResponse(runID, requestID string) (CancelResponse, error) {
	if err := validateID("run", runID); err != nil {
		return CancelResponse{}, err
	}
	var response CancelResponse
	// ResolveCancellation writes the response before removing the request so a
	// failed response write never loses durable cancellation intent. Read under
	// the same guard to prevent callers from observing that response during the
	// short interval before request cleanup completes.
	if err := withLeaseGuard(s.CancelRequestPath(runID), func() error {
		return readJSON(s.CancelResponsePath(runID), &response)
	}); err != nil {
		return CancelResponse{}, err
	}
	if response.RequestID != requestID {
		return CancelResponse{}, fmt.Errorf("cancel response belongs to request %q", response.RequestID)
	}
	return response, nil
}

func validateCancelRequest(request CancelRequest) error {
	if request.ID == "" || request.RequestedAt.IsZero() {
		return fmt.Errorf("cancel request is incomplete")
	}
	if (request.ActionID == "") != (request.ActionRequestHash == "") {
		return fmt.Errorf("cancel request action identity is incomplete")
	}
	return nil
}

func newCancelRequestID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate cancel request id: %w", err)
	}
	return "cancel-" + hex.EncodeToString(value), nil
}
