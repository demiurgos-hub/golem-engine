package box3d

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
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
		if len(m.PoseSegments) > 0 {
			if !w.config.Continuous || len(m.Segments) > 0 || len(m.PoseSegments) > 4096 || (v.desc.Mode != p.ScriptedAction && v.desc.Mode != p.ExternalPose) {
				err = p.ErrUnsupported
				return
			}
			pose := w.bodyState(m.Body, v).Pose
			var duration float32
			for _, segment := range m.PoseSegments {
				if !segment.From.Valid() || !segment.Target.Valid() || !p.Finite(segment.Delta) || segment.Delta <= 0 || segment.From.Position.Sub(pose.Position).Length() > 1e-5 || segment.From.Rotation != pose.Rotation || !w.bodyInBounds(v, segment.Target) {
					err = p.ErrInvalid
					return
				}
				if v.desc.Controller.Enabled && segment.Target.Rotation != segment.From.Rotation {
					err = p.ErrUnsupported
					return
				}
				if m.Validate != nil && !m.Validate(segment.From.Compose(v.desc.Anchor).Position, segment.Target.Compose(v.desc.Anchor).Position) {
					err = p.ErrInvalid
					return
				}
				duration += segment.Delta
				pose = segment.Target
			}
			if duration > f.Delta+1e-6 {
				err = p.ErrInvalid
				return
			}
		}
		elapsed := float32(0)
		for index, segment := range m.Segments {
			if !segment.From.InBounds() || !segment.Target.InBounds() || !segment.SurfaceNormal.Valid() || !p.Finite(segment.Delta) || segment.Delta < 0 || segment.Projection && segment.Delta != 0 || !segment.Projection && segment.Delta == 0 || segment.From.Sub(from).Length() > 0.00001 {
				err = p.ErrInvalid
				return
			}
			if segment.Projection && (index != 0 || segment.Target.Sub(segment.From).Length() > .5) {
				err = fmt.Errorf("%w: initial projection exceeds its bounded admission", p.ErrInvalid)
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
	for _, motion := range motions {
		if len(motion.PoseSegments) > 0 {
			if err = w.ValidatePoseMotion(motion.Body, motion.PoseSegments); err != nil {
				return
			}
		}
	}
	for key := range w.activeBodies {
		if w.bodyForHandle(key).desc.Controller.Enabled {
			if _, err = w.controllerGeometry(key); err != nil {
				return
			}
		}
	}
	projections := map[p.BodyHandle]p.Pose{}
	var rejected []p.MotionRejection
	for i, m := range motions {
		if m.Frozen || len(m.Segments) == 0 || !m.Segments[0].Projection {
			continue
		}
		delta := m.Segments[0].Target.Sub(m.Segments[0].From)
		if delta == (p.Vec3{}) {
			continue
		}
		var clear bool
		clear, err = w.projectionClear(m.Body, delta)
		if err != nil {
			return
		}
		if !clear {
			rejected = append(rejected, p.MotionRejection{Body: m.Body, Reason: "initial projection blocked"})
			motions[i].Segments = nil
			motions[i].Velocity = p.Vec3{}
			motions[i].MaxDriveForce = 0
			continue
		}
		pose := w.bodyState(m.Body, w.bodyForHandle(m.Body)).Pose
		pose.Position = pose.Position.Add(delta)
		projections[m.Body] = pose
	}
	frozen := make(map[p.BodyHandle]bool, len(motions))
	for _, m := range motions {
		frozen[m.Body] = m.Frozen
	}
	for key := range w.activeBodies {
		v := w.bodyForHandle(key)
		if v.desc.Mode == p.ExternalPose {
			v.id.SetLinearVelocity(b.Vec3{})
			v.id.SetAngularVelocity(b.Vec3{})
		}
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
	pathCapacity := w.config.Substeps + 1
	positions := make([]p.Vec3, len(keys)*pathCapacity)
	poses := make([]p.Pose, len(keys)*pathCapacity)
	fractions := make([]float32, len(keys)*pathCapacity)
	for i, h := range keys {
		start, end := i*pathCapacity, (i+1)*pathCapacity
		// Capacity fences keep each body's appended observations independently owned.
		paths[i] = p.BodyPath{Body: h, Positions: positions[start : start+1 : end], Poses: poses[start : start+1 : end], Fractions: fractions[start : start+1 : end]}
		paths[i].Positions[0] = w.bodyForHandle(h).committed.Anchor.Position
		paths[i].Poses[0] = w.bodyForHandle(h).committed.Pose
	}
	started := false
	tracker := w.tracker.Clone()
	w.pendingTracker = &tracker
	w.mutation++
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("physics3d: solver panic: %v", v)
		}
		if err != nil && started {
			w.stats.Fault = err
			w.stats.RejectedFrames++
		}
		if err != nil {
			w.pendingTracker = nil
		}
	}()
	for i, key := range keys {
		if pose, ok := projections[key]; ok {
			started = true
			w.bodyForHandle(key).id.SetTransform(bp(pose.Position), bq(pose.Rotation))
			paths[i].Positions = append(paths[i].Positions, w.bodyState(key, w.bodyForHandle(key)).Anchor.Position)
			paths[i].Poses = append(paths[i].Poses, pose)
			paths[i].Fractions = append(paths[i].Fractions, 0)
		}
	}
	result = p.FrameResult{ID: f.ID, Revision: w.revision + 1, Substeps: w.config.Substeps, Paths: paths, RejectedMotions: rejected, Events: append([]p.ContactEvent(nil), w.cleanup...)}
	started = true
	if err = w.integrateIntervals(f, motions, keys, paths, &result); err != nil {
		return
	}
	result.Elapsed = f.Delta
	for _, key := range keys {
		result.Bodies = append(result.Bodies, w.bodyState(key, w.bodyForHandle(key)))
	}
	private := result
	private.Bodies = append([]p.BodyState(nil), result.Bodies...)
	private.Events = nil
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
		w.bodyForHandle(state.Handle).committed = state
		w.bodyForHandle(state.Handle).forces = nil
		w.bodyForHandle(state.Handle).torque = p.Vec3{}
	}
	w.revision = r.Revision
	w.stats.AcceptedFrames++
	w.mutation++
	w.lastFrame = id
	w.stats.SimulatedSeconds += float64(r.Elapsed)
	w.tracker = *w.pendingTracker
	w.prunePolicyContacts()
	w.pendingTracker = nil
	w.pending = nil
	w.cleanup = nil
	w.restoring = false
	return nil
}

func motionVelocity(m p.Motion, start, dt float32) (p.Vec3, p.Vec3) {
	if len(m.Segments) == 0 {
		return m.Velocity, m.SurfaceNormal
	}
	elapsed := float32(0)
	for _, s := range m.Segments {
		if s.Projection {
			continue
		}
		if start < elapsed+s.Delta {
			return s.Target.Sub(s.From).Mul(1 / s.Delta), s.SurfaceNormal
		}
		elapsed += s.Delta
	}
	return p.Vec3{}, p.Vec3{Y: 1}
}

func (w *World) clipKinematic(h p.BodyHandle, velocity p.Vec3, dt float32) (p.Vec3, error) {
	v := w.bodyForHandle(h)
	delta := velocity.Mul(dt)
	for _, ch := range v.colliders {
		c := w.colliderForHandle(ch)
		if !c.desc.Enabled || !c.desc.Roles.Solid {
			continue
		}
		s := w.shapeForHandle(c.desc.Shape).desc
		if containsMesh(s) {
			// Kinematic triangle response uses native continuous contact solving. Passenger
			// and ceiling transport policy is handled by the host before solving.
			continue
		}
		pose := w.bodyState(h, v).Pose.Compose(c.desc.LocalPose)
		filter := p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &c.desc.Filter, ExcludeBodies: []p.BodyHandle{h}, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors}
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
	w.pendingTracker = nil
	return nil
}
