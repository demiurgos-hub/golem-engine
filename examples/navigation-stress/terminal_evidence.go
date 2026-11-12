package main

import "github.com/demiurgos-hub/golem-engine/golem"

func (m *measurements) recordTerminalTransition(tick uint64, measured bool, a *actor, state golem.NavigationState, bodies *collisionWorld) {
	previous := a.lastObservedMode
	a.lastObservedMode = state.Mode
	if previous == state.Mode || state.Mode != golem.NavigationBlocked && state.Mode != golem.NavigationFailed && state.Mode != golem.NavigationUnsupported {
		return
	}
	observations, omitted, limit := &m.FailureObservations, &m.FailureObservationsOmitted, 256
	if state.Mode == golem.NavigationBlocked {
		observations, omitted, limit = &m.BlockedObservations, &m.BlockedObservationsOmitted, 64
	} else if measured {
		if state.Mode == golem.NavigationFailed {
			m.FailedStates++
		} else {
			m.UnsupportedStates++
		}
	}
	if len(*observations) >= limit {
		*omitted++
		return
	}
	observation := failureObservation{Tick: tick, Measured: measured, EntityID: a.ID, Profile: a.Profile, Mode: state.Mode, Position: a.PositionValue, Destination: state.Destination, Code: state.Status.Code, Reason: state.Status.Reason, Quarantined: state.Quarantined, Generation: state.Generation, WorldRevision: state.WorldRevision, LastPhysicalEdit: bodies.lastEdit}
	if state.Status.Err != nil {
		observation.Error = state.Status.Err.Error()
	}
	observation.PhysicalOverlaps = bodies.overlaps(a.PositionValue, a.motor.radius, a.motor.height)
	*observations = append(*observations, observation)
}
