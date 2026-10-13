package collisionresolv

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

func TestCastGeometryContract(t *testing.T) {
	for _, shape := range []collision.Shape{collision.Circle{R: 1}, collision.AABB{W: 2, H: 2}} {
		b := New()
		b.Add(1, shape, 1, 1, false)
		b.Update(1, 5, 0)
		b.Step(0.01)
		for _, tc := range []struct {
			name     string
			hits     func() []collision.RaycastHit
			fraction float64
		}{
			{"ray", func() []collision.RaycastHit { return b.RaycastAll(0, 0, 10, 0, 1) }, .4},
			{"box", func() []collision.RaycastHit { return b.BoxCastAll(0, 0, 1, .5, 10, 0, 1) }, .3},
			{"circle", func() []collision.RaycastHit { return b.CircleCastAll(0, 0, 1, 10, 0, 1) }, .3},
			{"zero-radius", func() []collision.RaycastHit { return b.CircleCastAll(0, 0, 0, 10, 0, 1) }, .4},
			{"zero-box", func() []collision.RaycastHit { return b.BoxCastAll(0, 0, 0, 0, 10, 0, 1) }, .4},
			{"zero-width", func() []collision.RaycastHit { return b.BoxCastAll(0, 0, 0, 1, 10, 0, 1) }, .4},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hits := tc.hits()
				if len(hits) != 1 {
					t.Fatalf("%T: hits=%v", shape, hits)
				}
				h := hits[0]
				if h.EntityID != 1 || math.Abs(h.Fraction-tc.fraction) > 1e-12 || math.Abs(h.Point.X-10*tc.fraction) > 1e-12 || h.Normal != (collision.Vec2{X: -1}) {
					t.Fatalf("%T: hit=%+v", shape, h)
				}
			})
		}
		if hits := b.RaycastAll(0, 0, 0, 0, 1); hits != nil {
			t.Fatalf("zero segment: %v", hits)
		}
		if hits := b.RaycastAll(0, 0, 10, 0, 2); hits != nil {
			t.Fatalf("layer miss: %v", hits)
		}
		h, ok := b.Raycast(5, 0, 10, 0, 1)
		if !ok || h.Fraction != 0 || h.Normal != (collision.Vec2{}) {
			t.Fatalf("inside: %+v,%v", h, ok)
		}
	}
}

func TestQueryOverlapBoundaries(t *testing.T) {
	for _, shape := range []collision.Shape{collision.Circle{R: 1}, collision.AABB{W: 2, H: 2}} {
		b := New()
		b.Add(1, shape, 1, 1, true)
		if got := b.OverlapBox(2, 0, 1, 1, 1); got != nil {
			t.Fatalf("%T box touching=%v", shape, got)
		}
		if got := b.OverlapCircle(2, 0, 1, 1); got != nil {
			t.Fatalf("%T circle touching=%v", shape, got)
		}
		if got := b.OverlapBox(1.9, 0, 1, 1, 1); len(got) != 1 {
			t.Fatalf("%T box penetration=%v", shape, got)
		}
		if got := b.OverlapCircle(1.9, 0, 1, 1); len(got) != 1 {
			t.Fatalf("%T circle penetration=%v", shape, got)
		}
	}
}

func TestCastAllSorted(t *testing.T) {
	b := New()
	for id, x := range map[int64]float64{1: 8, 2: 3, 3: 5} {
		b.Add(id, collision.Circle{R: .5}, 1, 1, false)
		b.Update(id, x, 0)
	}
	b.Step(.01)
	hits := b.RaycastAll(0, 0, 10, 0, 1)
	if len(hits) != 3 || hits[0].EntityID != 2 || hits[1].EntityID != 3 || hits[2].EntityID != 1 {
		t.Fatalf("hits=%v", hits)
	}
	// The single-hit result is deliberately not compared with All in resolv:
	// it stops at the first hit in an unordered map traversal.
	h, ok := b.Raycast(0, 0, 10, 0, 1)
	if !ok || h.EntityID < 1 || h.EntityID > 3 {
		t.Fatalf("single=%v,%v", h, ok)
	}
}

func BenchmarkSpatialQueries(b *testing.B) {
	backend := New()
	for id := int64(1); id <= 32; id++ {
		backend.Add(id, collision.Circle{R: .4}, 1, 1, false)
		backend.Update(id, float64(id), 0)
	}
	backend.Step(.01)
	b.Run("cast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			backend.CircleCastAll(0, 0, .2, 40, 0, 1)
		}
	})
	b.Run("overlap", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			backend.OverlapCircle(16, 0, 4, 1)
		}
	})
}
