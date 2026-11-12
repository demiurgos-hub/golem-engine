package golem

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
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
	Placement                                  PhysicsPlacementPolicy
}

// PhysicsPlacementPolicy explicitly permits geometric penetration at admission.
// Its zero value rejects every positive penetration; touching remains admissible.
type PhysicsPlacementPolicy struct{ MaxPenetration float32 }

// PhysicsContactFunc observes copied contact events after all physics bodies,
// navigation support and entity bindings have committed. QueuePhysicsCommand
// schedules mutations for the next tick; never mutate the World in this callback.
// Shutdown also dispatches terminal accepted pairs; commands then return an error.
type PhysicsContactFunc func([]p.ContactEvent)

// PhysicsFrameMetrics describes one attempted server physics frame. Elapsed is
// accepted simulation time; RequestedDelta remains visible when an attempt fails.
// Duration includes navigation preparation, readback and callbacks. Solve measures
// only World.Simulate. The observer itself is excluded from both durations.
// Native CCD and TOI continuation counters include attempted work even when the
// frame is rejected. Continuation participant/row totals are additive, not peaks;
// see physics3d.FrameResult. Microsteps counts native calls, not path samples.
type PhysicsFrameMetrics struct {
	Frame                                                                      uint64
	RequestedDelta                                                             float64
	Elapsed                                                                    float32
	Substeps                                                                   int
	Intervals, Microsteps, MotionPairTests, MotionPathRefinements              int
	CCDCandidatePairs, CCDPairTests, CCDPredictiveContacts                     int
	TOIContinuationCandidates, TOIContinuationPairTests, TOIContinuationEvents int
	TOIContinuationIslandBodies, TOIContinuationContactRows                    int
	Duration, Solve                                                            time.Duration
	Accepted                                                                   bool
	World                                                                      p.Metrics
	Err                                                                        error
}

// PhysicsCommand is executed on the tick goroutine before gameplay and simulation.
// It must not integrate time, dispatch callbacks or change bound body anchors;
// use TeleportPhysics for a coordinated anchor change.
type PhysicsCommand func(p.World) error

type queuedPhysicsCommand struct {
	tick uint64
	run  PhysicsCommand
}

type physicsAttachment struct {
	policyGeneration uint64
	id               int64
	body             p.BodyHandle
	entity           Entity
	position         registry.Spatial3DEntity
	writer           registry.Position3DWriter
	rotation         PhysicsRotationWriter
	velocity         PhysicsVelocityWriter
	activation       PhysicsActivationWriter
	config           PhysicsAttachmentConfig
	localAnchor      p.Pose
}

type physicsService struct {
	server               *Server
	world                p.World
	agents               map[int64]*physicsAttachment
	bodies               map[p.BodyHandle]int64
	commands             []queuedPhysicsCommand
	dispatching          bool
	stepping             bool
	closed               bool
	restoring            bool
	fault                error
	onContact            PhysicsContactFunc
	observe              func(PhysicsFrameMetrics)
	geometry             map[string]*content.Instance
	geometryIdentities   map[string]PhysicsGeometryIdentity
	geometryPending      map[string]PhysicsGeometryPending
	trajectories         map[int64]*physicsTrajectory
	trajectoryGeneration uint64
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
// The world must implement physics3d.PlacementValidator; the complete solid volume
// is validated before ownership or bindings change, using cfg.Placement.
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
	if err := ps.world.Synchronize(); err != nil {
		return err
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
	if err := ps.validatePlacement(body, state.Pose, cfg.Placement); err != nil {
		return err
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
	delete(ps.trajectories, id)
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

// SetPhysicsControllerVelocity changes retained physical character intent. An
// active staged-navigation Motion takes precedence for its frame. Call on the
// tick outside accepted-frame callbacks; use Post there for a later tick.
func (s *Server) SetPhysicsControllerVelocity(id int64, velocity p.Vec3) error {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	a := ps.agents[id]
	if a == nil {
		return p.ErrStale
	}
	writer, ok := ps.world.(p.CharacterVelocityWriter)
	if !ok {
		return p.ErrUnsupported
	}
	if ps.trajectories[id].active() {
		return fmt.Errorf("%w: timed trajectory owns controller movement", p.ErrBusy)
	}
	if err := writer.SetCharacterVelocity(a.body, velocity); err != nil {
		return err
	}
	delete(ps.trajectories, id)
	return nil
}

// TeleportPhysics sets the gameplay anchor, preserving the body-local anchor
// transform. It synchronizes query visibility and entity bindings together.
// The complete solid volume must pass zero-penetration placement admission.
// Call before a frame, never from accepted-frame callbacks; queue a Server.Post
// callback there instead. The next navigation pass revalidates actual support.
func (s *Server) TeleportPhysics(id int64, anchor p.Pose) error {
	return s.TeleportPhysicsWithPolicy(id, anchor, PhysicsPlacementPolicy{})
}

// TeleportPhysicsWithPolicy is TeleportPhysics with explicit geometric penetration
// tolerance. This explicit teleport checks final placement, cancels active timed
// intent, and does not sweep intervening space. ExternalPose movement should use
// StartPhysicsTrajectory. Rejection changes neither body/entity poses nor intent.
func (s *Server) TeleportPhysicsWithPolicy(id int64, anchor p.Pose, policy PhysicsPlacementPolicy) error {
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
	pose := anchor.Compose(a.localAnchor.Inverse())
	if err := ps.world.Synchronize(); err != nil {
		return err
	}
	if err := ps.validatePlacement(a.body, pose, policy); err != nil {
		return err
	}
	if err := ps.world.SetPose(a.body, pose); err != nil {
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
	if action := ps.trajectories[id]; action.active() {
		action.state.Status, action.state.Reason = PhysicsTrajectoryCancelled, "teleported"
	}
	return nil
}

func (ps *physicsService) validatePlacement(body p.BodyHandle, pose p.Pose, policy PhysicsPlacementPolicy) error {
	if !p.Finite(policy.MaxPenetration) || policy.MaxPenetration < 0 {
		return p.ErrInvalid
	}
	validator, ok := ps.world.(p.PlacementValidator)
	if !ok {
		return fmt.Errorf("%w: physics body admission requires PlacementValidator", p.ErrUnsupported)
	}
	clear, err := validator.ValidatePlacement(body, pose, policy.MaxPenetration)
	if err != nil {
		return err
	}
	if !clear {
		return errors.New("golem: physical placement is obstructed")
	}
	return nil
}

// OnPhysicsContact registers the accepted-frame and terminal shutdown callback.
// Call at startup. Shutdown callbacks cannot queue further commands.
func (s *Server) OnPhysicsContact(fn PhysicsContactFunc) error {
	if err := s.physics.mutable(); err != nil {
		return err
	}
	s.physics.onContact = fn
	return nil
}

// ObservePhysicsFrames installs a read-only, tick-owned observation callback.
// It runs once per attempted physics frame, including failures; it must not mutate
// server/world state. No timing evidence is implied by enabling this observer.
func (s *Server) ObservePhysicsFrames(fn func(PhysicsFrameMetrics)) error {
	if err := s.physics.mutable(); err != nil {
		return err
	}
	s.physics.observe = fn
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
	if ps.stepping || ps.dispatching || ps.restoring {
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

func (ps *physicsService) step(dt float64) (stepErr error) {
	metrics := PhysicsFrameMetrics{Frame: ps.server.tick, RequestedDelta: dt}
	started := NavigationNow()
	defer func() {
		if ps.observe != nil {
			metrics.Duration, metrics.Err, metrics.World = navigationSince(started), stepErr, ps.world.Metrics()
			ps.observe(metrics)
		}
	}()
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
	submitted := make(map[p.BodyHandle]bool)
	proposals := make(map[int64]NavigationMotionProposal)
	trajectoryPlans := make(map[int64]*physicsTrajectoryFrame)
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
		if f.Contains(id) && !physicsNavigationMode(state.Mode) {
			return fmt.Errorf("golem: body mode cannot participate in staged navigation for entity %d", id)
		}
		motion := p.Motion{Body: a.body, MaxDriveForce: a.config.MaxDriveForce, Frozen: f.Contains(id) && f.Frozen(id)}
		if f.Contains(id) {
			motion.Validate = func(from, to p.Vec3) bool { return f.SafeSegment(id, physicsNavPoint(from), physicsNavPoint(to)) }
		}
		if proposal, ok := proposals[id]; ok {
			if state.Mode == p.FreeDynamic {
				return fmt.Errorf("golem: free dynamic body %d cannot consume navigation drive intent", id)
			}
			for _, segment := range proposal.Segments {
				motion.Segments = append(motion.Segments, p.MotionSegment{From: physicsPoint(segment.From), Target: physicsPoint(segment.Target),
					SurfaceNormal: physicsPoint(segment.SurfaceNormal), Delta: float32(segment.Delta), Projection: segment.Projection})
			}
		}
		trajectoryMotion, plan, err := ps.prepareTrajectory(id, state, f, frame.Delta)
		if err != nil {
			return err
		}
		if plan != nil {
			motion = trajectoryMotion
			trajectoryPlans[id] = plan
		} else if !f.Contains(id) && state.Mode != p.ExternalPose && (state.Mode != p.ScriptedAction || ps.trajectories[id] == nil) {
			continue
		}
		frame.Motions = append(frame.Motions, motion)
		submitted[a.body] = true
	}
	if err := s.ValidateNavigationFrame(f); err != nil {
		return err
	}
	ps.stepping = true
	defer func() { ps.stepping, ps.dispatching = false, false }()
	solveStarted := NavigationNow()
	result, err := ps.world.Simulate(frame)
	metrics.Solve = navigationSince(solveStarted)
	metrics.Intervals, metrics.Microsteps, metrics.MotionPairTests = result.Intervals, result.Microsteps, result.MotionPairTests
	metrics.MotionPathRefinements = result.MotionPathRefinements
	metrics.CCDCandidatePairs, metrics.CCDPairTests, metrics.CCDPredictiveContacts = result.CCDCandidatePairs, result.CCDPairTests, result.CCDPredictiveContacts
	metrics.TOIContinuationCandidates, metrics.TOIContinuationPairTests, metrics.TOIContinuationEvents = result.TOIContinuationCandidates, result.TOIContinuationPairTests, result.TOIContinuationEvents
	metrics.TOIContinuationIslandBodies, metrics.TOIContinuationContactRows = result.TOIContinuationIslandBodies, result.TOIContinuationContactRows
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
	physicalPaths := make(map[p.BodyHandle]p.BodyPath, len(result.Paths))
	for _, path := range result.Paths {
		if _, duplicate := paths[path.Body]; duplicate {
			return reject(errors.New("golem: duplicate physics body path"))
		}
		points := make([]surface.Vec3, len(path.Positions))
		for i, point := range path.Positions {
			points[i] = physicsNavPoint(point)
		}
		paths[path.Body] = points
		physicalPaths[path.Body] = path
	}
	rejectedMotions := make(map[p.BodyHandle]string, len(result.RejectedMotions))
	for _, motion := range result.RejectedMotions {
		_, exists := ps.bodies[motion.Body]
		if _, duplicate := rejectedMotions[motion.Body]; duplicate || !exists || !submitted[motion.Body] || motion.Reason == "" || len(motion.Reason) > 4096 {
			return reject(errors.New("golem: invalid rejected physics motion"))
		}
		rejectedMotions[motion.Body] = motion.Reason
	}
	var navResults []NavigationMotionResult
	for _, id := range ids {
		a := ps.agents[id]
		state, ok := states[a.body]
		if !ok || state.Owner != 0 && state.Owner != id {
			return reject(fmt.Errorf("golem: missing or foreign body result for entity %d", id))
		}
		if f.Contains(id) {
			reason := ""
			if _, hasPlan := proposals[id]; hasPlan {
				reason = rejectedMotions[a.body]
			}
			navResults = append(navResults, NavigationMotionResult{EntityID: id, Position: physicsNavPoint(state.Anchor.Position), Path: paths[a.body], RejectionReason: reason})
		}
	}
	accepted, err := s.validateNavigationResults(f, navResults)
	if err != nil {
		return reject(err)
	}
	if err := ps.validateTrajectoryResults(trajectoryPlans, states, paths, physicalPaths); err != nil {
		return reject(err)
	}
	if err := ps.world.Accept(result.ID); err != nil {
		return reject(err)
	}
	metrics.Accepted, metrics.Elapsed, metrics.Substeps = true, result.Elapsed, result.Substeps
	ps.acceptTrajectoryResults(trajectoryPlans, states, rejectedMotions)
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
	ps.trajectories = nil
	ps.geometry, ps.geometryIdentities, ps.geometryPending = nil, nil, nil
	if world, ok := ps.world.(p.ShutdownWorld); ok {
		events, err := world.CloseWithEvents()
		if err != nil {
			ps.fault = err
		}
		if ps.onContact != nil && len(events) != 0 {
			ps.onContact(append([]p.ContactEvent(nil), events...))
		}
	} else if err := ps.world.Close(); err != nil {
		ps.fault = err
	}
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
	if drive && s.physics.trajectories[id].active() {
		return fmt.Errorf("%w: scripted trajectory owns body movement", p.ErrBusy)
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
