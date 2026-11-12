package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

type motionContactState struct{ sensors map[[2]b.ShapeID]bool }

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

func (s *motionContactState) observe(w *World, sample b.MotionStepSample, frame, revision uint64, fraction float32) []p.ContactEvent {
	current := map[p.ContactPair]p.ContactEvent{}
	add := func(v p.ContactEvent) {
		v = motionCanonicalEvent(v)
		if old, ok := current[v.Pair]; ok {
			force := old.Force.Add(v.Force)
			if old.Depth >= v.Depth {
				v = old
			}
			v.Force = force
		}
		v.Fraction = fraction
		current[v.Pair] = v
	}
	for _, c := range sample.Contacts {
		a, aok := w.shapeOwners[c.ShapeIdA]
		z, zok := w.shapeOwners[c.ShapeIdB]
		if !aok || !zok {
			continue
		}
		ca, cz := w.colliders[a], w.colliders[z]
		for _, m := range c.Manifolds {
			for i := int32(0); i < m.PointCount; i++ {
				point := m.Points[i]
				if point.Separation > 0 && point.TotalNormalImpulse == 0 {
					continue
				}
				v := p.ContactEvent{Pair: p.ContactPair{A: a, B: z, Kind: p.SolidContact}, OwnerA: ca.desc.Owner, OwnerB: cz.desc.Owner,
					Point: pp(c.ShapeIdA.GetBody().GetWorldCenter()).Add(pv(point.AnchorA)), Normal: pv(m.Normal),
					Force: pv(m.Normal).Mul(point.TotalNormalImpulse / sample.Delta), Depth: max(0, -point.Separation)}
				add(v)
				if ca.desc.Roles.Sensor || cz.desc.Roles.Sensor {
					v.Pair.Kind = p.SensorContact
					v.Force = p.Vec3{}
					add(v)
				}
			}
		}
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
	values := make([]p.ContactEvent, 0, len(current))
	for _, v := range current {
		values = append(values, v)
	}
	events := w.pendingTracker.Update(values, frame, revision, w.restoring)
	for i := range events {
		events[i].Fraction = fraction
	}
	return events
}

func (w *World) captureMotionSample(delta float32) b.MotionStepSample {
	s := b.MotionStepSample{Delta: delta, EndFraction: 1, SensorEvents: w.backend.GetSensorEvents()}
	seen := map[b.ContactID]bool{}
	for key := range w.activeBodies {
		id := w.bodies[key].id
		contacts := make([]b.ContactData, id.GetContactCapacity())
		for _, c := range contacts[:id.GetContactData(contacts)] {
			if !seen[c.ContactId] {
				seen[c.ContactId] = true
				s.Contacts = append(s.Contacts, c)
			}
		}
	}
	return s
}
