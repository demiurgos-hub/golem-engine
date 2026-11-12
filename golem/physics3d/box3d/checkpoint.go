package box3d

import (
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// CaptureCheckpoint copies accepted state and unconsumed forces using stable
// authored IDs. Synchronize direct changes before capture; pending frames and
// geometry without unique stable IDs cannot be checkpointed.
func (w *World) CaptureCheckpoint(id, fingerprint string) (p.Checkpoint, error) {
	if err := w.guard(); err != nil {
		return p.Checkpoint{}, err
	}
	if w.dirty {
		return p.Checkpoint{}, fmt.Errorf("%w: synchronize before checkpoint capture", p.ErrBusy)
	}
	c := p.Checkpoint{Version: 1, ID: id, SourceFingerprint: fingerprint, Revision: w.revision, LastFrame: w.lastFrame, Config: w.config}
	for _, shape := range w.shapes {
		c.Shapes = append(c.Shapes, copyShape(shape.desc))
	}
	for _, body := range w.bodies {
		d := body.desc
		s := body.committed
		d.Pose, d.LinearVelocity, d.AngularVelocity, d.Enabled, d.Awake = s.Pose, s.LinearVelocity, s.AngularVelocity, s.Enabled, s.Awake
		record := p.CheckpointBody{Description: d, Torque: body.torque}
		for _, force := range body.forces {
			record.Forces = append(record.Forces, p.CheckpointForce{Force: force.force, Point: force.point})
		}
		c.Bodies = append(c.Bodies, record)
	}
	for _, collider := range w.colliders {
		c.Colliders = append(c.Colliders, p.CaptureCollider(collider.desc, w.bodies[collider.desc.Body].desc.ID, w.shapes[collider.desc.Shape].desc.ID))
	}
	for _, joint := range w.joints {
		record, err := p.CaptureJoint(joint.desc, w.bodies[joint.desc.A].desc.ID, w.bodies[joint.desc.B].desc.ID)
		if err != nil {
			return p.Checkpoint{}, err
		}
		c.Joints = append(c.Joints, record)
	}
	sort.Slice(c.Shapes, func(i, j int) bool { return c.Shapes[i].ID < c.Shapes[j].ID })
	sort.Slice(c.Bodies, func(i, j int) bool { return c.Bodies[i].Description.ID < c.Bodies[j].Description.ID })
	sort.Slice(c.Colliders, func(i, j int) bool { return c.Colliders[i].ID < c.Colliders[j].ID })
	sort.Slice(c.Joints, func(i, j int) bool { return c.Joints[i].ID < c.Joints[j].ID })
	if err := c.Validate(); err != nil {
		return p.Checkpoint{}, err
	}
	return c, nil
}

type preparedRestore struct {
	target, candidate *World
	world, mutation   uint64
	bodies            map[p.ContentID]p.BodyHandle
	shapes            map[p.ContentID]p.ShapeHandle
	colliders         map[p.ContentID]p.ColliderHandle
	joints            map[p.ContentID]p.JointHandle
	closed            bool
}

// PrepareRestore constructs a private replacement without altering live state.
// Faulted worlds may be restored, but pending frames must first be rejected.
func (w *World) PrepareRestore(c p.Checkpoint) (result p.PreparedRestore, err error) {
	if w.closed {
		return nil, p.ErrStale
	}
	if w.pending != nil {
		return nil, p.ErrBusy
	}
	if err = c.Validate(); err != nil {
		return nil, err
	}
	next, err := New(c.Config)
	if err != nil {
		return nil, err
	}
	prepared := &preparedRestore{target: w, candidate: next, world: w.id, mutation: w.mutation, bodies: map[p.ContentID]p.BodyHandle{}, colliders: map[p.ContentID]p.ColliderHandle{}, joints: map[p.ContentID]p.JointHandle{}}
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("physics3d: restoring checkpoint: %v", value)
		}
		if err != nil {
			prepared.Discard()
			result = nil
		}
	}()
	shapes := map[p.ContentID]p.ShapeHandle{}
	prepared.shapes = shapes
	for _, desc := range c.Shapes {
		h, e := next.CreateShape(desc)
		if e != nil {
			return nil, e
		}
		shapes[desc.ID] = h
	}
	for _, record := range c.Bodies {
		h, e := next.CreateBody(record.Description)
		if e != nil {
			return nil, e
		}
		prepared.bodies[record.Description.ID] = h
	}
	for _, record := range c.Colliders {
		h, e := next.CreateCollider(record.Description(prepared.bodies[record.Body], shapes[record.Shape]))
		if e != nil {
			return nil, e
		}
		prepared.colliders[record.ID] = h
	}
	for _, record := range c.Joints {
		desc, e := record.DecodeJoint(prepared.bodies[record.A], prepared.bodies[record.B])
		if e != nil {
			return nil, e
		}
		h, e := next.CreateJoint(desc)
		if e != nil {
			return nil, e
		}
		prepared.joints[record.ID] = h
	}
	for _, record := range c.Bodies {
		h := prepared.bodies[record.Description.ID]
		if e := next.SetVelocity(h, record.Description.LinearVelocity, record.Description.AngularVelocity); e != nil {
			return nil, e
		}
		next.bodies[h].id.SetAwake(record.Description.Awake)
		for _, force := range record.Forces {
			if e := next.ApplyForce(h, force.Force, force.Point); e != nil {
				return nil, e
			}
		}
		if e := next.ApplyTorque(h, record.Torque); e != nil {
			return nil, e
		}
	}
	if err = next.Synchronize(); err != nil {
		return nil, err
	}
	next.revision, next.lastFrame, next.restoring = c.Revision, c.LastFrame, true
	return prepared, nil
}

// Body returns an accepted replacement body by its stable content identity.
func (r *preparedRestore) Body(id p.ContentID) (p.BodyState, bool) {
	if r.closed {
		return p.BodyState{}, false
	}
	h, ok := r.bodies[id]
	if !ok {
		return p.BodyState{}, false
	}
	state, err := r.candidate.Body(h)
	return state, err == nil
}

// FindBody resolves a body in the private replacement.
func (r *preparedRestore) FindBody(id p.ContentID) (p.BodyState, bool) { return r.Body(id) }

// FindShape resolves a shape in the private replacement.
func (r *preparedRestore) FindShape(id p.ContentID) (p.ShapeHandle, bool) {
	h, ok := r.shapes[id]
	return h, ok && !r.closed
}

// FindCollider resolves a collider in the private replacement.
func (r *preparedRestore) FindCollider(id p.ContentID) (p.ColliderHandle, bool) {
	h, ok := r.colliders[id]
	return h, ok && !r.closed
}

// FindJoint resolves a joint in the private replacement.
func (r *preparedRestore) FindJoint(id p.ContentID) (p.JointHandle, bool) {
	h, ok := r.joints[id]
	return h, ok && !r.closed
}

// Commit atomically swaps prepared state and invalidates every old world handle.
func (r *preparedRestore) Commit() error {
	if r.closed {
		return p.ErrStale
	}
	w := r.target
	if w.closed || w.id != r.world || w.mutation != r.mutation || w.pending != nil {
		return fmt.Errorf("%w: checkpoint target changed", p.ErrStale)
	}
	old := w.backend
	*w = *r.candidate
	w.mutation = r.mutation + 1
	r.closed = true
	r.candidate = nil
	old.Destroy()
	return nil
}

// Discard releases the private replacement; it never changes the target world.
func (r *preparedRestore) Discard() {
	if r.closed {
		return
	}
	r.closed = true
	_ = r.candidate.Close()
	r.candidate = nil
}

var _ p.CheckpointWorld = (*World)(nil)
