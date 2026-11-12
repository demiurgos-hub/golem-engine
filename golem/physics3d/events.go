package physics3d

import "sort"

type ContactKind uint8

const (
	SolidContact ContactKind = iota + 1
	SensorContact
)

type ContactPhase uint8

const (
	Enter ContactPhase = iota + 1
	Stay
	Exit
	// RestoredContact rebuilds overlap state without replaying ordinary entry effects.
	RestoredContact
)

type CleanupReason uint8

const (
	Separated CleanupReason = iota
	Disabled
	Deleted
	Replaced
	Teleported
	Unloaded
	Shutdown
	Restored
)

type ContactPair struct {
	A, B ColliderHandle
	Kind ContactKind
}
type ContactEvent struct {
	Pair                 ContactPair
	OwnerA, OwnerB       int64
	Phase                ContactPhase
	Reason               CleanupReason
	Point, Normal, Force Vec3
	Depth                float32
	Frame, Revision      uint64
	// Fraction is the microstep endpoint within the containing frame.
	Fraction float32
}

// ContactTracker tracks exact generational collider pairs. Copies are returned;
// callback mutation cannot change the stored contacts or another event.
type ContactTracker struct{ pairs map[ContactPair]ContactEvent }

// Update emits enter/stay/exit transitions for a complete accepted frame.
// Restoring classifies existing contacts as Restored instead of ordinary entry.
func (t *ContactTracker) Update(current []ContactEvent, frame, revision uint64, restoring bool) []ContactEvent {
	next := make(map[ContactPair]ContactEvent, len(current))
	out := make([]ContactEvent, 0, len(current)+len(t.pairs))
	for _, v := range current {
		v = canonicalContact(v)
		if _, exists := next[v.Pair]; exists {
			continue
		}
		v.Frame = frame
		v.Revision = revision
		v.Reason = Separated
		v.Phase = Enter
		if _, ok := t.pairs[v.Pair]; ok {
			v.Phase = Stay
		}
		if restoring {
			v.Reason = Restored
			v.Phase = RestoredContact
		}
		next[v.Pair] = v
		out = append(out, v)
	}
	for pair, v := range t.pairs {
		if _, ok := next[pair]; !ok {
			v.Phase = Exit
			v.Frame = frame
			v.Revision = revision
			out = append(out, v)
		}
	}
	t.pairs = next
	sortEvents(out)
	return out
}

// Remove emits terminal events for a destroyed or disabled collider.
func (t *ContactTracker) Remove(h ColliderHandle, reason CleanupReason, revision uint64) []ContactEvent {
	var out []ContactEvent
	for p, v := range t.pairs {
		if p.A == h || p.B == h {
			v.Phase = Exit
			v.Reason = reason
			v.Revision = revision
			out = append(out, v)
			delete(t.pairs, p)
		}
	}
	sortEvents(out)
	return out
}
func canonicalContact(v ContactEvent) ContactEvent {
	if lessHandle(v.Pair.B, v.Pair.A) {
		v.Pair.A, v.Pair.B = v.Pair.B, v.Pair.A
		v.OwnerA, v.OwnerB = v.OwnerB, v.OwnerA
		v.Normal = v.Normal.Mul(-1)
		v.Force = v.Force.Mul(-1)
	}
	return v
}
func lessHandle(a, b ColliderHandle) bool {
	if a.World != b.World {
		return a.World < b.World
	}
	if a.Index != b.Index {
		return a.Index < b.Index
	}
	return a.Generation < b.Generation
}
func sortEvents(v []ContactEvent) {
	sort.Slice(v, func(i, j int) bool {
		a, b := v[i].Pair, v[j].Pair
		if a.A != b.A {
			return lessHandle(a.A, b.A)
		}
		if a.B != b.B {
			return lessHandle(a.B, b.B)
		}
		return a.Kind < b.Kind
	})
}
