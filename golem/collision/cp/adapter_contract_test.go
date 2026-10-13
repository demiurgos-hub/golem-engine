package collisioncp

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision/internal/querygeom"
	cp "github.com/jakecoffman/cp/v2"
)

func TestChipmunkQueryAdapterContract(t *testing.T) {
	b := New()
	for id, x := range map[int64]float64{1: 8, 2: 3, 3: 5} {
		b.Add(id, collision.Circle{R: .5}, 1, 1, false)
		b.Update(id, x, 0)
	}
	b.Step(.01)
	for _, cast := range []func() (collision.RaycastHit, bool){
		func() (collision.RaycastHit, bool) { return b.Raycast(0, 0, 10, 0, 1) },
		func() (collision.RaycastHit, bool) { return b.BoxCast(0, 0, .1, .1, 10, 0, 1) },
		func() (collision.RaycastHit, bool) { return b.CircleCast(0, 0, .1, 10, 0, 1) },
	} {
		if h, ok := cast(); !ok || h.EntityID != 2 {
			t.Fatalf("nearest=%v,%v", h, ok)
		}
	}
	b = New()
	b.Add(1, collision.Circle{R: 1}, 1, 0, false)
	if got := b.OverlapCircle(0, 0, 2, 1); got != nil {
		t.Fatalf("native filter must reject zero entity mask: %v", got)
	}
	if got := b.RaycastAll(-2, 0, 2, 0, 1); got != nil {
		t.Fatalf("native cast filter: %v", got)
	}
	unmanaged := cp.NewCircle(b.Space().StaticBody, 1, cp.Vector{})
	unmanaged.SetFilter(cp.NewShapeFilter(cp.NO_GROUP, 1, 1))
	b.Space().AddShape(unmanaged)
	if got := b.OverlapBox(0, 0, 2, 2, 1); got != nil {
		t.Fatalf("unmanaged shape included: %v", got)
	}
}

func TestChipmunkPrimitiveUsesCurrentMeasurements(t *testing.T) {
	b := New()
	b.Add(1, collision.AABB{W: 2, H: 4}, 1, 1, false)
	e := b.entries[1]
	e.body.SetAngle(math.Pi / 2)
	b.Space().ReindexShape(e.shape)
	primitive := primitiveForShape(e.shape)
	if !querygeom.OverlapBox(2, 0, .1, .1, 0, 0, primitive) {
		t.Fatal("rotated bounding box ignored")
	}
	if querygeom.OverlapBox(0, 2, .1, .1, 0, 0, primitive) {
		t.Fatal("original box extents retained")
	}
	circle := cp.NewCircle(e.body, 3, cp.Vector{X: 20})
	if !querygeom.OverlapCircle(3, 0, .1, 0, 0, primitiveForShape(circle)) {
		t.Fatal("circle radius not extracted")
	}
}
