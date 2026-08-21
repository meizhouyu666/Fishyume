package application

import (
	"context"

	"wf.local/wf-engine/internal/run"
)

// CompatibilityStatus is a read-only decoder surface for historical snapshots
// that cannot be represented by the frozen run.get response.
func (s *Service) CompatibilityStatus(runID string) (run.StatusView, *Error) {
	view, err := s.core.Status(runID)
	if err != nil {
		return run.StatusView{}, mapCoreError(err, "run not found")
	}
	return view, nil
}

func (s *Service) CompatibilityWaitControllers(ctx context.Context) error {
	return s.core.WaitControllers(ctx)
}
