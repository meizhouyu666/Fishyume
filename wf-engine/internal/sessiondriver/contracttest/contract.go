// Package contracttest provides reusable acceptance tests for Session Drivers.
package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
)

type Fixture struct {
	Driver       sessiondriver.Driver
	Start        sessiondriver.StartSessionRequest
	RespondTurn  sessiondriver.StartTurnRequest
	FollowupTurn sessiondriver.StartTurnRequest
	ActiveTurn   sessiondriver.StartTurnRequest
	PollInterval time.Duration
	Timeout      time.Duration
}

func Run(t *testing.T, factory func(*testing.T) Fixture) {
	t.Helper()
	t.Run("lifecycle", func(t *testing.T) {
		fixture := factory(t)
		if err := sessiondriver.ValidateCapabilities(fixture.Driver.Capabilities()); err != nil {
			t.Fatal(err)
		}
		if fixture.PollInterval <= 0 {
			fixture.PollInterval = 10 * time.Millisecond
		}
		if fixture.Timeout <= 0 {
			fixture.Timeout = 10 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Timeout)
		defer cancel()

		started, err := fixture.Driver.StartSession(ctx, fixture.Start)
		if err != nil {
			t.Fatal(err)
		}
		first, err := fixture.Driver.StartTurn(ctx, *started, fixture.RespondTurn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Driver.ParkSession(ctx, *started); !errors.Is(err, sessiondriver.ErrConflict) {
			t.Fatalf("stale pre-turn handle was not rejected: %v", err)
		}
		responded := awaitTurn(t, ctx, fixture, first.Session, first.Turn, sessiondriver.TurnResponded)
		if responded.Output == "" {
			t.Fatal("responded Session turn has no public output")
		}
		parked, err := fixture.Driver.ParkSession(ctx, responded.Session)
		if err != nil {
			t.Fatal(err)
		}
		resumed, err := fixture.Driver.ResumeSession(ctx, *parked)
		if err != nil {
			t.Fatal(err)
		}
		followup, err := fixture.Driver.StartTurn(ctx, *resumed, fixture.FollowupTurn)
		if err != nil {
			t.Fatal(err)
		}
		followedUp := awaitTurn(t, ctx, fixture, followup.Session, followup.Turn, sessiondriver.TurnResponded)
		if followedUp.Output == "" {
			t.Fatal("directed follow-up Session turn has no public output")
		}
		active, err := fixture.Driver.StartTurn(ctx, followedUp.Session, fixture.ActiveTurn)
		if err != nil {
			t.Fatal(err)
		}
		observed := awaitTurn(t, ctx, fixture, active.Session, active.Turn, sessiondriver.TurnActive)
		if _, err := fixture.Driver.ParkSession(ctx, observed.Session); !errors.Is(err, sessiondriver.ErrConflict) {
			t.Fatalf("parking an active turn did not return a conflict: %v", err)
		}
		cancelled, err := fixture.Driver.CancelTurn(ctx, observed.Session, observed.Turn)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.State != sessiondriver.CancelConfirmed {
			t.Fatalf("Session cancellation was not confirmed: %+v", cancelled)
		}
		if _, err := fixture.Driver.CloseSession(ctx, cancelled.Session); err != nil {
			t.Fatal(err)
		}
	})
}

func awaitTurn(t *testing.T, ctx context.Context, fixture Fixture, session sessiondriver.SessionHandle, turn sessiondriver.TurnHandle, expected sessiondriver.TurnState) *sessiondriver.TurnObservation {
	t.Helper()
	for {
		observed, err := fixture.Driver.ObserveTurn(ctx, session, turn)
		if err != nil {
			t.Fatal(err)
		}
		if observed.State == expected {
			return observed
		}
		if observed.State != sessiondriver.TurnDispatching && observed.State != sessiondriver.TurnActive {
			t.Fatalf("Session turn reached %q while waiting for %q: %+v", observed.State, expected, observed)
		}
		session, turn = observed.Session, observed.Turn
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(fixture.PollInterval):
		}
	}
}
