package golem

import (
	"errors"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func trajectoryRequest() PhysicsTrajectory {
	a, z := p.Identity(), p.Identity()
	a.Position.X = 1
	z.Position.X = 2
	return PhysicsTrajectory{Min: p.Vec3{X: -1, Y: -1, Z: -1}, Max: p.Vec3{X: 4, Y: 4, Z: 4}, Segments: []PhysicsTrajectorySegment{{Target: a, Duration: .05}, {Target: z, Duration: .05}}}
}
func trajectoryFakeStep(f p.Frame, r *p.FrameResult) {
	for _, motion := range f.Motions {
		if motion.Frozen || len(motion.PoseSegments) == 0 {
			continue
		}
		for i := range r.Bodies {
			if r.Bodies[i].Handle != motion.Body {
				continue
			}
			local := r.Bodies[i].Pose.Inverse().Compose(r.Bodies[i].Anchor)
			target := motion.PoseSegments[len(motion.PoseSegments)-1].Target
			r.Bodies[i].Pose, r.Bodies[i].Anchor = target, target.Compose(local)
			for j := range r.Paths {
				if r.Paths[j].Body == motion.Body {
					r.Paths[j].Positions = append(r.Paths[j].Positions[:1], r.Bodies[i].Anchor.Position)
				}
			}
		}
	}
}

func TestPhysicsTrajectoryOwnsCopiesProgressAndCancellationGeneration(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	e := &navigationTestEntity{id: 1}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	physicsTestAttach(t, s, w, 1, p.ScriptedAction, PhysicsAttachmentConfig{})
	w.simulate = trajectoryFakeStep
	request := trajectoryRequest()
	old, err := s.StartPhysicsTrajectory(1, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Segments[0].Target.Position.X = 500
	s.tick++
	if err = s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	state, _ := s.PhysicsTrajectoryState(1)
	if state.Elapsed != .05 || state.Status != PhysicsTrajectoryRunning || e.position.X != 1 {
		t.Fatalf("intent was aliased or time not accepted: %+v pose=%v", state, e.position)
	}
	fresh, err := s.StartPhysicsTrajectory(1, trajectoryRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelPhysicsTrajectory(old); !errors.Is(err, p.ErrStale) {
		t.Fatal("old cancellation affected replacement")
	}
	if err = s.CancelPhysicsTrajectory(fresh); err != nil {
		t.Fatal(err)
	}
	s.tick++
	if err = s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	if e.position.X != 1 {
		t.Fatal("cancellation rewound or continued trajectory")
	}
	if err = s.DetachPhysics(1); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelPhysicsTrajectory(fresh); !errors.Is(err, p.ErrStale) {
		t.Fatal("detached action token survived")
	}
}

func TestPhysicsTrajectoryQuarantinePausesClockAndUnaffectedActionContinues(t *testing.T) {
	s, entity, w := physicsNavigationTestServer(t)
	base := s.navigation.backend.(*navigationTestBackend)
	regional := &regionalTestBackend{navigationTestBackend: base, token: 1}
	s.navigation.backend = regional
	physicsTestAttach(t, s, w, 1, p.ScriptedAction, PhysicsAttachmentConfig{})
	request := trajectoryRequest()
	if _, err := s.StartPhysicsTrajectory(1, request); err != nil {
		t.Fatal(err)
	}
	other := &navigationTestEntity{id: 2, position: surface.Vec3{X: -3}}
	if err := s.CreateEntity(other); err != nil {
		t.Fatal(err)
	}
	physicsTestAttach(t, s, w, 2, p.ScriptedAction, PhysicsAttachmentConfig{})
	unaffected := trajectoryRequest()
	unaffected.Min.X = -4
	unaffected.Max.X = -1
	unaffected.SafetyProfile = "human"
	unaffected.Segments[0].Target.Position.X = -2.5
	unaffected.Segments[1].Target.Position.X = -2
	if _, err := s.StartPhysicsTrajectory(2, unaffected); err != nil {
		t.Fatal(err)
	}
	regional.edit(true, false)
	w.simulate = trajectoryFakeStep
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	state, _ := s.PhysicsTrajectoryState(1)
	if state.Status != PhysicsTrajectoryPaused || state.Elapsed != 0 || entity.position.X != 0 || other.position.X != -2.5 {
		t.Fatalf("quarantine/cadence wrong: %+v own=%v other=%v", state, entity.position, other.position)
	}
	regional.edit(false, false)
	s.tick++
	if err := s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	state, _ = s.PhysicsTrajectoryState(1)
	if state.Elapsed != .05 || entity.position.X != 1 {
		t.Fatalf("paused time caught up or never resumed: %+v %v", state, entity.position)
	}
}

func TestPhysicsTrajectoryCheckpointRestoresProgressWithFreshCancellationToken(t *testing.T) {
	s, w, _, _ := physicsCheckpointFixture(t)
	h := s.physics.agents[1].body
	state := w.bodies[h]
	state.Mode = p.ScriptedAction
	w.bodies[h] = state
	request := trajectoryRequest()
	request.Segments[0].Target.Position.X = 3
	request.Segments[1].Target.Position.X = 4
	old, err := s.StartPhysicsTrajectory(1, request)
	if err != nil {
		t.Fatal(err)
	}
	w.simulate = trajectoryFakeStep
	s.tick++
	if err = s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	capture, err := s.CapturePhysicsCheckpoint(PhysicsCheckpointOptions{ID: "trajectory", SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav", DesiredPhysicsRevision: 1, DesiredNavigationRevision: 1, DesiredPhysicsWorld: []byte(`{}`), DesiredNavigationWorld: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Trajectories) != 1 || capture.Trajectories[0].Elapsed != .05 {
		t.Fatal("accepted trajectory progress missing")
	}
	sentinel := errors.New("replacement geometry cannot admit trajectory")
	w.trajectoryErr = sentinel
	if err = s.RestorePhysicsCheckpoint(capture, physicsCheckpointOptions(t, s, nil)); !errors.Is(err, sentinel) {
		t.Fatalf("private trajectory admission failure was ignored: %v", err)
	}
	unchanged, _ := s.PhysicsTrajectoryState(1)
	if unchanged.Token != old || s.physics.agents[1].body != h {
		t.Fatal("failed trajectory recovery changed live ownership")
	}
	w.trajectoryErr = nil
	if err = s.RestorePhysicsCheckpoint(capture, physicsCheckpointOptions(t, s, nil)); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelPhysicsTrajectory(old); !errors.Is(err, p.ErrStale) {
		t.Fatal("pre-restore cancellation remained valid")
	}
	capture.Trajectories[0].Request.Segments[1].Target.Position.X = 500
	s.tick++
	if err = s.physics.step(.05); err != nil {
		t.Fatal(err)
	}
	actual, _ := s.PhysicsTrajectoryState(1)
	body, _ := s.PhysicsBody(1)
	if actual.Status != PhysicsTrajectoryCompleted || actual.Elapsed != .1 || body.Anchor.Position.X != 4 {
		t.Fatalf("restored action failed: %+v %+v", actual, body)
	}
}
