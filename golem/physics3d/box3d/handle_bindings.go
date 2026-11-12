package box3d

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

// Slots share the allocator's generation array and are published alongside the
// authoritative maps. Only the matching type has a non-nil pointer. Clear every
// pointer before reuse or retirement so a converted handle cannot change type.
type portableHandleBinding struct {
	body     *body
	collider *collider
	shape    *shape
}

func (w *World) bindingForHandle(h p.Handle) *portableHandleBinding {
	if h.World != w.id || h.Index == 0 || uint64(h.Index) >= uint64(len(w.handleBindings)) || w.generations[h.Index] != h.Generation {
		return nil
	}
	return &w.handleBindings[h.Index]
}

func (w *World) bodyForHandle(h p.BodyHandle) *body {
	if binding := w.bindingForHandle(p.Handle(h)); binding != nil {
		return binding.body
	}
	return nil
}

func (w *World) colliderForHandle(h p.ColliderHandle) *collider {
	if binding := w.bindingForHandle(p.Handle(h)); binding != nil {
		return binding.collider
	}
	return nil
}

func (w *World) shapeForHandle(h p.ShapeHandle) *shape {
	if binding := w.bindingForHandle(p.Handle(h)); binding != nil {
		return binding.shape
	}
	return nil
}
