package box3d_test

import (
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func motionPoseAt(x, y, z float32) p.Pose {
	v := p.Identity()
	v.Position = p.Vec3{X: x, Y: y, Z: z}
	return v
}

func TestMotionPairAndWorldCandidateTrajectories(t *testing.T) {
	w := world(t)
	_, a := add(t, w, p.Kinematic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	_, b := add(t, w, p.Kinematic, p.Vec3{X: 100}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	_, c := add(t, w, p.Static, p.Vec3{X: 1}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	am := box.ColliderMotion{Collider: a, Start: motionPoseAt(-2, 0, 0), End: motionPoseAt(2, 0, 0)}
	bm := box.ColliderMotion{Collider: b, Start: motionPoseAt(2, 0, 0), End: motionPoseAt(-2, 0, 0)}
	r, err := w.CastColliderMotion(am, bm, 1e-5, p.IncludeBackfaces)
	if err != nil || !r.Hit || math.Abs(float64(r.Fraction-.4375)) > 1e-4 || r.Normal.X < .99 {
		t.Fatalf("pair: %+v %v", r, err)
	}
	q := box.MotionQuery{Motion: am, Targets: []box.ColliderMotion{bm}, Filter: p.QueryFilter{Mask: ^uint64(0)}, Mode: p.All, Backfaces: p.IncludeBackfaces, Tolerance: 1e-5}
	var short [1]p.Hit
	result, err := w.QueryColliderMotion(q, short[:])
	if err != nil || result.Total != 2 || result.Count != 1 || !result.Incomplete || !result.Blocked || short[0].Collider != b || short[0].Normal.X > -.99 {
		t.Fatalf("trajectory candidate/overflow: %+v %+v %v", result, short, err)
	}
	var all [4]p.Hit
	result, err = w.QueryColliderMotion(q, all[:])
	if err != nil || result.Incomplete || all[0].Collider != b || all[1].Collider != c {
		t.Fatalf("stable fractions: %+v %v", all, err)
	}
	q.Targets = append(q.Targets, bm)
	if _, err = w.QueryColliderMotion(q, all[:]); err == nil {
		t.Fatal("duplicate trajectory accepted")
	}
}

func TestMotionRotatingMeshRetainsTriangleIdentity(t *testing.T) {
	w := world(t)
	_, mesh := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.TriangleMesh, Vertices: []p.Vec3{{X: -2, Z: -2}, {X: -2, Z: 2}, {X: 2, Z: -2}, {X: 2, Z: 2}}, Indices: []uint32{0, 1, 2, 2, 1, 3}})
	_, sphere := add(t, w, p.Kinematic, p.Vec3{X: 1, Y: 1}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	start, end := p.Identity(), p.Identity()
	end.Rotation = p.Quat{Z: float32(math.Sqrt(.5)), W: float32(math.Sqrt(.5))}
	q := motionPoseAt(1, 1, 0)
	r, err := w.CastColliderMotion(box.ColliderMotion{Collider: mesh, Start: start, End: end}, box.ColliderMotion{Collider: sphere, Start: q, End: q}, 1e-5, p.IncludeBackfaces)
	if err != nil || !r.Hit || !r.FeatureA.Valid || r.FeatureA.Triangle > 1 || r.Fraction < .35 || r.Fraction > .5 {
		t.Fatalf("mesh motion: %+v %v", r, err)
	}
}
