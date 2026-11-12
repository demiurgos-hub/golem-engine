package box3d_test

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

func TestQueryFeaturesPreserveDetailAndInteriorClassification(t *testing.T) {
	w := motionWorld(t)
	_, a := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: 1, Z: 1}})
	_, z := add(t, w, p.FreeDynamic, p.Vec3{X: 1.25}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	point := p.Vec3{X: .1, Y: .2, Z: .3}
	hit, err := w.ClosestPoint(a, point)
	if err != nil || hit.Point != point || !hit.Penetrating || hit.Touching || hit.Distance != 0 || hit.Feature.Detail != p.ClosestFeatureDetail {
		t.Fatal("interior closest point", hit, err)
	}
	penetration, err := w.Penetration(a, z)
	if err != nil || !penetration.Penetrating || penetration.FeatureA.Detail != p.PenetrationFeatureDetail || penetration.FeatureB.Detail != p.PenetrationFeatureDetail {
		t.Fatal("pair feature detail lost", penetration, err)
	}
	q := p.Query{Kind: p.Overlap, Mode: p.All, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .1}, Pose: p.Identity(), Filter: p.QueryFilter{Mask: ^uint64(0)}}
	hits := make([]p.Hit, 4)
	r, err := w.Query(q, hits)
	if err != nil || r.Count != 1 || hits[0].Collider != a || hits[0].Feature.Detail != p.PenetrationFeatureDetail {
		t.Fatal("overlap feature detail lost", r, hits, err)
	}
}
