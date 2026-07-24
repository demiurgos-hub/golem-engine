package footprint

import "testing"

func TestQuarterTurnIndex_acceptsSignedEquivalents(t *testing.T) {
	cases := []struct {
		deg  float64
		want int
	}{
		{0, 0},
		{90, 1},
		{180, 2},
		{270, 3},
		{360, 0},
		{-90, 3},
		{-180, 2},
		{-270, 1},
		{450, 1},
	}
	for _, tc := range cases {
		got, err := quarterTurnIndex(tc.deg)
		if err != nil || got != tc.want {
			t.Fatalf("quarterTurnIndex(%v) = %d, %v; want %d", tc.deg, got, err, tc.want)
		}
	}
}

func TestTransformPoint2D(t *testing.T) {
	x, y := transformPoint2D(1, 0, 2, 1)
	if x != 0 || y != 2 {
		t.Fatalf("got (%v,%v)", x, y)
	}
	x, y = transformPoint2D(1, 2, 1, 3)
	if x != 2 || y != -1 {
		t.Fatalf("270 got (%v,%v)", x, y)
	}
}

func TestTransformPoint3D(t *testing.T) {
	x, y, z := transformPoint3D(1, 5, 0, 1, 1)
	if x != 0 || y != 5 || z != -1 {
		t.Fatalf("yaw90 got (%v,%v,%v)", x, y, z)
	}
	x, y, z = transformPoint3D(1, 5, 0, 1, 3)
	if x != 0 || y != 5 || z != 1 {
		t.Fatalf("yaw270 got (%v,%v,%v)", x, y, z)
	}
}

func TestTransformAABBExtents(t *testing.T) {
	w, h := transformAABB2D(2, 1, 3, 1)
	if w != 3 || h != 6 {
		t.Fatalf("2d = %v,%v", w, h)
	}
	w, h, d := transformAABB3D(2, 1, 4, 1, 1)
	if w != 4 || h != 1 || d != 2 {
		t.Fatalf("3d = %v,%v,%v", w, h, d)
	}
}
