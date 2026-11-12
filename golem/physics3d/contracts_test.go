package physics3d

import (
	"math"
	"testing"
)

func TestQueryValidationAndEnvelope(t *testing.T) {
	q := Query{Kind: Ray, Pose: Identity()}
	if q.Validate() != nil {
		t.Fatal("stationary ray rejected")
	}
	q.Pose.Position.X = CoordinateLimit
	q.Translation.X = 1
	if q.Validate() == nil {
		t.Fatal("out of envelope accepted")
	}
	q.Translation.X = 0
	q.Pose.Position.Y = float32(math.NaN())
	if q.Validate() == nil {
		t.Fatal("NaN accepted")
	}
}
func TestShapeValidation(t *testing.T) {
	for _, s := range []ShapeDesc{{Kind: Sphere}, {Kind: Box, HalfExtents: Vec3{1, -1, 1}}, {Kind: TriangleMesh, Vertices: []Vec3{{}, {X: 1}, {X: 2}}, Indices: []uint32{0, 1, 2}}} {
		if s.Validate() == nil {
			t.Fatalf("invalid geometry accepted: %+v", s)
		}
	}
}
func TestTransformSpaces(t *testing.T) {
	p := Pose{Vec3{3, 4, 5}, Quat{Y: float32(math.Sqrt(.5)), W: float32(math.Sqrt(.5))}}
	v := Vec3{1, 2, 3}
	if got := p.Inverse().Transform(p.Transform(v)); got.Sub(v).Length() > 1e-5 {
		t.Fatal(got)
	}
	anchor := p.Compose(Pose{Vec3{Y: -1}, Quat{W: 1}})
	if abs(anchor.Position.Y-3) > 1e-5 {
		t.Fatal(anchor)
	}
}
func TestContactGenerationAndRestore(t *testing.T) {
	a := ColliderHandle{1, 1, 1}
	b := ColliderHandle{1, 2, 1}
	tr := ContactTracker{}
	e := ContactEvent{Pair: ContactPair{a, b, SolidContact}, Depth: 0}
	if out := tr.Update([]ContactEvent{e}, 1, 1, false); len(out) != 1 || out[0].Pair.Kind != SolidContact || out[0].Phase != Enter {
		t.Fatal(out)
	}
	if out := tr.Remove(a, Deleted, 2); len(out) != 1 || out[0].Reason != Deleted {
		t.Fatal(out)
	}
	a.Generation++
	e.Pair.A = a
	out := tr.Update([]ContactEvent{e}, 2, 2, true)
	if len(out) != 1 || out[0].Phase != Enter || out[0].Reason != Restored {
		t.Fatal(out)
	}
}
func TestOwnerAggregationOverflow(t *testing.T) {
	src := []Hit{{Owner: 7}, {Owner: 7}, {Collider: ColliderHandle{1, 1, 1}}, {Collider: ColliderHandle{1, 2, 1}}}
	dst := make([]Hit, 1)
	r := AggregateOwners(src, dst)
	if r.Total != 3 || r.Count != 1 || !r.Incomplete {
		t.Fatal(r)
	}
}
