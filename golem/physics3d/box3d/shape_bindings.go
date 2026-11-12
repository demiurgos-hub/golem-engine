package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// The native index selects a slot, never an identity. Every lookup compares the
// complete world/generation-qualified ID before using the cached pointers.
// Mutations are tick-owned; QueryBatch only reads this table after publication.
type nativeShapeBinding struct {
	id       b.ShapeID
	handle   p.ColliderHandle
	collider *collider
	body     *body
	child    uint32
}

func (w *World) bindNativeShape(id b.ShapeID, handle p.ColliderHandle, c *collider, v *body) {
	if id.Index1 <= 0 {
		panic("physics3d: invalid native shape registration")
	}
	index := int(id.Index1) - 1
	if index >= len(w.shapeBindings) {
		w.shapeBindings = append(w.shapeBindings, make([]nativeShapeBinding, index+1-len(w.shapeBindings))...)
	}
	w.shapeOwners[id] = handle
	w.shapeBindings[index] = nativeShapeBinding{id: id, handle: handle, collider: c, body: v, child: c.children[id]}
}

func (w *World) unbindNativeShape(id b.ShapeID) {
	delete(w.shapeOwners, id)
	if binding, ok := w.lookupShapeBinding(id); ok {
		*binding = nativeShapeBinding{}
	}
}

func (w *World) lookupShapeBinding(id b.ShapeID) (*nativeShapeBinding, bool) {
	if id.Index1 <= 0 || int(id.Index1) > len(w.shapeBindings) {
		return nil, false
	}
	binding := &w.shapeBindings[id.Index1-1]
	if binding.id != id || binding.collider == nil {
		return nil, false
	}
	return binding, true
}
