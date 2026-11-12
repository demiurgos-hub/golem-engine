package box3d

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func bindingFixture(t *testing.T, desc p.ShapeDesc) (*World, p.BodyHandle, p.ShapeHandle) {
	t.Helper()
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	desc.ID = "geometry"
	shape, err := w.CreateShape(desc)
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Static)
	d.ID = "body"
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	return w, body, shape
}

func TestShapeBindingsFollowPoolRestoreAndMetadata(t *testing.T) {
	w, body, shape := bindingFixture(t, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	d := p.DefaultCollider(body, shape)
	d.ID = "collider"
	h, err := w.CreateCollider(d)
	if err != nil {
		t.Fatal(err)
	}
	old := w.colliders[h].shapes[0]
	assertBinding := func(id b.ShapeID, handle p.ColliderHandle) {
		t.Helper()
		binding, ok := w.lookupShapeBinding(id)
		if !ok || binding.handle != handle || binding.collider != w.colliders[handle] || binding.body != w.bodies[binding.collider.desc.Body] || w.shapeOwners[id] != handle {
			t.Fatalf("binding differs from authoritative ownership: %+v", binding)
		}
	}
	assertBinding(old, h)
	wrongWorld, wrongGeneration := old, old
	wrongWorld.World0++
	wrongGeneration.Generation++
	for _, id := range []b.ShapeID{{}, {Index1: -1}, {Index1: 1 << 30}, wrongWorld, wrongGeneration} {
		if _, ok := w.lookupShapeBinding(id); ok {
			t.Fatalf("foreign or stale native ID resolved: %+v", id)
		}
	}
	if err := w.SetOwner(body, 42); err != nil || w.hit(old).Owner != 42 {
		t.Fatal("cached metadata did not follow owner binding", err)
	}
	filter := p.QueryFilter{Mask: ^uint64(0)}
	if err := w.SetEnabled(body, false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := w.eligible(old, filter); ok {
		t.Fatal("disabled body remained eligible")
	}
	if err := w.SetEnabled(body, true); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := w.eligible(old, filter); !ok {
		t.Fatal("reenabled body did not become eligible")
	}
	if err := w.DestroyCollider(h, p.Replaced); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.lookupShapeBinding(old); ok {
		t.Fatal("destroyed binding survived")
	}
	d.Owner = 42
	fresh, err := w.CreateCollider(d)
	if err != nil {
		t.Fatal(err)
	}
	id := w.colliders[fresh].shapes[0]
	if old.Index1 != id.Index1 || old.Generation == id.Generation || h.Index != fresh.Index || h.Generation == fresh.Generation {
		t.Fatal("fixture did not exercise native and portable slot reuse")
	}
	assertBinding(id, fresh)
	if _, ok := w.lookupShapeBinding(old); ok {
		t.Fatal("stale native generation aliased a replacement")
	}
	if err := w.DestroyCollider(h, p.Deleted); !errors.Is(err, p.ErrStale) {
		t.Fatal("stale portable destruction accepted", err)
	}
	assertBinding(id, fresh)
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := w.CaptureCheckpoint("binding-restore", "source")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := w.PrepareRestore(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	restored, ok := prepared.(p.ContentLookup).FindCollider("collider")
	if !ok {
		t.Fatal("prepared collider identity missing")
	}
	assertBinding(id, fresh)
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.lookupShapeBinding(id); ok {
		t.Fatal("pre-restore native world ID survived replacement")
	}
	newID := w.colliders[restored].shapes[0]
	assertBinding(newID, restored)
	if hit := w.hit(newID); hit.Collider != restored || hit.Owner != 42 || hit.Content != "collider" {
		t.Fatalf("restored metadata changed: %+v", hit)
	}
	if err := w.Close(); err != nil || w.shapeBindings != nil {
		t.Fatal("closed world retained cached pointers", err)
	}
}

func TestShapeBindingsCompoundChildrenAndPartialCreationRollback(t *testing.T) {
	desc := p.ShapeDesc{Kind: p.Compound, Children: []p.ShapeChild{
		{ID: "left", Pose: p.Identity(), Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .5}},
		{ID: "right", Pose: p.Pose{Position: p.Vec3{X: 2}, Rotation: p.Quat{W: 1}}, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .5}},
	}}
	w, body, shape := bindingFixture(t, desc)
	original := w.shapes[shape].cooked
	broken := *original
	// Inject a failure after one real native leaf, before registration publishes.
	broken.leaves = []cookedLeaf{original.leaves[0], {desc: p.ShapeDesc{Kind: 0}, pose: p.Identity()}}
	w.shapes[shape].cooked = &broken
	if _, err := w.CreateCollider(p.DefaultCollider(body, shape)); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal("partial attachment did not fail", err)
	}
	if len(w.shapeOwners) != 0 || len(w.shapeBindings) != 0 || len(w.colliders) != 0 || w.bodies[body].id.GetShapeCount() != 0 {
		t.Fatal("failed creation published native ownership")
	}
	w.shapes[shape].cooked = original
	h, err := w.CreateCollider(p.DefaultCollider(body, shape))
	if err != nil {
		t.Fatal(err)
	}
	for child, id := range w.colliders[h].shapes {
		if hit := w.hit(id); hit.Collider != h || hit.Feature.Child != uint32(child) || !hit.Feature.Valid {
			t.Fatalf("compound child identity changed: %+v", hit)
		}
	}
	ids := append([]b.ShapeID(nil), w.colliders[h].shapes...)
	if err := w.DestroyBody(body, p.Deleted); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, ok := w.lookupShapeBinding(id); ok {
			t.Fatal("destroyed body retained a child binding")
		}
	}
}
