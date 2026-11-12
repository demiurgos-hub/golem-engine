package box3d

import (
	"errors"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

func TestHandleGenerationRetiresBeforeWrap(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	a, err := w.claimHandle()
	if err != nil {
		t.Fatal(err)
	}
	w.generations[a.Index] = ^uint32(0)
	a.Generation = ^uint32(0)
	w.releaseHandle(a)
	b, err := w.claimHandle()
	if err != nil || b.Index == a.Index || b.Generation == 0 {
		t.Fatalf("wrapped live handle: %+v %v", b, err)
	}
}

func TestPooledBodyCollidersNeverInheritContactGenerations(t *testing.T) {
	config := p.DefaultConfig()
	config.Gravity = p.Vec3{}
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	sensorBody, err := w.CreateBody(p.DefaultBody(p.Static))
	if err != nil {
		t.Fatal(err)
	}
	sensorDesc := p.DefaultCollider(sensorBody, shape)
	sensorDesc.Roles = p.Roles{Sensor: true, Query: true}
	if _, err = w.CreateCollider(sensorDesc); err != nil {
		t.Fatal(err)
	}
	old, err := w.CreateBody(p.DefaultBody(p.FreeDynamic))
	if err != nil {
		t.Fatal(err)
	}
	oldCollider, err := w.CreateCollider(p.DefaultCollider(old, shape))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	if err = w.DestroyBody(old, p.Replaced); err != nil {
		t.Fatal(err)
	}
	body, err := w.CreateBody(p.DefaultBody(p.FreeDynamic))
	if err != nil {
		t.Fatal(err)
	}
	collider, err := w.CreateCollider(p.DefaultCollider(body, shape))
	if err != nil {
		t.Fatal(err)
	}
	if body.Index != old.Index || body.Generation == old.Generation || collider.Index != oldCollider.Index || collider.Generation == oldCollider.Generation {
		t.Fatalf("pool did not advance generations: %v %v / %v %v", old, body, oldCollider, collider)
	}
	if err = w.ApplyImpulse(old, p.Vec3{X: 10}, p.Vec3{}); !errors.Is(err, p.ErrStale) {
		t.Fatal("stale command accepted", err)
	}
	if err = w.DestroyCollider(oldCollider, p.Deleted); !errors.Is(err, p.ErrStale) {
		t.Fatal("stale collider accepted", err)
	}
	r, err := w.Simulate(p.Frame{ID: 2, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	exit, entry := false, false
	for _, event := range r.Events {
		if event.Pair.A == oldCollider || event.Pair.B == oldCollider {
			exit = exit || event.Phase == p.Exit && event.Reason == p.Replaced
		}
		if event.Pair.A == collider || event.Pair.B == collider {
			entry = entry || event.Phase == p.Enter
		}
	}
	if !exit || !entry {
		t.Fatalf("replacement inherited old contacts: %+v", r.Events)
	}
	if err = w.Accept(2); err != nil {
		t.Fatal(err)
	}
}
