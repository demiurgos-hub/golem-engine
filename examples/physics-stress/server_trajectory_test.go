package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func TestServerExternalTrajectoriesSweepTranslationAndRotation(t *testing.T) {
	for _, kind := range []string{"clear", "translation-blocked", "rotation-blocked"} {
		t.Run(kind, func(t *testing.T) {
			w, err := box3d.New(p.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			s := golem.NewServer(golem.ServerConfig{TickRate: 20})
			if err = s.SetPhysicsWorld(w); err != nil {
				t.Fatal(err)
			}
			extents := p.Vec3{X: .2, Y: .2, Z: .2}
			if kind == "rotation-blocked" {
				extents = p.Vec3{X: 1, Y: .2, Z: .1}
				controllerBody(t, w, p.Static, p.Vec3{X: .7, Z: -.7}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .1, Y: 1, Z: .1}}, false)
			}
			if kind == "translation-blocked" {
				controllerBody(t, w, p.Static, p.Vec3{X: 1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .05, Y: 1, Z: 1}}, false)
			}
			body, _ := controllerBody(t, w, p.ExternalPose, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: extents}, false)
			a := &actor{ID: 1, body: body}
			if err = s.CreateEntity(a); err != nil {
				t.Fatal(err)
			}
			if err = s.AttachPhysics(1, body, golem.PhysicsAttachmentConfig{}); err != nil {
				t.Fatal(err)
			}
			if err = w.SetVelocity(body, p.Vec3{X: 1}, p.Vec3{}); err == nil {
				t.Fatal("external velocity bypassed trajectory admission")
			}
			target := p.Identity()
			target.Position.X = 2
			target.Rotation = p.Quat{Y: float32(math.Sin(.25)), W: float32(math.Cos(.25))}
			if kind == "translation-blocked" {
				target.Rotation = p.Identity().Rotation
			}
			if kind == "rotation-blocked" {
				target.Position = p.Vec3{}
				target.Rotation = p.Quat{Y: float32(math.Sqrt(.5)), W: float32(math.Sqrt(.5))}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			count := 0
			s.OnTick(func(_ float64, server *golem.Server) {
				count++
				if count == 1 {
					_, err = server.StartPhysicsTrajectory(1, golem.PhysicsTrajectory{Min: p.Vec3{X: -3, Y: -3, Z: -3}, Max: p.Vec3{X: 3, Y: 3, Z: 3}, Segments: []golem.PhysicsTrajectorySegment{{Target: target, Duration: .1}}})
					if err != nil {
						t.Error(err)
						cancel()
					}
				}
			})
			var final p.BodyState
			var action golem.PhysicsTrajectoryState
			if err = s.ObservePhysicsFrames(func(m golem.PhysicsFrameMetrics) {
				if m.Err != nil {
					t.Error(m.Err)
					cancel()
					return
				}
				final, err = s.PhysicsBody(1)
				if err != nil {
					t.Error(err)
				}
				action, err = s.PhysicsTrajectoryState(1)
				if err != nil {
					t.Error(err)
				}
				if count >= 3 {
					cancel()
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err = s.Run(ctx); err != nil && ctx.Err() == nil {
				t.Fatal(err)
			}
			if count < 3 {
				t.Fatal("trajectory fixture stopped early")
			}
			if kind == "clear" {
				if action.Status != golem.PhysicsTrajectoryCompleted || final.Anchor.Position.Sub(target.Position).Length() > .002 {
					t.Fatalf("clear trajectory failed: action=%+v body=%+v", action, final)
				}
			} else if action.Status != golem.PhysicsTrajectoryBlocked {
				t.Fatalf("swept obstruction was skipped: action=%+v body=%+v", action, final)
			}
			if kind == "translation-blocked" && final.Anchor.Position.X > .751 {
				t.Fatal("external body crossed thin wall")
			}
		})
	}
}

func TestServerScriptedTrajectoryCancellationKeepsTransferredMomentum(t *testing.T) {
	var world *box3d.World
	var pushed p.BodyHandle
	var token golem.PhysicsTrajectoryToken
	var before, after p.BodyState
	var cancelledAt p.Vec3
	runControllerServer(t, p.ScriptedAction, p.Vec3{Y: .901}, 3, func(w *box3d.World) {
		world = w
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}}, false)
		pushed, _ = controllerBody(t, w, p.FreeDynamic, p.Vec3{X: 1.2, Y: .901}, p.ShapeDesc{Kind: p.Sphere, Radius: .2}, false)
	}, func(tick int, s *golem.Server) {
		if tick == 1 {
			target := p.Identity()
			target.Position = p.Vec3{X: 2, Y: .0010000467}
			var err error
			token, err = s.StartPhysicsTrajectory(1, golem.PhysicsTrajectory{Min: p.Vec3{X: -1, Y: -1, Z: -1}, Max: p.Vec3{X: 3, Y: 3, Z: 3}, Segments: []golem.PhysicsTrajectorySegment{{Target: target, Duration: .1}}})
			if err != nil {
				t.Error(err)
			}
		}
		if tick == 2 {
			actor, err := s.PhysicsBody(1)
			if err != nil {
				t.Error(err)
				return
			}
			cancelledAt = actor.Anchor.Position
			before, _ = world.Body(pushed)
			if err := s.CancelPhysicsTrajectory(token); err != nil {
				t.Error(err)
			}
			actorAfter, err := s.PhysicsBody(1)
			if err != nil || actorAfter.Pose != actor.Pose {
				t.Errorf("cancellation changed the actor's accepted pose: before=%+v after=%+v error=%v", actor, actorAfter, err)
			}
			after, _ = world.Body(pushed)
			if before.Pose != after.Pose || before.LinearVelocity != after.LinearVelocity {
				t.Error("cancellation rewound another body")
			}
		}
	}, func(state p.BodyState, r p.FrameResult) {
		if r.ID >= 2 && state.Anchor.Position.Sub(cancelledAt).Length() > 1e-5 {
			t.Errorf("cancelled scripted actor moved from %v to %+v", cancelledAt, state)
		}
		if r.ID == 3 {
			after, _ = world.Body(pushed)
		}
	})
	if before.LinearVelocity.X <= 1 || after.Pose.Position.X <= before.Pose.Position.X {
		t.Fatalf("transferred momentum was lost: before=%+v after=%+v", before, after)
	}
}

func TestServerScriptedJumpRetainsVerticalTrajectory(t *testing.T) {
	var final p.BodyState
	runControllerServer(t, p.ScriptedAction, p.Vec3{Y: .901}, 2, func(w *box3d.World) {
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}}, false)
	}, func(tick int, s *golem.Server) {
		if tick != 1 {
			return
		}
		top, end := p.Identity(), p.Identity()
		top.Position = p.Vec3{X: .5, Y: 1}
		end.Position = p.Vec3{X: 1, Y: .0010000467}
		if _, err := s.StartPhysicsTrajectory(1, golem.PhysicsTrajectory{Min: p.Vec3{X: -1, Y: -1, Z: -1}, Max: p.Vec3{X: 2, Y: 2, Z: 2}, Segments: []golem.PhysicsTrajectorySegment{{Target: top, Duration: .05}, {Target: end, Duration: .05}}}); err != nil {
			t.Error(err)
		}
	}, func(state p.BodyState, r p.FrameResult) {
		if r.ID == 1 && state.Anchor.Position.Sub(p.Vec3{X: .5, Y: 1}).Length() > .002 {
			t.Errorf("scripted apex was changed by controller gravity: %+v", state)
		}
		final = state
	})
	if final.Anchor.Position.Sub(p.Vec3{X: 1, Y: .0010000467}).Length() > .002 {
		t.Fatalf("scripted landing changed: %+v", final)
	}
}
