package main

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestCapacityPreflightDetectsCapturedPhysicalContact(t *testing.T) {
	from := navigation.Vec3{X: -116.5, Y: .10000038146972656, Z: -94.25}
	to := navigation.Vec3{X: -116.25, Y: .10000038146972656, Z: -91.25}
	wall := geom.Volume{ID: "capacity-divider-01", Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -116, Y: -2, Z: -93.8}, Max: geom.Vec3{X: 124, Y: 4, Z: -91.8}}}
	evidence, err := capacityPhysicalClearance(from, []navigation.Corner{{Position: to}}, geom.Profile{Radius: .4, Height: 1.8}, []geom.Volume{wall})
	if err != nil || evidence.Violations != 1 || evidence.FirstViolation == nil || evidence.FirstViolation.Obstacle != wall.ID || evidence.FirstViolation.Segment != 0 {
		t.Fatalf("captured source collision was not reported: %+v %v", evidence, err)
	}
	if separation := evidence.FirstViolation.MinimumSeparationMetres; math.Abs(separation-(.294811453494345-.4)) > 1e-9 {
		t.Fatalf("exact captured clearance differs: %.15g", separation)
	}
	if evidence.FirstViolation.From.X != from.X || evidence.FirstViolation.To.Z != to.Z || evidence.FirstViolation.ContactFraction <= 0 || evidence.FirstViolation.ContactFraction >= 1 {
		t.Fatalf("missing replay coordinates/contact: %+v", evidence.FirstViolation)
	}
}

func TestCapacityClearanceMatchesCircularAndVerticalMotor(t *testing.T) {
	wall := geom.Volume{ID: "wall", Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{}, Max: geom.Vec3{X: 2, Y: 2, Z: 2}}}
	for _, tc := range []struct {
		name     string
		from, to navigation.Vec3
		wantHit  bool
	}{
		{"clear rounded corner", navigation.Vec3{X: -.6, Z: -.2}, navigation.Vec3{X: -.2, Z: -.6}, false},
		{"face tangent", navigation.Vec3{X: -.4, Z: -1}, navigation.Vec3{X: -.4, Z: 3}, false},
		{"vertical separation", navigation.Vec3{X: -1, Y: 3, Z: 1}, navigation.Vec3{X: 3, Y: 3, Z: 1}, false},
		{"high speed contact", navigation.Vec3{X: -100, Z: 1}, navigation.Vec3{X: 100, Z: 1}, true},
		{"initial overlap escaping", navigation.Vec3{X: -.2, Z: 1}, navigation.Vec3{X: -1, Z: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := capacityPhysicalClearance(tc.from, []navigation.Corner{{Position: tc.to}}, geom.Profile{Radius: .4, Height: 1.8}, []geom.Volume{wall})
			if err != nil || (got.Violations != 0) != tc.wantHit {
				t.Fatalf("physical route validity: %+v %v", got, err)
			}
		})
	}
	if d, ok := segmentCylinderSeparation(surface.Vec3{X: -1, Z: 1}, surface.Vec3{X: 3, Z: 1}, .4, 1.8, wall.Bounds); !ok || math.Abs(d-(-1.4)) > 1e-12 {
		t.Fatalf("interior signed separation: %g %v", d, ok)
	}
}
