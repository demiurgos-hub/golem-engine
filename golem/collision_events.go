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

	// --- trigger events ---
	for key, c := range currentTrigger {
		eA, okA := s.reg.Get(c.A)
		eB, okB := s.reg.Get(c.B)
		_, wasActive := s.triggerPairs[key]
		if wasActive {
			if okA {
				if h, ok := eA.(TriggerStay); ok {
					h.OnTriggerStay(entityOrNil(eB, okB))
				}
			}
			if okB {
				if h, ok := eB.(TriggerStay); ok {
					h.OnTriggerStay(entityOrNil(eA, okA))
				}
			}
		} else {
			if okA {
				if h, ok := eA.(TriggerEnter); ok {
					h.OnTriggerEnter(entityOrNil(eB, okB))
				}
			}
			if okB {
				if h, ok := eB.(TriggerEnter); ok {
					h.OnTriggerEnter(entityOrNil(eA, okA))
				}
			}
		}
	}
	for key := range s.triggerPairs {
		if _, active := currentTrigger[key]; active {
			continue
		}
		eA, okA := s.reg.Get(key[0])
		eB, okB := s.reg.Get(key[1])
		if okA {
			if h, ok := eA.(TriggerExit); ok {
				h.OnTriggerExit(entityOrNil(eB, okB))
			}
		}
		if okB {
			if h, ok := eB.(TriggerExit); ok {
				h.OnTriggerExit(entityOrNil(eA, okA))
			}
		}
	}

	// --- solid collision events ---
	for key, c := range currentSolid {
		eA, okA := s.reg.Get(c.A)
		eB, okB := s.reg.Get(c.B)
		// Contact.Normal is the push direction for Contact.A (unit vector pointing from B toward A).
		// Negate it for entity B so both sides receive a normal pointing away from the other.
		normalForA := c.Normal
		normalForB := collision.Vec2{X: -c.Normal.X, Y: -c.Normal.Y}
		_, wasActive := s.solidPairs[key]
		if wasActive {
			if okA {
				if h, ok := eA.(CollisionStay); ok {
					h.OnCollisionStay(entityOrNil(eB, okB), normalForA, c.Depth)
				}
			}
			if okB {
				if h, ok := eB.(CollisionStay); ok {
					h.OnCollisionStay(entityOrNil(eA, okA), normalForB, c.Depth)
				}
			}
		} else {
			if okA {
				if h, ok := eA.(CollisionEnter); ok {
					h.OnCollisionEnter(entityOrNil(eB, okB), normalForA, c.Depth)
				}
			}
			if okB {
				if h, ok := eB.(CollisionEnter); ok {
					h.OnCollisionEnter(entityOrNil(eA, okA), normalForB, c.Depth)
				}
			}
		}
	}
	for key := range s.solidPairs {
		if _, active := currentSolid[key]; active {
			continue
		}
		eA, okA := s.reg.Get(key[0])
		eB, okB := s.reg.Get(key[1])
		if okA {
			if h, ok := eA.(CollisionExit); ok {
				h.OnCollisionExit(entityOrNil(eB, okB))
			}
		}
		if okB {
			if h, ok := eB.(CollisionExit); ok {
				h.OnCollisionExit(entityOrNil(eA, okA))
			}
		}
	}

	// Swap tracking maps for next tick.
	newTrigger := make(map[[2]int64]struct{}, len(currentTrigger))
	for key := range currentTrigger {
		newTrigger[key] = struct{}{}
	}
	s.triggerPairs = newTrigger

	newSolid := make(map[[2]int64]struct{}, len(currentSolid))
	for key := range currentSolid {
		newSolid[key] = struct{}{}
	}
	s.solidPairs = newSolid
}
