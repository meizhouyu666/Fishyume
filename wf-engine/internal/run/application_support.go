package run

import (
	"context"
	"encoding/json"
	"fmt"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

// ApplicationJournal exposes the durable state repository only to the
// in-process Application Service composition root.
func (s *Service) ApplicationJournal() *store.Store { return s.store }

type DriverCapabilityReport struct {
	Driver                   string
	Targets                  []string
	Ready                    bool
	Diagnostic               string
	MaxConcurrentAgents      int
	SupportsConcurrentCancel bool
}

func (s *Service) DriverCapabilityReports(ctx context.Context, project string) []DriverCapabilityReport {
	names := s.registry.Names()
	reports := make([]DriverCapabilityReport, 0, len(names))
	for _, name := range names {
		candidate, err := s.registry.Get(name)
		if err != nil {
			continue
		}
		capabilities := candidate.Capabilities()
		doctor := candidate.Doctor(ctx, backendDoctorRequest(project))
		reports = append(reports, DriverCapabilityReport{
			Driver: name, Targets: append([]string(nil), capabilities.Runtimes...), Ready: doctor.Ready,
			Diagnostic: doctorDiagnostic(doctor), MaxConcurrentAgents: capabilities.MaxConcurrentAgents,
			SupportsConcurrentCancel: capabilities.SupportsConcurrentCancel,
		})
	}
	return reports
}

func backendDoctorRequest(project string) backend.DoctorRequest {
	return backend.DoctorRequest{Workspace: project}
}

func (s *Service) ListRunIDs() ([]string, error) {
	if s.store == nil {
		return nil, fmt.Errorf("workflow state directory is unavailable")
	}
	return s.store.ListRunIDs()
}

func (s *Service) ReadEvents(runID string) ([]WorkflowEvent, error) {
	if s.store == nil {
		return nil, fmt.Errorf("workflow state directory is unavailable")
	}
	events := make([]WorkflowEvent, 0)
	var previous uint64
	err := s.store.ReadEvents(runID, func(raw json.RawMessage) error {
		var event WorkflowEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.RunID != runID || event.Sequence <= previous {
			return fmt.Errorf("event sequence is not strictly increasing")
		}
		previous = event.Sequence
		events = append(events, event)
		return nil
	})
	return events, err
}

func (s *Service) ReadAttempt(runID, nodeID string, number int) (AttemptSnapshot, error) {
	if s.store == nil {
		return AttemptSnapshot{}, fmt.Errorf("workflow state directory is unavailable")
	}
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(runID, nodeID, number, &attempt); err != nil {
		return AttemptSnapshot{}, err
	}
	return attempt, nil
}

func (s *Service) ReadAttemptOutput(runID, nodeID string, number int) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("workflow state directory is unavailable")
	}
	return s.store.ReadNodeOutput(runID, nodeID, number, 32*1024)
}
