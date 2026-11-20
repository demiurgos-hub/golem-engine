package golem

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// transferTestWorld is a physicsTestWorld issuing handles of its own world ID,
// with body/joint creation so RebuildPhysics callbacks use the real World API.
type transferTestWorld struct {
	*physicsTestWorld
	id     uint64
	name   string
	next   uint32
	joints map[p.JointHandle][2]p.BodyHandle
	log    *[]string
	simErr error
}

func newTransferTestWorld(id uint64, log *[]string) *transferTestWorld {
	return &transferTestWorld{physicsTestWorld: &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{}},
		id: id, name: fmt.Sprint(id), joints: map[p.JointHandle][2]p.BodyHandle{}, log: log}
}

func (w *transferTestWorld) record(event string) {
	if w.log != nil {
		*w.log = append(*w.log, event+":"+w.name)
	}
}

func (w *transferTestWorld) CreateBody(d p.BodyDesc) (p.BodyHandle, error) {
	w.next++
	h := p.BodyHandle{World: w.id, Index: w.next, Generation: 1}
	w.bodies[h] = p.BodyState{Handle: h, ID: d.ID, Owner: d.Owner, Mode: d.Mode, Pose: d.Pose, Anchor: d.Pose.Compose(d.Anchor),
		LinearVelocity: d.LinearVelocity, AngularVelocity: d.AngularVelocity, Mass: d.Mass, Enabled: d.Enabled, Awake: d.Awake}
	return h, nil
}

func (w *transferTestWorld) CreateJoint(d p.JointDesc) (p.JointHandle, error) {
	if _, err := w.Body(d.A); err != nil {
		return p.JointHandle{}, err
	}
	if _, err := w.Body(d.B); err != nil {
		return p.JointHandle{}, err
	}
	w.next++
	h := p.JointHandle{World: w.id, Index: w.next, Generation: 1}
	w.joints[h] = [2]p.BodyHandle{d.A, d.B}
	return h, nil
}

func (w *transferTestWorld) DestroyBody(h p.BodyHandle, reason p.CleanupReason) error {
	if err := w.physicsTestWorld.DestroyBody(h, reason); err != nil {
		return err
	}
	for j, pair := range w.joints {
		if pair[0] == h || pair[1] == h {
			delete(w.joints, j)
		}
	}
	return nil
}

func (w *transferTestWorld) Simulate(f p.Frame) (p.FrameResult, error) {
	w.record("step")
	if w.simErr != nil {
		w.simulated++
		return p.FrameResult{}, w.simErr
	}
	return w.physicsTestWorld.Simulate(f)
}

func (w *transferTestWorld) Close() error {
	w.record("close")
	return w.physicsTestWorld.Close()
}

// add creates an owned body whose anchor (and pose) sits at position.
func (w *transferTestWorld) add(owner int64, mode p.MotionMode, position p.Vec3) p.BodyHandle {
	pose := p.Identity()
	pose.Position = position
	h, _ := w.CreateBody(p.BodyDesc{Owner: owner, Mode: mode, Pose: pose, Anchor: p.Identity(), Mass: 1, Enabled: true, Awake: true})
	return h
}

func (w *transferTestWorld) owned(owner int64) []p.BodyHandle {
	var out []p.BodyHandle
	for h, state := range w.bodies {
		if state.Owner == owner {
			out = append(out, h)
		}
	}
	return out
}

func transferTestEntity(t *testing.T, s *Server, space SpaceID, id int64, position surface.Vec3) *navigationTestEntity {
	t.Helper()
	e := &navigationTestEntity{id: id, position: position}
	if err := s.CreateEntityIn(space, e); err != nil {
		t.Fatal(err)
	}
	return e
}

func transferTestAttach(t *testing.T, s *Server, w *transferTestWorld, e *navigationTestEntity, mode p.MotionMode, cfg PhysicsAttachmentConfig) p.BodyHandle {
	t.Helper()
	h := w.add(e.id, mode, physicsPoint(e.position))
	if err := s.AttachPhysics(e.id, h, cfg); err != nil {
		t.Fatal(err)
	}
	return h
}

// startTestSpaces runs every space's navigation pools as Run would, without a
// ticker; tests drive ticks with transferTestTick.
func startTestSpaces(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx = ctx
	s.life = lifecycleRunning
	_ = s.eachRuntime(func(rt *spaceRuntime) error {
		if rt.navigation != nil {
			rt.navigation.start(ctx)
		}
		return nil
	})
	t.Cleanup(func() { cancel(); s.stopServices() })
}

// transferTestTick runs the space phases of one server tick in loop order.
func transferTestTick(t *testing.T, s *Server, between func()) {
	t.Helper()
	s.tick++
	if err := s.drainPhysics(); err != nil {
		t.Fatal(err)
	}
	s.drainAsyncCallbacks()
	s.prepareNavigation()
	if between != nil {
		between()
	}
	if err := s.stepSpaces(.05); err != nil {
		t.Fatal(err)
	}
	s.reconcileNavigation()
}

var transferNavConfig = NavigationAgentConfig{StagedPhysics: true, Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}}

// newTransferTestServer configures physics and surface navigation in spaces 0
// and 5, each with its own world and backend.
func newTransferTestServer(t *testing.T) (*Server, *transferTestWorld, *transferTestWorld) {
	t.Helper()
	s := NewServer(ServerConfig{})
	a, b := newTransferTestWorld(1, nil), newTransferTestWorld(2, nil)
	if err := s.SetPhysicsWorld(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(5).SetPhysicsWorld(b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(5).SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	return s, a, b
}

// rebuildOffset recreates every source body in dst at its anchor shifted by
// offset, keeping mode and velocities, and the cart (3) to crate (2) joint.
func rebuildOffset(offset p.Vec3, calls *int) func(p.World, []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
	return func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		*calls++
		out := make(map[int64]p.BodyHandle, len(src))
		for _, source := range src {
			pose := source.Body.Pose
			pose.Position = pose.Position.Add(offset)
			h, err := dst.CreateBody(p.BodyDesc{Mode: source.Body.Mode, Pose: pose, Anchor: source.Body.Pose.Inverse().Compose(source.Body.Anchor),
				LinearVelocity: source.Body.LinearVelocity, Mass: source.Body.Mass, Enabled: true, Awake: true})
			if err != nil {
				return out, err
			}
			out[source.Entity] = h
		}
		if crate, ok := out[2]; ok {
			if cart, ok := out[3]; ok {
				if _, err := dst.CreateJoint(p.JointDesc{A: cart, B: crate}); err != nil {
					return out, err
				}
			}
		}
		return out, nil
	}
}

func TestSpaceTransferMovesAvatarCrateAndJointedCart(t *testing.T) {
	s, a, b := newTransferTestServer(t)
	avatar := transferTestEntity(t, s, 0, 1, surface.Vec3{X: 1})
	crate := transferTestEntity(t, s, 0, 2, surface.Vec3{X: 2})
	cart := transferTestEntity(t, s, 0, 3, surface.Vec3{X: 3})
	plain := transferTestEntity(t, s, 0, 4, surface.Vec3{X: 4})
	if err := s.AttachNavigation(1, transferNavConfig); err != nil {
		t.Fatal(err)
	}
	transferTestAttach(t, s, a, avatar, p.DrivenCharacter, PhysicsAttachmentConfig{})
	crateBody := transferTestAttach(t, s, a, crate, p.FreeDynamic, PhysicsAttachmentConfig{MaxDriveForce: 50})
	cartBody := transferTestAttach(t, s, a, cart, p.Kinematic, PhysicsAttachmentConfig{})
	post := a.add(0, p.Static, p.Vec3{X: 9})
	if _, err := a.CreateJoint(p.JointDesc{A: cartBody, B: crateBody}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateJoint(p.JointDesc{A: cartBody, B: post}); err != nil {
		t.Fatal(err)
	}
	startTestSpaces(t, s)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 20}); err != nil {
		t.Fatal(err)
	}
	transferTestTick(t, s, nil)

	calls := 0
	var seen []PhysicsTransferSource
	rebuild := rebuildOffset(p.Vec3{X: 100}, &calls)
	err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2, 3, 4}, RebuildPhysics: func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		if dst != p.World(b) {
			t.Error("RebuildPhysics did not receive the destination world")
		}
		seen = append(seen, src...)
		return rebuild(dst, src)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(seen) != 3 || seen[0].Entity != 1 || seen[1].Entity != 2 || seen[2].Entity != 3 || seen[1].Config.MaxDriveForce != 50 || seen[0].From != 0 || seen[0].Body.Mode != p.DrivenCharacter {
		t.Fatalf("rebuild sources = %+v (calls %d)", seen, calls)
	}
	for _, id := range []int64{1, 2, 3, 4} {
		if space, _ := s.EntitySpace(id); space != 5 {
			t.Fatalf("entity %d in space %d", id, space)
		}
	}
	if len(s.physics.agents) != 0 || len(a.owned(1))+len(a.owned(2))+len(a.owned(3)) != 0 || len(a.joints) != 0 {
		t.Fatalf("source physics kept attachments=%d joints=%d", len(s.physics.agents), len(a.joints))
	}
	if _, ok := a.bodies[post]; !ok {
		t.Fatal("unmoved jointed body was destroyed")
	}
	dst := s.runtime(5).physics
	if len(dst.agents) != 3 || len(b.joints) != 1 || dst.agents[2].config.MaxDriveForce != 50 {
		t.Fatalf("destination attachments=%d joints=%d", len(dst.agents), len(b.joints))
	}
	if avatar.position.X != 101 || crate.position.X != 102 || cart.position.X != 103 || plain.position.X != 4 {
		t.Fatalf("positions avatar=%v crate=%v cart=%v plain=%v", avatar.position, crate.position, cart.position, plain.position)
	}
	state, err := s.PhysicsBody(2)
	if err != nil || state.Handle.World != b.id || state.Owner != 2 || state.Mode != p.FreeDynamic {
		t.Fatalf("moved body state = %+v, %v", state, err)
	}
	if s.navigation.agents[1] != nil {
		t.Fatal("source navigation kept the avatar")
	}
	nav, ok := s.NavigationAgentState(1)
	if !ok || nav.Mode != NavigationIdle || nav.HasDestination || s.runtime(5).navigation.agents[1].config.Profile != transferNavConfig.Profile || !s.runtime(5).navigation.agents[1].config.StagedPhysics {
		t.Fatalf("destination navigation = %+v, %v", nav, ok)
	}
	if err := s.TeleportPhysics(3, p.Pose{Position: p.Vec3{X: 110}, Rotation: p.Quat{W: 1}}); err != nil || cart.position.X != 110 {
		t.Fatalf("teleport in destination: %v (%v)", err, cart.position)
	}
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 120}); err != nil {
		t.Fatal(err)
	}
	transferTestTick(t, s, nil)
	if nav, _ := s.NavigationAgentState(1); !nav.HasDestination {
		t.Fatal("destination navigation did not accept a new intent")
	}
	s.DeleteEntity(2)
	if len(b.owned(2)) != 0 || dst.agents[2] != nil {
		t.Fatal("deleting a moved entity left its destination body")
	}
}

func TestSpaceTransferRollsBackOnRebuildFailure(t *testing.T) {
	s, a, b := newTransferTestServer(t)
	avatar := transferTestEntity(t, s, 0, 1, surface.Vec3{X: 1})
	crate := transferTestEntity(t, s, 0, 2, surface.Vec3{X: 2})
	if err := s.AttachNavigation(1, transferNavConfig); err != nil {
		t.Fatal(err)
	}
	avatarBody := transferTestAttach(t, s, a, avatar, p.DrivenCharacter, PhysicsAttachmentConfig{})
	transferTestAttach(t, s, a, crate, p.FreeDynamic, PhysicsAttachmentConfig{})
	startTestSpaces(t, s)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 20}); err != nil {
		t.Fatal(err)
	}
	unchanged := func(label string) {
		t.Helper()
		for _, id := range []int64{1, 2} {
			if space, _ := s.EntitySpace(id); space != 0 || s.physics.agents[id] == nil {
				t.Fatalf("%s: entity %d moved or lost physics", label, id)
			}
		}
		if s.physics.agents[1].body != avatarBody || len(b.bodies) != 0 || len(b.joints) != 0 || len(s.runtime(5).physics.agents) != 0 {
			t.Fatalf("%s: destination bodies=%d joints=%d", label, len(b.bodies), len(b.joints))
		}
		if nav, _ := s.NavigationAgentState(1); !nav.HasDestination || s.runtime(5).navigation.agents[1] != nil {
			t.Fatalf("%s: navigation changed", label)
		}
		if avatar.position.X != 1 || crate.position.X != 2 {
			t.Fatalf("%s: positions changed", label)
		}
	}
	calls := 0
	failing := func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		out, _ := rebuildOffset(p.Vec3{X: 100}, &calls)(dst, src)
		return out, errors.New("cave is full")
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2}, RebuildPhysics: failing}); err == nil || !strings.Contains(err.Error(), "cave is full") {
		t.Fatalf("failing rebuild = %v", err)
	}
	unchanged("rebuild error")

	b.placement = func(h p.BodyHandle, _ p.Pose, _ float32) (bool, error) { return b.bodies[h].Mode != p.FreeDynamic, nil }
	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2}, RebuildPhysics: rebuildOffset(p.Vec3{X: 100}, &calls)}); err == nil || !strings.Contains(err.Error(), "obstructed") {
		t.Fatalf("obstructed rebuild = %v", err)
	}
	unchanged("placement")
	b.placement = nil

	missing := func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		out, err := rebuildOffset(p.Vec3{X: 100}, &calls)(dst, src)
		delete(out, 2)
		return out, err
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2}, RebuildPhysics: missing}); err == nil || !strings.Contains(err.Error(), "no body for entity 2") {
		t.Fatalf("missing body = %v", err)
	}
	b.bodies = map[p.BodyHandle]p.BodyState{} // the callback's own unreturned body is its responsibility
	unchanged("missing body")

	modeChange := func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		for i := range src {
			src[i].Body.Mode = p.Kinematic
		}
		return rebuildOffset(p.Vec3{X: 100}, &calls)(dst, src)
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2}, RebuildPhysics: modeChange}); err == nil || !strings.Contains(err.Error(), "motion mode") {
		t.Fatalf("mode change = %v", err)
	}
	unchanged("mode change")

	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2}}); err == nil || !strings.Contains(err.Error(), "RebuildPhysics is required") {
		t.Fatalf("missing RebuildPhysics = %v", err)
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 6, Entities: []int64{1, 2}, RebuildPhysics: rebuildOffset(p.Vec3{}, &calls)}); err == nil || !strings.Contains(err.Error(), "no physics world") {
		t.Fatalf("destination without physics = %v", err)
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{1, 2, 99}, RebuildPhysics: rebuildOffset(p.Vec3{}, &calls)}); err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("unknown entity = %v", err)
	}
	unchanged("validation")
	if calls != 4 {
		t.Fatalf("RebuildPhysics calls = %d, want 4 (validation failures must not call it)", calls)
	}
}

func TestSpaceTransferRejectsBusySpacesAndCallbacks(t *testing.T) {
	s, a, b := newTransferTestServer(t)
	crate := transferTestEntity(t, s, 0, 2, surface.Vec3{X: 2})
	transferTestAttach(t, s, a, crate, p.FreeDynamic, PhysicsAttachmentConfig{})
	other := transferTestEntity(t, s, 5, 7, surface.Vec3{X: 7})
	transferTestAttach(t, s, b, other, p.FreeDynamic, PhysicsAttachmentConfig{})
	startTestSpaces(t, s)
	calls := 0
	move := SpaceTransfer{To: 5, Entities: []int64{2}, RebuildPhysics: rebuildOffset(p.Vec3{X: 100}, &calls)}
	busy := func(label string, err error) {
		t.Helper()
		if !errors.Is(err, p.ErrBusy) {
			t.Fatalf("%s: move = %v, want ErrBusy", label, err)
		}
		if space, _ := s.EntitySpace(2); space != 0 || calls != 0 {
			t.Fatalf("%s: rejected move changed state", label)
		}
	}
	dst := s.runtime(5).physics
	dst.stepping = true
	busy("other space stepping", s.MoveToSpace(move))
	dst.stepping = false

	f, err := s.Space(5).PrepareNavigationFrame(.05)
	if err != nil || f == nil {
		t.Fatalf("prepare = %v, %v", f, err)
	}
	busy("prepared navigation frame", s.MoveToSpace(move))
	if err := s.AbortNavigationFrame(f); err == nil {
		t.Fatal("space 0 accepted space 5's frame")
	}
	if err := s.Space(5).AbortNavigationFrame(f); err != nil {
		t.Fatal(err)
	}

	var callbackErr error
	b.simulate = func(_ p.Frame, r *p.FrameResult) {
		callbackErr = s.MoveToSpace(move)
		r.Events = []p.ContactEvent{{}}
	}
	var contactErr error
	posted := false
	if err := s.Space(5).OnPhysicsContact(func([]p.ContactEvent) {
		contactErr = s.MoveToSpace(move)
		_ = s.Post(func(s *Server) { posted = s.MoveToSpace(move) == nil })
	}); err != nil {
		t.Fatal(err)
	}
	var reentrant error
	move.RebuildPhysics = func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error) {
		reentrant = s.MoveToSpace(SpaceTransfer{To: 5, Entities: []int64{2}})
		return rebuildOffset(p.Vec3{X: 100}, &calls)(dst, src)
	}
	transferTestTick(t, s, nil)
	busy("inside simulation", callbackErr)
	busy("inside contact callback", contactErr)
	b.simulate = nil
	transferTestTick(t, s, nil)
	if !posted || !errors.Is(reentrant, p.ErrBusy) {
		t.Fatalf("posted move = %v, reentrant = %v", posted, reentrant)
	}
	if space, _ := s.EntitySpace(2); space != 5 || crate.position.X != 102 {
		t.Fatalf("posted move did not apply: space %d position %v", space, crate.position)
	}
	s.life = lifecycleStopping
	if err := s.MoveToSpace(SpaceTransfer{To: 0, Entities: []int64{7}}); !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("stopping move = %v", err)
	}
}

func TestSpaceTransferNeverSimulatesTwicePerTick(t *testing.T) {
	var log []string
	s := NewServer(ServerConfig{})
	a, b := newTransferTestWorld(1, &log), newTransferTestWorld(2, &log)
	if err := s.SetPhysicsWorld(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(5).SetPhysicsWorld(b); err != nil {
		t.Fatal(err)
	}
	crate := transferTestEntity(t, s, 0, 2, surface.Vec3{X: 2})
	transferTestAttach(t, s, a, crate, p.FreeDynamic, PhysicsAttachmentConfig{})
	startTestSpaces(t, s)
	calls := 0
	move := SpaceTransfer{To: 5, Entities: []int64{2}, RebuildPhysics: rebuildOffset(p.Vec3{X: 100}, &calls)}
	var lateErr error
	a.simulate = func(p.Frame, *p.FrameResult) { lateErr = s.MoveToSpace(move) }
	transferTestTick(t, s, nil)
	if !errors.Is(lateErr, p.ErrBusy) || calls != 0 {
		t.Fatalf("move after the source stepped = %v", lateErr)
	}
	a.simulate = nil
	// A move between space phases (like onTick) runs before any space steps.
	transferTestTick(t, s, func() {
		if err := s.MoveToSpace(move); err != nil {
			t.Fatal(err)
		}
	})
	inFrame := func(w *transferTestWorld) int {
		n := 0
		for _, state := range w.pending.Bodies {
			if state.Owner == 2 {
				n++
			}
		}
		return n
	}
	if inFrame(a) != 0 || inFrame(b) != 1 {
		t.Fatalf("crate simulated in source %d and destination %d frames", inFrame(a), inFrame(b))
	}
	want := []string{"step:1", "step:2", "step:1", "step:2"}
	if !slices.Equal(log, want) {
		t.Fatalf("steps = %v, want %v", log, want)
	}
}

func TestSpaceTransferReattachesNavigationIdle(t *testing.T) {
	s := NewServer(ServerConfig{})
	src, dst := &navigationTestBackend{revision: 1}, &navigationTestBackend{revision: 1}
	if err := s.SetSurfaceNavigation(src, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(7).SetSurfaceNavigation(dst, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	e := transferTestEntity(t, s, 0, 1, surface.Vec3{})
	cfg := NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}
	if err := s.AttachNavigation(1, cfg); err != nil {
		t.Fatal(err)
	}
	startTestSpaces(t, s)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 50}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for state, _ := s.NavigationAgentState(1); state.Mode != NavigationFollowing; state, _ = s.NavigationAgentState(1) {
		if time.Now().After(deadline) {
			t.Fatalf("agent did not follow: %+v", state)
		}
		transferTestTick(t, s, nil)
		time.Sleep(time.Millisecond)
	}
	if err := s.SetEntitySpace(1, 7); err != nil {
		t.Fatal(err)
	}
	if src.releases.Load() != 1 || s.navigation.agents[1] != nil {
		t.Fatalf("source route released %d times; agent kept %v", src.releases.Load(), s.navigation.agents[1] != nil)
	}
	state, ok := s.NavigationAgentState(1)
	if !ok || state.Mode != NavigationIdle || state.HasDestination || s.runtime(7).navigation.agents[1].config.Profile != "human" {
		t.Fatalf("destination agent = %+v, %v", state, ok)
	}
	before := e.position
	for range 5 {
		transferTestTick(t, s, nil)
	}
	if e.position != before {
		t.Fatalf("idle reattached agent moved from %v to %v", before, e.position)
	}
	if err := s.SetEntitySpace(1, 8); err == nil || !strings.Contains(err.Error(), "navigation") {
		t.Fatalf("destination without navigation = %v", err)
	}
}

func TestSpacePhysicsStepOrderAndMetrics(t *testing.T) {
	var log []string
	s := NewServer(ServerConfig{})
	worlds := map[SpaceID]*transferTestWorld{0: newTransferTestWorld(10, &log), 9: newTransferTestWorld(19, &log), 2: newTransferTestWorld(12, &log)}
	var metrics []PhysicsFrameMetrics
	for _, space := range []SpaceID{9, 0, 2} {
		w := worlds[space]
		w.name = fmt.Sprint(space)
		if err := s.Space(space).SetPhysicsWorld(w); err != nil {
			t.Fatal(err)
		}
		if err := s.Space(space).ObservePhysicsFrames(func(m PhysicsFrameMetrics) { metrics = append(metrics, m) }); err != nil {
			t.Fatal(err)
		}
	}
	if s.physics != s.space0.physics || s.physics.world != p.World(worlds[0]) {
		t.Fatal("Space(0).SetPhysicsWorld must configure the space 0 alias")
	}
	if err := s.Space(3).SetPhysicsWorld(worlds[2]); err == nil {
		t.Fatal("one world served two spaces")
	}
	if err := s.Space(2).SetPhysicsWorld(newTransferTestWorld(30, nil)); err == nil {
		t.Fatal("space physics configured twice")
	}
	for _, space := range []SpaceID{0, 2, 9} {
		e := transferTestEntity(t, s, space, int64(space)+1, surface.Vec3{X: float64(space)})
		transferTestAttach(t, s, worlds[space], e, p.Kinematic, PhysicsAttachmentConfig{})
	}
	startTestSpaces(t, s)
	transferTestTick(t, s, nil)
	if want := []string{"step:0", "step:2", "step:9"}; !slices.Equal(log, want) {
		t.Fatalf("step order = %v, want %v", log, want)
	}
	if len(metrics) != 3 || metrics[0].Space != 0 || metrics[1].Space != 2 || metrics[2].Space != 9 || !metrics[2].Accepted || metrics[2].World.Bodies != 1 {
		t.Fatalf("metrics = %+v", metrics)
	}
	for space, w := range worlds {
		if w.simulated != 1 || len(w.pending.Bodies) != 1 || w.pending.Bodies[0].Owner != int64(space)+1 {
			t.Fatalf("space %d simulated %d frames with %d bodies", space, w.simulated, len(w.pending.Bodies))
		}
	}
	if err := s.Space(4).ObservePhysicsFrames(func(PhysicsFrameMetrics) {}); err == nil {
		t.Fatal("observer installed on a space without physics")
	}
	if _, ok := s.runtime(4), s.spaces[4] != nil; ok {
		t.Fatal("a query created a runtime")
	}
}

func TestSpacePhysicsFaultEndsRun(t *testing.T) {
	var log []string
	s := NewServer(ServerConfig{TickRate: 200})
	a, b := newTransferTestWorld(1, &log), newTransferTestWorld(2, &log)
	b.simErr = errors.New("cave collapsed")
	if err := s.SetPhysicsWorld(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(4).SetPhysicsWorld(b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "cave collapsed") {
		t.Fatalf("Run = %v", err)
	}
	ready, _, readyErr := s.Space(4).PhysicsReady()
	if ready || readyErr == nil {
		t.Fatal("faulted space reported ready")
	}
	if !a.closed || !b.closed || !slices.Contains(log, "close:1") || !slices.Contains(log, "close:2") {
		t.Fatalf("shutdown did not close every world: %v", log)
	}
}

func TestSpaceServicesShutdownNavigationBeforePhysics(t *testing.T) {
	var log []string
	s := NewServer(ServerConfig{})
	for _, space := range []SpaceID{3, 0} {
		w := newTransferTestWorld(uint64(space)+1, &log)
		w.name = fmt.Sprint(space)
		if err := s.Space(space).SetPhysicsWorld(w); err != nil {
			t.Fatal(err)
		}
		backend := &navigationLoggingBackend{navigationTestBackend: &navigationTestBackend{revision: 1}, log: &log, name: fmt.Sprint(space)}
		if err := s.Space(space).SetSurfaceNavigation(backend, NavigationConfig{MovementBudget: time.Second}); err != nil {
			t.Fatal(err)
		}
		transferTestEntity(t, s, space, int64(space)+1, surface.Vec3{})
		if err := s.AttachNavigation(int64(space)+1, NavigationAgentConfig{Profile: "human", Speed: 1, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.runCtx, s.life = ctx, lifecycleRunning
	_ = s.eachRuntime(func(rt *spaceRuntime) error { rt.navigation.start(ctx); return nil })
	for _, id := range []int64{1, 4} {
		if err := s.SetNavigationDestination(id, surface.Vec3{X: 100}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	following := func(id int64) bool { state, _ := s.NavigationAgentState(id); return state.Mode == NavigationFollowing }
	for !following(1) || !following(4) {
		if time.Now().After(deadline) {
			t.Fatal("agents did not follow")
		}
		transferTestTick(t, s, nil)
		time.Sleep(time.Millisecond)
	}
	log = log[:0]
	s.stopServices()
	if want := []string{"release:0", "release:3", "close:0", "close:3"}; !slices.Equal(log, want) {
		t.Fatalf("shutdown order = %v, want %v", log, want)
	}
}

// navigationLoggingBackend records view releases to observe shutdown order.
type navigationLoggingBackend struct {
	*navigationTestBackend
	log  *[]string
	name string
}

func (b *navigationLoggingBackend) Acquire(ctx context.Context, profile surface.ProfileID) (surface.View, error) {
	view, err := b.navigationTestBackend.Acquire(ctx, profile)
	if err != nil {
		return nil, err
	}
	return &navigationLoggingView{View: view, backend: b}, nil
}

type navigationLoggingView struct {
	surface.View
	backend *navigationLoggingBackend
}

func (v *navigationLoggingView) Release() {
	*v.backend.log = append(*v.backend.log, "release:"+v.backend.name)
	v.View.Release()
}

func TestAttachPhysicsRejectsBodyOfAnotherSpace(t *testing.T) {
	s, a, b := newTransferTestServer(t)
	cave := transferTestEntity(t, s, 5, 1, surface.Vec3{X: 1})
	surfaceEntity := transferTestEntity(t, s, 0, 2, surface.Vec3{X: 1})
	// A body issued by space 0's world, at the cave entity's anchor.
	foreign := a.add(0, p.FreeDynamic, physicsPoint(cave.position))
	if err := s.AttachPhysics(1, foreign, PhysicsAttachmentConfig{}); err == nil || !strings.Contains(err.Error(), "world of space 0") {
		t.Fatalf("foreign-world body = %v", err)
	}
	attached := transferTestAttach(t, s, a, surfaceEntity, p.FreeDynamic, PhysicsAttachmentConfig{})
	if err := s.AttachPhysics(1, attached, PhysicsAttachmentConfig{}); err == nil || !strings.Contains(err.Error(), "attached in space 0") {
		t.Fatalf("body attached in another space = %v", err)
	}
	own := b.add(1, p.FreeDynamic, physicsPoint(cave.position))
	if err := s.AttachPhysics(1, own, PhysicsAttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	if s.runtime(5).physics.agents[1] == nil || s.physics.agents[1] != nil {
		t.Fatal("cave entity attached in the wrong space")
	}
	plain := transferTestEntity(t, s, 6, 3, surface.Vec3{})
	if err := s.AttachPhysics(plain.id, b.add(3, p.FreeDynamic, p.Vec3{}), PhysicsAttachmentConfig{}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("attach in a space without physics = %v", err)
	}
	if err := s.AttachNavigation(plain.id, transferNavConfig); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("navigation in a space without navigation = %v", err)
	}
	// Entity-keyed APIs act on the entity's own space.
	if state, err := s.PhysicsBody(1); err != nil || state.Handle != own {
		t.Fatalf("PhysicsBody = %+v, %v", state, err)
	}
	s.DeleteEntity(1)
	if _, err := b.Body(own); err == nil || s.runtime(5).physics.agents[1] != nil {
		t.Fatal("DeleteEntity kept the cave body")
	}
}

func TestSpaceNavigationBuildsAreIsolated(t *testing.T) {
	s, _, _ := newTransferTestServer(t)
	startTestSpaces(t, s)
	committed := make(chan struct{}, 1)
	cave := s.Space(5)
	if _, err := cave.QueueNavigationBuild("cell", 0, func(context.Context) (NavigationBuildResult, error) {
		return &navigationBuildTestResult{commit: func() error { committed <- struct{}{}; return nil }}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.NavigationBuildStatus("cell"); ok {
		t.Fatal("space 5's build is visible in space 0")
	}
	s.CancelNavigationBuild("cell")
	if state, ok := cave.NavigationBuildStatus("cell"); !ok || !state.Pending {
		t.Fatalf("space 0 cancellation reached space 5: %+v", state)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.runtime(5).navigation.buildClock = func() time.Time { return NavigationNow().Add(time.Second) }
		transferTestTick(t, s, nil)
		if state, _ := cave.NavigationBuildStatus("cell"); state.Committed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("space 5 build did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	if cave.NavigationStats().Builds.Accepted != 1 || s.NavigationStats().Builds.Accepted != 0 {
		t.Fatalf("stats space0=%+v space5=%+v", s.NavigationStats().Builds, cave.NavigationStats().Builds)
	}
	if err := cave.ForgetNavigationBuild("cell"); err != nil {
		t.Fatal(err)
	}
	if !cave.NavigationReady().Ready || !s.NavigationReady().Ready {
		t.Fatal("navigation not ready")
	}
	if err := cave.BeginNavigationRestore(); err != nil {
		t.Fatal(err)
	}
	if !cave.NavigationReady().Restoring || s.NavigationReady().Restoring {
		t.Fatal("restoration barrier is not per space")
	}
	if err := s.MoveToSpace(SpaceTransfer{To: 0, Entities: []int64{}}); err == nil {
		t.Fatal("a space restoring navigation must block transfers")
	}
	if err := cave.CompleteNavigationRestore(nil); err != nil {
		t.Fatal(err)
	}
	if ready, _, _ := cave.PhysicsReady(); !ready {
		t.Fatal("space 5 physics not ready")
	}
	if s.Space(6).NavigationReady().Ready || s.Space(6).NavigationStats() != (NavigationStats{}) {
		t.Fatal("a space without navigation reported state")
	}
	select {
	case <-committed:
	default:
		t.Fatal("build commit callback did not run")
	}
}

type navigationBuildTestResult struct{ commit func() error }

func (r *navigationBuildTestResult) Commit() error { return r.commit() }
func (r *navigationBuildTestResult) Discard()      {}

func TestSpaceServicesGuardSnapshotsCheckpointsAndSetup(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := newTransferTestWorld(2, nil)
	if err := s.Space(5).SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadSnapshotStrict("missing", "fp", nil); err == nil || !strings.Contains(err.Error(), "entity-only") {
		t.Fatalf("strict load with physics in space 5 = %v", err)
	}
	if err := <-s.SaveContentSnapshot("fp", strings.Repeat("a", 64), t.TempDir()+"/manifest.json"); err == nil || !strings.Contains(err.Error(), "entity-only") {
		t.Fatalf("content snapshot with physics in space 5 = %v", err)
	}
	if _, err := s.CapturePhysicsCheckpoint(PhysicsCheckpointOptions{}); err == nil || !strings.Contains(err.Error(), "space 5 has physics but no checkpoint options") {
		t.Fatalf("checkpoint capture without space 5 options = %v", err)
	}
	if err := s.RestorePhysicsCheckpoint(PhysicsCheckpoint{}, PhysicsRestoreOptions{}); !errors.Is(err, p.ErrInvalid) {
		t.Fatalf("invalid checkpoint restore = %v", err)
	}
	if _, err := s.CaptureNavigationCheckpoint("id", "fp", 1, nil); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("space 0 navigation checkpoint without navigation = %v", err)
	}
	s.runtime(5).physics.stepping = true
	err := s.CreateEntityWithSetupIn(0, &navigationTestEntity{id: 9}, func(Entity) (func() error, error) { return nil, nil })
	s.runtime(5).physics.stepping = false
	if !errors.Is(err, p.ErrBusy) {
		t.Fatalf("entity setup while space 5 steps = %v", err)
	}
}
