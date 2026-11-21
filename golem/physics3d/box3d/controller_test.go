package box3d_test

import (
	"errors"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func capsuleController(t *testing.T, w *box.World, mode p.MotionMode, at p.Vec3) (p.BodyHandle, p.ColliderHandle) {
	t.Helper()
	s, err := w.CreateShape(p.ShapeDesc{ID: "capsule", Kind: p.Capsule, Radius: .4, PointA: p.Vec3{Y: -.5}, PointB: p.Vec3{Y: .5}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(mode)
	d.ID, d.Pose.Position, d.Anchor.Position.Y = "controller", at, -.9
	d.Controller = p.DefaultCharacterController(mode)
	h, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(h, s)
	c.ID = "capsule-collider"
	ch, err := w.CreateCollider(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	return h, ch
}

func TestControllerCheckpointPreservesFallingIntentAndOwnedValues(t *testing.T) {
	w := world(t)
	h, _ := capsuleController(t, w, p.Kinematic, p.Vec3{Y: 10})
	if err := w.SetCharacterVelocity(h, p.Vec3{X: 2}); err != nil {
		t.Fatal(err)
	}
	step(t, w, 1)
	capture, err := w.CaptureCheckpoint("fall", "controller-source")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Bodies[0].ControllerFallSpeed >= 0 {
		t.Fatal("fall velocity was not captured")
	}
	step(t, w, 2)
	expected, _ := w.Body(h)
	prepared, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	fresh, _ := prepared.Body("controller")
	if fresh.Support.Grounded {
		t.Fatal("ephemeral support survived restore")
	}
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	capture.Bodies[0].Description.Controller.Velocity.X = 100
	r, err := w.Simulate(p.Frame{ID: 2, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Bodies {
		r.Bodies[i].Support.Grounded = true
	}
	if err = w.Accept(2); err != nil {
		t.Fatal(err)
	}
	actual, _ := w.Body(fresh.Handle)
	if actual.Pose.Position.Sub(expected.Pose.Position).Length() > 1e-5 || actual.Support.Grounded {
		t.Fatalf("restored continuation/ownership mismatch: actual=%+v expected=%+v", actual, expected)
	}
}

func TestControllerPlacementRejectsUnsupportedTiltAndAngularVelocity(t *testing.T) {
	w := world(t)
	h, _ := capsuleController(t, w, p.Kinematic, p.Vec3{Y: 10})
	pose := p.Identity()
	pose.Position.Y = 10
	pose.Rotation = p.Quat{Z: float32(math.Sin(.1)), W: float32(math.Cos(.1))}
	if _, err := w.ValidatePlacement(h, pose, 0); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("tilted admission=%v", err)
	}
	if err := w.SetPose(h, pose); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("tilted mutation=%v", err)
	}
	if err := w.SetVelocity(h, p.Vec3{}, p.Vec3{Y: 1}); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("angular mutation=%v", err)
	}
	state, _ := w.Body(h)
	if state.Pose.Rotation != (p.Quat{W: 1}) || state.AngularVelocity != (p.Vec3{}) {
		t.Fatal("rejection changed accepted state")
	}
}

func TestMotionSegmentsPreserveCornersInsideOneConfiguredSubstep(t *testing.T) {
	w := motionWorld(t)
	h, _ := add(t, w, p.Kinematic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
	add(t, w, p.Static, p.Vec3{X: .5, Z: .5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .3, Z: .2}})
	m := p.Motion{Body: h, Segments: []p.MotionSegment{
		{From: p.Vec3{}, Target: p.Vec3{X: 1}, Delta: .00625},
		{From: p.Vec3{X: 1}, Target: p.Vec3{X: 1, Z: 1}, Delta: .00625},
	}}
	r := step(t, w, 1, m)
	s, _ := w.Body(h)
	if s.Pose.Position.Sub(p.Vec3{X: 1, Z: 1}).Length() > 1e-4 || r.Intervals <= r.Substeps || r.Elapsed != .05 {
		t.Fatalf("corner was averaged: %+v intervals=%d", s, r.Intervals)
	}
	for _, path := range r.Paths {
		if path.Body != h {
			continue
		}
		for _, point := range path.Positions {
			if point.Z > 1e-4 && point.X < .9999 {
				t.Fatalf("path cut corner: %v", path.Positions)
			}
		}
	}
}

func TestMotionPartitionDoesNotReapplyDriveOrMultiplyElapsedTime(t *testing.T) {
	var baseline p.BodyState
	for _, count := range []int{1, 40} {
		w := world(t)
		h, _ := add(t, w, p.DrivenCharacter, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .2})
		m := p.Motion{Body: h, MaxDriveForce: 2, SurfaceNormal: p.Vec3{Y: 1}}
		for i := 0; i < count; i++ {
			m.Segments = append(m.Segments, p.MotionSegment{From: p.Vec3{X: float32(i) / float32(count), Y: 10}, Target: p.Vec3{X: float32(i+1) / float32(count), Y: 10}, SurfaceNormal: p.Vec3{Y: 1}, Delta: .05 / float32(count)})
		}
		if err := w.ApplyForce(h, p.Vec3{Z: 4}, p.Vec3{Y: 10}); err != nil {
			t.Fatal(err)
		}
		r := step(t, w, 1, m)
		s, _ := w.Body(h)
		if count == 1 {
			baseline = s
		}
		if r.Elapsed != .05 || r.Substeps != 4 || s.LinearVelocity.Sub(baseline.LinearVelocity).Length() > 1e-5 {
			t.Fatalf("partition changed force duration: count=%d elapsed=%g velocity=%v baseline=%v", count, r.Elapsed, s.LinearVelocity, baseline.LinearVelocity)
		}
		if count == 40 && r.Intervals <= 4 {
			t.Fatal("test did not exercise extra intervals")
		}
	}
}

func TestControllerRejectsUnapprovedStairPathBeforePhysicalMotion(t *testing.T) {
	w := world(t)
	floorForCharacter(t, w)
	add(t, w, p.Static, p.Vec3{X: 2, Y: .1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .1, Z: 2}})
	h, _ := capsuleController(t, w, p.Kinematic, p.Vec3{Y: .901})
	r := step(t, w, 1, p.Motion{Body: h, Velocity: p.Vec3{X: 40}, Validate: func(a, b p.Vec3) bool { return a.Y < .1 && b.Y < .1 }})
	s, _ := w.Body(h)
	if len(r.RejectedMotions) != 1 || s.Anchor.Position.X > .61 || s.Anchor.Position.Y > .1 || w.Metrics().Fault != nil {
		t.Fatalf("unsafe proposed stair was not rejected privately: state=%+v rejections=%v fault=%v", s, r.RejectedMotions, w.Metrics().Fault)
	}
}

var _ p.CharacterControllerReader = (*box.World)(nil)

// TestCharacterControllerReaderReportsCurrentPolicy covers the reader the root
// space transfer uses: a rebuilt character created from another world's
// reported policy (velocity intent cleared) reports the same configuration.
func TestCharacterControllerReaderReportsCurrentPolicy(t *testing.T) {
	src, dst := world(t), world(t)
	createCharacter := func(w *box.World, cfg p.CharacterControllerConfig) p.BodyHandle {
		t.Helper()
		s, err := w.CreateShape(p.ShapeDesc{ID: "capsule", Kind: p.Capsule, Radius: .4, PointA: p.Vec3{Y: -.5}, PointB: p.Vec3{Y: .5}})
		if err != nil {
			t.Fatal(err)
		}
		d := p.DefaultBody(p.Kinematic)
		d.ID, d.Controller = "character", cfg
		h, err := w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.CreateCollider(p.DefaultCollider(h, s)); err != nil {
			t.Fatal(err)
		}
		if err = w.Synchronize(); err != nil {
			t.Fatal(err)
		}
		return h
	}
	cfg := p.DefaultCharacterController(p.Kinematic)
	cfg.StepHeight, cfg.MaxSlopeDegrees, cfg.Iterations, cfg.Velocity = .2, 30, 5, p.Vec3{X: 1}
	h := createCharacter(src, cfg)
	if got, ok, err := src.CharacterController(h); err != nil || !ok || got != cfg {
		t.Fatalf("created controller = %+v, %v, %v; want %+v", got, ok, err, cfg)
	}
	if err := src.SetCharacterVelocity(h, p.Vec3{Z: 3}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := src.CharacterController(h)
	if err != nil || !ok || got.Velocity != (p.Vec3{Z: 3}) {
		t.Fatalf("controller after velocity write = %+v, %v, %v", got, ok, err)
	}

	got.Velocity = p.Vec3{}
	rebuilt := createCharacter(dst, got)
	again, ok, err := dst.CharacterController(rebuilt)
	want := cfg
	want.Velocity = p.Vec3{}
	if err != nil || !ok || again != want {
		t.Fatalf("rebuilt controller = %+v, %v, %v; want %+v", again, ok, err, want)
	}
	if _, ok, err := dst.CharacterController(h); h.World == rebuilt.World || !errors.Is(err, p.ErrStale) || ok {
		t.Fatalf("foreign handle = %v, %v; want ErrStale", ok, err)
	}

	scripted := p.MotionPolicy{Mode: p.ScriptedAction, Controller: p.DefaultCharacterController(p.ScriptedAction), Velocity: p.VelocityClear}
	scripted.Controller.MaxHits = 7
	if err := src.SetMotionPolicy(h, scripted); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := src.CharacterController(h); err != nil || !ok || got != scripted.Controller {
		t.Fatalf("controller after policy change = %+v, %v, %v", got, ok, err)
	}
	if err := src.SetMotionPolicy(h, p.MotionPolicy{Mode: p.Kinematic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := src.CharacterController(h); err != nil || ok || got != (p.CharacterControllerConfig{}) {
		t.Fatalf("controller removed by policy = %+v, %v, %v", got, ok, err)
	}

	crate, _ := add(t, src, p.FreeDynamic, p.Vec3{X: 5}, p.ShapeDesc{ID: "box", Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}})
	if got, ok, err := src.CharacterController(crate); err != nil || ok || got != (p.CharacterControllerConfig{}) {
		t.Fatalf("non-character controller = %+v, %v, %v", got, ok, err)
	}
	if err := src.DestroyBody(crate, p.Deleted); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := src.CharacterController(crate); !errors.Is(err, p.ErrStale) || ok {
		t.Fatalf("destroyed body = %v, %v; want ErrStale", ok, err)
	}
}
