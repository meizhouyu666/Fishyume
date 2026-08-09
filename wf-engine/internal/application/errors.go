package application

import (
	"encoding/json"
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidArgument       ErrorCode = "invalid_argument"
	CodeInvalidWorkflow       ErrorCode = "invalid_workflow"
	CodeNotFound              ErrorCode = "not_found"
	CodeConflict              ErrorCode = "conflict"
	CodeCapabilityUnavailable ErrorCode = "capability_unavailable"
	CodeNotReady              ErrorCode = "not_ready"
	CodeProtocolMismatch      ErrorCode = "protocol_mismatch"
	CodeInternal              ErrorCode = "internal"
)

var StableErrorCodes = []ErrorCode{
	CodeInvalidArgument,
	CodeInvalidWorkflow,
	CodeNotFound,
	CodeConflict,
	CodeCapabilityUnavailable,
	CodeNotReady,
	CodeProtocolMismatch,
	CodeInternal,
}

type Error struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code ErrorCode, message string, data map[string]any) *Error {
	return &Error{Code: code, Message: message, Data: boundErrorData(data)}
}

func boundErrorData(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	encoded, err := json.Marshal(data)
	if err == nil && len(encoded) <= MaxErrorDataBytes {
		return data
	}
	// Keep the stable error code and message intact while making oversized or
	// non-serializable diagnostic detail safe for every adapter to emit.
	return map[string]any{"truncated": true}
}
