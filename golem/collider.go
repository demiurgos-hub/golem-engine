package golem

import (
	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
)

// ColliderProvider is implemented by entities that expose a 2D schema-declared
// (or hand-written) collider. CreateEntity registers the shape on the server's
// CollisionLayers when SetCollisionLayers has been called (typically via
// generated Runtime.EnableCollision).
//
// Wrapper types that embed a generated Synced* inherit Collider(); overriding
// it intentionally replaces the schema shape for that wrapper.
type ColliderProvider interface {
	Collider() (shape CollisionShape, layer string, trigger bool)
}

// ColliderProvider3D is implemented by entities that expose a 3D schema-declared
// (or hand-written) collider. CreateEntity registers the shape on the server's
// CollisionLayers3D when SetCollisionLayers3D has been called (typically via
// generated Runtime.EnableCollision3D).
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

// SetCollisionLayers stores the 2D named-layer helper used for automatic
// ColliderProvider registration in CreateEntity / removal in DeleteEntity.
// A nil helper disables automatic registration without panicking.
//
// Call before spawning or restoring collidable entities (for example before
// Runtime.LoadSnapshot). EnableCollision generated helpers set this after
// binding the backend.
func (s *Server) SetCollisionLayers(l *collision.Layers) {
	s.layers = l
}

// SetCollisionLayers3D stores the 3D named-layer helper used for automatic
// ColliderProvider3D registration. A nil helper disables automatic registration
// without panicking. Call before spawning or restoring collidable 3D entities.
func (s *Server) SetCollisionLayers3D(l *collision3d.Layers) {
	s.layers3D = l
}

// registerCollider adds provider shapes when matching layer helpers are set.
// 2D and 3D providers are handled independently: an entity implementing both
// registers on both helpers when both are configured. Missing helpers are a
// no-op (no panic) so entities can still be created before EnableCollision /
// EnableCollision3D; those entities will not have shapes until re-created
// after layers are configured.
func (s *Server) registerCollider(e Entity) {
	if p, ok := e.(ColliderProvider); ok && s.layers != nil {
		shape, layer, trigger := p.Collider()
		s.layers.Add(e.EntityID(), shape, layer, trigger)
	}
	if p, ok := e.(ColliderProvider3D); ok && s.layers3D != nil {
		shape, layer, trigger := p.Collider3D()
		s.layers3D.Add(e.EntityID(), shape, layer, trigger)
	}
}

// unregisterCollider removes provider shapes when matching layer helpers are set.
// 2D and 3D removals are independent (same dual-provider semantics as register).
func (s *Server) unregisterCollider(e Entity) {
	if _, ok := e.(ColliderProvider); ok && s.layers != nil {
		s.layers.Remove(e.EntityID())
	}
	if _, ok := e.(ColliderProvider3D); ok && s.layers3D != nil {
		s.layers3D.Remove(e.EntityID())
	}
}
