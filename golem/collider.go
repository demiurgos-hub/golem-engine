package golem

import (
	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
)

// ColliderProvider is implemented by entities that expose a 2D schema-declared
// (or hand-written) collider. CreateEntity registers the shape on the
// CollisionLayers of the entity's space when one is configured (typically via
// generated Runtime.EnableCollision or EnableSpaceCollision), and MoveToSpace
// moves it between spaces.
//
// Wrapper types that embed a generated Synced* inherit Collider(); overriding
// it intentionally replaces the schema shape for that wrapper.
type ColliderProvider interface {
	Collider() (shape CollisionShape, layer string, trigger bool)
}

// ColliderProvider3D is implemented by entities that expose a 3D schema-declared
// (or hand-written) collider. CreateEntity registers the shape on the
// CollisionLayers3D of the entity's space when one is configured (typically via
// generated Runtime.EnableCollision3D or EnableSpaceCollision3D).
//
// Wrapper types that embed a generated Synced* inherit Collider3D(); overriding
// it intentionally replaces the schema shape for that wrapper.
type ColliderProvider3D interface {
	Collider3D() (shape CollisionShape3D, layer string, trigger bool)
}

// MustCollisionBackend panics when backend is nil. Generated EnableCollision
// calls this before configuring Layers so a nil backend cannot install a
// half-configured helper on the Server. The panic preserves the convenient
// no-error EnableCollision API.
func MustCollisionBackend(backend CollisionBackend) {
	if backend == nil {
		panic("golem: EnableCollision: backend must be non-nil")
	}
}

// MustCollisionBackend3D panics when backend is nil. Generated EnableCollision3D
// calls this before configuring Layers3D so a nil backend cannot install a
// half-configured helper on the Server.
func MustCollisionBackend3D(backend CollisionBackend3D) {
	if backend == nil {
		panic("golem: EnableCollision3D: backend must be non-nil")
	}
}

// SetCollisionLayers stores space 0's 2D named-layer helper used for automatic
// ColliderProvider registration in CreateEntity / removal in DeleteEntity.
// Space(id).SetCollisionLayers configures other spaces.
// A nil helper disables automatic registration without panicking.
//
// Call before spawning or restoring collidable entities (for example before
// Runtime.LoadSnapshot). EnableCollision generated helpers set this after
// binding the backend. Panics when l already serves another space: each space
// needs its own helper.
func (s *Server) SetCollisionLayers(l *collision.Layers) {
	s.mustUnshared("SetCollisionLayers", "collision layers", 0, l, layersOf)
	s.space0.layers = l
}

// SetCollisionLayers3D stores space 0's 3D named-layer helper used for automatic
// ColliderProvider3D registration. A nil helper disables automatic registration
// without panicking. Call before spawning or restoring collidable 3D entities.
// Panics when l already serves another space.
func (s *Server) SetCollisionLayers3D(l *collision3d.Layers) {
	s.mustUnshared("SetCollisionLayers3D", "3D collision layers", 0, l, layers3DOf)
	s.space0.layers3D = l
}

// SetCollisionBackend attaches a 2D collision backend to this space. Each tick
// the space's backend receives only the positions of the space's entities, and
// its contacts join the other spaces' contacts in one OnContact call and one
// per-entity contact event pass. Space(0).SetCollisionBackend is
// Server.SetCollisionBackend. Call before Run or on the tick goroutine (not
// enforced). Panics when b already serves another space; installing it again
// in this space is allowed, and a space that cleared it with nil releases it.
func (sp *Space) SetCollisionBackend(b CollisionBackend) {
	if b == nil && sp.srv.runtime(sp.id) == nil {
		return
	}
	sp.srv.mustUnshared("Space.SetCollisionBackend", "collision backend", sp.id, b, collisionOf)
	sp.srv.ensureRuntime(sp.id).collision = b
}

// SetCollision3DBackend attaches a 3D collision backend to this space, with the
// same per-space update and merged OnContact3D semantics as
// SetCollisionBackend. Call before Run or on the tick goroutine (not
// enforced). Panics when b already serves another space.
func (sp *Space) SetCollision3DBackend(b CollisionBackend3D) {
	if b == nil && sp.srv.runtime(sp.id) == nil {
		return
	}
	sp.srv.mustUnshared("Space.SetCollision3DBackend", "3D collision backend", sp.id, b, collision3DOf)
	sp.srv.ensureRuntime(sp.id).collision3D = b
}

// SetCollisionLayers stores this space's 2D named-layer helper. Entities
// created in the space, or moved into it with MoveToSpace, register their
// ColliderProvider shape on it; a nil helper disables registration in this
// space. Bind it to this space's backend. Call before spawning or restoring
// collidable entities in the space, before Run or on the tick goroutine (not
// enforced). Panics when l already serves another space.
func (sp *Space) SetCollisionLayers(l *collision.Layers) {
	if l == nil && sp.srv.runtime(sp.id) == nil {
		return
	}
	sp.srv.mustUnshared("Space.SetCollisionLayers", "collision layers", sp.id, l, layersOf)
	sp.srv.ensureRuntime(sp.id).layers = l
}

// SetCollisionLayers3D stores this space's 3D named-layer helper for
// ColliderProvider3D registration, like SetCollisionLayers. Panics when l
// already serves another space.
func (sp *Space) SetCollisionLayers3D(l *collision3d.Layers) {
	if l == nil && sp.srv.runtime(sp.id) == nil {
		return
	}
	sp.srv.mustUnshared("Space.SetCollisionLayers3D", "3D collision layers", sp.id, l, layers3DOf)
	sp.srv.ensureRuntime(sp.id).layers3D = l
}
