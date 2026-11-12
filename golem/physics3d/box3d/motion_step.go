package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

type motionContactState struct {
	sensors map[[2]b.ShapeID]bool
	current map[p.ContactPair]int
	bodies  map[b.BodyID]b.MotionBodySample
}

func (s *motionContactState) prepareBodies(sample b.MotionStepSample) {
	if s.bodies == nil {
		s.bodies = make(map[b.BodyID]b.MotionBodySample, len(sample.Bodies))
	}
	clear(s.bodies)
	for _, body := range sample.Bodies {
		s.bodies[body.Body] = body
	}
}

func (s *motionContactState) bodyState(w *World, key p.BodyHandle, body *body) p.BodyState {
	state := w.bodyState(key, body)
	if sample, ok := s.bodies[body.id]; ok {
		state.Pose = p.Pose{Position: pp(sample.End.P), Rotation: pq(sample.End.Q)}
		state.Anchor = state.Pose.Compose(body.desc.Anchor)
		state.CenterOfMass = state.Pose.Transform(pv(body.id.GetLocalCenter()))
		state.LinearVelocity, state.AngularVelocity = pv(sample.LinearVelocity), pv(sample.AngularVelocity)
	}
	return state
}

func (s *motionContactState) worldCenter(body b.BodyID) p.Vec3 {
	if sample, ok := s.bodies[body]; ok {
		pose := p.Pose{Position: pp(sample.End.P), Rotation: pq(sample.End.Q)}
		return pose.Transform(pv(body.GetLocalCenter()))
	}
	return pp(body.GetWorldCenter())
}

func motionSensorKey(a, z b.ShapeID) [2]b.ShapeID {
	if z.Index1 < a.Index1 {
		a, z = z, a
	}
	return [2]b.ShapeID{a, z}
}

func (w *World) newMotionContactState() *motionContactState {
	s := &motionContactState{sensors: map[[2]b.ShapeID]bool{}}
	for _, c := range w.sensorColliders {
		for _, id := range c.shapes {
			if !id.IsSensor() {
				continue
			}
			visitors := make([]b.ShapeID, id.GetSensorCapacity())
			for _, other := range visitors[:id.GetSensorData(visitors)] {
				s.sensors[motionSensorKey(id, other)] = true
			}
		}
	}
	return s
}

func motionCanonicalEvent(v p.ContactEvent) p.ContactEvent {
	if v.Pair.B.Index < v.Pair.A.Index {
		v.Pair.A, v.Pair.B = v.Pair.B, v.Pair.A
		v.OwnerA, v.OwnerB = v.OwnerB, v.OwnerA
		v.Normal, v.Force = v.Normal.Mul(-1), v.Force.Mul(-1)
	}
	return v
}

func (s *motionContactState) capture(w *World, sample b.MotionStepSample, fraction float32) []p.ContactEvent {
	s.prepareBodies(sample)
	return s.capturePrepared(w, sample, fraction)
}

func (s *motionContactState) capturePrepared(w *World, sample b.MotionStepSample, fraction float32) []p.ContactEvent {
	impulseDuration := sample.ImpulseDuration
	if impulseDuration == 0 {
		impulseDuration = sample.Delta
	}
	if s.current == nil {
		s.current = make(map[p.ContactPair]int)
	}
	values := make([]p.ContactEvent, 0, len(s.current))
	clear(s.current)
	add := func(v p.ContactEvent) {
		v = motionCanonicalEvent(v)
		index, ok := s.current[v.Pair]
		if ok {
			old := values[index]
			force := old.Force.Add(v.Force)
			if old.Depth >= v.Depth {
				v = old
			}
			v.Force = force
		}
		v.Fraction = fraction
		if ok {
			values[index] = v
		} else {
			s.current[v.Pair] = len(values)
			values = append(values, v)
		}
	}
	captureContact := func(c b.ContactData, impulseOnly bool) {
		a, aok := w.shapeOwners[c.ShapeIdA]
		z, zok := w.shapeOwners[c.ShapeIdB]
		if !aok || !zok {
			return
		}
		ca, cz := w.colliders[a], w.colliders[z]
		for _, m := range c.Manifolds {
			for i := int32(0); i < m.PointCount; i++ {
				point := m.Points[i]
				if impulseOnly && point.TotalNormalImpulse == 0 {
					continue
				}
				if point.Separation > 0 && point.TotalNormalImpulse == 0 {
					continue
				}
				v := p.ContactEvent{Pair: p.ContactPair{A: a, B: z, Kind: p.SolidContact}, OwnerA: ca.desc.Owner, OwnerB: cz.desc.Owner,
					Point: s.worldCenter(c.ShapeIdA.GetBody()).Add(pv(point.AnchorA)), Normal: pv(m.Normal),
					Force: pv(m.Normal).Mul(point.TotalNormalImpulse / impulseDuration), Depth: max(0, -point.Separation)}
				add(v)
				if !impulseOnly && point.Separation <= 0 && (ca.desc.Roles.Sensor || cz.desc.Roles.Sensor) {
					v.Pair.Kind = p.SensorContact
					v.Force = p.Vec3{}
					add(v)
				}
			}
		}
	}
	for _, c := range sample.Contacts {
		captureContact(c, false)
	}
	// A speculative constraint can transfer force before geometric touching.
	// Its owned witness contributes a solid response, never a sensor overlap.
	for _, c := range sample.ImpulseContacts {
		captureContact(c, true)
	}
	for _, event := range sample.SensorEvents.EndEvents {
		delete(s.sensors, motionSensorKey(event.SensorShapeId, event.VisitorShapeId))
	}
	for _, event := range sample.SensorEvents.BeginEvents {
		s.sensors[motionSensorKey(event.SensorShapeId, event.VisitorShapeId)] = true
	}
	for pair := range s.sensors {
		a, aok := w.shapeOwners[pair[0]]
		z, zok := w.shapeOwners[pair[1]]
		if !aok || !zok {
			continue
		}
		ca, cz := w.colliders[a], w.colliders[z]
		if !ca.desc.Roles.Sensor && !cz.desc.Roles.Sensor {
			continue
		}
		add(p.ContactEvent{Pair: p.ContactPair{A: a, B: z, Kind: p.SensorContact}, OwnerA: ca.desc.Owner, OwnerB: cz.desc.Owner})
	}
	return values
}

func (s *motionContactState) observePrepared(w *World, sample b.MotionStepSample, frame, revision uint64, fraction float32) []p.ContactEvent {
	events := w.pendingTracker.Update(s.capturePrepared(w, sample, fraction), frame, revision, w.restoring)
	for i := range events {
		events[i].Fraction = fraction
	}
	return events
}
