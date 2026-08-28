package backend

import (
	"context"
	"strings"
	"testing"
)

type registryBackend struct {
	name  string
	ready bool
}

func (b *registryBackend) Name() string             { return b.name }
func (*registryBackend) Capabilities() Capabilities { return Capabilities{} }
func (b *registryBackend) Doctor(context.Context, DoctorRequest) DoctorReport {
	return DoctorReport{Backend: b.name, Ready: b.ready}
}
func (*registryBackend) Start(context.Context, AgentExecutionSpec) (*ExecutionHandle, error) {
	return nil, nil
}
func (*registryBackend) Observe(context.Context, ExecutionHandle) (*ExecutionObservation, error) {
	return nil, nil
}
func (*registryBackend) Output(context.Context, ExecutionHandle, int) (string, error) { return "", nil }
func (*registryBackend) Cancel(context.Context, ExecutionHandle) (*CancelResult, error) {
	return nil, nil
}

func TestRegistryRejectsDuplicatesAndSortsNames(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"codex", "fixture"} {
		if err := registry.Register(&registryBackend{name: name, ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(&registryBackend{name: "codex"}); err == nil {
		t.Fatal("duplicate Backend was accepted")
	}
	if got := strings.Join(registry.Names(), ","); got != "codex,fixture" {
		t.Fatalf("Names() = %q", got)
	}
}

func TestRegistryKeepsUnavailableBackendsIsolated(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&registryBackend{name: "broken", ready: false}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&registryBackend{name: "ready", ready: true}); err != nil {
		t.Fatal(err)
	}
	candidate, err := registry.Get("ready")
	if err != nil || !candidate.Doctor(context.Background(), DoctorRequest{}).Ready {
		t.Fatalf("ready Backend unavailable: candidate=%v err=%v", candidate, err)
	}
	if _, err := registry.Get("missing"); err == nil || !strings.Contains(err.Error(), "available Backends: broken, ready") {
		t.Fatalf("unknown Backend error = %v", err)
	}
}
