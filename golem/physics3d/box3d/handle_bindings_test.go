package box3d

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func assertPortableBindings(t *testing.T, w *World) {
	t.Helper()
	if len(w.generations) != len(w.handleBindings) {
		t.Fatal("allocator and bindings have different slot counts")
	}
	for i, binding := range w.handleBindings {
		h := p.Handle{World: w.id, Index: uint32(i), Generation: w.generations[i]}
		if binding.body != w.bodies[p.BodyHandle(h)] || binding.collider != w.colliders[p.ColliderHandle(h)] || binding.shape != w.shapes[p.ShapeHandle(h)] {
			t.Fatalf("slot %d differs from authoritative ownership", i)
		}
	}
	for h, v := range w.bodies {
		if w.bodyForHandle(h) != v {
			t.Fatalf("body missing from slot: %+v", h)
		}
	}
	for h, v := range w.colliders {
		if w.colliderForHandle(h) != v {
			t.Fatalf("collider missing from slot: %+v", h)
		}
	}
	for h, v := range w.shapes {
		if w.shapeForHandle(h) != v {
			t.Fatalf("shape missing from slot: %+v", h)
		}
	}
}

func TestPortableBindingsRejectForeignStaleAndWrongTypes(t *testing.T) {
	w, body, shape := bindingFixture(t, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	collider, err := w.CreateCollider(p.DefaultCollider(body, shape))
	if err != nil {
		t.Fatal(err)
	}
	assertPortableBindings(t, w)
	for _, h := range []p.Handle{p.Handle(body), p.Handle(collider), p.Handle(shape)} {
		if (w.bodyForHandle(p.BodyHandle(h)) != nil) != (h == p.Handle(body)) || (w.colliderForHandle(p.ColliderHandle(h)) != nil) != (h == p.Handle(collider)) || (w.shapeForHandle(p.ShapeHandle(h)) != nil) != (h == p.Handle(shape)) {
			t.Fatalf("wrong-type cast resolved: %+v", h)
		}
		foreign, stale := h, h
		foreign.World++
		stale.Generation++
		for _, invalid := range []p.Handle{{}, {World: w.id, Index: ^uint32(0), Generation: 1}, {World: w.id, Generation: 1}, foreign, stale} {
			if w.bodyForHandle(p.BodyHandle(invalid)) != nil || w.colliderForHandle(p.ColliderHandle(invalid)) != nil || w.shapeForHandle(p.ShapeHandle(invalid)) != nil {
				t.Fatalf("invalid identity resolved: %+v", invalid)
			}
			if _, err := w.Body(p.BodyHandle(invalid)); !errors.Is(err, p.ErrStale) {
				t.Fatal("public body lookup accepted invalid identity", err)
			}
		}
	}
	if _, err := w.Body(p.BodyHandle(shape)); !errors.Is(err, p.ErrStale) {
		t.Fatal("public body lookup accepted shape handle", err)
	}
	peer, err := w.CreateBody(p.DefaultBody(p.FreeDynamic))
	if err != nil {
		t.Fatal(err)
	}
	joint, err := w.CreateJoint(p.JointDesc{Kind: p.Distance, A: body, B: peer, FrameA: p.Identity(), FrameB: p.Identity(), Length: 3})
	if err != nil {
		t.Fatal(err)
	}
	if w.bodyForHandle(p.BodyHandle(joint)) != nil || w.colliderForHandle(p.ColliderHandle(joint)) != nil || w.shapeForHandle(p.ShapeHandle(joint)) != nil {
		t.Fatal("joint identity resolved as another type")
	}
	if err := w.DestroyJoint(joint); err != nil {
		t.Fatal(err)
	}
	if err := w.DestroyBody(peer, p.Deleted); err != nil {
		t.Fatal(err)
	}
	if err := w.DestroyCollider(collider, p.Replaced); err != nil {
		t.Fatal(err)
	}
	if err := w.DestroyBody(body, p.Replaced); err != nil {
		t.Fatal(err)
	}
	if err := w.DestroyShape(shape); err != nil {
		t.Fatal(err)
	}
	newBody, err := w.CreateBody(p.DefaultBody(p.Static))
	if err != nil {
		t.Fatal(err)
	}
	newShape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	newCollider, err := w.CreateCollider(p.DefaultCollider(newBody, newShape))
	if err != nil {
		t.Fatal(err)
	}
	if newBody.Index != shape.Index || newShape.Index != body.Index || newCollider.Index != collider.Index {
		t.Fatal("fixture did not reuse slots across types")
	}
	for _, old := range []p.Handle{p.Handle(body), p.Handle(collider), p.Handle(shape)} {
		if w.bodyForHandle(p.BodyHandle(old)) != nil || w.colliderForHandle(p.ColliderHandle(old)) != nil || w.shapeForHandle(p.ShapeHandle(old)) != nil {
			t.Fatal("old handle revived after cross-type reuse")
		}
	}
	assertPortableBindings(t, w)
	if err := w.Close(); err != nil || w.handleBindings != nil {
		t.Fatal("closed world retained bindings", err)
	}
	if _, err := w.Body(newBody); !errors.Is(err, p.ErrStale) {
		t.Fatal("closed-world body remained accessible", err)
	}
}

func TestPortableBindingsClearRetiredSlots(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, binding := range []portableHandleBinding{{body: &body{}}, {collider: &collider{}}, {shape: &shape{}}} {
		h, err := w.claimHandle()
		if err != nil {
			t.Fatal(err)
		}
		w.generations[h.Index] = ^uint32(0)
		h.Generation = ^uint32(0)
		w.handleBindings[h.Index] = binding
		w.releaseHandle(h)
		if w.handleBindings[h.Index] != (portableHandleBinding{}) || len(w.freeHandles) != 0 {
			t.Fatal("retired slot retained pointers or entered reuse pool")
		}
		if w.bodyForHandle(p.BodyHandle(h)) != nil || w.colliderForHandle(p.ColliderHandle(h)) != nil || w.shapeForHandle(p.ShapeHandle(h)) != nil {
			t.Fatal("retired identity remained accessible")
		}
	}
}

func TestPortableBindingsRestoreAndDiscardStayIsolated(t *testing.T) {
	w, body, shape := bindingFixture(t, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	d := p.DefaultCollider(body, shape)
	d.ID = "collider"
	collider, err := w.CreateCollider(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := w.CaptureCheckpoint("portable-bindings", "source")
	if err != nil {
		t.Fatal(err)
	}
	discarded, err := w.PrepareRestore(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	candidate := discarded.(*preparedRestore).candidate
	assertPortableBindings(t, candidate)
	discarded.Discard()
	if candidate.handleBindings != nil || w.bodyForHandle(body) == nil {
		t.Fatal("discard affected the live world or retained candidate pointers")
	}
	prepared, err := w.PrepareRestore(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	assertPortableBindings(t, w)
	if w.bodyForHandle(body) != nil || w.colliderForHandle(collider) != nil || w.shapeForHandle(shape) != nil {
		t.Fatal("pre-restore world identity resolved in replacement")
	}
}
