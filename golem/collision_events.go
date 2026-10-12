package golem

import "github.com/demiurgos-hub/golem-engine/golem/collision"

// TriggerEnter is optionally implemented by entities that want to be notified
// when another entity's trigger shape begins overlapping their shape.
// other is the entity that entered; it may be nil if that entity was removed
// in the same tick.
type TriggerEnter interface {
	OnTriggerEnter(other Entity)
}

// TriggerStay is optionally implemented by entities that want to be notified
// every tick while another entity's trigger shape continues to overlap theirs.
// other may be nil if the other entity was removed in the same tick.
type TriggerStay interface {
	OnTriggerStay(other Entity)
}

// TriggerExit is optionally implemented by entities that want to be notified
// when another entity's trigger shape stops overlapping their shape.
// other may be nil if the other entity was removed and is no longer in the registry.
type TriggerExit interface {
	OnTriggerExit(other Entity)
}

// CollisionEnter is optionally implemented by entities that want to be notified
// when a solid collision with another entity begins.
// normal is a unit vector pointing away from other (the push direction for the receiver).
// Contact.Normal from the backend points away from the other entity toward the receiver,
// and is negated for the B side so both entities always receive "away from the other".
// other may be nil if the other entity was removed in the same tick.
type CollisionEnter interface {
	OnCollisionEnter(other Entity, normal CollisionVec2, depth float64)
}

// CollisionStay is optionally implemented by entities that want to be notified
// every tick while a solid collision with another entity persists.
// normal and depth carry the current frame's contact data.
// other may be nil if the other entity was removed in the same tick.
type CollisionStay interface {
	OnCollisionStay(other Entity, normal CollisionVec2, depth float64)
}

// CollisionExit is optionally implemented by entities that want to be notified
// when a solid collision with another entity ends.
// other may be nil if the other entity was removed and is no longer in the registry.
type CollisionExit interface {
	OnCollisionExit(other Entity)
}

// EnableContactEvents activates per-entity collision event dispatch for this server.
// When enabled, the tick loop tracks which entity pairs are overlapping each tick and
// calls OnTriggerEnter/Stay/Exit and OnCollisionEnter/Stay/Exit on entities that implement
// those interfaces. This requires a collision backend (SetCollisionBackend) to have any
// effect — without a backend, no contacts are produced and no events fire.
// Safe to call after Run has started; the tracking maps begin empty, so all
// currently-overlapping pairs fire as Enter on the next tick.
func (s *Server) EnableContactEvents() {
	s.contactEventsEnabled = true
	s.triggerPairs = make(map[[2]int64]struct{})
	s.solidPairs = make(map[[2]int64]struct{})
}

// contactKey returns a canonical pair key with the smaller ID first so that
// {A,B} and {B,A} map to the same key.
func contactKey(a, b int64) [2]int64 {
	if a < b {
		return [2]int64{a, b}
	}
	return [2]int64{b, a}
}

// entityOrNil returns e when ok is true, otherwise a nil Entity interface value.
func entityOrNil(e Entity, ok bool) Entity {
	if ok {
		return e
	}
	return nil
}

// dispatchContactEvents computes Enter/Stay/Exit transitions from the current
// contact list and fires per-entity callbacks on entities that implement the
// relevant interfaces. Must be called every tick (even when contacts is empty)
// so that Exit events fire when all overlaps end.
func (s *Server) dispatchContactEvents(contacts []collision.Contact) {
	currentTrigger := make(map[[2]int64]collision.Contact, len(contacts))
	currentSolid := make(map[[2]int64]collision.Contact, len(contacts))
	for _, c := range contacts {
		key := contactKey(c.A, c.B)
		if c.Depth == 0 {
			currentTrigger[key] = c
		} else {
			currentSolid[key] = c
		}
	}

	nextTrigger := s.dispatchPairTransitions(currentTrigger, &s.triggerPairs, emitTriggerContact)
	nextSolid := s.dispatchPairTransitions(currentSolid, &s.solidPairs, emitSolidContact)
	s.triggerPairs, s.solidPairs = nextTrigger, nextSolid
}

type contactPhase uint8

const (
	contactEnter contactPhase = iota
	contactStay
	contactExit
)

// dispatchPairTransitions resolves both endpoints before invoking either callback.
// Canonical keys identify pairs; the stored contact keeps its original A/B normal.
// Read tracking through its field because callbacks can reset EnableContactEvents.
func (s *Server) dispatchPairTransitions(current map[[2]int64]collision.Contact, previous *map[[2]int64]struct{}, emit func(contactPhase, collision.Contact, Entity, Entity)) map[[2]int64]struct{} {
	for key, c := range current {
		phase := contactEnter
		if _, known := (*previous)[key]; known {
			phase = contactStay
		}
		a, okA := s.reg.Get(c.A)
		b, okB := s.reg.Get(c.B)
		emit(phase, c, entityOrNil(a, okA), entityOrNil(b, okB))
	}
	for key := range *previous {
		if _, active := current[key]; active {
			continue
		}
		a, okA := s.reg.Get(key[0])
		b, okB := s.reg.Get(key[1])
		emit(contactExit, collision.Contact{A: key[0], B: key[1]}, entityOrNil(a, okA), entityOrNil(b, okB))
	}
	next := make(map[[2]int64]struct{}, len(current))
	for key := range current {
		next[key] = struct{}{}
	}
	return next
}

func emitTriggerContact(phase contactPhase, _ collision.Contact, a, b Entity) {
	emitTriggerEndpoint(phase, a, b)
	emitTriggerEndpoint(phase, b, a)
}

func emitTriggerEndpoint(phase contactPhase, receiver, other Entity) {
	switch phase {
	case contactEnter:
		if h, ok := receiver.(TriggerEnter); ok {
			h.OnTriggerEnter(other)
		}
	case contactStay:
		if h, ok := receiver.(TriggerStay); ok {
			h.OnTriggerStay(other)
		}
	case contactExit:
		if h, ok := receiver.(TriggerExit); ok {
			h.OnTriggerExit(other)
		}
	}
}

func emitSolidContact(phase contactPhase, c collision.Contact, a, b Entity) {
	emitSolidEndpoint(phase, a, b, c.Normal, c.Depth)
	emitSolidEndpoint(phase, b, a, collision.Vec2{X: -c.Normal.X, Y: -c.Normal.Y}, c.Depth)
}

func emitSolidEndpoint(phase contactPhase, receiver, other Entity, normal CollisionVec2, depth float64) {
	switch phase {
	case contactEnter:
		if h, ok := receiver.(CollisionEnter); ok {
			h.OnCollisionEnter(other, normal, depth)
		}
	case contactStay:
		if h, ok := receiver.(CollisionStay); ok {
			h.OnCollisionStay(other, normal, depth)
		}
	case contactExit:
		if h, ok := receiver.(CollisionExit); ok {
			h.OnCollisionExit(other)
		}
	}
}
