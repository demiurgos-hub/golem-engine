package main

import (
	"errors"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func TestServerMotionPolicyDrivenJumpReturnsWithoutReattachment(t *testing.T) {
	var original p.BodyHandle
	var token golem.PhysicsTrajectoryToken
	var final p.BodyState
	runControllerServer(t, p.DrivenCharacter, p.Vec3{Y: .901}, 4, func(w *box3d.World) {
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}}, false)
	}, func(tick int, s *golem.Server) {
		if tick == 1 {
			state, err := s.PhysicsBody(1)
			if err != nil {
				t.Error(err)
				return
			}
			original = state.Handle
			if err = s.SetPhysicsMotionPolicy(1, p.MotionPolicy{Mode: p.ScriptedAction, Controller: p.DefaultCharacterController(p.ScriptedAction), Velocity: p.VelocityClear}); err != nil {
				t.Error(err)
				return
			}
			top, end := p.Identity(), p.Identity()
			top.Position = p.Vec3{X: .5, Y: 1}
			end.Position = p.Vec3{X: 1, Y: .0010000467}
			token, err = s.StartPhysicsTrajectory(1, golem.PhysicsTrajectory{Min: p.Vec3{X: -1, Y: -1, Z: -1}, Max: p.Vec3{X: 3, Y: 3, Z: 3}, Segments: []golem.PhysicsTrajectorySegment{{Target: top, Duration: .05}, {Target: end, Duration: .05}}})
			if err != nil {
				t.Error(err)
			}
		}
		if tick == 3 {
			cfg := p.DefaultCharacterController(p.DrivenCharacter)
			cfg.Velocity.X = 2
			if err := s.SetPhysicsMotionPolicy(1, p.MotionPolicy{Mode: p.DrivenCharacter, Controller: cfg, Velocity: p.VelocityClear}); err != nil {
				t.Error(err)
			}
			if err := s.CancelPhysicsTrajectory(token); !errors.Is(err, p.ErrStale) {
				t.Error("pre-transition token survived", err)
			}
		}
	}, func(state p.BodyState, frame p.FrameResult) {
		if state.Handle != original {
			t.Error("body handle changed")
		}
		if frame.ID == 1 && state.Anchor.Position.Sub(p.Vec3{X: .5, Y: 1}).Length() > .002 {
			t.Error("jump apex not retained", state)
		}
		if frame.ID == 2 && state.Anchor.Position.Sub(p.Vec3{X: 1, Y: .0010000467}).Length() > .002 {
			t.Error("jump landing not retained", state)
		}
		final = state
	})
	if final.Mode != p.DrivenCharacter || final.Anchor.Position.X < 1.1 {
		t.Fatal("driven motion did not resume", final)
	}
}
