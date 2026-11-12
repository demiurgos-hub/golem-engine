package main

import (
	"errors"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func TestBlockedTransitionsRetainExactBoundedEvidenceWithoutFailingAgentGate(t *testing.T) {
	m := &measurements{}
	w := &collisionWorld{lastEdit: 8}
	state := golem.NavigationState{Mode: golem.NavigationBlocked, Status: surface.Status{Code: surface.Unreachable, Reason: surface.Unreachable, Err: errors.New("navigation stalled after bounded replanning")}, Generation: 12, WorldRevision: 7, Destination: surface.Vec3{X: 50}}
	for i := 0; i < 66; i++ {
		a := &actor{ID: int64(i + 1), Profile: i % 2, PositionValue: surface.Vec3{X: float64(i)}, motor: &bodyMotor{radius: .8, height: 2.4}, lastObservedMode: golem.NavigationFollowing}
		m.recordTerminalTransition(42, true, a, state, w)
		m.recordTerminalTransition(43, true, a, state, w)
	}
	if len(m.BlockedObservations) != 64 || m.BlockedObservationsOmitted != 2 || len(m.FailureObservations) != 0 || m.FailedStates != 0 || m.UnsupportedStates != 0 {
		t.Fatalf("blocked mode repeated, escaped bounds or altered failure gates: %+v", m.BlockedObservations)
	}
	got := m.BlockedObservations[1]
	if got.Tick != 42 || !got.Measured || got.Profile != 1 || got.Position.X != 1 || got.Destination != state.Destination || got.Mode != state.Mode || got.Code != state.Status.Code || got.Reason != state.Status.Reason || got.Error != state.Status.Err.Error() || got.Generation != 12 || got.WorldRevision != 7 || got.LastPhysicalEdit != 8 {
		t.Fatalf("lost post-admission terminal detail: %+v", got)
	}
	a := &actor{motor: &bodyMotor{}, lastObservedMode: golem.NavigationFollowing}
	m.recordTerminalTransition(44, true, a, golem.NavigationState{Mode: golem.NavigationUnsupported}, w)
	if m.UnsupportedStates != 1 || len(m.FailureObservations) != 1 {
		t.Fatal("actual unsupported transition no longer affects the existing failure gate")
	}
}
