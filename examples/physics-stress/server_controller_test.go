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

type controllerCaptureWorld struct {
	*box3d.World
	last p.FrameResult
}

func (w *controllerCaptureWorld) Simulate(f p.Frame) (p.FrameResult, error) {
	r, e := w.World.Simulate(f)
	w.last = r
	return r, e
}

func controllerBody(t *testing.T, w *box3d.World, mode p.MotionMode, at p.Vec3, shape p.ShapeDesc, controller bool) (p.BodyHandle, p.ColliderHandle) {
	t.Helper()
	geometry, err := w.CreateShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(mode)
	d.Pose.Position = at
	if controller {
		d.Controller = p.DefaultCharacterController(mode)
		d.Anchor.Position.Y = -.9
	}
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	collider, err := w.CreateCollider(p.DefaultCollider(body, geometry))
	if err != nil {
		t.Fatal(err)
	}
	return body, collider
}

func runControllerServer(t *testing.T, mode p.MotionMode, at p.Vec3, ticks int, build func(*box3d.World), tick func(int, *golem.Server), observe func(p.BodyState, p.FrameResult)) {
	t.Helper()
	w, err := box3d.New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	capture := &controllerCaptureWorld{World: w}
	s := golem.NewServer(golem.ServerConfig{TickRate: 20})
	if err = s.SetPhysicsWorld(capture); err != nil {
		t.Fatal(err)
	}
	if build != nil {
		build(w)
	}
	body, _ := controllerBody(t, w, mode, at, p.ShapeDesc{Kind: p.Capsule, Radius: .4, PointA: p.Vec3{Y: -.5}, PointB: p.Vec3{Y: .5}}, true)
	a := &actor{ID: 1, position: at.Add(p.Vec3{Y: -.9}), body: body}
	if err = s.CreateEntity(a); err != nil {
		t.Fatal(err)
	}
	if err = s.AttachPhysics(1, body, golem.PhysicsAttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	count := 0
	s.OnTick(func(_ float64, server *golem.Server) {
		count++
		if tick != nil {
			tick(count, server)
		}
	})
	if err = s.ObservePhysicsFrames(func(metrics golem.PhysicsFrameMetrics) {
		if metrics.Err != nil {
			t.Errorf("frame failed: %v", metrics.Err)
			cancel()
			return
		}
		state, e := s.PhysicsBody(1)
		if e != nil {
			t.Error(e)
			cancel()
			return
		}
		if observe != nil {
			observe(state, capture.last)
		}
		if count >= ticks {
			cancel()
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Run(ctx); err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	if count < ticks {
		t.Fatalf("server stopped after %d/%d ticks", count, ticks)
	}
}

func TestServerControllerRetainsStairPhases(t *testing.T) {
	runControllerServer(t, p.Kinematic, p.Vec3{Y: .901}, 1, func(w *box3d.World) {
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}}, false)
		controllerBody(t, w, p.Static, p.Vec3{X: 2, Y: .1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .1, Z: 2}}, false)
	}, func(_ int, s *golem.Server) {
		if err := s.SetPhysicsControllerVelocity(1, p.Vec3{X: 40}); err != nil {
			t.Error(err)
		}
	}, func(state p.BodyState, r p.FrameResult) {
		if state.Anchor.Position.X < 1.9 || state.Anchor.Position.Y < .19 || !state.Support.Grounded {
			t.Errorf("stair result=%+v", state)
		}
		var path []p.Vec3
		for _, v := range r.Paths {
			if v.Body == state.Handle {
				path = v.Positions
			}
		}
		up, forward, down := false, false, false
		for i := 1; i < len(path); i++ {
			d := path[i].Sub(path[i-1])
			up = up || d.Y > .01
			forward = forward || up && d.X > .01
			down = down || forward && d.Y < -.01
		}
		if !up || !forward || !down || r.Intervals <= r.Substeps || r.Elapsed != .05 {
			t.Errorf("stair phases collapsed: intervals=%d substeps=%d path=%v", r.Intervals, r.Substeps, path)
		}
	})
}

func TestServerDrivenCharacterClimbsPhysicalStair(t *testing.T) {
	var final p.BodyState
	runControllerServer(t, p.DrivenCharacter, p.Vec3{Y: .901}, 30, func(w *box3d.World) {
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 10, Y: .5, Z: 10}}, false)
		controllerBody(t, w, p.Static, p.Vec3{X: 2, Y: .1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .1, Z: 2}}, false)
	}, func(_ int, s *golem.Server) {
		if err := s.SetPhysicsControllerVelocity(1, p.Vec3{X: 2}); err != nil {
			t.Error(err)
		}
	}, func(state p.BodyState, _ p.FrameResult) { final = state })
	if final.Anchor.Position.X < 1.5 || final.Anchor.Position.Y < .15 {
		t.Fatalf("finite-mass character failed physical stair: %+v", final)
	}
}

func TestServerControllerMovingAndRotatingSupport(t *testing.T) {
	for _, rotating := range []bool{false, true} {
		t.Run(map[bool]string{false: "translation", true: "rotation"}[rotating], func(t *testing.T) {
			var support p.ColliderHandle
			at := p.Vec3{Y: .901}
			if rotating {
				at.X = 1
			}
			runControllerServer(t, p.Kinematic, at, 1, func(w *box3d.World) {
				platform, c := controllerBody(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}}, false)
				support = c
				linear, angular := p.Vec3{X: 2}, p.Vec3{}
				if rotating {
					linear = p.Vec3{}
					angular.Y = 2
				}
				if err := w.SetVelocity(platform, linear, angular); err != nil {
					t.Fatal(err)
				}
			}, nil, func(state p.BodyState, r p.FrameResult) {
				expected := p.Vec3{X: .1, Y: .001}
				if rotating {
					expected.X = float32(math.Cos(.1))
					expected.Z = -float32(math.Sin(.1))
				}
				if state.Anchor.Position.Sub(expected).Length() > .004 || !state.Support.Grounded || state.Support.Collider != support {
					t.Errorf("support transport state=%+v expected=%+v", state, expected)
				}
				if rotating && r.Intervals <= r.Substeps {
					t.Error("support rotation was not bounded into intervals")
				}
			})
		})
	}
}

func TestServerControllerSupportCeilingAndRemoval(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "ceiling", true: "removed"}[remove], func(t *testing.T) {
			var world *box3d.World
			var platform p.BodyHandle
			runControllerServer(t, p.Kinematic, p.Vec3{Y: .901}, 2, func(w *box3d.World) {
				world = w
				platform, _ = controllerBody(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}}, false)
				if !remove {
					controllerBody(t, w, p.Static, p.Vec3{Y: 2.1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}}, false)
					if err := w.SetVelocity(platform, p.Vec3{Y: 1}, p.Vec3{}); err != nil {
						t.Fatal(err)
					}
				}
			}, func(tick int, _ *golem.Server) {
				if remove && tick == 2 {
					if err := world.DestroyBody(platform, p.Deleted); err != nil {
						t.Error(err)
					}
					if err := world.Synchronize(); err != nil {
						t.Error(err)
					}
				}
			}, func(state p.BodyState, r p.FrameResult) {
				if !remove && state.Anchor.Position.Y > .05 {
					t.Errorf("passenger passed through ceiling: %+v", state)
				}
				if remove && r.ID == 2 && (state.Support.Grounded || state.Anchor.Position.Y >= .001) {
					t.Errorf("removed support remained attached: %+v", state)
				}
			})
		})
	}
}

func TestServerDrivenCharacterFollowsMovingSupport(t *testing.T) {
	var collider p.ColliderHandle
	var final p.BodyState
	runControllerServer(t, p.DrivenCharacter, p.Vec3{Y: .901}, 10, func(w *box3d.World) {
		platform, c := controllerBody(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 3, Y: .25, Z: 3}}, false)
		collider = c
		if err := w.SetVelocity(platform, p.Vec3{X: 1}, p.Vec3{}); err != nil {
			t.Fatal(err)
		}
	}, nil, func(state p.BodyState, _ p.FrameResult) { final = state })
	if final.Anchor.Position.X < .4 || final.Anchor.Position.X > .6 || !final.Support.Grounded || final.Support.Collider != collider {
		t.Fatalf("driven passenger lost support: %+v", final)
	}
}

func TestServerControllerRejectsReusedSupportGeneration(t *testing.T) {
	var world *box3d.World
	var platform p.BodyHandle
	var old, fresh p.ColliderHandle
	runControllerServer(t, p.Kinematic, p.Vec3{Y: .901}, 2, func(w *box3d.World) {
		world = w
		platform, old = controllerBody(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}}, false)
	}, func(tick int, _ *golem.Server) {
		if tick != 2 {
			return
		}
		if err := world.DestroyCollider(old, p.Replaced); err != nil {
			t.Error(err)
			return
		}
		shape, err := world.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
		if err != nil {
			t.Error(err)
			return
		}
		fresh, err = world.CreateCollider(p.DefaultCollider(platform, shape))
		if err != nil {
			t.Error(err)
			return
		}
		if err = world.Synchronize(); err != nil {
			t.Error(err)
		}
	}, func(state p.BodyState, r p.FrameResult) {
		if r.ID == 2 && (!state.Support.Grounded || state.Support.Collider != fresh || state.Support.Collider == old) {
			t.Errorf("stale support reused: %+v old=%v fresh=%v", state.Support, old, fresh)
		}
	})
}

func TestServerControllerWalksWhileSupportDescends(t *testing.T) {
	runControllerServer(t, p.Kinematic, p.Vec3{Y: .901}, 1, func(w *box3d.World) {
		platform, _ := controllerBody(t, w, p.Kinematic, p.Vec3{Y: -.25}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 3, Y: .25, Z: 3}}, false)
		if err := w.SetVelocity(platform, p.Vec3{Y: -5}, p.Vec3{}); err != nil {
			t.Fatal(err)
		}
	}, func(_ int, s *golem.Server) {
		if err := s.SetPhysicsControllerVelocity(1, p.Vec3{X: 2}); err != nil {
			t.Error(err)
		}
	}, func(state p.BodyState, r p.FrameResult) {
		if state.Anchor.Position.Sub(p.Vec3{X: .1, Y: -.249}).Length() > .003 || !state.Support.Grounded {
			t.Errorf("transport/locomotion lost simultaneity: %+v intervals=%d", state, r.Intervals)
		}
	})
}

func TestServerControllerMovingWallCannotCrossIdleCharacter(t *testing.T) {
	var world *box3d.World
	var wall p.BodyHandle
	runControllerServer(t, p.Kinematic, p.Vec3{Y: .901}, 1, func(w *box3d.World) {
		world = w
		controllerBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 5, Y: .5, Z: 5}}, false)
		wall, _ = controllerBody(t, w, p.Kinematic, p.Vec3{X: -1, Y: 1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: 1, Z: 2}}, false)
		if err := w.SetVelocity(wall, p.Vec3{X: 20}, p.Vec3{}); err != nil {
			t.Fatal(err)
		}
	}, nil, func(state p.BodyState, _ p.FrameResult) {
		obstacle, err := world.Body(wall)
		if err != nil {
			t.Error(err)
			return
		}
		if obstacle.Pose.Position.X+.2 > state.Pose.Position.X-.4+.002 {
			t.Errorf("moving wall crossed character: wall=%v character=%v", obstacle.Pose.Position, state.Pose.Position)
		}
	})
}

func TestServerControllerRotatingSupportUsesBodyOriginAndCenterOfMass(t *testing.T) {
	var support p.ColliderHandle
	runControllerServer(t, p.Kinematic, p.Vec3{X: 1, Y: .901}, 1, func(w *box3d.World) {
		shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .25, Z: 2}})
		if err != nil {
			t.Fatal(err)
		}
		d := p.DefaultBody(p.FreeDynamic)
		d.Pose.Position.Y = -.25
		d.GravityScale = 0
		d.Mass = 1000
		d.ManualCenterOfMass = true
		d.CenterOfMass.X = .75
		d.AngularVelocity.Y = 2
		platform, err := w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		support, err = w.CreateCollider(p.DefaultCollider(platform, shape))
		if err != nil {
			t.Fatal(err)
		}
		if err = w.SetVelocity(platform, p.Vec3{}, p.Vec3{Y: 2}); err != nil {
			t.Fatal(err)
		}
	}, nil, func(state p.BodyState, _ p.FrameResult) {
		expected := p.Vec3{X: .75 + .25*float32(math.Cos(.1)), Y: .001, Z: -.25 * float32(math.Sin(.1))}
		if state.Anchor.Position.Sub(expected).Length() > .004 || state.Support.Collider != support {
			t.Errorf("passenger rotated about wrong origin: got=%v expected=%v support=%+v", state.Anchor.Position, expected, state.Support)
		}
	})
}
