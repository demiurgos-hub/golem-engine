package box3d

import (
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func (w *World) policyContactCandidates(body p.BodyHandle) ([]p.ContactPair, error) {
	var pairs []p.ContactPair
	for _, event := range w.tracker.Current() {
		a, z := w.colliders[event.Pair.A], w.colliders[event.Pair.B]
		if event.Pair.Kind != p.SolidContact || a == nil || z == nil || a.desc.Body != body && z.desc.Body != body {
			continue
		}
		if _, _, err := w.policyPairEndpoint(event.Pair); err != nil {
			return nil, err
		}
		pairs = append(pairs, event.Pair)
	}
	return pairs, nil
}

func (w *World) policyPairEnabled(pair p.ContactPair) bool {
	a, z := w.colliders[pair.A], w.colliders[pair.B]
	if a == nil || z == nil {
		return false
	}
	for _, joint := range w.joints {
		if !joint.desc.CollideConnected && (joint.desc.A == a.desc.Body && joint.desc.B == z.desc.Body || joint.desc.A == z.desc.Body && joint.desc.B == a.desc.Body) {
			return false
		}
	}
	return a.desc.Enabled && z.desc.Enabled && a.desc.Roles.Solid && z.desc.Roles.Solid &&
		w.bodies[a.desc.Body].id.IsEnabled() && w.bodies[z.desc.Body].id.IsEnabled() && p.CollisionAllowed(a.desc.Filter, z.desc.Filter)
}

func (w *World) policyPairEndpoint(pair p.ContactPair) (p.ContactEvent, bool, error) {
	event := p.ContactEvent{Pair: pair}
	if !w.policyPairEnabled(pair) {
		return event, false, nil
	}
	a, z := w.colliders[pair.A], w.colliders[pair.B]
	event.OwnerA, event.OwnerB = a.desc.Owner, z.desc.Owner
	touching := false
	for _, x := range a.shapes {
		for _, y := range z.shapes {
			hit, valid := x.PairPenetration(y)
			if !valid {
				return event, false, fmt.Errorf("%w: policy contact geometry", p.ErrUnsupported)
			}
			if (hit.Touching || hit.Penetrating) && (!touching || hit.Depth > event.Depth) {
				touching = true
				event.Point, event.Normal, event.Depth = pp(hit.PointA), pv(hit.Normal), hit.Depth
			}
		}
	}
	return event, touching, nil
}

func (w *World) policyEndpoint(values []p.ContactEvent) ([]p.ContactEvent, error) {
	for pair := range w.policyContacts {
		found := false
		for _, v := range values {
			if v.Pair == pair {
				found = true
				break
			}
		}
		if found {
			continue
		}
		event, touching, err := w.policyPairEndpoint(pair)
		if err != nil {
			return nil, err
		}
		if touching {
			values = append(values, event)
		}
	}
	return values, nil
}

func (w *World) policyBoundaries(motion func(p.ColliderHandle, *collider) ColliderMotion, current map[p.ContactPair]p.ContactEvent) ([]motionSensorBoundary, error) {
	var boundaries []motionSensorBoundary
	intervals := make([]ContactInterval, 16)
	for pair := range w.policyContacts {
		event := p.ContactEvent{Pair: pair}
		if !w.policyPairEnabled(pair) {
			if old, ok := current[pair]; ok {
				boundaries = append(boundaries, motionSensorBoundary{0, false, old})
			}
			continue
		}
		a, z := w.colliders[pair.A], w.colliders[pair.B]
		event.OwnerA, event.OwnerB = a.desc.Owner, z.desc.Owner
		r, err := w.ColliderMotionIntervals(motion(pair.A, a), motion(pair.B, z), .00001, intervals)
		if err == nil && r.Incomplete && r.Total <= 16384 {
			intervals = make([]ContactInterval, r.Total)
			r, err = w.ColliderMotionIntervals(motion(pair.A, a), motion(pair.B, z), .00001, intervals)
		}
		if err != nil {
			return nil, fmt.Errorf("physics3d: policy contact intervals incomplete: %w", err)
		}
		if r.Incomplete {
			return nil, fmt.Errorf("%w: policy contact interval capacity", p.ErrUnsupported)
		}
		if _, active := current[pair]; active && (r.Count == 0 || intervals[0].Enter.Fraction > 0) {
			boundaries = append(boundaries, motionSensorBoundary{0, false, event})
		}
		for _, interval := range intervals[:r.Count] {
			enter, exit := event, event
			enter.Point, enter.Normal, enter.Depth = interval.Enter.PointA, interval.Enter.Normal, interval.Enter.Depth
			exit.Point, exit.Normal, exit.Depth = interval.Exit.PointA, interval.Exit.Normal, interval.Exit.Depth
			boundaries = append(boundaries, motionSensorBoundary{interval.Enter.Fraction, true, enter})
			if interval.Exit.Fraction < 1 {
				boundaries = append(boundaries, motionSensorBoundary{interval.Exit.Fraction, false, exit})
			}
			if len(boundaries) > 32768 {
				return nil, fmt.Errorf("physics3d: policy contact boundary limit")
			}
		}
	}
	return boundaries, nil
}

func (w *World) prunePolicyContacts() {
	if len(w.policyContacts) == 0 {
		return
	}
	current := w.tracker.Current()
	for pair := range w.policyContacts {
		found := false
		for _, event := range current {
			if event.Pair == pair {
				found = true
				break
			}
		}
		if !found {
			delete(w.policyContacts, pair)
		}
	}
}
