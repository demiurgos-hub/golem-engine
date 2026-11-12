package main

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func TestServerPlacementAndSequentialOwnerBinding(t *testing.T) {
	w, err := box3d.New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	s := golem.NewServer(golem.ServerConfig{})
	if err = s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	shape, err := w.CreateShape(p.ShapeDesc{ID: "sphere", Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	var actors []*actor
	for index := 0; index < 2; index++ {
		a := &actor{ID: int64(index + 1), position: p.Vec3{X: float32(index) * 4}}
		if err = s.CreateEntity(a); err != nil {
			t.Fatal(err)
		}
		d := p.DefaultBody(p.ExternalPose)
		d.Pose.Position = a.position
		a.body, err = w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.CreateCollider(p.DefaultCollider(a.body, shape)); err != nil {
			t.Fatal(err)
		}
		if err = s.AttachPhysics(a.ID, a.body, golem.PhysicsAttachmentConfig{}); err != nil {
			t.Fatal(err)
		}
		var hits [4]p.Hit
		q := p.Query{Kind: p.Overlap, Mode: p.All, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .1}, Pose: d.Pose, Filter: p.QueryFilter{Mask: ^uint64(0)}}
		result, queryErr := w.Query(q, hits[:])
		if queryErr != nil || result.Incomplete || result.Count != 1 || hits[0].Owner != a.ID {
			t.Fatalf("owner binding dirtied geometry or lost identity: result=%+v err=%v hits=%+v", result, queryErr, hits)
		}
		actors = append(actors, a)
	}
	before, err := s.PhysicsBody(1)
	if err != nil {
		t.Fatal(err)
	}
	target := p.Identity()
	target.Position = actors[1].position
	if err = s.TeleportPhysics(1, target); err == nil {
		t.Fatal("teleport into another solid body was admitted")
	}
	after, err := s.PhysicsBody(1)
	if err != nil || before != after || actors[0].position != before.Anchor.Position {
		t.Fatal("placement rejection changed accepted physics or entity pose")
	}
}
