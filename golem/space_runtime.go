package golem

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/nav"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// spaceRuntime holds the engine services of one space: legacy collision,
// named collision layers and the 2D navigation backend. Space 0's runtime is
// embedded in Server (Server.space0) and always exists; other spaces get one
// lazily when a service is first configured through their Space handle.
//
// physics and navigation hold the space's native physics world and 3D surface
// navigation; each service points back at its runtime (service.space) so the
// physics step prepares its sibling navigation frame. Server.physics /
// Server.navigation alias space 0's services (assigned together in
// SetPhysicsWorld and SetSurfaceNavigation). Physics and surface navigation are
// installed before Run only (their setters return an error afterwards); an
// entity's attachments always live in the services of its own space.
//
// Runtimes are created and mutated before Run or on the tick goroutine, like
// the Server setters they back; for the legacy collision and 2D navigation
// setters that is documented, not enforced. No service is ever installed in
// two spaces: every setter checks the other runtimes first (serviceOwner).
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

// phys returns the space's physics service, or nil (also for a nil runtime).
func (rt *spaceRuntime) phys() *physicsService {
	if rt == nil {
		return nil
	}
	return rt.physics
}

// nav returns the space's surface navigation service, or nil (also for a nil
// runtime).
func (rt *spaceRuntime) nav() *navigationService {
	if rt == nil {
		return nil
	}
	return rt.navigation
}

// faultServices stops the space's services after a backend failure that left
// them inconsistent, such as a space transfer that could not finish. Physics
// keeps its first fault, which closes readiness and ends Run at the next
// drain. Navigation readiness closes like a failed paired restore (restoring,
// with err as its restoration error). A space with navigation but no physics
// does not end Run: it reports not ready and blocks space moves and entity
// setup until a navigation restore completes.
func (rt *spaceRuntime) faultServices(err error) {
	if rt == nil || err == nil {
		return
	}
	if ps := rt.physics; ps != nil && ps.fault == nil {
		ps.fault = err
	}
	if n := rt.navigation; n != nil {
		n.restoring = true
		if n.restoreErr == nil {
			n.restoreErr = err
		}
	}
}

// physicsAttached reports whether id is bound to a physics body of this space,
// in which case legacy collision neither updates nor writes back its position.
func (rt *spaceRuntime) physicsAttached(id int64) bool {
	return rt.physics != nil && rt.physics.agents[id] != nil
}

// eachRuntime calls fn for space 0 and then every other runtime in ascending
// space order, stopping at the first error.
func (s *Server) eachRuntime(fn func(*spaceRuntime) error) error {
	if err := fn(&s.space0); err != nil {
		return err
	}
	for _, rt := range s.spaceOrder {
		if err := fn(rt); err != nil {
			return err
		}
	}
	return nil
}

// nilService reports whether v is nil or a typed nil of a nillable kind (such
// as a nil backend pointer), which the collision setters treat as nil.
func nilService(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

// pointerLike reports whether v has a kind sameService compares.
func pointerLike(v any) bool {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.UnsafePointer:
		return true
	}
	return false
}

// sameService reports whether a and b hold the same non-nil pointer-like value
// of the same dynamic type, so one backend, layer helper or world is not
// installed in two spaces. Values of other kinds are never considered shared:
// they are not compared with ==, which panics for non-comparable dynamic types.
func sameService(a, b any) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if !va.IsValid() || !vb.IsValid() || va.Type() != vb.Type() {
		return false
	}
	switch va.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.UnsafePointer:
		return va.Pointer() != 0 && va.Pointer() == vb.Pointer()
	}
	return false
}

// serviceOwner returns a space other than space whose service selected by get
// is svc. Re-installing a service into its own space is allowed, and a space
// that cleared its slot no longer owns the service.
func (s *Server) serviceOwner(space SpaceID, svc any, get func(*spaceRuntime) any) (SpaceID, bool) {
	var owner SpaceID
	found := false
	_ = s.eachRuntime(func(rt *spaceRuntime) error {
		if !found && rt.id != space && sameService(get(rt), svc) {
			owner, found = rt.id, true
		}
		return nil
	})
	return owner, found
}

// mustUnshared panics when svc already serves another space in the slot get
// selects. The legacy collision and 2D navigation setters have no error result,
// and sharing one stateful backend between spaces is a setup bug: the backend
// would be stepped once per space and leak contacts or paths across spaces.
func (s *Server) mustUnshared(op, what string, space SpaceID, svc any, get func(*spaceRuntime) any) {
	if owner, shared := s.serviceOwner(space, svc, get); shared {
		panic(fmt.Sprintf("golem: %s: %s for space %d already serves space %d; each space needs its own", op, what, space, owner))
	}
}

// mustBackendUnshared panics when collision backend b serves another space:
// installed as that space's backend (backendOf) or bound to that space's layer
// helper (helperBackendOf). Checking both keeps the generated EnableCollision
// order (backend setter, then layers setter) from panicking after the backend
// is installed: a fresh helper bound to b then passes mustLayersUnshared.
func (s *Server) mustBackendUnshared(op, what string, space SpaceID, b any, backendOf, helperBackendOf func(*spaceRuntime) any) {
	if b == nil {
		return
	}
	s.mustUnshared(op, what, space, b, backendOf)
	if owner, shared := s.serviceOwner(space, b, helperBackendOf); shared {
		panic(fmt.Sprintf("golem: %s: %s for space %d is bound to the collision layers of space %d; each space needs its own", op, what, space, owner))
	}
}

// mustLayersUnshared panics when helper l, or the backend it is bound to,
// serves another space. A helper bound to another space's backend would
// register this space's shapes there.
func (s *Server) mustLayersUnshared(op string, space SpaceID, l *collision.Layers) {
	s.mustUnshared(op, "collision layers", space, l, layersOf)
	s.mustBackendUnshared(op, "backend of the collision layers", space, layersBackend(l), collisionOf, layersBackendOf)
}

// mustLayers3DUnshared is mustLayersUnshared for 3D helpers.
func (s *Server) mustLayers3DUnshared(op string, space SpaceID, l *collision3d.Layers) {
	s.mustUnshared(op, "3D collision layers", space, l, layers3DOf)
	s.mustBackendUnshared(op, "backend of the 3D collision layers", space, layers3DBackend(l), collision3DOf, layers3DBackendOf)
}

func collisionOf(rt *spaceRuntime) any       { return rt.collision }
func collision3DOf(rt *spaceRuntime) any     { return rt.collision3D }
func layersOf(rt *spaceRuntime) any          { return rt.layers }
func layers3DOf(rt *spaceRuntime) any        { return rt.layers3D }
func navBackendOf(rt *spaceRuntime) any      { return rt.navBackend }
func layersBackendOf(rt *spaceRuntime) any   { return layersBackend(rt.layers) }
func layers3DBackendOf(rt *spaceRuntime) any { return layers3DBackend(rt.layers3D) }

// layersBackend returns the backend l is bound to as an untyped value, nil
// (not a nil interface wrapped in any) when l is nil or unbound.
func layersBackend(l *collision.Layers) any {
	if b := l.Backend(); b != nil {
		return b
	}
	return nil
}

// layers3DBackend is layersBackend for 3D helpers.
func layers3DBackend(l *collision3d.Layers) any {
	if b := l.Backend(); b != nil {
		return b
	}
	return nil
}

func physicsWorldOf(rt *spaceRuntime) any {
	if rt.physics == nil {
		return nil
	}
	return rt.physics.world
}

func surfaceBackendOf(rt *spaceRuntime) any {
	if rt.navigation == nil {
		return nil
	}
	return rt.navigation.backend
}

// anyPhysics reports whether any space has a physics world.
func (s *Server) anyPhysics() bool {
	if s.space0.physics != nil {
		return true
	}
	for _, rt := range s.spaceOrder {
		if rt.physics != nil {
			return true
		}
	}
	return false
}

// servicesQuiescent rejects work that must not interleave with any space's
// physics step, accepted-frame callbacks, restoration or open navigation frame.
// Physics errors wrap physics3d.ErrBusy (or ErrFaulted/ErrStale).
func (s *Server) servicesQuiescent(op string) error {
	return s.eachRuntime(func(rt *spaceRuntime) error {
		if ps := rt.physics; ps != nil {
			if err := ps.mutable(); err != nil {
				return fmt.Errorf("golem: %s: %w", op, err)
			}
		}
		if n := rt.navigation; n != nil && (n.restoring || n.restoreErr != nil || n.frame != nil) {
			return fmt.Errorf("golem: %s is unavailable during navigation frames or restoration: %w", op, p.ErrBusy)
		}
		return nil
	})
}

// registerCollider adds provider shapes on the layer helpers of the entity's
// space. 2D and 3D providers are handled independently: an entity implementing
// both registers on both helpers when both are configured. Missing helpers
// are a no-op (no panic) so entities can still be created before
// EnableCollision / EnableCollision3D; those entities will not have shapes
// until re-created after layers are configured.
//
// A configured helper must be usable, as planColliderAdd requires for
// transfers and paired restore: an unbound helper panics in Layers.Add, and a
// helper rebound with Bind after installation to a backend other than the
// space's own (when both are pointer-like) or to another space's backend
// panics here (helperBackend), before any shape is registered. The panic
// matches the create paths' existing failure mode for an unbound helper; use
// SetCollisionLayers to install a correctly bound helper.
func (s *Server) registerCollider(e Entity) {
	rt := s.runtimeOf(e.EntityID())
	if rt == nil {
		return
	}
	if _, ok := e.(ColliderProvider); ok && rt.layers.Bound() {
		if err := s.helperBackend(rt, "collision layers", layersBackend(rt.layers), rt.collision, collisionOf); err != nil {
			panic(fmt.Sprintf("golem: register collider: entity %d: %v", e.EntityID(), err))
		}
	}
	if _, ok := e.(ColliderProvider3D); ok && rt.layers3D.Bound() {
		if err := s.helperBackend(rt, "3D collision layers", layers3DBackend(rt.layers3D), rt.collision3D, collision3DOf); err != nil {
			panic(fmt.Sprintf("golem: register collider: entity %d: %v", e.EntityID(), err))
		}
	}
	rt.registerCollider(e)
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

// colliderAdd is one entity's legacy collider registration on a space's
// layer helpers, validated before anything is published.
type colliderAdd struct {
	add2D   bool
	shape2D CollisionShape
	layer2D string
	trig2D  bool
	add3D   bool
	shape3D CollisionShape3D
	layer3D string
	trig3D  bool
}

// planColliderAdd validates that e's collider providers can register on the
// helpers of rt's space without panicking: every configured helper must define
// the collider's layer and be bound to a backend of this space (helperBackend).
// A missing helper (or a nil runtime) skips that registration, as CreateEntity
// does. Errors read "entity <id> ..." so callers prefix their operation.
func (s *Server) planColliderAdd(rt *spaceRuntime, e Entity) (colliderAdd, error) {
	var add colliderAdd
	if rt == nil {
		return add, nil
	}
	id := e.EntityID()
	if p2, ok := e.(ColliderProvider); ok && rt.layers != nil {
		add.shape2D, add.layer2D, add.trig2D = p2.Collider()
		if _, _, ok := rt.layers.Lookup(add.layer2D); !ok {
			return add, fmt.Errorf("entity %d collision layer %q is not defined in space %d", id, add.layer2D, rt.id)
		}
		if !rt.layers.Bound() {
			return add, fmt.Errorf("entity %d: space %d collision layers have no bound backend", id, rt.id)
		}
		if err := s.helperBackend(rt, "collision layers", layersBackend(rt.layers), rt.collision, collisionOf); err != nil {
			return add, fmt.Errorf("entity %d: %w", id, err)
		}
		add.add2D = true
	}
	if p3, ok := e.(ColliderProvider3D); ok && rt.layers3D != nil {
		add.shape3D, add.layer3D, add.trig3D = p3.Collider3D()
		if _, _, ok := rt.layers3D.Lookup(add.layer3D); !ok {
			return add, fmt.Errorf("entity %d 3D collision layer %q is not defined in space %d", id, add.layer3D, rt.id)
		}
		if !rt.layers3D.Bound() {
			return add, fmt.Errorf("entity %d: space %d 3D collision layers have no bound backend", id, rt.id)
		}
		if err := s.helperBackend(rt, "3D collision layers", layers3DBackend(rt.layers3D), rt.collision3D, collision3DOf); err != nil {
			return add, fmt.Errorf("entity %d: %w", id, err)
		}
		add.add3D = true
	}
	return add, nil
}

// helperBackend checks the backend a space's layer helper is bound to,
// which may have been rebound with Bind after the setters checked it: it must
// be the space's own backend when the space has a pointer-like one, and never
// another space's backend.
func (s *Server) helperBackend(rt *spaceRuntime, what string, bound, own any, backendOf func(*spaceRuntime) any) error {
	if own != nil && pointerLike(own) && pointerLike(bound) && !sameService(bound, own) {
		return fmt.Errorf("space %d %s are bound to a backend other than the space's collision backend", rt.id, what)
	}
	if owner, shared := s.serviceOwner(rt.id, bound, backendOf); shared {
		return fmt.Errorf("space %d %s are bound to the collision backend of space %d", rt.id, what, owner)
	}
	return nil
}

// checkColliderRemove reports an error when removing e's colliders from rt's
// helpers would panic because a configured helper has no bound backend.
func (rt *spaceRuntime) checkColliderRemove(e Entity) error {
	if rt == nil {
		return nil
	}
	if _, ok := e.(ColliderProvider); ok && rt.layers != nil && !rt.layers.Bound() {
		return fmt.Errorf("entity %d: space %d collision layers have no bound backend", e.EntityID(), rt.id)
	}
	if _, ok := e.(ColliderProvider3D); ok && rt.layers3D != nil && !rt.layers3D.Bound() {
		return fmt.Errorf("entity %d: space %d 3D collision layers have no bound backend", e.EntityID(), rt.id)
	}
	return nil
}

// applyColliderAdd registers a planned collider on rt's helpers.
func (rt *spaceRuntime) applyColliderAdd(id int64, add colliderAdd) {
	if add.add2D {
		rt.layers.Add(id, add.shape2D, add.layer2D, add.trig2D)
	}
	if add.add3D {
		rt.layers3D.Add(id, add.shape3D, add.layer3D, add.trig3D)
	}
}

// colliderMove is one entity's legacy collider transfer between the layer
// helpers of two spaces, validated before the registry move.
type colliderMove struct {
	e           Entity
	id          int64
	incarnation uint64 // registry incarnation of e at validation
	from, to    *spaceRuntime
	colliderAdd
}

// planColliderMoves validates the legacy collider transfers of t without
// changing anything. A destination with layer helpers must define each moving
// collider's layer; a destination without helpers simply drops the collider,
// as CreateEntity skips registration when no helper is configured. Every
// helper the move removes from or adds to must be bound to a backend, so
// applyColliderMoves cannot panic after the registry has moved.
func (s *Server) planColliderMoves(op string, t SpaceTransfer) ([]colliderMove, error) {
	var plan []colliderMove
	for _, id := range t.Entities {
		e, ok := s.reg.Get(id)
		if !ok {
			continue // the registry move reports it
		}
		_, has2D := e.(ColliderProvider)
		_, has3D := e.(ColliderProvider3D)
		if !has2D && !has3D {
			continue
		}
		from, _ := s.reg.Space(id)
		if from == t.To {
			continue
		}
		incarnation, _ := s.reg.Incarnation(id)
		m := colliderMove{e: e, id: id, incarnation: incarnation, from: s.runtime(from), to: s.runtime(t.To)}
		if err := m.from.checkColliderRemove(e); err != nil {
			return nil, fmt.Errorf("golem: %s: %w", op, err)
		}
		add, err := s.planColliderAdd(m.to, e)
		if err != nil {
			return nil, fmt.Errorf("golem: %s: %w", op, err)
		}
		m.colliderAdd = add
		plan = append(plan, m)
	}
	return plan, nil
}

// applyColliderMoves removes each planned collider from its old space's
// helpers and registers it on the new space's helpers. A newly added shape is
// synced to the entity's position on the destination's collision backend at
// once, as the next collision step would, so spatial queries made later in the
// move's tick find it where the entity is rather than at the origin. Backends
// ignore updates of entities registered on another backend. A collider entity
// is never physics-attached (attachPhysics and paired restore reject
// ColliderProvider and ColliderProvider3D entities), so no physics body owns
// its position here.
func applyColliderMoves(plan []colliderMove) {
	for _, m := range plan {
		m.from.unregisterCollider(m.e, m.id)
		if m.add2D {
			m.to.layers.Add(m.id, m.shape2D, m.layer2D, m.trig2D)
			if b := m.to.collision; b != nil {
				x, y := m.e.Position()
				b.Update(m.id, float64(x), float64(y))
			}
		}
		if m.add3D {
			m.to.layers3D.Add(m.id, m.shape3D, m.layer3D, m.trig3D)
			if b := m.to.collision3D; b != nil {
				if sp, ok := m.e.(registry.Spatial3DEntity); ok {
					x, y, z := sp.Position3D()
					b.Update(m.id, float64(x), float64(y), float64(z))
				}
			}
		}
	}
}

// stepCollision runs the legacy 2D and then 3D collision of every space in
// ascending order: each space updates only its own entities, steps and reads
// back. Contact callbacks and per-entity contact events then run once per
// tick with the contacts of all spaces merged. While every entity is in
// space 0 the entity list is the plain registry snapshot. Space moves are
// rejected for the whole phase (collisionStepping), so a 2D contact callback
// cannot move an entity before the 3D step; callbacks use Post.
func (s *Server) stepCollision(dt float64) {
	s.collisionStepping = true
	defer func() { s.collisionStepping = false }()
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
					if rt.physicsAttached(e.EntityID()) {
						continue
					}
					x, y := e.Position()
					b.Update(e.EntityID(), float64(x), float64(y))
				}
			}
		} else {
			for _, se := range spaced {
				if se.Space != rt.id || rt.physicsAttached(se.Entity.EntityID()) {
					continue
				}
				x, y := se.Entity.Position()
				b.Update(se.Entity.EntityID(), float64(x), float64(y))
			}
		}
		contacts := b.Step(dt)
		b.ReadBack(func(id int64, x, y float64) {
			if rt.physicsAttached(id) || !s.inCollisionSpace(rt, id, spaced != nil) {
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
	update := func(rt *spaceRuntime, b collision3d.Backend, e Entity) {
		if rt.physicsAttached(e.EntityID()) {
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
					update(rt, b, e)
				}
			}
		} else {
			for _, se := range spaced {
				if se.Space == rt.id {
					update(rt, b, se.Entity)
				}
			}
		}
		contacts := b.Step(dt)
		b.ReadBack(func(id int64, x, y, z float64) {
			if rt.physicsAttached(id) || !s.inCollisionSpace(rt, id, spaced != nil) {
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

// drainPhysics runs every space's due queued physics commands, space 0 first
// and then in ascending space order. A failing space ends the tick (and Run).
func (s *Server) drainPhysics() error {
	if ps := s.space0.physics; ps != nil {
		if err := ps.drain(); err != nil {
			return err
		}
	}
	for _, rt := range s.spaceOrder {
		if rt.physics != nil {
			if err := rt.physics.drain(); err != nil {
				return err
			}
		}
	}
	return nil
}

// prepareNavigation publishes every space's completed navigation builds and
// path results in ascending space order.
func (s *Server) prepareNavigation() {
	if n := s.space0.navigation; n != nil {
		n.prepare()
	}
	for _, rt := range s.spaceOrder {
		if rt.navigation != nil {
			rt.navigation.prepare()
		}
	}
}

// stepSpaces advances each space once in ascending order: a space with physics
// steps its world (preparing its own staged navigation frame), a space with only
// surface navigation advances it. While several runtimes exist, space moves are
// rejected for the whole loop so no entity is simulated in two spaces in one
// tick.
func (s *Server) stepSpaces(dt float64) error {
	if len(s.spaceOrder) != 0 {
		s.simulatingSpaces = true
		defer func() { s.simulatingSpaces = false }()
	}
	if err := s.space0.stepServices(dt); err != nil {
		return err
	}
	for _, rt := range s.spaceOrder {
		if err := rt.stepServices(dt); err != nil {
			return err
		}
	}
	return nil
}

func (rt *spaceRuntime) stepServices(dt float64) error {
	if rt.physics != nil {
		return rt.physics.step(dt)
	}
	if rt.navigation != nil {
		rt.navigation.advance(dt)
	}
	return nil
}

// reconcileNavigation reconciles every space's navigation agents with their
// actual bodies after collision readback, in ascending space order.
func (s *Server) reconcileNavigation() {
	if n := s.space0.navigation; n != nil {
		n.reconcile()
	}
	for _, rt := range s.spaceOrder {
		if rt.navigation != nil {
			rt.navigation.reconcile()
		}
	}
}

// stopServices stops every space's navigation service and then every space's
// physics service, each in ascending space order.
func (s *Server) stopServices() {
	_ = s.eachRuntime(func(rt *spaceRuntime) error {
		if rt.navigation != nil {
			rt.navigation.stop()
		}
		return nil
	})
	_ = s.eachRuntime(func(rt *spaceRuntime) error {
		if rt.physics != nil {
			rt.physics.stop()
		}
		return nil
	})
}
