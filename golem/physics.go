package golem

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// PhysicsRotationWriter optionally receives the accepted gameplay anchor rotation.
// Like Position3DWriter, this setter must only update data, without callbacks.
type PhysicsRotationWriter interface{ SetPhysicsRotation(p.Quat) }

// PhysicsVelocityWriter optionally receives accepted centre-of-mass linear and
// angular velocities. This setter must only update data, without callbacks.
type PhysicsVelocityWriter interface{ SetPhysicsVelocity(linear, angular p.Vec3) }

// PhysicsActivationWriter optionally receives the accepted body's enabled state.
// This setter must only update data, without callbacks.
type PhysicsActivationWriter interface{ SetPhysicsActive(bool) }

// PhysicsAttachmentConfig selects optional state bindings. Position is always
// bound to the gameplay anchor. A zero MaxDriveForce selects 1,000 newtons.
type PhysicsAttachmentConfig struct {
	BindRotation, BindVelocity, BindActivation bool
	MaxDriveForce                              float32
}

// PhysicsContactFunc observes copied contact events after all physics bodies,
// navigation support and entity bindings have committed. QueuePhysicsCommand
// schedules mutations for the next tick; never mutate the World in this callback.
type PhysicsContactFunc func([]p.ContactEvent)

// PhysicsCommand is executed on the tick goroutine before gameplay and simulation.
// It must not integrate time, dispatch callbacks or change bound body anchors;
// use TeleportPhysics for a coordinated anchor change.
type PhysicsCommand func(p.World) error

type queuedPhysicsCommand struct {
	tick uint64
	run  PhysicsCommand
}

type physicsAttachment struct {
	id          int64
	body        p.BodyHandle
	entity      Entity
	position    registry.Spatial3DEntity
	writer      registry.Position3DWriter
	rotation    PhysicsRotationWriter
	velocity    PhysicsVelocityWriter
	activation  PhysicsActivationWriter
	config      PhysicsAttachmentConfig
	localAnchor p.Pose
}

type physicsService struct {
	server      *Server
	world       p.World
	agents      map[int64]*physicsAttachment
	bodies      map[p.BodyHandle]int64
	commands    []queuedPhysicsCommand
	dispatching bool
	stepping    bool
	closed      bool
	fault       error
	onContact   PhysicsContactFunc
}

// SetPhysicsWorld installs a tick-owned physics world before Run. The server
// closes it on shutdown. Legacy collision backends may coexist for other entities.
func (s *Server) SetPhysicsWorld(world p.World) error {
	if world == nil || reflect.ValueOf(world).Kind() == reflect.Pointer && reflect.ValueOf(world).IsNil() {
		return errors.New("golem: physics requires a non-nil world")
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleCreated || s.physics != nil {
		return errors.New("golem: configure physics once before Run")
	}
	s.physics = &physicsService{server: s, world: world, agents: make(map[int64]*physicsAttachment), bodies: make(map[p.BodyHandle]int64)}
	return nil
}

// AttachPhysics transfers ownership of a body to a registered 3D entity. Its
// world-space gameplay anchor must already match the entity position. The server
// destroys the body, its colliders and its joints on detach or entity deletion.
// Configure optional writer interfaces explicitly; legacy collider providers and
// synchronous navigation cannot share pose authority with this attachment.
// Call before Run or on the tick goroutine, outside physics callbacks.
func (s *Server) AttachPhysics(id int64, body p.BodyHandle, cfg PhysicsAttachmentConfig) error {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	if _, exists := ps.agents[id]; exists {
		return errors.New("golem: entity already has physics")
	}
	if _, exists := ps.bodies[body]; exists {
		return errors.New("golem: physics body already attached")
	}
	e, exists := s.Get(id)
	if !exists {
		return errors.New("golem: physics entity is not registered")
	}
	position, readable := e.(registry.Spatial3DEntity)
	writer, writable := e.(registry.Position3DWriter)
	if !readable || !writable {
		return errors.New("golem: physics requires Position3D and SetPosition3D")
	}
	if _, conflict := e.(ColliderProvider); conflict {
		return errors.New("golem: physics cannot share a legacy collider entity")
	}
	if _, conflict := e.(ColliderProvider3D); conflict {
		return errors.New("golem: physics cannot share a legacy 3D collider entity")
	}
	if n := s.navigation; n != nil {
		if a := n.agents[id]; a != nil && !a.config.StagedPhysics {
			return errors.New("golem: physics requires staged navigation")
		}
	}
	if !p.Finite(cfg.MaxDriveForce) || cfg.MaxDriveForce < 0 {
		return errors.New("golem: invalid physics drive force")
	}
	if cfg.MaxDriveForce == 0 {
		cfg.MaxDriveForce = 1000
	}
	state, err := ps.world.Body(body)
	if err != nil {
		return err
	}
	if n := s.navigation; n != nil {
		if agent := n.agents[id]; agent != nil && (!physicsNavigationMode(state.Mode) || agent.state.HasDestination && state.Mode == p.FreeDynamic) {
			return errors.New("golem: body mode cannot consume this navigation attachment or intent")
		}
	}
	x, y, z := position.Position3D()
	if !validPhysicsState(state) || state.Handle != body || state.Owner != 0 && state.Owner != id || state.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
		return errors.New("golem: physics body owner or gameplay anchor does not match entity")
	}
	a := &physicsAttachment{id: id, body: body, entity: e, position: position, writer: writer,
		config: cfg, localAnchor: state.Pose.Inverse().Compose(state.Anchor)}
	if cfg.BindRotation {
		a.rotation, _ = e.(PhysicsRotationWriter)
		if a.rotation == nil {
			return errors.New("golem: rotation binding requires PhysicsRotationWriter")
		}
	}
	if cfg.BindVelocity {
		a.velocity, _ = e.(PhysicsVelocityWriter)
		if a.velocity == nil {
			return errors.New("golem: velocity binding requires PhysicsVelocityWriter")
		}
	}
	if cfg.BindActivation {
		a.activation, _ = e.(PhysicsActivationWriter)
		if a.activation == nil {
			return errors.New("golem: activation binding requires PhysicsActivationWriter")
		}
	}
	if state.Owner == 0 {
		binder, ok := ps.world.(p.OwnerBinder)
		if !ok {
			return errors.New("golem: an unowned physics body requires OwnerBinder")
		}
		if err := binder.SetOwner(body, id); err != nil {
			return err
		}
		state.Owner = id
	}
	ps.agents[id], ps.bodies[body] = a, id
	ps.write(a, state, true)
	return nil
}

// DetachPhysics destroys an attachment and its owned body. It is idempotent.
// During accepted-frame callbacks it queues removal for the next tick.
func (s *Server) DetachPhysics(id int64) error {
	ps := s.physics
	if ps == nil || ps.agents[id] == nil {
		return nil
	}
	if ps.dispatching {
		body := ps.agents[id].body
		return s.QueuePhysicsCommand(func(p.World) error {
			if a := ps.agents[id]; a != nil && a.body == body {
				return ps.detach(id, p.Deleted)
			}
			return nil
		})
	}
	if err := ps.mutable(); err != nil {
		return err
	}
	return ps.detach(id, p.Deleted)
}

func (ps *physicsService) detach(id int64, reason p.CleanupReason) error {
	a := ps.agents[id]
	if a == nil {
		return nil
	}
	if err := ps.world.DestroyBody(a.body, reason); err != nil {
		return err
	}
	delete(ps.agents, id)
	delete(ps.bodies, a.body)
	ps.server.DetachNavigation(id)
	return nil
}

// PhysicsBody returns the accepted state of an attached body on the tick goroutine.
func (s *Server) PhysicsBody(id int64) (p.BodyState, error) {
	if s.physics == nil || s.physics.agents[id] == nil {
		return p.BodyState{}, p.ErrStale
	}
	return s.physics.world.Body(s.physics.agents[id].body)
}

// TeleportPhysics sets the gameplay anchor, preserving the body-local anchor
// transform. It synchronizes query visibility and entity bindings together.
// Call before a frame, never from accepted-frame callbacks; queue a Server.Post
// callback there instead. The next navigation pass revalidates actual support.
func (s *Server) TeleportPhysics(id int64, anchor p.Pose) error {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	a := ps.agents[id]
	if a == nil {
		return p.ErrStale
	}
	if !anchor.Valid() {
		return p.ErrInvalid
	}
	if err := ps.world.SetPose(a.body, anchor.Compose(a.localAnchor.Inverse())); err != nil {
		return err
	}
	if err := ps.world.Synchronize(); err != nil {
		ps.fault = err
		return err
	}
	state, err := ps.world.Body(a.body)
	if err != nil || !validPhysicsState(state) {
		ps.fault = fmt.Errorf("golem: invalid teleported physics state: %w", errors.Join(p.ErrFaulted, err))
		return ps.fault
	}
	ps.write(a, state, true)
	return nil
}

// OnPhysicsContact registers the accepted-frame event callback. Call at startup.
func (s *Server) OnPhysicsContact(fn PhysicsContactFunc) error {
	if err := s.physics.mutable(); err != nil {
		return err
	}
	s.physics.onContact = fn
	return nil
}

// QueuePhysicsCommand schedules a command for the next tick's input boundary.
// The queue is bounded by PostQueueCapacity and never executes recursively.
// It is tick-owned; use Server.Post when submitting from another goroutine.
func (s *Server) QueuePhysicsCommand(command PhysicsCommand) error {
	ps := s.physics
	if ps == nil || ps.closed || command == nil {
		return p.ErrInvalid
	}
	if ps.fault != nil {
		return fmt.Errorf("%w: %v", p.ErrFaulted, ps.fault)
	}
	if len(ps.commands) >= s.config.PostQueueCapacity {
		return ErrPostQueueFull
	}
	ps.commands = append(ps.commands, queuedPhysicsCommand{tick: s.tick, run: command})
	return nil
}

func (ps *physicsService) mutable() error {
	if ps == nil {
		return errors.New("golem: physics is not configured")
	}
	if ps.closed {
		return p.ErrStale
	}
	if ps.fault != nil {
		return fmt.Errorf("%w: %v", p.ErrFaulted, ps.fault)
	}
	if ps.stepping || ps.dispatching {
		return p.ErrBusy
	}
	return nil
}

func (ps *physicsService) queueDelete(id int64) error {
	return ps.server.QueuePhysicsCommand(func(p.World) error {
		ps.server.DeleteEntity(id)
		return ps.fault
	})
}

func (ps *physicsService) drain() error {
	if err := ps.mutable(); err != nil {
		return err
	}
	count := 0
	for count < len(ps.commands) && ps.commands[count].tick < ps.server.tick {
		count++
	}
	commands := append([]queuedPhysicsCommand(nil), ps.commands[:count]...)
	ps.commands = ps.commands[count:]
	for _, command := range commands {
		if err := command.run(ps.world); err != nil {
			ps.fault = err
			return fmt.Errorf("golem: queued physics command: %w", err)
		}
		if ps.fault != nil {
			return ps.fault
		}
	}
	return nil
}

func (ps *physicsService) step(dt float64) error {
	if err := ps.mutable(); err != nil {
		return err
	}
	s := ps.server
	f, err := s.PrepareNavigationFrame(dt)
	if err != nil {
		return err
	}
	defer s.AbortNavigationFrame(f)
	if f != nil {
		for _, id := range f.order {
			if ps.agents[id] == nil {
				return fmt.Errorf("golem: staged navigation entity %d has no physics body", id)
			}
		}
	}
	ids := ps.ordered()
	frame := p.Frame{ID: s.tick, Delta: float32(dt)}
	proposals := make(map[int64]NavigationMotionProposal)
	for _, proposal := range f.Proposals() {
		proposals[proposal.EntityID] = proposal
	}
	for _, id := range ids {
		a := ps.agents[id]
		state, readErr := ps.world.Body(a.body)
		if readErr != nil {
			return readErr
		}
		entity, exists := s.Get(id)
		x, y, z := a.position.Position3D()
		if !exists || entity != a.entity || !validPhysicsState(state) || state.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
			return fmt.Errorf("golem: physics entity %d was changed outside its pose authority", id)
		}
		if !f.Contains(id) {
			continue
		}
		if !physicsNavigationMode(state.Mode) {
			return fmt.Errorf("golem: body mode cannot participate in staged navigation for entity %d", id)
		}
		motion := p.Motion{Body: a.body, MaxDriveForce: a.config.MaxDriveForce, Frozen: f.Frozen(id)}
		motion.Validate = func(from, to p.Vec3) bool { return f.SafeSegment(id, physicsNavPoint(from), physicsNavPoint(to)) }
		if proposal, ok := proposals[id]; ok {
			if state.Mode == p.FreeDynamic {
				return fmt.Errorf("golem: free dynamic body %d cannot consume navigation drive intent", id)
			}
			for _, segment := range proposal.Segments {
				motion.Segments = append(motion.Segments, p.MotionSegment{From: physicsPoint(segment.From), Target: physicsPoint(segment.Target),
					SurfaceNormal: physicsPoint(segment.SurfaceNormal), Delta: float32(segment.Delta), Projection: segment.Projection})
			}
		}
		frame.Motions = append(frame.Motions, motion)
	}
	if err := s.ValidateNavigationFrame(f); err != nil {
		return err
	}
	ps.stepping = true
	defer func() { ps.stepping, ps.dispatching = false, false }()
	result, err := ps.world.Simulate(frame)
	if err != nil {
		ps.fault = err
		return err
	}
	reject := func(cause error) error {
		ps.fault = cause
		_ = ps.world.Reject(result.ID, cause)
		return fmt.Errorf("%w: %v", p.ErrFaulted, cause)
	}
	if result.ID != frame.ID || result.Elapsed != frame.Delta || result.Substeps < 1 {
		return reject(errors.New("golem: mismatched physics frame result"))
	}
	states := make(map[p.BodyHandle]p.BodyState, len(result.Bodies))
	for _, state := range result.Bodies {
		if _, duplicate := states[state.Handle]; duplicate || !validPhysicsState(state) {
			return reject(errors.New("golem: duplicate or invalid physics body result"))
		}
		states[state.Handle] = state
	}
	paths := make(map[p.BodyHandle][]surface.Vec3, len(result.Paths))
	for _, path := range result.Paths {
		if _, duplicate := paths[path.Body]; duplicate {
			return reject(errors.New("golem: duplicate physics body path"))
		}
		points := make([]surface.Vec3, len(path.Positions))
		for i, point := range path.Positions {
			points[i] = physicsNavPoint(point)
		}
		paths[path.Body] = points
	}
	var navResults []NavigationMotionResult
	for _, id := range ids {
		a := ps.agents[id]
		state, ok := states[a.body]
		if !ok || state.Owner != 0 && state.Owner != id {
			return reject(fmt.Errorf("golem: missing or foreign body result for entity %d", id))
		}
		if f.Contains(id) {
			navResults = append(navResults, NavigationMotionResult{EntityID: id, Position: physicsNavPoint(state.Anchor.Position), Path: paths[a.body]})
		}
	}
	accepted, err := s.validateNavigationResults(f, navResults)
	if err != nil {
		return reject(err)
	}
	if err := ps.world.Accept(result.ID); err != nil {
		return reject(err)
	}
	// There are no gameplay callbacks between result validation, world acceptance
	// and data-only binding writes. All entities are visible before reconciliation.
	for _, id := range ids {
		a := ps.agents[id]
		ps.write(a, states[a.body], !f.Contains(id))
	}
	ps.dispatching = true
	if f != nil {
		s.commitNavigationResults(f, accepted)
	}
	if ps.onContact != nil && len(result.Events) > 0 {
		ps.onContact(append([]p.ContactEvent(nil), result.Events...))
	}
	return nil
}

func (ps *physicsService) write(a *physicsAttachment, state p.BodyState, position bool) {
	if position {
		v := state.Anchor.Position
		a.writer.SetPosition3D(v.X, v.Y, v.Z)
	}
	if a.rotation != nil {
		a.rotation.SetPhysicsRotation(state.Anchor.Rotation)
	}
	if a.velocity != nil {
		a.velocity.SetPhysicsVelocity(state.LinearVelocity, state.AngularVelocity)
	}
	if a.activation != nil {
		a.activation.SetPhysicsActive(state.Enabled)
	}
}

func (ps *physicsService) ordered() []int64 {
	ids := make([]int64, 0, len(ps.agents))
	for id := range ps.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (ps *physicsService) stop() {
	if ps.closed {
		return
	}
	ps.closed = true
	ps.commands = nil
	_ = ps.world.Close()
}

func validPhysicsState(state p.BodyState) bool {
	return state.Pose.Valid() && state.Anchor.Valid() && state.LinearVelocity.Valid() && state.AngularVelocity.Valid() && state.CenterOfMass.InBounds()
}

func physicsNavigationMode(mode p.MotionMode) bool {
	return mode == p.DrivenCharacter || mode == p.Kinematic || mode == p.ScriptedAction || mode == p.FreeDynamic
}

func (s *Server) validatePhysicsNavigation(id int64, drive bool) error {
	if s.physics == nil || s.physics.agents[id] == nil {
		return nil
	}
	state, err := s.PhysicsBody(id)
	if err != nil {
		return err
	}
	if !physicsNavigationMode(state.Mode) || drive && state.Mode == p.FreeDynamic {
		return errors.New("golem: body mode does not support this navigation operation")
	}
	return nil
}

func physicsNavPoint(v p.Vec3) surface.Vec3 {
	return surface.Vec3{X: float64(v.X), Y: float64(v.Y), Z: float64(v.Z)}
}

func physicsPoint(v surface.Vec3) p.Vec3 {
	return p.Vec3{X: float32(v.X), Y: float32(v.Y), Z: float32(v.Z)}
}
