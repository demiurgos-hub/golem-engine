package box3d

import (
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func TestOwnedObservedPoseAndContactWitnessIgnoreLaterNativePose(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	desc := p.DefaultBody(p.FreeDynamic)
	desc.Pose.Position = p.Vec3{X: 100, Y: 20}
	desc.Anchor.Position = p.Vec3{Y: .5}
	desc.ManualCenterOfMass = true
	desc.CenterOfMass = p.Vec3{X: .3}
	a, err := w.CreateBody(desc)
	if err != nil {
		t.Fatal(err)
	}
	colliderDesc := p.DefaultCollider(a, shape)
	colliderDesc.Roles.Sensor = true
	ca, err := w.CreateCollider(colliderDesc)
	if err != nil {
		t.Fatal(err)
	}
	desc = p.DefaultBody(p.Static)
	z, err := w.CreateBody(desc)
	if err != nil {
		t.Fatal(err)
	}
	cz, err := w.CreateCollider(p.DefaultCollider(z, shape))
	if err != nil {
		t.Fatal(err)
	}
	pose := p.Pose{Position: p.Vec3{X: 2, Y: 4}, Rotation: p.Quat{Z: .70710677, W: .70710677}}
	owned := b.MotionBodySample{Body: w.bodies[a].id, End: b.WorldTransform{P: bp(pose.Position), Q: bq(pose.Rotation)}, LinearVelocity: b.Vec3{X: 3}, AngularVelocity: b.Vec3{Z: 2}}
	m := b.Manifold{Normal: b.Vec3{Y: 1}, PointCount: 1}
	m.Points[0] = b.ManifoldPoint{AnchorA: b.Vec3{X: .2, Y: .1}, Separation: .005, TotalNormalImpulse: .25}
	sample := b.MotionStepSample{Delta: 0, ImpulseDuration: .0125, Bodies: []b.MotionBodySample{owned}, ImpulseContacts: []b.ContactData{{ShapeIdA: w.colliders[ca].shapes[0], ShapeIdB: w.colliders[cz].shapes[0], Manifolds: []b.Manifold{m}, ManifoldCount: 1}}}
	state := w.newMotionContactState()
	events := state.capture(w, sample, .4)
	got := state.bodyState(w, a, w.bodies[a])
	if got.Pose != pose || got.Anchor.Position.Sub(pose.Transform(p.Vec3{Y: .5})).Length() > .00001 || got.CenterOfMass.Sub(pose.Transform(p.Vec3{X: .3})).Length() > .00001 || got.LinearVelocity != (p.Vec3{X: 3}) || got.AngularVelocity != (p.Vec3{Z: 2}) {
		t.Fatalf("observation used later native state: %+v", got)
	}
	want := pose.Transform(p.Vec3{X: .3}).Add(p.Vec3{X: .2, Y: .1})
	if len(events) != 1 || events[0].Pair.Kind != p.SolidContact || events[0].Point.Sub(want).Length() > .00001 || absFloat(events[0].Force.Y-20) > .00001 {
		t.Fatalf("owned contact witness/impulse: %+v, want point %+v", events, want)
	}
	geometric := sample.ImpulseContacts[0]
	geometric.Manifolds = append([]b.Manifold(nil), geometric.Manifolds...)
	geometric.Manifolds[0].Points[0].Separation = -.005
	geometric.Manifolds[0].Points[0].TotalNormalImpulse = 0
	sample.Contacts = []b.ContactData{geometric}
	events = state.capture(w, sample, .4)
	var force float32
	var sensors int
	for _, event := range events {
		force += event.Force.Y
		if event.Pair.Kind == p.SensorContact {
			sensors++
		}
	}
	if len(events) != 2 || sensors != 1 || absFloat(force-20) > .00001 {
		t.Fatalf("geometry/impulse aggregation duplicated force or sensor: %+v", events)
	}
	sample.ImpulseContacts = nil
	events = state.capture(w, sample, .5)
	for _, event := range events {
		if event.Force != (p.Vec3{}) {
			t.Fatalf("later geometry replayed an impulse: %+v", events)
		}
	}
	if native := w.bodyState(a, w.bodies[a]); native.Pose.Position.X != 100 {
		t.Fatal("observation changed the actual native body")
	}
}
