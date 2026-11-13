package box3d

import (
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

type motionContactRecord struct {
	sample     b.MotionStepSample
	values     []p.ContactEvent
	start, end float32
}

type motionSensorBoundary struct {
	fraction float32
	active   bool
	event    p.ContactEvent
}

// The contact tracker already owns the exact endpoint observations. Reuse them
// only when every observed body stayed at that endpoint and no world mutation
// changed static geometry, participation, or ownership in the meantime.
type stationarySensorReplay struct {
	ready bool
	poses map[b.BodyID]b.WorldTransform
}

func (s *stationarySensorReplay) unchanged(sample b.MotionStepSample) bool {
	if !s.ready || len(s.poses) != len(sample.Bodies) {
		return false
	}
	for _, body := range sample.Bodies {
		previous, ok := s.poses[body.Body]
		if !ok || previous != body.Start || body.Start != body.End {
			return false
		}
	}
	return true
}

func (s *stationarySensorReplay) remember(sample b.MotionStepSample) {
	if s.poses == nil {
		s.poses = make(map[b.BodyID]b.WorldTransform, len(sample.Bodies))
	}
	clear(s.poses)
	for _, body := range sample.Bodies {
		s.poses[body.Body] = body.End
	}
	s.ready = true
}

func (s *motionContactState) replay(w *World, record motionContactRecord, frame, revision uint64) ([]p.ContactEvent, error) {
	if !w.config.Continuous || len(w.sensorColliders) == 0 && len(w.policyContacts) == 0 {
		values, err := w.policyEndpoint(record.values)
		if err != nil {
			return nil, err
		}
		return motionTrackEndpoint(w, values, frame, revision, record.end), nil
	}
	if len(w.policyContacts) == 0 && w.sensorReplay.unchanged(record.sample) {
		values := make([]p.ContactEvent, 0, len(record.values))
		for _, event := range record.values {
			if event.Pair.Kind != p.SensorContact {
				values = append(values, event)
			}
		}
		for _, event := range w.pendingTracker.Current() {
			if event.Pair.Kind == p.SensorContact {
				event.Force = p.Vec3{}
				values = append(values, event)
			}
		}
		return motionTrackEndpoint(w, values, frame, revision, record.end), nil
	}
	poses := make(map[b.BodyID]b.MotionBodySample, len(record.sample.Bodies))
	for _, sample := range record.sample.Bodies {
		poses[sample.Body] = sample
	}
	motion := func(h p.ColliderHandle, c *collider) ColliderMotion {
		body := w.bodies[c.desc.Body]
		if sample, ok := poses[body.id]; ok {
			return ColliderMotion{h, p.Pose{Position: pp(sample.Start.P), Rotation: pq(sample.Start.Q)}, p.Pose{Position: pp(sample.End.P), Rotation: pq(sample.End.Q)}}
		}
		pose := w.bodyState(c.desc.Body, body).Pose
		return ColliderMotion{h, pose, pose}
	}
	var targets []ColliderMotion
	for key := range w.activeBodies {
		body := w.bodies[key]
		if _, ok := poses[body.id]; !ok {
			continue
		}
		for _, h := range body.colliders {
			targets = append(targets, motion(h, w.colliders[h]))
		}
	}
	current := map[p.ContactPair]p.ContactEvent{}
	for _, event := range w.pendingTracker.Current() {
		current[event.Pair] = event
	}
	var boundaries []motionSensorBoundary
	if len(w.policyContacts) > 0 {
		var err error
		boundaries, err = w.policyBoundaries(motion, current)
		if err != nil {
			return nil, err
		}
	}
	seen := map[p.ContactPair]bool{}
	hits := make([]p.Hit, 64)
	intervals := make([]ContactInterval, 16)
	for handle, sensor := range w.sensorColliders {
		if !sensor.desc.Enabled || !w.bodies[sensor.desc.Body].id.IsEnabled() {
			continue
		}
		query := MotionQuery{Motion: motion(handle, sensor), Targets: targets, Filter: p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &sensor.desc.Filter, IncludeNonQuery: true, Sensors: p.IncludeSensors}, Mode: p.All, Initial: p.ReportInitial, Backfaces: p.IncludeBackfaces, Tolerance: .00001}
		r, err := w.queryColliderMotion(query, hits, true)
		if err == nil && r.Incomplete && r.Total <= 16384 {
			hits = make([]p.Hit, r.Total)
			r, err = w.queryColliderMotion(query, hits, true)
		}
		if err != nil || r.Incomplete {
			return nil, fmt.Errorf("physics3d: continuous sensor candidates incomplete: %v", err)
		}
		for _, hit := range hits[:r.Count] {
			other := w.colliders[hit.Collider]
			if !other.desc.Roles.Solid && !other.desc.Roles.Sensor {
				continue
			}
			event := motionCanonicalEvent(p.ContactEvent{Pair: p.ContactPair{A: handle, B: hit.Collider, Kind: p.SensorContact}, OwnerA: sensor.desc.Owner, OwnerB: other.desc.Owner})
			if seen[event.Pair] {
				continue
			}
			seen[event.Pair] = true
			ir, err := w.ColliderMotionIntervals(query.Motion, motion(hit.Collider, other), query.Tolerance, intervals)
			if err == nil && ir.Incomplete && ir.Total <= 16384 {
				intervals = make([]ContactInterval, ir.Total)
				ir, err = w.ColliderMotionIntervals(query.Motion, motion(hit.Collider, other), query.Tolerance, intervals)
			}
			if err != nil || ir.Incomplete {
				return nil, fmt.Errorf("physics3d: continuous sensor intervals incomplete: %v", err)
			}
			if _, active := current[event.Pair]; active && (ir.Count == 0 || intervals[0].Enter.Fraction > 0) {
				boundaries = append(boundaries, motionSensorBoundary{0, false, event})
			}
			for _, interval := range intervals[:ir.Count] {
				enter, exit := event, event
				enter.Point, enter.Normal, enter.Depth = interval.Enter.PointA, interval.Enter.Normal, interval.Enter.Depth
				exit.Point, exit.Normal, exit.Depth = interval.Exit.PointA, interval.Exit.Normal, interval.Exit.Depth
				if event.Pair.A != handle {
					enter.Normal, exit.Normal = enter.Normal.Mul(-1), exit.Normal.Mul(-1)
				}
				boundaries = append(boundaries, motionSensorBoundary{interval.Enter.Fraction, true, enter})
				if interval.Exit.Fraction < 1 {
					boundaries = append(boundaries, motionSensorBoundary{interval.Exit.Fraction, false, exit})
				}
				if len(boundaries) > 32768 {
					return nil, b.ErrMotionIntervalLimit
				}
			}
		}
	}
	for pair, event := range current {
		if pair.Kind == p.SensorContact && !seen[pair] {
			boundaries = append(boundaries, motionSensorBoundary{0, false, event})
		}
	}
	sort.SliceStable(boundaries, func(i, j int) bool {
		a, z := boundaries[i], boundaries[j]
		if a.fraction != z.fraction {
			return a.fraction < z.fraction
		}
		if a.event.Pair.A != z.event.Pair.A {
			return a.event.Pair.A.Index < z.event.Pair.A.Index
		}
		if a.event.Pair.B != z.event.Pair.B {
			return a.event.Pair.B.Index < z.event.Pair.B.Index
		}
		return a.active && !z.active
	})
	var events []p.ContactEvent
	for _, boundary := range boundaries {
		_, active := current[boundary.event.Pair]
		if active == boundary.active {
			continue
		}
		fraction := record.start + (record.end-record.start)*boundary.fraction
		if boundary.active {
			boundary.event.Fraction = fraction
			current[boundary.event.Pair] = boundary.event
		} else {
			delete(current, boundary.event.Pair)
		}
		values := make([]p.ContactEvent, 0, len(current))
		for _, v := range current {
			values = append(values, v)
		}
		for _, event := range motionTrackEndpoint(w, values, frame, revision, fraction) {
			if event.Pair == boundary.event.Pair && event.Phase != p.Stay {
				event.Force = p.Vec3{}
				events = append(events, event)
			}
		}
	}
	values := make([]p.ContactEvent, 0, len(record.values)+len(w.policyContacts))
	for _, event := range record.values {
		_, policy := w.policyContacts[event.Pair]
		if event.Pair.Kind != p.SensorContact && !policy {
			values = append(values, event)
		}
	}
	for pair, event := range current {
		_, policy := w.policyContacts[pair]
		if pair.Kind == p.SensorContact || policy {
			event.Force = p.Vec3{}
			if policy {
				for _, native := range record.values {
					if native.Pair == pair {
						event = native
						break
					}
				}
			}
			values = append(values, event)
		}
	}
	events = append(events, motionTrackEndpoint(w, values, frame, revision, record.end)...)
	if len(w.policyContacts) == 0 {
		w.sensorReplay.remember(record.sample)
	} else {
		w.sensorReplay.ready = false
	}
	return events, nil
}

func motionTrackEndpoint(w *World, values []p.ContactEvent, frame, revision uint64, fraction float32) []p.ContactEvent {
	events := w.pendingTracker.Update(values, frame, revision, w.restoring)
	for i := range events {
		events[i].Fraction = fraction
	}
	return events
}
