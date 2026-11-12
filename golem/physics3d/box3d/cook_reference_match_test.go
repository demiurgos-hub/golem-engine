package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

func TestUnityReferenceIdentityMatchingPreservesMultiplicityAndPrecision(t *testing.T) {
	a, b := p.ColliderHandle{World: 1, Index: 1, Generation: 1}, p.ColliderHandle{World: 1, Index: 2, Generation: 1}
	lookup := func(id string) (p.ColliderHandle, bool) {
		if id == "a" {
			return a, true
		}
		if id == "b" {
			return b, true
		}
		return p.ColliderHandle{}, false
	}
	got := []p.Hit{{Collider: a, Content: "a", Distance: 1, Point: p.Vec3{X: 1}, Normal: p.Vec3{Y: 1}}, {Collider: b, Content: "b", Distance: 1, Point: p.Vec3{X: 1}, Normal: p.Vec3{Y: 1}}}
	want := []unityReferenceHit{{ColliderID: "b", Distance: 1, Point: p.Vec3{X: 1}, Normal: p.Vec3{Y: 1}}, {ColliderID: "a", Distance: 1, Point: p.Vec3{X: 1}, Normal: p.Vec3{Y: 1}}}
	indices, err := matchUnityReferenceHits(got, want, lookup)
	if err != nil || len(indices) != 2 || indices[0] != 1 || indices[1] != 0 {
		t.Fatalf("equal-distance permutation: %v %v", indices, err)
	}
	for i, reference := range want {
		if err := unityReferenceGeometryError(got[indices[i]], reference, .0001); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := matchUnityReferenceHits(got[:1], want, lookup); err == nil {
		t.Fatal("missing hit accepted")
	}
	duplicate := append([]unityReferenceHit(nil), want...)
	duplicate[1] = want[0]
	if _, err := matchUnityReferenceHits(got, duplicate, lookup); err == nil {
		t.Fatal("wrong identity multiplicity accepted")
	}
	wrong := append([]p.Hit(nil), got...)
	wrong[0].Content = "unknown"
	if _, err := matchUnityReferenceHits(wrong, want, lookup); err == nil {
		t.Fatal("unexpected content identity accepted")
	}
	for _, mutate := range []func(*p.Hit){func(h *p.Hit) { h.Distance += .001 }, func(h *p.Hit) { h.Point.Z += .001 }, func(h *p.Hit) { h.Normal.X += .001 }} {
		hit := got[1]
		mutate(&hit)
		if err := unityReferenceGeometryError(hit, want[0], .0001); err == nil {
			t.Fatal("unchanged geometric tolerance was not enforced")
		}
	}
}
