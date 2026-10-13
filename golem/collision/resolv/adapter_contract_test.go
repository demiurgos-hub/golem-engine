package collisionresolv

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/solarlune/resolv"
)

func TestResolvQueryUsesLayerOnlyAndOriginalShape(t *testing.T) {
	b := New()
	b.Add(1, collision.AABB{W: 2, H: 4}, 1, 0, true)
	if got := b.OverlapBox(0, 0, .1, .1, 1); len(got) != 1 {
		t.Fatalf("zero mask excluded=%v", got)
	}
	if got := b.RaycastAll(-2, 0, 2, 0, 1); len(got) != 1 {
		t.Fatalf("zero mask cast excluded=%v", got)
	}
	e := b.entries[1]
	e.shape.(*resolv.ConvexPolygon).SetRotation(.7)
	if got := b.OverlapBox(1.5, 0, .1, .1, 1); got != nil {
		t.Fatalf("query stopped using original dimensions=%v", got)
	}
	if got := b.OverlapBox(0, 1.5, .1, .1, 1); len(got) != 1 {
		t.Fatalf("original dimensions ignored=%v", got)
	}
}
