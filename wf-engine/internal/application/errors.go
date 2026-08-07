package application

import "fmt"

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
	return &Error{Code: code, Message: message, Data: data}
}
