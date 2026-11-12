package box3d_test

import (
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestRisingSupportClipsPassengerTransportUnderCeiling(t *testing.T) {
	w := world(t)
	platform, platformCollider := add(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
	_, ceiling := add(t, w, p.Static, p.Vec3{Y: 2.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
	passenger, capsule := capsuleController(t, w, p.Kinematic, p.Vec3{Y: .901})
	if err := w.SetVelocity(platform, p.Vec3{Y: 10}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	for frame := uint64(1); frame <= 5; frame++ {
		r := step(t, w, frame)
		body, _ := w.Body(passenger)
		support, _ := w.Body(platform)
		if body.Pose.Position.Y > 1.101 || body.Pose.Position.Y-.9 < support.Pose.Position.Y+.25-.0001 {
			t.Fatalf("passenger crushed or crossed ceiling: passenger=%+v platform=%+v", body, support)
		}
		if !body.Support.Grounded || body.Support.Collider != platformCollider || body.Support.Body != platform {
			t.Fatalf("transport lost exact support identity: %+v", body.Support)
		}
		if r.Elapsed != .05 {
			t.Fatalf("blocked transport changed elapsed time: %+v", r)
		}
		for _, other := range []p.ColliderHandle{platformCollider, ceiling} {
			contact, err := w.Penetration(capsule, other)
			if err != nil || contact.Depth > .0001 {
				t.Fatalf("transport leaves penetration: %+v error=%v", contact, err)
			}
		}
	}
	body, _ := w.Body(passenger)
	if body.Pose.Position.Y < 1.05 {
		t.Fatalf("support stopped before using available headroom: %+v", body)
	}
	if err := w.DestroyBody(platform, p.Deleted); err != nil {
		t.Fatal(err)
	}
	step(t, w, 6)
	afterRemoval, _ := w.Body(passenger)
	if afterRemoval.Support.Grounded || afterRemoval.Pose.Position.Y >= body.Pose.Position.Y {
		t.Fatalf("removed support retained passenger or blocked falling: %+v", afterRemoval)
	}
}

func TestRotatingSupportClipsPassengerTransportUnderCeiling(t *testing.T) {
	w := world(t)
	platform, platformCollider := add(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
	_, ceiling := add(t, w, p.Static, p.Vec3{Y: 2.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 3, Y: .25, Z: 3}})
	passenger, capsule := capsuleController(t, w, p.Kinematic, p.Vec3{X: 1, Y: .901})
	if err := w.SetVelocity(platform, p.Vec3{}, p.Vec3{Z: 4}); err != nil {
		t.Fatal(err)
	}
	for frame := uint64(1); frame <= 5; frame++ {
		step(t, w, frame)
		body, _ := w.Body(passenger)
		if body.Pose.Position.Y > 1.101 || !body.Support.Grounded || body.Support.Collider != platformCollider {
			t.Fatalf("rotating transport lost support or crossed ceiling: %+v", body)
		}
		for _, other := range []p.ColliderHandle{platformCollider, ceiling} {
			contact, err := w.Penetration(capsule, other)
			if err != nil || contact.Depth > .0001 {
				t.Fatalf("rotating transport leaves penetration: %+v error=%v", contact, err)
			}
		}
	}
	body, _ := w.Body(passenger)
	if body.Pose.Position.Y < 1.05 {
		t.Fatalf("rotating support stopped before using available headroom: %+v", body)
	}
}
