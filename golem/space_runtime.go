package golem

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/nav"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// spaceRuntime holds the engine services of one space: legacy collision,
// named collision layers and the 2D navigation backend. Space 0's runtime is
// embedded in Server (Server.space0) and always exists; other spaces get one
// lazily when a service is first configured through their Space handle.
//
// physics and navigation are only ever set on space 0 for now, and
// Server.physics / Server.navigation alias them (both are assigned together in
// SetPhysicsWorld and SetSurfaceNavigation).
//
// Runtimes are created and mutated before Run or on the tick goroutine, like
// the Server setters they back.
type spaceRuntime struct {
	id          SpaceID
	collision   collision.Backend
	collision3D collision3d.Backend
	layers      *collision.Layers
	layers3D    *collision3d.Layers
	navBackend  nav.Backend
	navigation  *navigationService
	physics     *physicsService
}

// runtime returns the runtime of space without creating it, or nil when the
// space has none. Space 0 always has one.
func (s *Server) runtime(space SpaceID) *spaceRuntime {
	if space == 0 {
		return &s.space0
	}
	return s.spaces[space]
}

// ensureRuntime returns the runtime of space, creating it (and keeping
// spaceOrder ascending) on first use.
func (s *Server) ensureRuntime(space SpaceID) *spaceRuntime {
	if rt := s.runtime(space); rt != nil {
		return rt
	}
	if s.spaces == nil {
		s.spaces = make(map[SpaceID]*spaceRuntime)
	}
	rt := &spaceRuntime{id: space}
	s.spaces[space] = rt
	i, _ := slices.BinarySearchFunc(s.spaceOrder, space, func(r *spaceRuntime, id SpaceID) int {
		return cmp.Compare(r.id, id)
	})
	s.spaceOrder = slices.Insert(s.spaceOrder, i, rt)
	return rt
}

// runtimeOf returns the runtime of the entity's space without creating one,
// or nil when that space has none. Unknown entities resolve to space 0, and
// while no entity is outside space 0 this takes no lock.
func (s *Server) runtimeOf(entityID int64) *spaceRuntime {
	if s.reg == nil || !s.reg.SpacesInUse() {
		return &s.space0
	}
	space, _ := s.reg.Space(entityID)
	return s.runtime(space)
}

// physicsAttached reports whether id is bound to a physics body, in which case
// legacy collision neither updates nor writes back its position.
func (s *Server) physicsAttached(id int64) bool {
	return s.physics != nil && s.physics.agents[id] != nil
}

// registerCollider adds provider shapes on the layer helpers of the entity's
// space. 2D and 3D providers are handled independently: an entity implementing
// both registers on both helpers when both are configured. Missing helpers
// are a no-op (no panic) so entities can still be created before
// EnableCollision / EnableCollision3D; those entities will not have shapes
// until re-created after layers are configured.
func (s *Server) registerCollider(e Entity) {
	s.runtimeOf(e.EntityID()).registerCollider(e)
}

// unregisterCollider removes provider shapes from the layer helpers of the
// entity's space (same dual-provider semantics as registerCollider).
func (s *Server) unregisterCollider(e Entity) {
	s.runtimeOf(e.EntityID()).unregisterCollider(e, e.EntityID())
}

func (rt *spaceRuntime) registerCollider(e Entity) {
	if rt == nil {
		return
	}
	if p, ok := e.(ColliderProvider); ok && rt.layers != nil {
		shape, layer, trigger := p.Collider()
		rt.layers.Add(e.EntityID(), shape, layer, trigger)
	}
	if p, ok := e.(ColliderProvider3D); ok && rt.layers3D != nil {
		shape, layer, trigger := p.Collider3D()
		rt.layers3D.Add(e.EntityID(), shape, layer, trigger)
	}
}

func (rt *spaceRuntime) unregisterCollider(e Entity, id int64) {
	if rt == nil {
		return
	}
	if _, ok := e.(ColliderProvider); ok && rt.layers != nil {
		rt.layers.Remove(id)
	}
	if _, ok := e.(ColliderProvider3D); ok && rt.layers3D != nil {
		rt.layers3D.Remove(id)
	}
}

// colliderMove is one entity's legacy collider transfer between the layer
// helpers of two spaces, validated before the registry move.
type colliderMove struct {
	e        Entity
	id       int64
	from, to *spaceRuntime
	add2D    bool
	shape2D  CollisionShape
	layer2D  string
	trig2D   bool
	add3D    bool
	shape3D  CollisionShape3D
	layer3D  string
	trig3D   bool
}

// planColliderMoves validates the legacy collider transfers of t without
// changing anything. A destination with layer helpers must define each moving
// collider's layer; a destination without helpers simply drops the collider,
// as CreateEntity skips registration when no helper is configured.
func (s *Server) planColliderMoves(op string, t SpaceTransfer) ([]colliderMove, error) {
	var plan []colliderMove
	for _, id := range t.Entities {
		e, ok := s.reg.Get(id)
		if !ok {
			continue // the registry move reports it
		}
		p2, has2D := e.(ColliderProvider)
		p3, has3D := e.(ColliderProvider3D)
		if !has2D && !has3D {
			continue
		}
		from, _ := s.reg.Space(id)
		if from == t.To {
			continue
		}
		m := colliderMove{e: e, id: id, from: s.runtime(from), to: s.runtime(t.To)}
		if has2D && m.to != nil && m.to.layers != nil {
			m.shape2D, m.layer2D, m.trig2D = p2.Collider()
			if _, _, ok := m.to.layers.Lookup(m.layer2D); !ok {
				return nil, fmt.Errorf("golem: %s: entity %d collision layer %q is not defined in space %d", op, id, m.layer2D, t.To)
			}
			m.add2D = true
		}
		if has3D && m.to != nil && m.to.layers3D != nil {
			m.shape3D, m.layer3D, m.trig3D = p3.Collider3D()
			if _, _, ok := m.to.layers3D.Lookup(m.layer3D); !ok {
				return nil, fmt.Errorf("golem: %s: entity %d 3D collision layer %q is not defined in space %d", op, id, m.layer3D, t.To)
			}
			m.add3D = true
		}
		plan = append(plan, m)
	}
	return plan, nil
}

// applyColliderMoves removes each planned collider from its old space's
// helpers and registers it on the new space's helpers.
func applyColliderMoves(plan []colliderMove) {
	for _, m := range plan {
		m.from.unregisterCollider(m.e, m.id)
		if m.add2D {
			m.to.layers.Add(m.id, m.shape2D, m.layer2D, m.trig2D)
		}
		if m.add3D {
			m.to.layers3D.Add(m.id, m.shape3D, m.layer3D, m.trig3D)
		}
	}
}

// stepCollision runs the legacy 2D and then 3D collision of every space in
// ascending order: each space updates only its own entities, steps and reads
// back. Contact callbacks and per-entity contact events then run once per
// tick with the contacts of all spaces merged. While every entity is in
// space 0 the entity list is the plain registry snapshot.
func (s *Server) stepCollision(dt float64) {
	s.stepCollision2D(dt)
	s.stepCollision3D(dt)
}

// collisionEntities snapshots the live entities for one collision phase:
// all entities (space 0) while spaces are unused, else entities with spaces.
func (s *Server) collisionEntities() (all []Entity, spaced []registry.SpacedEntity) {
	if !s.reg.SpacesInUse() {
		return s.reg.All(), nil
	}
	s.spacedScratch = s.reg.AppendSpaced(s.spacedScratch[:0])
	return nil, s.spacedScratch
}

// releaseSpacedScratch drops entity references held by the scratch slice.
func (s *Server) releaseSpacedScratch(spaced []registry.SpacedEntity) {
	if spaced != nil {
		clear(spaced)
	}
}

// inCollisionSpace reports whether entity id belongs to rt's space for a
// collision ReadBack.
func (s *Server) inCollisionSpace(rt *spaceRuntime, id int64, spaced bool) bool {
	if !spaced {
		return rt.id == 0
	}
	space, _ := s.reg.Space(id)
	return space == rt.id
}

func (s *Server) stepCollision2D(dt float64) {
	if s.space0.collision == nil && !s.anyCollision2D() {
		return
	}
	all, spaced := s.collisionEntities()
	defer s.releaseSpacedScratch(spaced)
	var merged []collision.Contact
	scratch := false
	step := func(rt *spaceRuntime) {
		b := rt.collision
		if b == nil {
			return
		}
		if spaced == nil {
			if rt.id == 0 {
				for _, e := range all {
					if s.physicsAttached(e.EntityID()) {
						continue
					}
					x, y := e.Position()
					b.Update(e.EntityID(), float64(x), float64(y))
				}
			}
		} else {
			for _, se := range spaced {
				if se.Space != rt.id || s.physicsAttached(se.Entity.EntityID()) {
					continue
				}
				x, y := se.Entity.Position()
				b.Update(se.Entity.EntityID(), float64(x), float64(y))
			}
		}
		contacts := b.Step(dt)
		b.ReadBack(func(id int64, x, y float64) {
			if s.physicsAttached(id) || !s.inCollisionSpace(rt, id, spaced != nil) {
				return
			}
			e, ok := s.reg.Get(id)
			if !ok {
				return
			}
			if pw, ok := e.(registry.PositionWriter); ok {
				pw.SetPosition(float32(x), float32(y))
			}
		})
		switch {
		case len(contacts) == 0:
		case merged == nil:
			merged = contacts
		default:
			if !scratch {
				s.contactScratch = append(s.contactScratch[:0], merged...)
				scratch = true
			}
			s.contactScratch = append(s.contactScratch, contacts...)
			merged = s.contactScratch
		}
	}
	step(&s.space0)
	for _, rt := range s.spaceOrder {
		step(rt)
	}
	if s.onContact != nil && len(merged) > 0 {
		s.onContact(merged)
	}
	if s.contactEventsEnabled {
		s.dispatchContactEvents(merged)
	}
}

func (s *Server) stepCollision3D(dt float64) {
	if s.space0.collision3D == nil && !s.anyCollision3D() {
		return
	}
	all, spaced := s.collisionEntities()
	defer s.releaseSpacedScratch(spaced)
	var merged []collision3d.Contact
	scratch := false
	update := func(b collision3d.Backend, e Entity) {
		if s.physicsAttached(e.EntityID()) {
			return
		}
		sp, ok := e.(registry.Spatial3DEntity)
		if !ok {
			return
		}
		x, y, z := sp.Position3D()
		b.Update(e.EntityID(), float64(x), float64(y), float64(z))
	}
	step := func(rt *spaceRuntime) {
		b := rt.collision3D
		if b == nil {
			return
		}
		if spaced == nil {
			if rt.id == 0 {
				for _, e := range all {
					update(b, e)
				}
			}
		} else {
			for _, se := range spaced {
				if se.Space == rt.id {
					update(b, se.Entity)
				}
			}
		}
		contacts := b.Step(dt)
		b.ReadBack(func(id int64, x, y, z float64) {
			if s.physicsAttached(id) || !s.inCollisionSpace(rt, id, spaced != nil) {
				return
			}
			e, ok := s.reg.Get(id)
			if !ok {
				return
			}
			if pw, ok := e.(registry.Position3DWriter); ok {
				pw.SetPosition3D(float32(x), float32(y), float32(z))
			}
		})
		switch {
		case len(contacts) == 0:
		case merged == nil:
			merged = contacts
		default:
			if !scratch {
				s.contactScratch3D = append(s.contactScratch3D[:0], merged...)
				scratch = true
			}
			s.contactScratch3D = append(s.contactScratch3D, contacts...)
			merged = s.contactScratch3D
		}
	}
	step(&s.space0)
	for _, rt := range s.spaceOrder {
		step(rt)
	}
	if s.onContact3D != nil && len(merged) > 0 {
		s.onContact3D(merged)
	}
}

func (s *Server) anyCollision2D() bool {
	for _, rt := range s.spaceOrder {
		if rt.collision != nil {
			return true
		}
	}
	return false
}

func (s *Server) anyCollision3D() bool {
	for _, rt := range s.spaceOrder {
		if rt.collision3D != nil {
			return true
		}
	}
	return false
}
