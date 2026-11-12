package box3d

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/character"
	b "github.com/gataengine/box3d-go"
	"sort"
)

// Simulate resolves one fixed-time frame privately. Mutations remain frozen until
// Accept or Reject. Every body is reported, including idle bodies moved by impacts.
func (w *World) Simulate(f p.Frame) (result p.FrameResult, err error) {
	if err = w.guard(); err != nil {
		return
	}
	if f.ID == 0 || f.ID <= w.lastFrame || !p.Finite(f.Delta) || f.Delta <= 0 || f.Delta > 1 {
		err = p.ErrInvalid
		return
	}
	motions := append([]p.Motion(nil), f.Motions...)
	sort.Slice(motions, func(i, j int) bool { return motions[i].Body.Index < motions[j].Body.Index })
	for i, m := range motions {
		v, e := w.findBody(m.Body)
		if e != nil {
			err = e
			return
		}
		if i > 0 && motions[i-1].Body == m.Body || !m.Velocity.Valid() || !m.SurfaceNormal.Valid() || !p.Finite(m.MaxDriveForce) || m.MaxDriveForce < 0 || v.desc.Mode == p.Static {
			err = p.ErrInvalid
			return
		}
		from := w.bodyState(m.Body, v).Anchor.Position
		elapsed := float32(0)
		for _, segment := range m.Segments {
			if !segment.From.InBounds() || !segment.Target.InBounds() || !segment.SurfaceNormal.Valid() || !p.Finite(segment.Delta) || segment.Delta < 0 || segment.Projection && segment.Delta != 0 || !segment.Projection && segment.Delta == 0 || segment.From.Sub(from).Length() > 0.00001 {
				err = p.ErrInvalid
				return
			}
			if segment.Projection && segment.Target != segment.From {
				err = fmt.Errorf("%w: zero-time projection requires placement admission", p.ErrUnsupported)
				return
			}
			if m.Validate != nil && !m.Validate(segment.From, segment.Target) {
				err = fmt.Errorf("%w: rejected motion segment", p.ErrInvalid)
				return
			}
			elapsed += segment.Delta
			from = segment.Target
		}
		if elapsed > f.Delta+1e-6 {
			err = fmt.Errorf("%w: proposal exceeds frame time", p.ErrInvalid)
			return
		}
		from = w.bodyState(m.Body, v).Anchor.Position
		if !from.Add(m.Velocity.Mul(f.Delta)).InBounds() || m.Validate != nil && !m.Validate(from, from.Add(m.Velocity.Mul(f.Delta))) {
			err = fmt.Errorf("%w: rejected motion proposal", p.ErrInvalid)
			return
		}
	}
	if err = w.Synchronize(); err != nil {
		return
	}
	frozen := make(map[p.BodyHandle]bool, len(motions))
	for _, m := range motions {
		frozen[m.Body] = m.Frozen
	}
	for key, v := range w.bodies {
		if frozen[key] {
			if !v.frozen {
				v.id.SetType(b.KinematicBody)
				v.frozen = true
			}
			v.id.SetLinearVelocity(b.Vec3{})
			v.id.SetAngularVelocity(b.Vec3{})
			v.forces = nil
			v.torque = p.Vec3{}
		} else if v.frozen {
			switch v.desc.Mode {
			case p.FreeDynamic, p.DrivenCharacter:
				v.id.SetType(b.DynamicBody)
				w.adjustMass(v)
			case p.Static:
				v.id.SetType(b.StaticBody)
			default:
				v.id.SetType(b.KinematicBody)
			}
			v.id.SetLinearVelocity(b.Vec3{})
			v.id.SetAngularVelocity(b.Vec3{})
			v.frozen = false
		}
	}
	keys := w.orderedActiveBodies()
	paths := make([]p.BodyPath, len(keys))
	for i, h := range keys {
		paths[i] = p.BodyPath{Body: h, Positions: []p.Vec3{w.bodies[h].committed.Anchor.Position}}
	}
	started := false
	w.mutation++
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("physics3d: solver panic: %v", v)
		}
		if err != nil && started {
			w.stats.Fault = err
			w.stats.RejectedFrames++
		}
	}()
	h := f.Delta / float32(w.config.Substeps)
	for sub := 0; sub < w.config.Substeps; sub++ {
		for _, key := range keys {
			v := w.bodies[key]
			for _, f := range v.forces {
				v.id.ApplyForce(bv(f.force), bp(f.point), true)
			}
			if v.torque != (p.Vec3{}) {
				v.id.ApplyTorque(bv(v.torque), true)
			}
		}
		for _, m := range motions {
			v := w.bodies[m.Body]
			if m.Frozen {
				continue
			}
			velocity, normal := motionVelocity(m, float32(sub)*h, h)
			switch v.desc.Mode {
			case p.DrivenCharacter:
				n := normal.Unit()
				if n.Length() == 0 {
					n = p.Vec3{Y: 1}
				}
				delta := velocity.Sub(pv(v.id.GetLinearVelocity()))
				delta = delta.Sub(n.Mul(delta.Dot(n)))
				force := delta.Mul(v.id.GetMass() / h)
				if length := force.Length(); length > m.MaxDriveForce {
					force = force.Mul(m.MaxDriveForce / length)
				}
				v.id.ApplyForceToCenter(bv(force), true)
			case p.Kinematic, p.ScriptedAction, p.ExternalPose:
				velocity, err = w.clipKinematic(m.Body, velocity, h)
				if err != nil {
					return
				}
				v.id.SetLinearVelocity(bv(velocity))
			}
		}
		started = true
		w.backend.Step(h, 1)
		for i, key := range keys {
			state := w.bodyState(key, w.bodies[key])
			if !w.bodyInBounds(w.bodies[key], state.Pose) || !state.Anchor.Position.InBounds() || !state.LinearVelocity.Valid() || !state.AngularVelocity.Valid() {
				err = fmt.Errorf("physics3d: body %v left supported state", key)
				return
			}
			paths[i].Positions = append(paths[i].Positions, state.Anchor.Position)
		}
		for _, m := range motions {
			if m.Validate != nil {
				v := w.bodies[m.Body]
				now := w.bodyState(m.Body, v).Anchor.Position
				var previous p.Vec3
				for i, key := range keys {
					if key == m.Body {
						previous = paths[i].Positions[len(paths[i].Positions)-2]
						break
					}
				}
				if !m.Validate(previous, now) {
					err = fmt.Errorf("physics3d: captured safety failed after substep")
					return
				}
			}
		}
	}
	result = p.FrameResult{ID: f.ID, Revision: w.revision + 1, Substeps: w.config.Substeps, Elapsed: f.Delta, Paths: paths}
	for _, key := range keys {
		result.Bodies = append(result.Bodies, w.bodyState(key, w.bodies[key]))
	}
	result.Events = w.contacts(f.ID, result.Revision, h)
	private := result
	private.Bodies = append([]p.BodyState(nil), result.Bodies...)
	private.Events = append([]p.ContactEvent(nil), result.Events...)
	private.Paths = nil
	w.pending = &private
	return
}

// Accept publishes every body and contact transition together.
func (w *World) Accept(id uint64) error {
	if w.closed {
		return p.ErrStale
	}
	if w.stats.Fault != nil {
		return p.ErrFaulted
	}
	if w.pending == nil || w.pending.ID != id {
		return p.ErrStale
	}
	r := w.pending
	for _, state := range r.Bodies {
		w.bodies[state.Handle].committed = state
		w.bodies[state.Handle].forces = nil
		w.bodies[state.Handle].torque = p.Vec3{}
	}
	w.revision = r.Revision
	w.stats.AcceptedFrames++
	w.mutation++
	w.lastFrame = id
	w.stats.SimulatedSeconds += float64(r.Elapsed)
	w.pending = nil
	w.cleanup = nil
	w.restoring = false
	return nil
}

func motionVelocity(m p.Motion, start, dt float32) (p.Vec3, p.Vec3) {
	if len(m.Segments) == 0 {
		return m.Velocity, m.SurfaceNormal
	}
	var displacement p.Vec3
	elapsed := float32(0)
	normal := p.Vec3{Y: 1}
	for _, s := range m.Segments {
		if s.Projection {
			continue
		}
		overlap := min(start+dt, elapsed+s.Delta) - max(start, elapsed)
		if overlap > 0 {
			displacement = displacement.Add(s.Target.Sub(s.From).Mul(overlap / s.Delta))
			normal = s.SurfaceNormal
		}
		elapsed += s.Delta
	}
	return displacement.Mul(1 / dt), normal
}

func (w *World) clipKinematic(h p.BodyHandle, velocity p.Vec3, dt float32) (p.Vec3, error) {
	v := w.bodies[h]
	delta := velocity.Mul(dt)
	for _, ch := range v.colliders {
		c := w.colliders[ch]
		if !c.desc.Enabled || !c.desc.Roles.Solid {
			continue
		}
		s := w.shapes[c.desc.Shape].desc
		pose := w.bodyState(h, v).Pose.Compose(c.desc.LocalPose)
		filter := p.QueryFilter{Mask: c.desc.Filter.Mask, ExcludeBodies: []p.BodyHandle{h}, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors}
		if s.Kind == p.Capsule && s.PointA.X == s.PointB.X && s.PointA.Z == s.PointB.Z && pose.Rotation == (p.Quat{W: 1}) {
			cfg := character.DefaultConfig()
			cfg.Radius = s.Radius
			cfg.Height = s.PointB.Sub(s.PointA).Length() + 2*s.Radius
			cfg.StepHeight = 0
			center := s.PointA.Add(s.PointB).Mul(.5)
			pose.Position = pose.Transform(center)
			m, e := cfg.Move(w, pose, delta, filter)
			if e != nil {
				return p.Vec3{}, e
			}
			delta = m.Pose.Position.Sub(pose.Position)
		} else {
			q := p.Query{Kind: p.Sweep, Mode: p.All, Shape: s, Pose: pose, Translation: delta, Filter: filter, Initial: p.ReportInitial}
			hits := make([]p.Hit, 32)
			r, e := w.Query(q, hits)
			if e != nil {
				return p.Vec3{}, e
			}
			if r.Incomplete {
				hits = make([]p.Hit, r.Total)
				r, e = w.Query(q, hits)
				if e != nil || r.Incomplete {
					return p.Vec3{}, fmt.Errorf("physics3d: movement query incomplete")
				}
			}
			for _, hit := range hits[:r.Count] {
				if !hit.Blocking || hit.Initial && hit.Normal.Dot(delta) >= 0 {
					continue
				}
				if length := delta.Length(); length > 0 {
					delta = delta.Mul(max(0, hit.Distance-.001) / length)
				}
				break
			}
		}
	}
	return delta.Mul(1 / dt), nil
}

// Reject faults the entire world, retaining the last committed public body states.
func (w *World) Reject(id uint64, cause error) error {
	if w.pending == nil || w.pending.ID != id {
		return p.ErrStale
	}
	if cause == nil {
		cause = fmt.Errorf("physics frame rejected")
	}
	w.stats.Fault = cause
	w.mutation++
	w.stats.RejectedFrames++
	w.pending = nil
	return nil
}
func (w *World) contacts(frame, revision uint64, h float32) []p.ContactEvent {
	var current []p.ContactEvent
	for _, key := range w.orderedActiveBodies() {
		v := w.bodies[key]
		data := make([]b.ContactData, v.id.GetContactCapacity())
		n := v.id.GetContactData(data)
		for _, c := range data[:n] {
			a, aok := w.shapeOwners[c.ShapeIdA]
			z, zok := w.shapeOwners[c.ShapeIdB]
			if !aok || !zok {
				continue
			}
			for _, m := range c.Manifolds {
				for i := int32(0); i < m.PointCount; i++ {
					pt := m.Points[i]
					if pt.Separation > 0 && pt.TotalNormalImpulse == 0 {
						continue
					}
					ca, cb := w.colliders[a], w.colliders[z]
					point := pp(c.ShapeIdA.GetBody().GetWorldCenter()).Add(pv(pt.AnchorA))
					current = append(current, p.ContactEvent{Pair: p.ContactPair{A: a, B: z, Kind: p.SolidContact}, OwnerA: ca.desc.Owner, OwnerB: cb.desc.Owner, Point: point, Normal: pv(m.Normal), Force: pv(m.Normal).Mul(pt.TotalNormalImpulse / h), Depth: max(0, -pt.Separation)})
					if ca.desc.Roles.Sensor || cb.desc.Roles.Sensor {
						sensor := current[len(current)-1]
						sensor.Pair.Kind = p.SensorContact
						sensor.Force = p.Vec3{}
						current = append(current, sensor)
					}
				}
			}
		}
	}
	for handle, c := range w.colliders {
		if !c.desc.Roles.Sensor {
			continue
		}
		for _, id := range c.shapes {
			if !id.IsSensor() {
				continue
			}
			visitors := make([]b.ShapeID, id.GetSensorCapacity())
			n := id.GetSensorData(visitors)
			for _, other := range visitors[:n] {
				o, ok := w.shapeOwners[other]
				if !ok {
					continue
				}
				current = append(current, p.ContactEvent{Pair: p.ContactPair{A: handle, B: o, Kind: p.SensorContact}, OwnerA: c.desc.Owner, OwnerB: w.colliders[o].desc.Owner})
			}
		}
	}
	events := w.tracker.Update(current, frame, revision, w.restoring)
	return append(append([]p.ContactEvent(nil), w.cleanup...), events...)
}
