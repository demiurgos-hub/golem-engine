package golem

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

type policyTestWorld struct {
	*physicsTestWorld
	policyErr error
}

func (w *policyTestWorld) SetMotionPolicy(h p.BodyHandle, policy p.MotionPolicy) error {
	if w.policyErr != nil {
		return w.policyErr
	}
	state := w.bodies[h]
	state.Mode = policy.Mode
	if policy.Velocity == p.VelocityClear {
		state.LinearVelocity, state.AngularVelocity = p.Vec3{}, p.Vec3{}
	}
	w.bodies[h] = state
	return nil
}

func TestPhysicsMotionPolicyInvalidatesTrajectoryOnlyOnSuccess(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &policyTestWorld{physicsTestWorld: &physicsTestWorld{}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	e := &navigationTestEntity{id: 1}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w.physicsTestWorld, 1, p.ScriptedAction, PhysicsAttachmentConfig{})
	token, err := s.StartPhysicsTrajectory(1, trajectoryRequest())
	if err != nil {
		t.Fatal(err)
	}
	a := s.physics.agents[1]
	policy := p.MotionPolicy{Mode: p.DrivenCharacter, Velocity: p.VelocityRetain}
	w.policyErr = p.ErrUnsupported
	if err = s.SetPhysicsMotionPolicy(1, policy); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal(err)
	}
	intent, _ := s.PhysicsTrajectoryState(1)
	if intent.Token != token || intent.Status != PhysicsTrajectoryRunning {
		t.Fatal("rejection invalidated trajectory")
	}
	w.policyErr = nil
	if err = w.SetVelocity(h, p.Vec3{X: 3}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPhysicsMotionPolicy(1, policy); err != nil {
		t.Fatal(err)
	}
	state, _ := s.PhysicsBody(1)
	if s.physics.agents[1] != a || state.Handle != h || state.Mode != p.DrivenCharacter || state.LinearVelocity.X != 3 || e.position.X != 0 {
		t.Fatal("policy recreated or rewound attachment", state)
	}
	if err = s.CancelPhysicsTrajectory(token); !errors.Is(err, p.ErrStale) {
		t.Fatal("old cancellation token survived", err)
	}
	policy.Mode = p.ScriptedAction
	if err = s.SetPhysicsMotionPolicy(1, policy); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.StartPhysicsTrajectory(1, trajectoryRequest())
	if err != nil || fresh.Generation <= token.Generation {
		t.Fatal("trajectory generation not fresh", fresh, err)
	}
}

func TestPhysicsMotionPolicyQueueRejectsSupersededAttachment(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &policyTestWorld{physicsTestWorld: &physicsTestWorld{}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEntity(&navigationTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	physicsTestAttach(t, s, w.physicsTestWorld, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	s.physics.dispatching = true
	queued := p.MotionPolicy{Mode: p.ScriptedAction, Velocity: p.VelocityClear}
	if err := s.SetPhysicsMotionPolicy(1, queued); !errors.Is(err, p.ErrBusy) {
		t.Fatal("callback mutated policy", err)
	}
	if err := s.QueuePhysicsMotionPolicy(1, queued); err != nil {
		t.Fatal(err)
	}
	s.physics.dispatching = false
	if err := s.SetPhysicsMotionPolicy(1, p.MotionPolicy{Mode: p.FreeDynamic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	s.tick++
	if err := s.physics.drain(); err != nil {
		t.Fatal(err)
	}
	state, _ := s.PhysicsBody(1)
	if state.Mode != p.FreeDynamic {
		t.Fatal("old queued policy superseded newer choice")
	}
	if err := s.QueuePhysicsMotionPolicy(1, queued); err != nil {
		t.Fatal(err)
	}
	if err := s.DetachPhysics(1); err != nil {
		t.Fatal(err)
	}
	physicsTestAttach(t, s, w.physicsTestWorld, 1, p.DrivenCharacter, PhysicsAttachmentConfig{})
	s.tick++
	if err := s.physics.drain(); err != nil {
		t.Fatal(err)
	}
	state, _ = s.PhysicsBody(1)
	if state.Mode != p.DrivenCharacter {
		t.Fatal("queued policy followed replacement")
	}
	if err := s.QueuePhysicsMotionPolicy(1, queued); err != nil {
		t.Fatal(err)
	}
	s.tick++
	if err := s.physics.drain(); err != nil {
		t.Fatal(err)
	}
	state, _ = s.PhysicsBody(1)
	if state.Mode != p.ScriptedAction {
		t.Fatal("next-tick transition missing")
	}
}
