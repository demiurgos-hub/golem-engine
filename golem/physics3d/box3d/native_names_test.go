package box3d

import (
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// Box3D's name cache only grows, so a native name per body or collider would keep every ID ever created.
func TestNativeBodiesAndShapesAreUnnamed(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	shape, err := w.CreateShape(p.ShapeDesc{ID: "box", Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}})
	if err != nil {
		t.Fatal(err)
	}
	desc := p.DefaultBody(p.Static)
	desc.ID = "crate/1"
	body, err := w.CreateBody(desc)
	if err != nil {
		t.Fatal(err)
	}
	collider, err := w.CreateCollider(p.ColliderDesc{ID: "crate/1", Body: body, Shape: shape, LocalPose: p.Identity(), Enabled: true,
		Roles: p.Roles{Query: true}, Filter: p.CollisionFilter{Category: 1, Mask: ^uint64(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if name := w.bodies[body].id.GetName(); name != "" {
		t.Fatalf("native body named %q", name)
	}
	c := w.colliders[collider]
	if len(c.shapes) == 0 {
		t.Fatal("collider has no native shapes")
	}
	for _, id := range c.shapes {
		if name := id.GetName(); name != "" {
			t.Fatalf("native shape named %q", name)
		}
	}
}
