package box3d_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func motionFloor() p.ShapeDesc {
	return p.ShapeDesc{Kind: p.TriangleMesh, Vertices: []p.Vec3{{X: -2, Z: -2}, {X: -2, Z: 2}, {X: 2, Z: -2}, {X: 2, Z: 2}}, Indices: []uint32{0, 1, 2, 2, 1, 3}}
}

func motionWorld(t *testing.T) *box.World {
	t.Helper()
	c := p.DefaultConfig()
	c.Gravity = p.Vec3{}
	c.AllowSleep = false
	w, err := box.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestMotionStepMovingMeshTransfersMomentumAndRetainsPoses(t *testing.T) {
	w := motionWorld(t)
	floor, _ := add(t, w, p.Kinematic, p.Vec3{Y: -2}, motionFloor())
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err := w.SetVelocity(floor, p.Vec3{Y: 80}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	state, _ := w.Body(body)
	if state.Pose.Position.Y < 2.45 || state.LinearVelocity.Y < 79 || r.Microsteps <= r.Substeps || r.MotionPairTests == 0 {
		t.Fatalf("mesh response: %+v state=%+v", r, state)
	}
	force := false
	for _, event := range r.Events {
		force = force || event.Pair.Kind == p.SolidContact && event.Force.Length() > 1
	}
	if !force {
		t.Fatal("solver impulse events lost")
	}
	for _, path := range r.Paths {
		if len(path.Poses) != r.Microsteps+1 || len(path.Fractions) != len(path.Poses) || len(path.Positions) != len(path.Poses) || path.Fractions[0] != 0 || path.Fractions[len(path.Fractions)-1] != 1 {
			t.Fatalf("missing microstep poses: %+v", path)
		}
	}
}

func TestMotionStepRotatingMeshMovesIdleBody(t *testing.T) {
	w := motionWorld(t)
	floor, _ := add(t, w, p.Kinematic, p.Vec3{}, motionFloor())
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: 1.5, Y: .5}, p.ShapeDesc{Kind: p.Sphere, Radius: .2})
	if err := w.SetVelocity(floor, p.Vec3{}, p.Vec3{Z: 20}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	state, _ := w.Body(body)
	if state.Pose.Position.Y < .6 || r.Microsteps <= 10 {
		t.Fatalf("rotator failed: steps=%d state=%+v", r.Microsteps, state)
	}
}

func TestMotionStepRetainsSensorEnterAndExitInsideFrame(t *testing.T) {
	w := motionWorld(t)
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	if err != nil {
		t.Fatal(err)
	}
	body, err := w.CreateBody(p.DefaultBody(p.Static))
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultCollider(body, shape)
	d.Roles = p.Roles{Sensor: true, Query: true}
	sensor, err := w.CreateCollider(d)
	if err != nil {
		t.Fatal(err)
	}
	visitor, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	if err = w.SetVelocity(visitor, p.Vec3{X: 80}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	enter, exit := float32(-1), float32(-1)
	for _, event := range r.Events {
		if event.Pair.Kind != p.SensorContact || event.Pair.A != sensor && event.Pair.B != sensor {
			continue
		}
		if event.Phase == p.Enter {
			enter = event.Fraction
		}
		if event.Phase == p.Exit {
			exit = event.Fraction
		}
	}
	if enter < 0 || exit <= enter || exit > 1 {
		t.Fatalf("lost sensor transitions: %+v", r.Events)
	}
}

func TestMotionStepAggregatesManifoldPointForces(t *testing.T) {
	w := world(t)
	add(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 4, Y: .5, Z: 4}})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: .505}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}})
	var r p.FrameResult
	for i := uint64(1); i <= 30; i++ {
		r = step(t, w, i)
	}
	state, _ := w.Body(body)
	last := float32(0)
	for _, event := range r.Events {
		if event.Pair.Kind == p.SolidContact && event.Fraction == 1 {
			last += float32(math.Abs(float64(event.Force.Y)))
		}
	}
	if math.Abs(float64(last-state.Mass*9.81)) > .5 {
		t.Fatalf("manifold impulses were discarded: force=%g mass=%g events=%+v", last, state.Mass, r.Events)
	}
}

func TestMotionStepMovingMeshRequiresKinematicContinuousMode(t *testing.T) {
	for _, tc := range []struct {
		mode       p.MotionMode
		continuous bool
	}{{p.FreeDynamic, true}, {p.DrivenCharacter, true}, {p.Kinematic, false}} {
		config := p.DefaultConfig()
		config.Continuous = tc.continuous
		w, err := box.New(config)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := w.CreateShape(motionFloor())
		if err != nil {
			t.Fatal(err)
		}
		body, err := w.CreateBody(p.DefaultBody(tc.mode))
		if err != nil {
			t.Fatal(err)
		}
		_, err = w.CreateCollider(p.DefaultCollider(body, shape))
		if !errors.Is(err, p.ErrUnsupported) {
			t.Fatalf("unsupported moving geometry accepted: %+v %v", tc, err)
		}
		_ = w.Close()
	}
}

func TestMotionStepSpinningBodyOnLargeCoordinateTriangleFloor(t *testing.T) {
	w := world(t)
	mesh := p.ShapeDesc{Kind: p.TriangleMesh}
	const cells = 50
	for z := 0; z <= cells; z++ {
		for x := 0; x <= cells; x++ {
			mesh.Vertices = append(mesh.Vertices, p.Vec3{X: -384 + 768*float32(x)/cells, Z: -384 + 768*float32(z)/cells})
		}
	}
	for z := 0; z < cells; z++ {
		for x := 0; x < cells; x++ {
			a := uint32(z*(cells+1) + x)
			mesh.Indices = append(mesh.Indices, a, a+cells+1, a+1, a+1, a+cells+1, a+cells+2)
		}
	}
	add(t, w, p.Static, p.Vec3{}, mesh)
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -192, Y: 2, Z: 192}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .4, Y: .5, Z: .4}})
	if err := w.SetVelocity(body, p.Vec3{}, p.Vec3{X: .3, Y: .2, Z: .1}); err != nil {
		t.Fatal(err)
	}
	for frame := uint64(1); frame <= 30; frame++ {
		step(t, w, frame)
	}
	state, _ := w.Body(body)
	if state.Pose.Position.Y < .35 {
		t.Fatalf("body crossed floor: %+v", state)
	}
}

func TestMotionStepDenseFloorFallingBoxCluster(t *testing.T) {
	for _, workers := range []int{1, 8} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			config := p.DefaultConfig()
			config.Workers = uint32(workers)
			w, err := box.New(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Close() })
			mesh := p.ShapeDesc{Kind: p.TriangleMesh}
			const cells = 50
			for z := 0; z <= cells; z++ {
				for x := 0; x <= cells; x++ {
					mesh.Vertices = append(mesh.Vertices, p.Vec3{X: -32 + 64*float32(x)/cells, Z: -32 + 64*float32(z)/cells})
				}
			}
			for z := 0; z < cells; z++ {
				for x := 0; x < cells; x++ {
					a := uint32(z*(cells+1) + x)
					mesh.Indices = append(mesh.Indices, a, a+cells+1, a+1, a+1, a+cells+1, a+cells+2)
				}
			}
			add(t, w, p.Static, p.Vec3{}, mesh)
			cube := p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .4, Y: .5, Z: .4}}
			for i := 0; i < 199; i++ {
				add(t, w, p.Static, p.Vec3{X: 32 * (.65 + .3*float32(i%15)/15), Y: .5, Z: 32 * (-.95 + 1.9*float32(i/15)/15)}, cube)
			}
			var motions []p.Motion
			for i := 0; i < 16; i++ {
				radius, height := float32(.4), float32(1.8)
				if i%2 == 1 {
					radius, height = .8, 2.4
				}
				shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Capsule, Radius: radius, PointA: p.Vec3{Y: radius}, PointB: p.Vec3{Y: height - radius}})
				if err != nil {
					t.Fatal(err)
				}
				d := p.DefaultBody(p.DrivenCharacter)
				d.Pose.Position = p.Vec3{X: -24 + float32(i%4)*2.8, Z: float32(i/4-2) * 2.8}
				d.Mass = 80
				d.AllowSleep = false
				d.Continuous = true
				d.Locks = p.MotionLocks{AngularX: true, AngularY: true, AngularZ: true}
				body, err := w.CreateBody(d)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.CreateCollider(p.DefaultCollider(body, shape)); err != nil {
					t.Fatal(err)
				}
				motions = append(motions, p.Motion{Body: body, Velocity: p.Vec3{X: 3}, SurfaceNormal: p.Vec3{Y: 1}, MaxDriveForce: 1000})
			}
			var bodies []p.BodyHandle
			for i := 0; i < 16; i++ {
				shape, err := w.CreateShape(cube)
				if err != nil {
					t.Fatal(err)
				}
				d := p.DefaultBody(p.FreeDynamic)
				d.Pose.Position = p.Vec3{X: -16 + float32(i%4)*2.8, Y: 2 + float32(i%3), Z: 16 + float32(i/4)*1.1}
				d.Mass = 20
				d.AllowSleep = false
				d.Continuous = true
				body, err := w.CreateBody(d)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.CreateCollider(p.DefaultCollider(body, shape)); err != nil {
					t.Fatal(err)
				}
				if err = w.SetVelocity(body, p.Vec3{}, p.Vec3{X: .3, Y: .2, Z: .1}); err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, body)
			}
			for i := 0; i+1 < len(bodies); i += 10 {
				if _, err = w.CreateJoint(p.JointDesc{Kind: p.Distance, A: bodies[i], B: bodies[i+1], FrameA: p.Identity(), FrameB: p.Identity(), Length: 3, Spring: true, Hertz: 2, Damping: .7}); err != nil {
					t.Fatal(err)
				}
			}
			for frame := uint64(1); frame <= 80; frame++ {
				if _, err = w.Simulate(p.Frame{ID: frame, Delta: .05, Motions: motions}); err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if err = w.Accept(frame); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
