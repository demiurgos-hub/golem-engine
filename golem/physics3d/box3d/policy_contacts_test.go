package box3d

import (
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func TestMotionPolicyContinuousRotationReentryBetweenContactingEndpoints(t *testing.T) {
	testMotionPolicyRotation(t, .005)
}

func TestMotionPolicyExactTangentRotationRetainsReentry(t *testing.T) {
	testMotionPolicyRotation(t, 0)
}

func testMotionPolicyRotation(t *testing.T, depth float32) {
	config := p.DefaultConfig()
	config.Gravity = p.Vec3{}
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	shape := p.ShapeDesc{Kind: p.Compound}
	for _, x := range []float32{-1, 1} {
		pose := p.Identity()
		pose.Position.X = x
		shape.Children = append(shape.Children, p.ShapeChild{Pose: pose, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .25}})
	}
	sh, err := w.CreateShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	bodyDesc := p.DefaultBody(p.FreeDynamic)
	bodyDesc.Locks.LinearX, bodyDesc.Locks.LinearY, bodyDesc.Locks.LinearZ = true, true, true
	h, err := w.CreateBody(bodyDesc)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := w.CreateCollider(p.DefaultCollider(h, sh))
	if err != nil {
		t.Fatal(err)
	}
	sh, err = w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Static)
	d.Pose.Position.X = 1.5 - depth
	z, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	zh, err := w.CreateCollider(p.DefaultCollider(z, sh))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	if err = w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.Kinematic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	if len(w.policyContacts) != 1 {
		t.Fatal("fixture did not preserve accepted pair")
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	private := w.tracker.Clone()
	w.pendingTracker = &private
	a, end := p.Identity(), p.Identity()
	end.Rotation.Y, end.Rotation.W = 1, 0
	record := motionContactRecord{start: 0, end: 1, sample: b.MotionStepSample{Delta: .05, Bodies: []b.MotionBodySample{{Body: w.bodies[h].id, Start: b.WorldTransform{P: bp(a.Position), Q: bq(a.Rotation)}, End: b.WorldTransform{P: bp(end.Position), Q: bq(end.Rotation)}}}}}
	events, err := w.newMotionContactState().replay(w, record, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	var phases []p.ContactPhase
	for _, event := range events {
		if event.Phase != p.Stay {
			phases = append(phases, event.Phase)
		}
		if event.Force != (p.Vec3{}) {
			t.Fatal("fabricated impulse")
		}
	}
	if len(phases) != 2 || phases[0] != p.Exit || phases[1] != p.Enter {
		t.Fatalf("rotation path erased exit/reentry: %+v", events)
	}
	if len(w.tracker.Current()) != 1 || len(w.pendingTracker.Current()) != 1 {
		t.Fatal("accepted and pending histories not retained")
	}
	// The contact replay only consumes copied paths; it must not move the solver.
	if w.bodyState(h, w.bodies[h]).Pose != a {
		t.Fatal("replay moved the physical body")
	}
	_ = ch
	_ = zh
}
