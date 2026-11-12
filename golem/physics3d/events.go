package physics3d

import "slices"

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
	// Fraction is the contact boundary or microstep endpoint within the frame.
	Fraction float32
}

// ContactTracker tracks exact generational collider pairs. Copies are returned;
// callback mutation cannot change the stored contacts or another event.
type ContactTracker struct {
	pairs map[ContactPair]ContactEvent
	spare map[ContactPair]ContactEvent
}

// Clone returns independent contact history for a private simulation transaction.
func (t *ContactTracker) Clone() ContactTracker {
	next := ContactTracker{pairs: make(map[ContactPair]ContactEvent, len(t.pairs))}
	for pair, event := range t.pairs {
		next.pairs[pair] = event
	}
	return next
}

// Current returns independent, stably ordered copies of retained contact pairs.
func (t *ContactTracker) Current() []ContactEvent {
	out := make([]ContactEvent, 0, len(t.pairs))
	for _, event := range t.pairs {
		out = append(out, event)
	}
	sortEvents(out)
	return out
}

// Drain ends every retained pair once with zero force and clears its history.
func (t *ContactTracker) Drain(reason CleanupReason, frame, revision uint64) []ContactEvent {
	out := make([]ContactEvent, 0, len(t.pairs))
	for _, event := range t.pairs {
		event.Phase, event.Reason = Exit, reason
		event.Force = Vec3{}
		event.Frame, event.Revision, event.Fraction = frame, revision, 1
		out = append(out, event)
	}
	t.pairs = nil
	t.spare = nil
	sortEvents(out)
	return out
}

// ShutdownWorld optionally returns terminal contact events when closing a world.
// Only accepted pair history is eligible; pending or rejected frames cannot add
// pairs. Repeated calls return no events. Mutations are closed before returning.
type ShutdownWorld interface {
	CloseWithEvents() ([]ContactEvent, error)
}

// Update emits enter/stay/exit transitions for a complete accepted frame.
// Restoring classifies existing contacts as Restored instead of ordinary entry.
// Exit events retain the last geometry but never repeat its solver force.
func (t *ContactTracker) Update(current []ContactEvent, frame, revision uint64, restoring bool) []ContactEvent {
	next := t.spare
	if next == nil {
		next = make(map[ContactPair]ContactEvent, len(current))
	} else {
		clear(next)
	}
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
			v.Reason = Separated
			v.Force = Vec3{}
			v.Frame = frame
			v.Revision = revision
			out = append(out, v)
		}
	}
	t.spare, t.pairs = t.pairs, next
	sortEvents(out)
	return out
}

// Remove emits zero-force terminal events for a destroyed or disabled collider.
func (t *ContactTracker) Remove(h ColliderHandle, reason CleanupReason, revision uint64) []ContactEvent {
	var out []ContactEvent
	for p, v := range t.pairs {
		if p.A == h || p.B == h {
			v.Phase = Exit
			v.Force = Vec3{}
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
	slices.SortFunc(v, func(x, y ContactEvent) int {
		a, b := x.Pair, y.Pair
		if a.A != b.A {
			if lessHandle(a.A, b.A) {
				return -1
			}
			return 1
		}
		if a.B != b.B {
			if lessHandle(a.B, b.B) {
				return -1
			}
			return 1
		}
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	})
}
