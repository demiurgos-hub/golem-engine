// Package box3d adapts the pure-Go Box3D solver to Golem's physics contracts.
// A World and every query are owned by one simulation goroutine.
package box3d

import (
	"fmt"
	"sort"
	"sync/atomic"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

const UpstreamCommit = "2a00e926a8d92ae1d57a2e425aadebc4610b0ed5"

var nextWorld atomic.Uint64

type shape struct {
	desc   p.ShapeDesc
	refs   int
	cooked *CookedShape
}
type body struct {
	id        b.BodyID
	desc      p.BodyDesc
	committed p.BodyState
	colliders []p.ColliderHandle
	forces    []forceCommand
	torque    p.Vec3
	frozen    bool
	support   p.CharacterSupport
	fallSpeed float32
}
type forceCommand struct{ force, point p.Vec3 }
type collider struct {
	desc     p.ColliderDesc
	shapes   []b.ShapeID
	children map[b.ShapeID]uint32
}
type joint struct {
	id   b.JointID
	desc p.JointDesc
}
type World struct {
	id              uint64
	backend         b.WorldID
	config          p.Config
	generations     []uint32
	freeHandles     []uint32
	shapes          map[p.ShapeHandle]*shape
	bodies          map[p.BodyHandle]*body
	activeBodies    map[p.BodyHandle]struct{}
	colliders       map[p.ColliderHandle]*collider
	sensorColliders map[p.ColliderHandle]*collider
	shapeOwners     map[b.ShapeID]p.ColliderHandle
	queryContexts   []*queryScratch
	joints          map[p.JointHandle]*joint
	revision        uint64
	mutation        uint64
	lastFrame       uint64
	dirty, closed   bool
	pending         *p.FrameResult
	stats           p.Metrics
	tracker         p.ContactTracker
	pendingTracker  *p.ContactTracker
	policyContacts  map[p.ContactPair]struct{}
	cleanup         []p.ContactEvent
	restoring       bool
	materials       map[uint64]p.Material
	materialKeys    map[p.Material]uint64
	nextMaterial    uint64
}

// New creates an isolated physics world with validated simulation settings.
func New(config p.Config) (*World, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	d := b.DefaultWorldDef()
	d.Gravity = bv(config.Gravity)
	d.EnableSleep = config.AllowSleep
	d.EnableContinuous = config.Continuous
	d.WorkerCount = config.Workers
	w := &World{id: nextWorld.Add(1), backend: b.CreateWorld(&d), config: config, shapes: map[p.ShapeHandle]*shape{}, bodies: map[p.BodyHandle]*body{}, colliders: map[p.ColliderHandle]*collider{}, shapeOwners: map[b.ShapeID]p.ColliderHandle{}, joints: map[p.JointHandle]*joint{}, revision: 1}
	if err := w.backend.SetSpeculativeCCD(config.Continuous); err != nil {
		w.backend.Destroy()
		return nil, err
	}
	if err := w.backend.SetTOIContinuation(config.Continuous); err != nil {
		w.backend.Destroy()
		return nil, err
	}
	w.backend.SetConsistentParallelWarmStart(true)
	w.initMaterials()
	w.activeBodies = map[p.BodyHandle]struct{}{}
	return w, nil
}
func (w *World) guard() error {
	if w.closed {
		return p.ErrStale
	}
	if w.stats.Fault != nil {
		return fmt.Errorf("%w: %v", p.ErrFaulted, w.stats.Fault)
	}
	if w.pending != nil {
		return p.ErrBusy
	}
	return nil
}
func (w *World) changed()     { w.mutation++; w.dirty = true }
func bv(v p.Vec3) b.Vec3      { return b.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func pv(v b.Vec3) p.Vec3      { return p.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func bq(q p.Quat) b.Quat      { return b.Quat{V: b.Vec3{X: q.X, Y: q.Y, Z: q.Z}, S: q.W} }
func pq(q b.Quat) p.Quat      { return p.Quat{X: q.V.X, Y: q.V.Y, Z: q.V.Z, W: q.S} }
func bp(v p.Vec3) b.Pos       { return b.Pos{X: v.X, Y: v.Y, Z: v.Z} }
func pp(v b.Pos) p.Vec3       { return p.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func bt(v p.Pose) b.Transform { return b.Transform{P: bv(v.Position), Q: bq(v.Rotation)} }
func (w *World) findBody(h p.BodyHandle) (*body, error) {
	v, ok := w.bodies[h]
	if !ok {
		return nil, p.ErrStale
	}
	return v, nil
}
func copyShape(s p.ShapeDesc) p.ShapeDesc {
	s.Vertices = append([]p.Vec3(nil), s.Vertices...)
	s.Indices = append([]uint32(nil), s.Indices...)
	s.Children = append([]p.ShapeChild(nil), s.Children...)
	for i := range s.Children {
		s.Children[i].Shape = copyShape(s.Children[i].Shape)
	}
	return s
}

// CreateShape copies portable geometry into a reusable shape asset.
func (w *World) CreateShape(d p.ShapeDesc) (p.ShapeHandle, error) {
	if err := w.guard(); err != nil {
		return p.ShapeHandle{}, err
	}
	cooked, err := CookShape(d, DefaultCookSettings())
	if err != nil {
		return p.ShapeHandle{}, err
	}
	return w.CreateCookedShape(cooked)
}

// DestroyShape removes an unreferenced asset.
func (w *World) DestroyShape(h p.ShapeHandle) error {
	if err := w.guard(); err != nil {
		return err
	}
	v, ok := w.shapes[h]
	if !ok {
		return p.ErrStale
	}
	if v.refs != 0 {
		return fmt.Errorf("%w: shape in use", p.ErrBusy)
	}
	delete(w.shapes, h)
	w.releaseHandle(p.Handle(h))
	w.mutation++
	return nil
}

// CreateBody creates a body with a separate gameplay anchor.
func (w *World) CreateBody(d p.BodyDesc) (p.BodyHandle, error) {
	if err := w.guard(); err != nil {
		return p.BodyHandle{}, err
	}
	if err := d.Validate(); err != nil {
		return p.BodyHandle{}, err
	}
	def := b.DefaultBodyDef()
	switch d.Mode {
	case p.Static:
		def.Type = b.StaticBody
	case p.FreeDynamic, p.DrivenCharacter:
		def.Type = b.DynamicBody
	default:
		def.Type = b.KinematicBody
	}
	def.Position = bp(d.Pose.Position)
	def.Rotation = bq(d.Pose.Rotation)
	def.LinearVelocity = bv(d.LinearVelocity)
	def.AngularVelocity = bv(d.AngularVelocity)
	def.GravityScale = d.GravityScale
	def.LinearDamping = d.LinearDamping
	def.AngularDamping = d.AngularDamping
	def.IsEnabled = d.Enabled
	def.IsAwake = d.Awake
	def.EnableSleep = d.AllowSleep
	def.IsBullet = d.Continuous
	def.Name = string(d.ID)
	l := d.Locks
	def.MotionLocks = b.MotionLocks{LinearX: l.LinearX, LinearY: l.LinearY, LinearZ: l.LinearZ, AngularX: l.AngularX, AngularY: l.AngularY, AngularZ: l.AngularZ}
	if d.Mode == p.DrivenCharacter {
		def.MotionLocks.AngularX = true
		def.MotionLocks.AngularY = true
		def.MotionLocks.AngularZ = true
		def.EnableContactRecycling = false
	}
	identity, err := w.claimHandle()
	if err != nil {
		return p.BodyHandle{}, err
	}
	h := p.BodyHandle(identity)
	v := &body{id: w.backend.CreateBody(&def), desc: d}
	w.adjustMass(v)
	w.bodies[h] = v
	if d.Mode != p.Static || d.Owner != 0 {
		w.activeBodies[h] = struct{}{}
	}
	v.committed = w.bodyState(h, v)
	w.changed()
	return h, nil
}
func (w *World) bodyState(h p.BodyHandle, v *body) p.BodyState {
	pose := p.Pose{Position: pp(v.id.GetPosition()), Rotation: pq(v.id.GetRotation())}
	return p.BodyState{Handle: h, ID: v.desc.ID, Owner: v.desc.Owner, Mode: v.desc.Mode, Pose: pose, Anchor: pose.Compose(v.desc.Anchor), LinearVelocity: pv(v.id.GetLinearVelocity()), AngularVelocity: pv(v.id.GetAngularVelocity()), CenterOfMass: pp(v.id.GetWorldCenter()), Mass: v.id.GetMass(), Enabled: v.id.IsEnabled(), Awake: v.id.IsAwake(), Support: v.support}
}

// Body returns the last committed state while a frame awaits acceptance.
func (w *World) Body(h p.BodyHandle) (p.BodyState, error) {
	v, err := w.findBody(h)
	if err != nil {
		return p.BodyState{}, err
	}
	return v.committed, nil
}

// DestroyBody removes its colliders and joints, preserving explicit cleanup reasons.
func (w *World) DestroyBody(h p.BodyHandle, reason p.CleanupReason) error {
	if err := w.guard(); err != nil {
		return err
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	for j, x := range w.joints {
		if x.desc.A == h || x.desc.B == h {
			if err := w.DestroyJoint(j); err != nil {
				return err
			}
		}
	}
	for _, c := range append([]p.ColliderHandle(nil), v.colliders...) {
		if err := w.DestroyCollider(c, reason); err != nil {
			return err
		}
	}
	v.id.Destroy()
	delete(w.bodies, h)
	w.releaseHandle(p.Handle(h))
	delete(w.activeBodies, h)
	w.changed()
	return nil
}

// SetPose stages an explicit pose change. Call Synchronize before queries.
func (w *World) SetPose(h p.BodyHandle, pose p.Pose) error {
	if err := w.guard(); err != nil {
		return err
	}
	if !pose.Valid() {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	if !w.bodyInBounds(v, pose) {
		return p.ErrInvalid
	}
	if v.desc.Controller.Enabled {
		if _, err := w.controllerGeometryAt(h, pose); err != nil {
			return err
		}
	}
	v.id.SetTransform(bp(pose.Position), bq(pose.Rotation))
	w.changed()
	return nil
}

// SetVelocity sets linear and angular velocity without advancing simulation time.
func (w *World) SetVelocity(h p.BodyHandle, linear, angular p.Vec3) error {
	if err := w.guard(); err != nil {
		return err
	}
	if !linear.Valid() || !angular.Valid() {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	if v.desc.Controller.Enabled && angular != (p.Vec3{}) {
		return fmt.Errorf("%w: capsule controller rotation is pose-owned", p.ErrUnsupported)
	}
	if v.desc.Mode == p.ExternalPose && (linear != (p.Vec3{}) || angular != (p.Vec3{})) {
		return fmt.Errorf("%w: external pose requires admitted timed motion", p.ErrUnsupported)
	}
	v.id.SetLinearVelocity(bv(linear))
	v.id.SetAngularVelocity(bv(angular))
	w.changed()
	return nil
}

// SetEnabled changes body activation at a mutation boundary.
func (w *World) SetEnabled(h p.BodyHandle, enabled bool) error {
	if err := w.guard(); err != nil {
		return err
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	if enabled {
		v.id.Enable()
	} else {
		v.id.Disable()
		for _, c := range v.colliders {
			w.cleanup = append(w.cleanup, w.tracker.Remove(c, p.Disabled, w.revision+1)...)
		}
	}
	v.desc.Enabled = enabled
	w.changed()
	return nil
}

// ApplyForce queues a world-space force at a world-space point for the next step.
func (w *World) ApplyForce(h p.BodyHandle, force, point p.Vec3) error {
	if err := w.guard(); err != nil {
		return err
	}
	if !force.Valid() || !point.InBounds() {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	v.forces = append(v.forces, forceCommand{force, point})
	w.mutation++
	return nil
}

// ApplyTorque queues world-space torque for the next step.
func (w *World) ApplyTorque(h p.BodyHandle, torque p.Vec3) error {
	if err := w.guard(); err != nil {
		return err
	}
	if !torque.Valid() {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	v.torque = v.torque.Add(torque)
	w.mutation++
	return nil
}

// ApplyImpulse applies an instantaneous world-space impulse at a point.
func (w *World) ApplyImpulse(h p.BodyHandle, impulse, point p.Vec3) error {
	if err := w.guard(); err != nil {
		return err
	}
	if !impulse.Valid() || !point.InBounds() {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	v.id.ApplyLinearImpulse(bv(impulse), bp(point), true)
	w.changed()
	return nil
}

// Synchronize publishes direct mutations without integrating time or gravity.
func (w *World) Synchronize() error {
	if err := w.guard(); err != nil {
		return err
	}
	if w.dirty {
		w.revision++
		for h, v := range w.bodies {
			v.committed = w.bodyState(h, v)
		}
		w.dirty = false
	}
	return nil
}

// Metrics returns counters; no backend memory estimate is presented as measured memory.
func (w *World) Metrics() p.Metrics {
	m := w.stats
	m.Revision = w.revision
	m.Bodies = len(w.bodies)
	m.Shapes = len(w.shapes)
	m.Colliders = len(w.colliders)
	m.Joints = len(w.joints)
	m.Substeps = w.config.Substeps
	if !w.closed {
		m.Awake = w.backend.GetAwakeBodyCount()
	}
	return m
}

// Close destroys the world and invalidates all of its handles; it is idempotent.
func (w *World) Close() error {
	_, err := w.CloseWithEvents()
	return err
}

// CloseWithEvents closes the world and returns terminal accepted contact events.
// It may close a faulted or pending world; neither can publish private contacts.
func (w *World) CloseWithEvents() ([]p.ContactEvent, error) {
	if w.closed {
		return nil, nil
	}
	events := append([]p.ContactEvent(nil), w.cleanup...)
	events = append(events, w.tracker.Drain(p.Shutdown, w.lastFrame, w.revision)...)
	w.backend.Destroy()
	w.mutation++
	w.closed = true
	w.bodies = nil
	w.colliders = nil
	w.sensorColliders = nil
	w.shapes = nil
	w.joints = nil
	w.shapeOwners = nil
	w.activeBodies = nil
	w.queryContexts = nil
	w.generations, w.freeHandles = nil, nil
	w.pending, w.cleanup = nil, nil
	w.pendingTracker = nil
	w.tracker = p.ContactTracker{}
	w.materials, w.materialKeys = nil, nil
	return events, nil
}
func (w *World) orderedBodies() []p.BodyHandle {
	keys := make([]p.BodyHandle, 0, len(w.bodies))
	for k := range w.bodies {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Index < keys[j].Index })
	return keys
}
