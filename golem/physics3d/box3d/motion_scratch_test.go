package box3d

import (
	"reflect"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func TestMotionContactScratchPreservesAllImpulsesAndOwnedRecords(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	var native [2]b.ShapeID
	for i := range native {
		body, err := w.CreateBody(p.DefaultBody(p.FreeDynamic))
		if err != nil {
			t.Fatal(err)
		}
		desc := p.DefaultCollider(body, shape)
		desc.Roles.Sensor = i == 0
		collider, err := w.CreateCollider(desc)
		if err != nil {
			t.Fatal(err)
		}
		native[i] = w.colliders[collider].shapes[0]
	}
	m := b.Manifold{Normal: b.Vec3{Y: 1}, PointCount: 2}
	m.Points[0] = b.ManifoldPoint{Separation: -.01, TotalNormalImpulse: .1}
	m.Points[1] = b.ManifoldPoint{Separation: -.02, TotalNormalImpulse: .2}
	sample := b.MotionStepSample{Delta: .01, Contacts: []b.ContactData{{ShapeIdA: native[0], ShapeIdB: native[1], Manifolds: []b.Manifold{m}, ManifoldCount: 1}}}
	state := w.newMotionContactState()
	first := state.capture(w, sample, .25)
	if len(first) != 2 || first[0].Pair.Kind != p.SolidContact || first[0].Force.Y < 29.999 || first[0].Force.Y > 30.001 || first[0].Depth != .02 || first[1].Pair.Kind != p.SensorContact || first[1].Force != (p.Vec3{}) {
		t.Fatalf("lost solid/sensor aggregation: %+v", first)
	}
	retained := append([]p.ContactEvent(nil), first...)
	for i := 0; i < 3; i++ {
		out := state.capture(w, sample, .5)
		if len(out) != 2 || out[0].Fraction != .5 {
			t.Fatal(out)
		}
		out[0].Depth = 99
	}
	if !reflect.DeepEqual(first, retained) {
		t.Fatal("later captures changed an independently retained contact record")
	}
	allocs := testing.AllocsPerRun(20, func() {
		if len(state.capture(w, sample, 1)) != 2 {
			panic("missing contacts")
		}
	})
	if allocs > 1 {
		t.Fatalf("capture allocated %.0f times; want only its owned event slice", allocs)
	}
}
