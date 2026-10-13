package collision3d

import (
	"reflect"
	"sort"
	"testing"
)

func TestOverlapTraversalContract(t *testing.T) {
	b := NewSimpleBackend()
	b.Add(1, Sphere{R: 1}, 1, 0, true)
	b.Add(2, AABB{W: 2, H: 2, D: 2}, 1, 0, false)
	b.Add(3, Sphere{R: 1}, 2, 1, false)
	for _, query := range []func(float64, uint32) []int64{
		func(x float64, m uint32) []int64 { return b.OverlapBox(x, 0, 0, 1, 1, 1, m) },
		func(x float64, m uint32) []int64 { return b.OverlapSphere(x, 0, 0, 1, m) },
	} {
		for _, x := range []float64{0, 1.9} {
			ids := query(x, 1)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if !reflect.DeepEqual(ids, []int64{1, 2}) {
				t.Fatalf("overlap at %g=%v", x, ids)
			}
		}
		if ids := query(2, 1); ids != nil {
			t.Fatalf("touching=%v", ids)
		}
		if ids := query(0, 0); ids != nil {
			t.Fatalf("zero query mask=%v", ids)
		}
	}
}

func TestSegmentNumericalPolicyRemains3D(t *testing.T) {
	if _, _, ok := SegmentVsSphere(Vec3{X: -1.0000001}, Vec3{X: -.9999999}, Vec3{}, 1); ok {
		t.Fatal("3D epsilon must reject tiny sphere segment")
	}
	f, n, ok := SegmentVsAABB(Vec3{}, Vec3{}, Vec3{}, Vec3{X: 1, Y: 1, Z: 1})
	if !ok || f != 0 || n != (Vec3{}) {
		t.Fatalf("stationary inside AABB=%g,%v,%v", f, n, ok)
	}
}

func BenchmarkOverlapTraversal(b *testing.B) {
	backend := NewSimpleBackend()
	for id := int64(1); id <= 32; id++ {
		backend.Add(id, Sphere{R: .4}, 1, 1, false)
		backend.Update(id, float64(id), 0, 0)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		backend.OverlapSphere(16, 0, 0, 4, 1)
	}
}
