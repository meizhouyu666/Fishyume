package application

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewErrorBoundsSerializedDataWithoutChangingCodeOrMessage(t *testing.T) {
	message := "stable message"
	err := NewError(CodeInvalidArgument, message, map[string]any{"detail": strings.Repeat("x", MaxErrorDataBytes*2)})
	if err.Code != CodeInvalidArgument || err.Message != message {
		t.Fatalf("stable fields changed: %+v", err)
	}
	encoded, marshalErr := json.Marshal(err.Data)
	if marshalErr != nil || len(encoded) > MaxErrorDataBytes || err.Data["truncated"] != true {
		t.Fatalf("bounded data len=%d err=%v data=%+v", len(encoded), marshalErr, err.Data)
	}
}
