package main

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestCircularMotorPreservesCapturedClearCorner(t *testing.T) {
	box := geom.Volume{ID: "capacity-divider-14", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -124, Y: -2, Z: 107.26666666666668}, Max: geom.Vec3{X: 116, Y: 4, Z: 109.26666666666668}}}
	w := collisionWorld{volumes: []geom.Volume{box}}
	w.rebuildBuckets()
	for _, v := range []golem.NavigationMotion{
		{From: surface.Vec3{X: 117.25, Y: .10000038146972656, Z: 107}, Target: surface.Vec3{X: 116.5, Y: .10000038146972656, Z: 106.25}},
		{From: surface.Vec3{X: 116.80001831054688, Y: .10000038146972656, Z: 106.55001831054688}, Target: surface.Vec3{X: 116.65859985351562, Y: .10000038146972656, Z: 106.40859985351562}},
	} {
		if got := w.sweepFraction(v, .8, 2.4); got != 1 {
			t.Fatalf("captured path with1.072445m corner clearance blocked at%g", got)
		}
		if overlapsVolume(box, v.From, .8, 2.4) || overlapsVolume(box, v.Target, .8, 2.4) {
			t.Fatal("square corner incorrectly classified as circular overlap")
		}
		if after, moved, clear := w.dislodge(v.From, .8, 2.4); after != v.From || moved || !clear {
			t.Fatal("clear circular footprint was unnecessarily ejected")
		}
		motor := bodyMotor{actor: &actor{}, metrics: &measurements{}, tick: func() uint64 { return 1 }, measured: func() bool { return false }, radius: .8, height: 2.4, bodies: &w}
		if after, err := motor.MoveNavigation(v); err != nil || after != v.Target {
			t.Fatalf("motor/backoff still blocks the clear path: %v %v", after, err)
		}
	}
}

func TestCircularMotorAnalyticContactsAndVerticalIntervals(t *testing.T) {
	box := geom.Bounds{Min: geom.Vec3{}, Max: geom.Vec3{X: 2, Y: 4, Z: 2}}
	for _, test := range []struct {
		name         string
		from, target surface.Vec3
		radius       float64
		want         float64
	}{
		{"face", surface.Vec3{X: -3, Y: 1, Z: 1}, surface.Vec3{X: 3, Y: 1, Z: 1}, .5, 2.5 / 6},
		{"corner", surface.Vec3{X: -2, Y: 1, Z: -2}, surface.Vec3{X: 1, Y: 1, Z: 1}, .5, (2 - .5/math.Sqrt2) / 3},
		{"corner_tangent", surface.Vec3{X: -2, Y: 1, Z: -.5}, surface.Vec3{X: 2, Y: 1, Z: -.5}, .5, 1},
		{"boundary_separating", surface.Vec3{X: -.5, Y: 1, Z: 1}, surface.Vec3{X: -1, Y: 1, Z: 1}, .5, 1},
		{"boundary_closing", surface.Vec3{X: -.5, Y: 1, Z: 1}, surface.Vec3{X: 0, Y: 1, Z: 1}, .5, 0},
		{"boundary_parallel", surface.Vec3{X: -.5, Y: 1, Z: 1}, surface.Vec3{X: -.5, Y: 1, Z: 2}, .5, 1},
		{"overlap_separating", surface.Vec3{X: -.25, Y: 1, Z: 1}, surface.Vec3{X: -1, Y: 1, Z: 1}, .5, 1},
		{"overlap_closing", surface.Vec3{X: -.25, Y: 1, Z: 1}, surface.Vec3{X: 1, Y: 1, Z: 1}, .5, 0},
		{"overlap_stationary", surface.Vec3{X: -.25, Y: 1, Z: 1}, surface.Vec3{X: -.25, Y: 1, Z: 1}, .5, 0},
		{"nearest_vertical_closing", surface.Vec3{X: -.25, Y: 3.9, Z: 1}, surface.Vec3{X: -1, Y: 3, Z: 1}, .5, 0},
		{"nearest_vertical_separating", surface.Vec3{X: -.25, Y: 3.9, Z: 1}, surface.Vec3{X: 1, Y: 4.5, Z: 1}, .5, 1},
		{"high_speed_face", surface.Vec3{X: -10000, Y: 1, Z: 1}, surface.Vec3{X: 10000, Y: 1, Z: 1}, .5, 9999.5 / 20000},
		{"high_speed_corner", surface.Vec3{X: -10000, Y: 1, Z: -10000}, surface.Vec3{X: 10000, Y: 1, Z: 10000}, .5, (10000 - .5/math.Sqrt2) / 20000},
		{"above", surface.Vec3{X: -3, Y: 5, Z: 1}, surface.Vec3{X: 3, Y: 5, Z: 1}, .5, 1},
		{"disjoint_horizontal_vertical_times", surface.Vec3{X: -3, Y: 0, Z: 1}, surface.Vec3{X: 3, Y: 12, Z: 1}, .5, 1},
		{"downward_entry", surface.Vec3{X: 1, Y: 6, Z: 1}, surface.Vec3{X: 1, Y: -6, Z: 1}, .5, 1.0 / 6},
		{"head_height_entry", surface.Vec3{X: 1, Y: -3, Z: 1}, surface.Vec3{X: 1, Y: 6, Z: 1}, .5, 2.0 / 9},
		{"zero_radius", surface.Vec3{X: -3, Y: 1, Z: 1}, surface.Vec3{X: 3, Y: 1, Z: 1}, 0, .5},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sweepCylinderBox(test.from, test.target, test.radius, 1, box); math.Abs(got-test.want) > 1e-10 {
				t.Fatalf("contact fraction%g want%g", got, test.want)
			}
		})
	}
	point := surface.Vec3{X: -.2, Y: 1, Z: -.2}
	w := collisionWorld{volumes: []geom.Volume{{ID: "corner", Exclude: true, Bounds: box}}}
	if !overlapsVolume(w.volumes[0], point, .5, 1) {
		t.Fatal("true corner penetration missed")
	}
	if after, moved, clear := w.dislodge(point, .5, 1); !moved || !clear || overlapsVolume(w.volumes[0], after, .5, 1) {
		t.Fatal("circular penetration was not resolved")
	}
}
