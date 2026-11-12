package box3d_test

import (
	"errors"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/character"
)

func world(t *testing.T) *box.World {
	t.Helper()
	w, e := box.New(p.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func TestFrameForcesAndSegmentsDoNotMultiplyTime(t *testing.T) {
	for _, count := range []int{1, 8} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			w := world(t)
			h, _ := add(t, w, p.DrivenCharacter, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
			m := p.Motion{Body: h, MaxDriveForce: 2, SurfaceNormal: p.Vec3{Y: 1}}
			for i := 0; i < count; i++ {
				m.Segments = append(m.Segments, p.MotionSegment{From: p.Vec3{X: float32(i) / float32(count), Y: 10}, Target: p.Vec3{X: float32(i+1) / float32(count), Y: 10}, SurfaceNormal: p.Vec3{Y: 1}, Delta: .05 / float32(count)})
			}
			if e := w.ApplyForce(h, p.Vec3{Z: 4}, p.Vec3{Y: 10}); e != nil {
				t.Fatal(e)
			}
			r := step(t, w, 1, m)
			s, _ := w.Body(h)
			if r.Elapsed != .05 || r.Substeps != 4 || math.Abs(float64(s.LinearVelocity.Y+.4905)) > 1e-5 {
				t.Fatalf("segment time multiplied: %+v", s)
			}
			if s.LinearVelocity.Z < .18 {
				t.Fatalf("force did not span full frame: %v", s.LinearVelocity)
			}
		})
	}
}
func TestFrozenBodyIsConstraintAndResumesWithoutMomentum(t *testing.T) {
	w := world(t)
	h, _ := add(t, w, p.DrivenCharacter, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if e := w.SetVelocity(h, p.Vec3{X: 20}, p.Vec3{}); e != nil {
		t.Fatal(e)
	}
	if e := w.ApplyForce(h, p.Vec3{X: 100}, p.Vec3{Y: 10}); e != nil {
		t.Fatal(e)
	}
	step(t, w, 1, p.Motion{Body: h, Frozen: true})
	s, _ := w.Body(h)
	if s.Pose.Position != (p.Vec3{Y: 10}) || s.LinearVelocity != (p.Vec3{}) {
		t.Fatal(s)
	}
	step(t, w, 2)
	s, _ = w.Body(h)
	if s.LinearVelocity.X != 0 || s.LinearVelocity.Y >= 0 {
		t.Fatalf("frozen momentum returned: %+v", s)
	}
}
func TestReturnedFrameCannotMutatePendingState(t *testing.T) {
	w := world(t)
	h, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	r, e := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if e != nil {
		t.Fatal(e)
	}
	for i := range r.Bodies {
		r.Bodies[i].Pose.Position.X = 1000
	}
	if e = w.Accept(1); e != nil {
		t.Fatal(e)
	}
	s, _ := w.Body(h)
	if s.Pose.Position.X != 0 {
		t.Fatal("caller mutated pending state")
	}
}
func TestCharacterSlidingAndDepenetration(t *testing.T) {
	w := world(t)
	add(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}})
	add(t, w, p.Static, p.Vec3{X: 2, Y: 2}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: 2, Z: 10}})
	c := character.DefaultConfig()
	c.StepHeight = 0
	start := p.Identity()
	start.Position.Y = c.Height/2 + .005
	r, e := c.Move(w, start, p.Vec3{X: 4, Z: 2}, p.QueryFilter{Mask: ^uint64(0)})
	if e != nil {
		t.Fatal(e)
	}
	if !r.Blocked || r.Pose.Position.X > 1.102 || r.Pose.Position.Z < 1.99 || !r.Grounded {
		t.Fatalf("slide result: %+v", r)
	}
	start.Position.X = 1.2
	r, e = c.Move(w, start, p.Vec3{}, p.QueryFilter{Mask: ^uint64(0)})
	if e != nil || r.Pose.Position.X > 1.102 {
		t.Fatalf("depenetration: %+v %v", r, e)
	}
}
func TestPairTouchingDistinctFromSeparatedAndPlacementThreshold(t *testing.T) {
	w := world(t)
	_, a := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	h, z := add(t, w, p.Kinematic, p.Vec3{X: 2}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	for _, offset := range []float32{0, .0001, -.0001, -.001} {
		pose := p.Identity()
		pose.Position.X = 2 + offset
		w.SetPose(h, pose)
		w.Synchronize()
		r, e := w.Penetration(a, z)
		if e != nil {
			t.Fatal(e)
		}
		if offset == 0 && !r.Touching || offset > 0 && (r.Touching || r.Penetrating) || offset < 0 && (!r.Penetrating || math.Abs(float64(r.Depth+offset)) > .0000002) {
			t.Fatalf("offset=%v %+v", offset, r)
		}
	}
}
func add(t *testing.T, w *box.World, mode p.MotionMode, position p.Vec3, s p.ShapeDesc) (p.BodyHandle, p.ColliderHandle) {
	t.Helper()
	sh, e := w.CreateShape(s)
	if e != nil {
		t.Fatal(e)
	}
	d := p.DefaultBody(mode)
	d.Pose.Position = position
	bh, e := w.CreateBody(d)
	if e != nil {
		t.Fatal(e)
	}
	ch, e := w.CreateCollider(p.DefaultCollider(bh, sh))
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Synchronize(); e != nil {
		t.Fatal(e)
	}
	return bh, ch
}
func step(t *testing.T, w *box.World, id uint64, m ...p.Motion) p.FrameResult {
	t.Helper()
	r, e := w.Simulate(p.Frame{ID: id, Delta: .05, Motions: m})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Accept(id); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestGravityContactsAndPrivateFrame(t *testing.T) {
	w := world(t)
	add(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}})
	h, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 3}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	old, _ := w.Body(h)
	r, e := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := w.Body(h)
	if before.Pose != old.Pose {
		t.Fatal("unaccepted body leaked")
	}
	if r.Substeps != 4 || r.Elapsed != .05 || len(r.Paths[0].Positions) != r.Microsteps+1 {
		t.Fatal(r)
	}
	if e = w.SetPose(h, p.Identity()); !errors.Is(e, p.ErrBusy) {
		t.Fatal(e)
	}
	if e = w.Accept(1); e != nil {
		t.Fatal(e)
	}
	contact := false
	for i := uint64(2); i < 80; i++ {
		r = step(t, w, i)
		for _, event := range r.Events {
			if event.Pair.Kind == p.SolidContact {
				contact = true
			}
		}
	}
	state, _ := w.Body(h)
	if math.Abs(float64(state.Pose.Position.Y-.5)) > .03 || !contact {
		t.Fatalf("state=%+v contact=%v", state, contact)
	}
}
func TestDrivenForceCapAndNoHover(t *testing.T) {
	w := world(t)
	h, _ := add(t, w, p.DrivenCharacter, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Capsule, Radius: .5, PointA: p.Vec3{Y: -.5}, PointB: p.Vec3{Y: .5}})
	step(t, w, 1, p.Motion{Body: h, Velocity: p.Vec3{X: 10}, SurfaceNormal: p.Vec3{Y: 1}, MaxDriveForce: 2})
	s, _ := w.Body(h)
	if s.LinearVelocity.X > .10001 || s.LinearVelocity.X < .0999 {
		t.Fatalf("force cap: %v", s.LinearVelocity)
	}
	if s.LinearVelocity.Y >= -.45 {
		t.Fatalf("unsupported character hovered: %v", s.LinearVelocity)
	}
}
func TestRejectDoesNotPublishAndRequiresRestore(t *testing.T) {
	w := world(t)
	h, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	before, _ := w.Body(h)
	if _, e := w.Simulate(p.Frame{ID: 1, Delta: .05}); e != nil {
		t.Fatal(e)
	}
	if e := w.Reject(1, errors.New("test safety failure")); e != nil {
		t.Fatal(e)
	}
	after, _ := w.Body(h)
	if after != before {
		t.Fatal("partial rollback state exposed")
	}
	if _, e := w.Simulate(p.Frame{ID: 2, Delta: .05}); !errors.Is(e, p.ErrFaulted) {
		t.Fatal(e)
	}
}
func TestStaleProposalHasNoForceOrTime(t *testing.T) {
	w := world(t)
	h, _ := add(t, w, p.DrivenCharacter, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	before, _ := w.Body(h)
	_, e := w.Simulate(p.Frame{ID: 1, Delta: .05, Motions: []p.Motion{{Body: h, Velocity: p.Vec3{X: 10}, MaxDriveForce: 100, Validate: func(a, b p.Vec3) bool { return false }}}})
	if e == nil {
		t.Fatal("rejected proposal accepted")
	}
	after, _ := w.Body(h)
	if after != before || w.Metrics().SimulatedSeconds != 0 {
		t.Fatal("rejected proposal applied effects")
	}
	step(t, w, 1)
	after, _ = w.Body(h)
	if after.LinearVelocity.X != 0 {
		t.Fatal("rejected drive leaked")
	}
}
func TestQueriesIdentityOverlapAndOverflow(t *testing.T) {
	w := world(t)
	_, c := add(t, w, p.Static, p.Vec3{X: 3}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	q := p.Query{Kind: p.Ray, Mode: p.All, Pose: p.Identity(), Translation: p.Vec3{X: 6}, Filter: p.QueryFilter{Mask: ^uint64(0)}}
	r, e := w.Query(q, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Total != 1 || !r.Incomplete || !r.Blocked {
		t.Fatal(r)
	}
	hits := make([]p.Hit, 4)
	r, e = w.Query(q, hits)
	if e != nil || r.Count != 1 || hits[0].Collider != c || math.Abs(float64(hits[0].Distance-2)) > 1e-4 {
		t.Fatalf("%+v %+v %v", r, hits, e)
	}
	q.Kind = p.Sweep
	q.Shape = p.ShapeDesc{Kind: p.Sphere, Radius: .5}
	q.Pose.Position.X = 3
	q.Translation = p.Vec3{}
	r, e = w.Query(q, hits)
	if e != nil || r.Count != 1 || !hits[0].Initial || !hits[0].Penetrating {
		t.Fatalf("stationary overlap: %+v %+v %v", r, hits, e)
	}
}
func TestWorldQualifiedHandlesAndSync(t *testing.T) {
	w, z := world(t), world(t)
	h, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if _, e := z.Body(h); !errors.Is(e, p.ErrStale) {
		t.Fatal(e)
	}
	pose := p.Identity()
	pose.Position.X = 2
	if e := w.SetPose(h, pose); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Query(p.Query{Kind: p.Ray, Pose: p.Identity()}, nil); !errors.Is(e, p.ErrBusy) {
		t.Fatal(e)
	}
	if e := w.Synchronize(); e != nil {
		t.Fatal(e)
	}
	s, _ := w.Body(h)
	if s.Pose != pose {
		t.Fatal(s)
	}
}
func TestAllJointFamiliesCreateAndStep(t *testing.T) {
	for _, kind := range []p.JointKind{p.Weld, p.Distance, p.Hinge, p.Slider, p.Spherical, p.Motor, p.Upright, p.Wheel} {
		t.Run(string(rune('0'+kind)), func(t *testing.T) {
			w := world(t)
			a, _ := add(t, w, p.Static, p.Vec3{Y: 5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .2, Z: .2}})
			z, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 4}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .2, Z: .2}})
			d := p.JointDesc{Kind: kind, A: a, B: z, FrameA: p.Identity(), FrameB: p.Identity(), Length: 1, Damping: 1, TargetRotation: p.Quat{W: 1}}
			j, e := w.CreateJoint(d)
			if e != nil {
				t.Fatal(e)
			}
			step(t, w, 1)
			s, e := w.Joint(j)
			if e != nil || !s.Force.Valid() || !s.Torque.Valid() {
				t.Fatalf("%+v %v", s, e)
			}
			if e := w.DestroyBody(z, p.Deleted); e != nil {
				t.Fatal(e)
			}
			if _, e := w.Joint(j); !errors.Is(e, p.ErrStale) {
				t.Fatal(e)
			}
		})
	}
}
