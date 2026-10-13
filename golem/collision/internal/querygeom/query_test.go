package querygeom

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

func TestCastBoundariesAndNormals(t *testing.T) {
	tests := []struct {
		name                     string
		px, py, qx, qy, qhw, qhh float64
		target                   Primitive
		wantT                    float64
		normal                   collision.Vec2
		hit                      bool
	}{
		{"circle-tangent", -2, 1, 2, 1, 0, 0, Circle(1), .5, collision.Vec2{Y: 1}, true},
		{"box-endpoint", -2, 0, -1, 0, 0, 0, Box(1, 1), 1, collision.Vec2{X: -1}, true},
		{"on-box-face", -1, 0, 2, 0, 0, 0, Box(1, 1), 0, collision.Vec2{}, true},
		{"circle-centre", 0, 0, 2, 0, 0, 0, Circle(1), 0, collision.Vec2{}, true},
		{"circle-moving-away", 2, 0, 3, 0, 0, 0, Circle(1), 0, collision.Vec2{X: 1}, true},
		{"zero-height-fallback", -2, 0, 2, 0, 1, 0, Circle(1), .5, collision.Vec2{}, true},
		{"negative-radius", -2, 0, 2, 0, 1, 0, Circle(2), .25, collision.Vec2{X: -1}, true},
		{"exact-nonzero-segment", -1.0000001, 0, -.9999999, 0, 0, 0, Circle(1), .5, collision.Vec2{X: -1}, true},
		{"unsupported", -2, 0, 2, 0, 0, 0, Primitive{}, 0, collision.Vec2{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, n, ok := CastSegment(tc.px, tc.py, tc.qx, tc.qy, tc.qhw, tc.qhh, 0, 0, tc.target)
			if ok != tc.hit || math.Abs(f-tc.wantT) > 1e-8 || n != tc.normal {
				t.Fatalf("got (%g,%v,%v) want (%g,%v,%v)", f, n, ok, tc.wantT, tc.normal, tc.hit)
			}
		})
	}
}

func TestRoundedCornerAndTolerance(t *testing.T) {
	f, n, ok := CastSegment(-3, -3, 3, 3, -1, 0, 0, 0, Box(1, 1))
	want := (2 - 1/math.Sqrt(2)) / 6
	if !ok || math.Abs(f-want) > 1e-12 || math.Abs(n.X+1/math.Sqrt(2)) > 1e-12 || math.Abs(n.Y-n.X) > 1e-12 {
		t.Fatalf("corner=%g,%v,%v", f, n, ok)
	}
	// Exercise both sides of the existing corner/slab quadrant tolerance.
	for _, y := range []float64{1 - 2e-9, 1 - .5e-9, 1, 1 + .5e-9, 1 + 2e-9} {
		f, n, ok := CastSegment(-3, y, 3, y, -1, 0, 0, 0, Box(1, 1))
		wantT, wantN, wantOK := collision.SegmentVsRoundedRect(-3, y, 3, y, 0, 0, 1, 1, 1)
		if f != wantT || n != wantN || ok != wantOK {
			t.Fatalf("tolerance y=%g: %g,%v,%v", y, f, n, ok)
		}
	}
	if a := testing.AllocsPerRun(100, func() { CastSegment(-3, -3, 3, 3, -1, 0, 0, 0, Box(1, 1)) }); a != 0 {
		t.Fatalf("geometry allocated %g", a)
	}
}

func TestPrimitiveShapeSupport(t *testing.T) {
	for _, s := range []collision.Shape{nil, &collision.Circle{R: 1}, &collision.AABB{W: 2, H: 2}} {
		p, ok := FromShape(s)
		if ok || OverlapBox(0, 0, 2, 2, 0, 0, p) || OverlapCircle(0, 0, 2, 0, 0, p) {
			t.Fatalf("unsupported shape %T", s)
		}
	}
}
