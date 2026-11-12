package main

import (
	"math"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

const displacementTolerance = 1e-5 // metres; ignores numerical jitter, not stopped agents

type tickDisplacement struct {
	Measured                                           bool
	Agents, Moving, NonHostMoving, HostMoving, Invalid uint64
	StationaryByMode                                   [8]uint64
	StationaryQuarantined                              uint64
	DistanceMetres                                     float64
}

func (d *tickDisplacement) observe(before, after surface.Vec3, state golem.NavigationState, hostMotion bool) {
	d.Agents++
	if !before.Finite() || !after.Finite() || int(state.Mode) >= len(d.StationaryByMode) {
		d.Invalid++
		return
	}
	dx, dy, dz := after.X-before.X, after.Y-before.Y, after.Z-before.Z
	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	d.DistanceMetres += distance
	if distance > displacementTolerance {
		d.Moving++
		if hostMotion {
			d.HostMoving++
		} else {
			d.NonHostMoving++
		}
	} else {
		d.StationaryByMode[state.Mode]++
		if state.Quarantined {
			d.StationaryQuarantined++
		}
	}
}

type displacementEvidence struct {
	MeasuredTicks, MovingAgentTicks, NonHostMovingAgentTicks, HostMovingAgentTicks uint64
	MinMovingAgents, MinNonHostMovingAgents, InvalidAgentTicks                     uint64
	StationaryByMode                                                               [8]uint64
	StationaryQuarantined                                                          uint64
	DistanceMetres                                                                 float64
}

func (d *displacementEvidence) add(tick tickDisplacement) {
	if !tick.Measured {
		return
	}
	if d.MeasuredTicks == 0 || tick.Moving < d.MinMovingAgents {
		d.MinMovingAgents = tick.Moving
	}
	if d.MeasuredTicks == 0 || tick.NonHostMoving < d.MinNonHostMovingAgents {
		d.MinNonHostMovingAgents = tick.NonHostMoving
	}
	d.MeasuredTicks++
	d.MovingAgentTicks += tick.Moving
	d.NonHostMovingAgentTicks += tick.NonHostMoving
	d.HostMovingAgentTicks += tick.HostMoving
	d.InvalidAgentTicks += tick.Invalid
	d.DistanceMetres += tick.DistanceMetres
	d.StationaryQuarantined += tick.StationaryQuarantined
	for mode, count := range tick.StationaryByMode {
		d.StationaryByMode[mode] += count
	}
}

func (d displacementEvidence) summary() any {
	mean, nonHostMean := 0.0, 0.0
	if d.MeasuredTicks > 0 {
		mean = float64(d.MovingAgentTicks) / float64(d.MeasuredTicks)
		nonHostMean = float64(d.NonHostMovingAgentTicks) / float64(d.MeasuredTicks)
	}
	return map[string]any{"counts": d, "minimum_moving_agents": d.MinMovingAgents, "mean_moving_agents": mean, "minimum_moving_agents_without_host_recovery": d.MinNonHostMovingAgents, "mean_moving_agents_without_host_recovery": nonHostMean, "tolerance_metres": displacementTolerance, "mode_order": []string{"Idle", "Pending", "Following", "Arrived", "Blocked", "Unsupported", "Traversing", "Failed"}, "interpretation": "Final reconciled body position minus the same tick's OnTickStart position; only complete ticks inside the measured interval. Net 3D displacement above tolerance counts as moving. Host ejection/fall motion is counted separately and excluded from simultaneous navigation movement. Static exclusion-wall motor only; no agent-agent collision or avoidance."}
}

func fullMeasuredTick(start, end, measureStart, measureEnd time.Time) bool {
	return !start.Before(measureStart) && start.Before(measureEnd) && !end.Before(start) && !end.After(measureEnd)
}
