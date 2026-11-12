package main

import (
	"math"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

func TestDisplacementUsesFinalPositionRatherThanModeOrVelocity(t *testing.T) {
	tick := tickDisplacement{Measured: true}
	// Following with a nonzero requested velocity can remain physically blocked.
	tick.observe(surface.Vec3{X: 2}, surface.Vec3{X: 2}, golem.NavigationState{Mode: golem.NavigationFollowing, Velocity: surface.Vec3{X: 4}}, false)
	tick.observe(surface.Vec3{}, surface.Vec3{Y: .1}, golem.NavigationState{Mode: golem.NavigationTraversing}, false)
	tick.observe(surface.Vec3{}, surface.Vec3{X: .1}, golem.NavigationState{Mode: golem.NavigationArrived}, false)
	tick.observe(surface.Vec3{}, surface.Vec3{X: 10}, golem.NavigationState{Mode: golem.NavigationPending}, true)
	tick.observe(surface.Vec3{}, surface.Vec3{X: displacementTolerance / 2}, golem.NavigationState{Mode: golem.NavigationBlocked, Quarantined: true}, false)
	tick.observe(surface.Vec3{}, surface.Vec3{X: math.NaN()}, golem.NavigationState{Mode: golem.NavigationFollowing}, false)
	if tick.Agents != 6 || tick.Moving != 3 || tick.NonHostMoving != 2 || tick.HostMoving != 1 || tick.Invalid != 1 || tick.StationaryByMode[golem.NavigationFollowing] != 1 || tick.StationaryByMode[golem.NavigationBlocked] != 1 || tick.StationaryQuarantined != 1 {
		t.Fatalf("incorrect physical displacement classification: %+v", tick)
	}
	var counts displacementEvidence
	counts.add(tick)
	counts.add(tickDisplacement{Measured: false})
	counts.add(tickDisplacement{Measured: true, Agents: 6, Moving: 1, NonHostMoving: 1})
	if counts.MeasuredTicks != 2 || counts.MinMovingAgents != 1 || counts.MinNonHostMovingAgents != 1 || counts.MovingAgentTicks != 4 || counts.NonHostMovingAgentTicks != 3 {
		t.Fatalf("warm tick or minima/means corrupted: %+v", counts)
	}
}

func TestDisplacementRequiresBothEndsOfSameMeasuredTick(t *testing.T) {
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	for _, test := range []struct {
		from, to time.Time
		want     bool
	}{
		{start.Add(-time.Nanosecond), start.Add(time.Millisecond), false},
		{start, start.Add(time.Millisecond), true},
		{start.Add(time.Millisecond), end, true},
		{end.Add(-time.Millisecond), end.Add(time.Nanosecond), false},
		{end, end.Add(time.Millisecond), false},
		{end, start, false},
	} {
		if got := fullMeasuredTick(test.from, test.to, start, end); got != test.want {
			t.Fatalf("tick %v..%v measured=%v want%v", test.from, test.to, got, test.want)
		}
	}
}

func TestCapacityDistanceIncludesWarmupAndRejectsChurn(t *testing.T) {
	o := options{Warmup: 2 * time.Minute, Duration: 10 * time.Minute}
	if got := capacityRequiredDistance(o); math.Abs(got-3268) > 1e-9 {
		t.Fatalf("normal-speed warmup and measurement margin lost: %v", got)
	}
	for _, change := range []func(*options){func(o *options) { o.Churn = .1 }, func(o *options) { o.Edits = 2 }, func(o *options) { o.Burst = 20 }} {
		changed := o
		change(&changed)
		if _, err := capacityPreflight(changed, worldgen.Fixture{Config: worldgen.Config{Family: "capacity"}}, nil); err == nil {
			t.Fatal("capacity silently accepted mixed-workload churn")
		}
	}
}
