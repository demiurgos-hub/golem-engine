package physics3d

import "sort"

// ContactParticipant identifies an owner, or an exact unowned collider. Collider
// is zero for an owned participant, so its child colliders aggregate explicitly.
type ContactParticipant struct {
	Owner    int64
	Collider ColliderHandle
}

// OwnerContactPair aggregates one contact kind without merging unowned geometry.
type OwnerContactPair struct {
	A, B ContactParticipant
	Kind ContactKind
}

// OwnerContactEvent describes an aggregate transition. ChildPairs counts live
// generational collider pairs after the transition. Force sums their latest
// forces; Point and Normal describe the deepest remaining child constraint.
type OwnerContactEvent struct {
	Pair                 OwnerContactPair
	Phase                ContactPhase
	Reason               CleanupReason
	ChildPairs           int
	Point, Normal, Force Vec3
	Depth                float32
	Frame, Revision      uint64
	Fraction             float32
}

// OwnerContactTracker optionally aggregates accepted exact-pair events. A child
// exit does not produce an owner exit while another child remains in contact.
// Feed each accepted event once, including lifecycle cleanup and restore events.
type OwnerContactTracker struct{ pairs map[ContactPair]ContactEvent }

// Reset discards transient pair state when restoring or replacing the entire
// world. Feed the replacement world's RestoredContact events after resetting.
func (t *OwnerContactTracker) Reset() { t.pairs = nil }

// Update applies events in frame/fraction order and emits one transition per
// affected owner pair at each boundary. Simultaneous child replacement is a stay;
// a swept entry and exit at different fractions remain separate transitions.
func (t *OwnerContactTracker) Update(events []ContactEvent) []OwnerContactEvent {
	if t.pairs == nil {
		t.pairs = make(map[ContactPair]ContactEvent)
	}
	ordered := append([]ContactEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Frame != ordered[j].Frame {
			return ordered[i].Frame < ordered[j].Frame
		}
		return ordered[i].Fraction < ordered[j].Fraction
	})
	var out []OwnerContactEvent
	for start := 0; start < len(ordered); {
		end := start + 1
		for end < len(ordered) && ordered[end].Frame == ordered[start].Frame && ordered[end].Fraction == ordered[start].Fraction {
			end++
		}
		changed := make(map[OwnerContactPair]ContactEvent)
		before := t.aggregates()
		for _, event := range ordered[start:end] {
			event = canonicalContact(event)
			if old, ok := t.pairs[event.Pair]; ok {
				changed[ownerContact(old).Pair] = event
			}
			changed[ownerContact(event).Pair] = event
			if event.Phase == Exit {
				delete(t.pairs, event.Pair)
			} else {
				t.pairs[event.Pair] = event
			}
		}
		after := t.aggregates()
		keys := make([]OwnerContactPair, 0, len(changed))
		for key := range changed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return ownerPairLess(keys[i], keys[j]) })
		for _, key := range keys {
			old, now := before[key], after[key]
			if old.ChildPairs == 0 && now.ChildPairs == 0 {
				continue
			}
			reason := changed[key]
			v := now
			if now.ChildPairs == 0 {
				v = old
				v.ChildPairs, v.Force, v.Phase = 0, Vec3{}, Exit
			} else if old.ChildPairs == 0 {
				v.Phase = Enter
			} else {
				v.Phase = Stay
			}
			if reason.Phase == RestoredContact && now.ChildPairs > 0 {
				v.Phase = RestoredContact
			}
			v.Reason, v.Frame, v.Revision, v.Fraction = reason.Reason, reason.Frame, reason.Revision, reason.Fraction
			out = append(out, v)
		}
		start = end
	}
	return out
}

func (t *OwnerContactTracker) aggregates() map[OwnerContactPair]OwnerContactEvent {
	out := make(map[OwnerContactPair]OwnerContactEvent)
	// Stable child ordering also fixes deepest-constraint ties and force summation.
	events := make([]ContactEvent, 0, len(t.pairs))
	for _, event := range t.pairs {
		events = append(events, event)
	}
	sortEvents(events)
	for _, event := range events {
		v := ownerContact(event)
		previous := out[v.Pair]
		if previous.ChildPairs == 0 || v.Depth > previous.Depth {
			previous.Pair, previous.Point, previous.Normal, previous.Depth = v.Pair, v.Point, v.Normal, v.Depth
		}
		previous.ChildPairs++
		previous.Force = previous.Force.Add(v.Force)
		out[v.Pair] = previous
	}
	return out
}

func ownerContact(e ContactEvent) OwnerContactEvent {
	a := ContactParticipant{Owner: e.OwnerA}
	b := ContactParticipant{Owner: e.OwnerB}
	if a.Owner == 0 {
		a.Collider = e.Pair.A
	}
	if b.Owner == 0 {
		b.Collider = e.Pair.B
	}
	if participantLess(b, a) {
		a, b = b, a
		e.Normal, e.Force = e.Normal.Mul(-1), e.Force.Mul(-1)
	}
	return OwnerContactEvent{Pair: OwnerContactPair{A: a, B: b, Kind: e.Pair.Kind}, Point: e.Point, Normal: e.Normal, Force: e.Force, Depth: e.Depth}
}
func participantLess(a, b ContactParticipant) bool {
	if a.Owner != b.Owner {
		return a.Owner < b.Owner
	}
	return lessHandle(a.Collider, b.Collider)
}
func ownerPairLess(a, b OwnerContactPair) bool {
	if a.A != b.A {
		return participantLess(a.A, b.A)
	}
	if a.B != b.B {
		return participantLess(a.B, b.B)
	}
	return a.Kind < b.Kind
}
