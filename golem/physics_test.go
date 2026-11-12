package golem

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

type physicsTestWorld struct {
	p.World
	bodies                                      map[p.BodyHandle]p.BodyState
	frame                                       p.Frame
	pending                                     p.FrameResult
	simulate                                    func(p.Frame, *p.FrameResult)
	acceptErr                                   error
	simulated, accepted, rejected, synchronized int
	destroyed                                   []p.BodyHandle
	closed                                      bool
	placement                                   func(p.BodyHandle, p.Pose, float32) (bool, error)
}

func (w *physicsTestWorld) ValidatePlacement(h p.BodyHandle, pose p.Pose, tolerance float32) (bool, error) {
	if w.placement != nil {
		return w.placement(h, pose, tolerance)
	}
	return true, nil
}

func (w *physicsTestWorld) Body(h p.BodyHandle) (p.BodyState, error) {
	s, ok := w.bodies[h]
	if !ok {
		return p.BodyState{}, p.ErrStale
	}
	return s, nil
}
func (w *physicsTestWorld) SetPose(h p.BodyHandle, pose p.Pose) error {
	s, err := w.Body(h)
	if err != nil {
		return err
	}
	local := s.Pose.Inverse().Compose(s.Anchor)
	s.Pose, s.Anchor = pose, pose.Compose(local)
	w.bodies[h] = s
	return nil
}
func (w *physicsTestWorld) SetOwner(h p.BodyHandle, owner int64) error {
	state, err := w.Body(h)
	if err != nil {
		return err
	}
	state.Owner = owner
	w.bodies[h] = state
	return nil
}
func (w *physicsTestWorld) Synchronize() error { w.synchronized++; return nil }
func (w *physicsTestWorld) DestroyBody(h p.BodyHandle, _ p.CleanupReason) error {
	if _, err := w.Body(h); err != nil {
		return err
	}
	delete(w.bodies, h)
	w.destroyed = append(w.destroyed, h)
	return nil
}
func (w *physicsTestWorld) Simulate(f p.Frame) (p.FrameResult, error) {
	w.simulated++
	w.frame = f
	r := p.FrameResult{ID: f.ID, Revision: uint64(w.accepted + 1), Substeps: 4, Elapsed: f.Delta}
	for _, s := range w.bodies {
		r.Bodies = append(r.Bodies, s)
		r.Paths = append(r.Paths, p.BodyPath{Body: s.Handle, Positions: []p.Vec3{s.Anchor.Position, s.Anchor.Position}})
	}
	if w.simulate != nil {
		w.simulate(f, &r)
	}
	w.pending = r
	return r, nil
}
func (w *physicsTestWorld) Accept(id uint64) error {
	if w.acceptErr != nil {
		return w.acceptErr
	}
	if id != w.pending.ID {
		return p.ErrStale
	}
	for _, s := range w.pending.Bodies {
		w.bodies[s.Handle] = s
	}
	w.accepted++
	return nil
}
func (w *physicsTestWorld) Reject(uint64, error) error { w.rejected++; return nil }
func (w *physicsTestWorld) Close() error               { w.closed = true; return nil }
func (w *physicsTestWorld) Metrics() p.Metrics {
	return p.Metrics{Bodies: len(w.bodies), AcceptedFrames: uint64(w.accepted), RejectedFrames: uint64(w.rejected)}
}

func physicsTestAttach(t *testing.T, s *Server, w *physicsTestWorld, id int64, mode p.MotionMode, cfg PhysicsAttachmentConfig) p.BodyHandle {
	t.Helper()
	e, ok := s.Get(id)
	if !ok {
		t.Fatal("fixture entity missing")
	}
	x, y, z := e.(interface {
		Position3D() (float32, float32, float32)
	}).Position3D()
	h := p.BodyHandle{World: 1, Index: uint32(id), Generation: 1}
	state := p.BodyState{Handle: h, Owner: id, Mode: mode, Pose: p.Identity(), Anchor: p.Identity(), Mass: 1, Enabled: true, Awake: true}
	state.Pose.Position, state.Anchor.Position = p.Vec3{X: x, Y: y, Z: z}, p.Vec3{X: x, Y: y, Z: z}
	if w.bodies == nil {
		w.bodies = make(map[p.BodyHandle]p.BodyState)
	}
	w.bodies[h] = state
	if err := s.AttachPhysics(id, h, cfg); err != nil {
		t.Fatal(err)
	}
	return h
}

func physicsTestMove(r *p.FrameResult, h p.BodyHandle, position p.Vec3) {
	for i := range r.Bodies {
		if r.Bodies[i].Handle == h {
			r.Bodies[i].Pose.Position = r.Bodies[i].Pose.Position.Add(position.Sub(r.Bodies[i].Anchor.Position))
			r.Bodies[i].Anchor.Position = position
		}
	}
	for i := range r.Paths {
		if r.Paths[i].Body == h {
			r.Paths[i].Positions[len(r.Paths[i].Positions)-1] = position
		}
	}
}

func physicsNavigationTestServer(t *testing.T) (*Server, *navigationTestEntity, *physicsTestWorld) {
	t.Helper()
	s := NewServer(ServerConfig{})
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	e := &navigationTestEntity{id: 1}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachNavigation(1, NavigationAgentConfig{StagedPhysics: true, Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx, s.life = ctx, lifecycleRunning
	s.navigation.start(ctx)
	t.Cleanup(func() { cancel(); s.navigation.stop(); s.physics.stop() })
	return s, e, w
}

func TestPhysicsFrameIntegratesOnceAndCapturesIdleAttachments(t *testing.T) {
	s, e, w := physicsNavigationTestServer(t)
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: .03}}, surface.Corner{Position: surface.Vec3{X: 10}})
	idle := &navigationTestEntity{id: 2, position: surface.Vec3{Z: 2}}
	if err := s.CreateEntity(idle); err != nil {
		t.Fatal(err)
	}
	cfg := s.navigation.agents[1].config
	if err := s.AttachNavigation(2, cfg); err != nil {
		t.Fatal(err)
	}
	one := physicsTestAttach(t, s, w, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	two := physicsTestAttach(t, s, w, 2, p.FreeDynamic, PhysicsAttachmentConfig{})
	w.simulate = func(f p.Frame, r *p.FrameResult) {
		if len(f.Motions) != 2 {
			t.Fatalf("motions=%d, idle body omitted", len(f.Motions))
		}
		var duration float32
		for _, segment := range f.Motions[0].Segments {
			duration += segment.Delta
		}
		if math.Abs(float64(duration-.05)) > 1e-6 {
			t.Fatalf("catch-up time leaked into frame: %g", duration)
		}
		if len(f.Motions[0].Segments) != 2 {
			t.Fatalf("segments=%+v", f.Motions[0].Segments)
		}
		if f.Motions[1].Validate == nil || !f.Motions[1].Validate(p.Vec3{Z: 2}, p.Vec3{Y: .2, Z: 2}) {
			t.Fatal("idle body has no captured safety")
		}
		physicsTestMove(r, one, p.Vec3{X: .04})
		physicsTestMove(r, two, p.Vec3{Y: .2, Z: 2})
	}
	s.tick = 20 // Skipped scheduling must not integrate twenty physics frames.
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	if w.simulated != 1 || w.accepted != 1 || e.position.X != float64(float32(.04)) || idle.position.Y != float64(float32(.2)) {
		t.Fatalf("world=%+v entities=%+v %+v", w, e, idle)
	}
	if s.navigation.agents[2].state.Mode != NavigationUnsupported {
		t.Fatal("idle moved body did not reconcile lost support")
	}
}

func TestPhysicsFrameRejectsAllBodiesBeforeAnyEntityWrite(t *testing.T) {
	s, first, w := physicsNavigationTestServer(t)
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
	second := &navigationTestEntity{id: 2, position: surface.Vec3{Z: 2}}
	if err := s.CreateEntity(second); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachNavigation(2, s.navigation.agents[1].config); err != nil {
		t.Fatal(err)
	}
	one := physicsTestAttach(t, s, w, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	two := physicsTestAttach(t, s, w, 2, p.FreeDynamic, PhysicsAttachmentConfig{})
	w.simulate = func(_ p.Frame, r *p.FrameResult) {
		physicsTestMove(r, one, p.Vec3{X: .1})
		physicsTestMove(r, two, p.Vec3{X: .1, Z: 2})
		for i := range r.Paths {
			if r.Paths[i].Body == two {
				r.Paths[i].Positions = nil
			}
		}
	}
	s.tick++
	err := s.physics.step(.05)
	if !errors.Is(err, p.ErrFaulted) || w.rejected != 1 || w.accepted != 0 {
		t.Fatalf("err=%v accepted=%d rejected=%d", err, w.accepted, w.rejected)
	}
	if first.position != (surface.Vec3{}) || second.position != (surface.Vec3{Z: 2}) {
		t.Fatal("partial body readback escaped rejected frame")
	}
	if err := s.physics.step(.05); !errors.Is(err, p.ErrFaulted) || w.simulated != 1 {
		t.Fatal("faulted world was stepped again")
	}
}

func TestPhysicsAcceptFailureDoesNotWriteEntities(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &navigationTestEntity{id: 1}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	w := &physicsTestWorld{acceptErr: errors.New("accept failure")}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w, 1, p.FreeDynamic, PhysicsAttachmentConfig{})
	w.simulate = func(_ p.Frame, r *p.FrameResult) { physicsTestMove(r, h, p.Vec3{X: 1}) }
	s.tick++
	if err := s.physics.step(.05); err == nil {
		t.Fatal("accept failure ignored")
	}
	if e.position != (surface.Vec3{}) || w.rejected != 1 {
		t.Fatal("accept failure exposed unaccepted pose")
	}
}

type physicsBoundEntity struct {
	navigationTestEntity
	rotation        p.Quat
	linear, angular p.Vec3
	active          bool
}

func (e *physicsBoundEntity) SetPhysicsRotation(q p.Quat)    { e.rotation = q }
func (e *physicsBoundEntity) SetPhysicsActive(active bool)   { e.active = active }
func (e *physicsBoundEntity) SetPhysicsVelocity(v, a p.Vec3) { e.linear, e.angular = v, a }

func TestPhysicsBindingsPublishBeforeEventsAndDeferCallbackCommands(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: 1}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w, 1, p.FreeDynamic, PhysicsAttachmentConfig{BindRotation: true, BindVelocity: true, BindActivation: true})
	if !e.active {
		t.Fatal("initial activation binding was not read back")
	}
	commandRan, eventRan, removeRan := false, false, false
	e.removed = func() {
		removeRan = true
		if _, err := w.Body(h); !errors.Is(err, p.ErrStale) {
			t.Error("OnRemove saw live owned body")
		}
	}
	w.simulate = func(_ p.Frame, r *p.FrameResult) {
		physicsTestMove(r, h, p.Vec3{X: 1})
		r.Bodies[0].Pose.Position.Y = 2 // Body origin is deliberately different from the anchor.
		r.Bodies[0].CenterOfMass.Y = 3
		r.Bodies[0].LinearVelocity = p.Vec3{X: 2}
		r.Bodies[0].AngularVelocity = p.Vec3{Y: 3}
		r.Bodies[0].Anchor.Rotation = p.Quat{Y: 1}
		r.Bodies[0].Enabled = false
		r.Events = []p.ContactEvent{{OwnerA: 1, Phase: p.Enter}}
	}
	if err := s.OnPhysicsContact(func(events []p.ContactEvent) {
		eventRan = true
		state, err := s.PhysicsBody(1)
		if err != nil || state.Anchor.Position.X != 1 || e.position != (surface.Vec3{X: 1}) || e.rotation != (p.Quat{Y: 1}) || e.linear.X != 2 || e.angular.Y != 3 || e.active {
			t.Error("callback observed incomplete body bindings")
		}
		if err := s.QueuePhysicsCommand(func(p.World) error { commandRan = true; return nil }); err != nil {
			t.Error(err)
		}
		s.DeleteEntity(1)
		if _, exists := s.Get(1); !exists || removeRan || commandRan {
			t.Error("callback mutation ran recursively")
		}
		events[0].OwnerA = 999
	}); err != nil {
		t.Fatal(err)
	}
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	if !eventRan || commandRan || removeRan || w.pending.Events[0].OwnerA != 1 {
		t.Fatal("event ownership or callback boundary incorrect")
	}
	if err := s.physics.drain(); err != nil || commandRan || removeRan {
		t.Fatal("a command ran again in its source tick")
	}
	s.tick++
	if err := s.physics.drain(); err != nil {
		t.Fatal(err)
	}
	if !commandRan || !removeRan || len(w.destroyed) != 1 {
		t.Fatal("queued mutation was not applied at next boundary")
	}
}

func TestPhysicsTeleportPreservesLocalAnchor(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &navigationTestEntity{id: 1, position: surface.Vec3{Y: 2}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	h := p.BodyHandle{World: 1, Index: 1, Generation: 1}
	state := p.BodyState{Handle: h, Owner: 1, Mode: p.FreeDynamic, Pose: p.Identity(), Anchor: p.Identity()}
	state.Anchor.Position.Y = 2
	w := &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{h: state}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachPhysics(1, h, PhysicsAttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	target := p.Pose{Position: p.Vec3{X: 4, Y: 1}, Rotation: p.Quat{Z: 1}}
	if err := s.TeleportPhysics(1, target); err != nil {
		t.Fatal(err)
	}
	got, _ := w.Body(h)
	if got.Anchor != target || got.Pose.Position != (p.Vec3{X: 4, Y: 3}) || e.position != (surface.Vec3{X: 4, Y: 1}) || w.synchronized != 3 {
		t.Fatalf("teleport state=%+v entity=%+v", got, e)
	}
}

func TestPhysicsPlacementRejectionPreservesPoseOwnershipAndBindings(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: 1}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	h := p.BodyHandle{World: 1, Index: 1, Generation: 1}
	state := p.BodyState{Handle: h, Mode: p.ExternalPose, Pose: p.Identity(), Anchor: p.Identity()}
	w := &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{h: state}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	w.placement = func(got p.BodyHandle, pose p.Pose, tolerance float32) (bool, error) {
		if got != h || pose != state.Pose || tolerance != 0 {
			t.Error("incorrect attachment placement input")
		}
		return false, nil
	}
	if err := s.AttachPhysics(1, h, PhysicsAttachmentConfig{}); err == nil {
		t.Fatal("obstructed attachment admitted")
	}
	if len(s.physics.agents) != 0 || w.bodies[h].Owner != 0 || e.position != (surface.Vec3{}) {
		t.Fatal("rejected admission partially changed authority")
	}
	w.placement = nil
	if err := s.AttachPhysics(1, h, PhysicsAttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	before := w.bodies[h]
	target := p.Identity()
	target.Position.X = 5
	w.placement = func(got p.BodyHandle, pose p.Pose, tolerance float32) (bool, error) {
		if got != h || pose != target {
			t.Error("incorrect teleport placement input")
		}
		return tolerance >= .01, nil
	}
	if err := s.TeleportPhysics(1, target); err == nil {
		t.Fatal("obstructed teleport admitted")
	}
	if w.bodies[h] != before || e.position != (surface.Vec3{}) || s.physics.fault != nil {
		t.Fatal("rejected teleport changed accepted state")
	}
	if err := s.TeleportPhysicsWithPolicy(1, target, PhysicsPlacementPolicy{MaxPenetration: .01}); err != nil {
		t.Fatal(err)
	}
	if e.position.X != 5 || w.bodies[h].Anchor != target {
		t.Fatal("explicit admitted external pose did not publish")
	}
}

func TestPhysicsRejectedMotionCommitsActualDisplacementWithoutProposedProgress(t *testing.T) {
	s, entity, w := physicsNavigationTestServer(t)
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: .03}}, surface.Corner{Position: surface.Vec3{X: 10}})
	h := physicsTestAttach(t, s, w, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	w.simulate = func(_ p.Frame, result *p.FrameResult) {
		result.RejectedMotions = []p.MotionRejection{{Body: h, Reason: "initial projection blocked"}}
		result.Bodies[0].Pose.Position.X, result.Bodies[0].Anchor.Position.X = .02, .02
		result.Paths[0].Positions[1].X = .02
	}
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	a := s.navigation.agents[1]
	if w.accepted != 1 || w.rejected != 0 || entity.position.X != float64(float32(.02)) || a.state.Mode != NavigationBlocked || a.route != nil || a.next != 0 || a.location == nil || a.state.Destination.X != 10 {
		t.Fatalf("rejected intent lost actual pose/support or committed progress: entity=%+v state=%+v", entity.position, a.state)
	}
}

func TestPhysicsIdleControllerRejectionRetainsAcceptedSupport(t *testing.T) {
	s, entity, w := physicsNavigationTestServer(t)
	h := physicsTestAttach(t, s, w, 1, p.Kinematic, PhysicsAttachmentConfig{})
	w.simulate = func(f p.Frame, r *p.FrameResult) {
		if len(f.Motions) != 1 || len(f.Motions[0].Segments) != 0 {
			t.Fatal("fixture was not an idle safety capture")
		}
		r.RejectedMotions = []p.MotionRejection{{Body: h, Reason: "controller path rejected by captured safety"}}
	}
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	if w.accepted != 1 || w.rejected != 0 || entity.position != (surface.Vec3{}) {
		t.Fatal("safe idle admission was treated as a world fault")
	}
}

func TestPhysicsRunLoopStepsOncePerTickAndClosesWorld(t *testing.T) {
	s := NewServer(ServerConfig{TickRate: 100})
	e := &navigationTestEntity{id: 1}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w, 1, p.FreeDynamic, PhysicsAttachmentConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ticks int
	s.OnTick(func(dt float64, _ *Server) {
		ticks++
		if dt != .01 {
			t.Errorf("dt=%v", dt)
		}
	})
	w.simulate = func(f p.Frame, r *p.FrameResult) {
		if f.Delta != .01 {
			t.Errorf("physics delta=%v", f.Delta)
		}
		physicsTestMove(r, h, p.Vec3{X: float32(f.ID)})
	}
	s.OnTickEnd(func(tick uint64, _ time.Duration) {
		if e.position.X != float64(tick) {
			t.Error("end tick preceded readback")
		}
		if tick == 2 {
			cancel()
		}
	})
	err := s.Run(ctx)
	if !errors.Is(err, context.Canceled) || ticks != 2 || w.simulated != 2 || w.accepted != 2 || !w.closed {
		t.Fatalf("err=%v ticks=%d simulated=%d accepted=%d closed=%v", err, ticks, w.simulated, w.accepted, w.closed)
	}
}

func TestPhysicsFrozenIdleDoesNotStopUnaffectedBodies(t *testing.T) {
	s, e, w := physicsNavigationTestServer(t)
	base := s.navigation.backend.(*navigationTestBackend)
	regional := &regionalTestBackend{navigationTestBackend: base, token: 1}
	s.navigation.backend = regional
	physicsTestAttach(t, s, w, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	other := &navigationTestEntity{id: 2, position: surface.Vec3{X: -3}}
	if err := s.CreateEntity(other); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w, 2, p.FreeDynamic, PhysicsAttachmentConfig{})
	regional.edit(true, false)
	w.simulate = func(f p.Frame, r *p.FrameResult) {
		if len(f.Motions) != 1 || !f.Motions[0].Frozen || len(f.Motions[0].Segments) != 0 {
			t.Fatal("idle quarantined body did not carry pre-solver hold")
		}
		if !f.Motions[0].Validate(p.Vec3{}, p.Vec3{}) {
			t.Fatal("stationary quarantined result was rejected")
		}
		physicsTestMove(r, h, p.Vec3{X: -2.9})
	}
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	if e.position != (surface.Vec3{}) || other.position.X != float64(float32(-2.9)) || w.accepted != 1 {
		t.Fatal("quarantine stopped an unrelated body")
	}
}

type physicsLegacyEntity struct{ navigationTestEntity }

func (*physicsLegacyEntity) Collider3D() (CollisionShape3D, string, bool) { return nil, "", false }

func TestPhysicsAttachmentRejectsCompetingOrMissingBindings(t *testing.T) {
	for _, kind := range []string{"rotation", "velocity", "activation", "legacy", "anchor", "navigation"} {
		t.Run(kind, func(t *testing.T) {
			s := NewServer(ServerConfig{})
			var e Entity = &navigationTestEntity{id: 1}
			if kind == "legacy" {
				e = &physicsLegacyEntity{navigationTestEntity{id: 1}}
			}
			if err := s.CreateEntity(e); err != nil {
				t.Fatal(err)
			}
			if kind == "navigation" {
				if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
					t.Fatal(err)
				}
				if err := s.AttachNavigation(1, NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}); err != nil {
					t.Fatal(err)
				}
			}
			h := p.BodyHandle{World: 1, Index: 1, Generation: 1}
			state := p.BodyState{Handle: h, Owner: 1, Mode: p.DrivenCharacter, Pose: p.Identity(), Anchor: p.Identity()}
			if kind == "anchor" {
				state.Anchor.Position.X = 1
			}
			w := &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{h: state}}
			if err := s.SetPhysicsWorld(w); err != nil {
				t.Fatal(err)
			}
			cfg := PhysicsAttachmentConfig{BindRotation: kind == "rotation", BindVelocity: kind == "velocity", BindActivation: kind == "activation"}
			if err := s.AttachPhysics(1, h, cfg); err == nil {
				t.Fatal("conflicting attachment accepted")
			}
			if len(s.physics.agents) != 0 || len(s.physics.bodies) != 0 || len(w.destroyed) != 0 {
				t.Fatal("failed attachment changed ownership")
			}
		})
	}
}

func TestPhysicsAttachmentAdoptsUnownedBodyIdentity(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &navigationTestEntity{id: 42}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	h := p.BodyHandle{World: 1, Index: 7, Generation: 1}
	w := &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{h: {Handle: h, Mode: p.FreeDynamic, Pose: p.Identity(), Anchor: p.Identity()}}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachPhysics(42, h, PhysicsAttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	state, err := s.PhysicsBody(42)
	if err != nil || state.Owner != 42 {
		t.Fatalf("body identity was not bound: state=%+v err=%v", state, err)
	}
}

func TestPhysicsFreeDynamicRejectsDriveWithoutChangingIntent(t *testing.T) {
	s, _, w := physicsNavigationTestServer(t)
	physicsTestAttach(t, s, w, 1, p.FreeDynamic, PhysicsAttachmentConfig{})
	a := s.navigation.agents[1]
	generation := a.state.Generation
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 1}); err == nil {
		t.Fatal("free body accepted drive intent")
	}
	if a.state.Generation != generation || a.state.HasDestination || a.needsPath {
		t.Fatal("rejected drive changed navigation state")
	}
}

func TestPhysicsStaticAndNavigationRejectAtAttachment(t *testing.T) {
	for _, navigationFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "physics-first", true: "navigation-first"}[navigationFirst], func(t *testing.T) {
			s, _, w := physicsNavigationTestServer(t)
			cfg := s.navigation.agents[1].config
			if !navigationFirst {
				s.DetachNavigation(1)
			}
			h := p.BodyHandle{World: 1, Index: 1, Generation: 1}
			w.bodies = map[p.BodyHandle]p.BodyState{h: {Handle: h, Owner: 1, Mode: p.Static, Pose: p.Identity(), Anchor: p.Identity()}}
			err := s.AttachPhysics(1, h, PhysicsAttachmentConfig{})
			if navigationFirst {
				if err == nil || len(s.physics.agents) != 0 {
					t.Fatal("static body accepted an existing navigation attachment")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := s.AttachNavigation(1, cfg); err == nil || len(s.navigation.agents) != 0 {
					t.Fatal("navigation attached to static body")
				}
			}
		})
	}
}

func TestPhysicsFrameObserverReportsAcceptedAndRejectedAttempts(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[rejected], func(t *testing.T) {
			s := NewServer(ServerConfig{})
			w := &physicsTestWorld{}
			if rejected {
				w.acceptErr = errors.New("test rejection")
			}
			if err := s.SetPhysicsWorld(w); err != nil {
				t.Fatal(err)
			}
			var observations []PhysicsFrameMetrics
			if err := s.ObservePhysicsFrames(func(m PhysicsFrameMetrics) { observations = append(observations, m) }); err != nil {
				t.Fatal(err)
			}
			s.tick = 1
			_ = s.physics.step(.05)
			if len(observations) != 1 {
				t.Fatal("observer cadence differs from attempted frames")
			}
			m := observations[0]
			if m.Frame != 1 || m.RequestedDelta != .05 || m.Accepted == rejected || (m.Err != nil) != rejected || m.Duration < m.Solve {
				t.Fatalf("metrics=%+v", m)
			}
			if !rejected && (m.Elapsed != .05 || m.Substeps != 4) {
				t.Fatal("accepted simulated time absent")
			}
		})
	}
}
