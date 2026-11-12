package box3d_test

import (
	"errors"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"math"
	"testing"
)

func TestJointForcesRemainPrivateUntilFrameAcceptance(t *testing.T) {
	w := motionWorld(t)
	a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .2})
	joint, err := w.CreateJoint(p.JointDesc{Kind: p.Weld, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity()})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.ApplyForce(body, p.Vec3{X: 20}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Joint(joint); !errors.Is(err, p.ErrBusy) {
		t.Fatal("private joint force was observable", err)
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Joint(joint); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Simulate(p.Frame{ID: 2, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if err = w.Reject(2, errors.New("unsafe frame")); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Joint(joint); !errors.Is(err, p.ErrFaulted) {
		t.Fatal("rejected joint force was observable", err)
	}
}

func TestJointLimitsStopActiveDrives(t *testing.T) {
	for _, kind := range []p.JointKind{p.Hinge, p.Slider} {
		t.Run(map[p.JointKind]string{p.Hinge: "hinge", p.Slider: "slider"}[kind], func(t *testing.T) {
			w := motionWorld(t)
			a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
			body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .3, Y: .2, Z: .2}})
			d := p.JointDesc{Kind: kind, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), Limit: true, Lower: -.4, Upper: .4, Drive: true, MaxForce: 100, MaxTorque: 20, Speed: 2}
			joint, err := w.CreateJoint(d)
			if err != nil {
				t.Fatal(err)
			}
			for frame := uint64(1); frame <= 40; frame++ {
				step(t, w, frame)
			}
			state, err := w.Body(body)
			if err != nil {
				t.Fatal(err)
			}
			value := state.Pose.Position.X
			if kind == p.Hinge {
				value = 2 * float32(math.Atan2(float64(state.Pose.Rotation.Z), float64(state.Pose.Rotation.W)))
			}
			if value < .3 || value > .45 {
				t.Fatalf("drive failed to reach/hold limit: %g %+v", value, state)
			}
			force, err := w.Joint(joint)
			if err != nil || !force.Force.Valid() || !force.Torque.Valid() {
				t.Fatal(force, err)
			}
		})
	}
}

func TestSphericalConeAndTwistLimitsResistMotor(t *testing.T) {
	for _, axis := range []p.Vec3{{X: 1}, {Z: 1}} {
		w := motionWorld(t)
		a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .3, Z: .4}})
		d := p.JointDesc{Kind: p.Spherical, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), TargetRotation: p.Quat{W: 1}, ConeLimit: true, ConeAngle: .3, TwistLimit: true, LowerTwist: -.4, UpperTwist: .4, Drive: true, MaxTorque: 10, AngularVelocity: axis.Mul(2)}
		if _, err := w.CreateJoint(d); err != nil {
			t.Fatal(err)
		}
		for frame := uint64(1); frame <= 40; frame++ {
			step(t, w, frame)
		}
		state, _ := w.Body(body)
		var angle float64
		if axis.X != 0 {
			z := state.Pose.Rotation.Rotate(p.Vec3{Z: 1})
			angle = math.Acos(min(1, max(-1, float64(z.Z))))
			if angle < .25 || angle > .33 {
				t.Fatalf("cone limit: angle=%g state=%+v", angle, state)
			}
		} else {
			angle = 2 * math.Atan2(float64(state.Pose.Rotation.Z), float64(state.Pose.Rotation.W))
			if angle < .35 || angle > .43 {
				t.Fatalf("twist limit: angle=%g state=%+v", angle, state)
			}
		}
	}
}

func TestWeldHoldsLinearAndAngularLoads(t *testing.T) {
	w := motionWorld(t)
	a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: 1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .25, Y: .25, Z: .25}})
	d := p.JointDesc{Kind: p.Weld, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity()}
	d.FrameA.Position.X = 1
	if _, err := w.CreateJoint(d); err != nil {
		t.Fatal(err)
	}
	for frame := uint64(1); frame <= 40; frame++ {
		if err := w.ApplyForce(body, p.Vec3{Y: 50}, p.Vec3{X: 1}); err != nil {
			t.Fatal(err)
		}
		if err := w.ApplyTorque(body, p.Vec3{Z: 10}); err != nil {
			t.Fatal(err)
		}
		step(t, w, frame)
	}
	state, _ := w.Body(body)
	if state.Pose.Position.Sub(p.Vec3{X: 1}).Length() > .02 || math.Abs(float64(state.Pose.Rotation.Z)) > .01 {
		t.Fatalf("weld yielded to load: %+v", state)
	}
}

func TestDistanceSpringSettlesAndWheelMotorSpins(t *testing.T) {
	t.Run("distance-spring", func(t *testing.T) {
		w := motionWorld(t)
		a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: 2}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		d := p.JointDesc{Kind: p.Distance, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), Length: 1, Spring: true, Hertz: 3, Damping: 1}
		if _, err := w.CreateJoint(d); err != nil {
			t.Fatal(err)
		}
		for frame := uint64(1); frame <= 40; frame++ {
			step(t, w, frame)
		}
		state, _ := w.Body(body)
		if math.Abs(float64(state.Pose.Position.Length()-1)) > .02 || state.LinearVelocity.Length() > .05 {
			t.Fatalf("spring failed to settle: %+v", state)
		}
	})
	t.Run("wheel-drive", func(t *testing.T) {
		w := motionWorld(t)
		a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .2})
		d := p.JointDesc{Kind: p.Wheel, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), Drive: true, Speed: 4, MaxTorque: 5, Spring: true, Hertz: 5, Damping: 1, Limit: true, Lower: -.1, Upper: .1}
		if _, err := w.CreateJoint(d); err != nil {
			t.Fatal(err)
		}
		for frame := uint64(1); frame <= 30; frame++ {
			step(t, w, frame)
		}
		state, _ := w.Body(body)
		if math.Abs(float64(state.AngularVelocity.Z-4)) > .1 || math.Abs(float64(state.Pose.Position.X)) > .12 {
			t.Fatalf("wheel drive/suspension: %+v", state)
		}
	})
}

func TestMotorAndUprightConstraintRespond(t *testing.T) {
	t.Run("motor", func(t *testing.T) {
		w := motionWorld(t)
		a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .2})
		d := p.JointDesc{Kind: p.Motor, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), LinearVelocity: p.Vec3{X: 2}, AngularVelocity: p.Vec3{Z: 1}, MaxForce: 20, MaxTorque: 5}
		if _, err := w.CreateJoint(d); err != nil {
			t.Fatal(err)
		}
		for frame := uint64(1); frame <= 20; frame++ {
			step(t, w, frame)
		}
		state, _ := w.Body(body)
		if state.LinearVelocity.X < 1.9 || state.AngularVelocity.Z < .9 || state.Pose.Position.X < 1.5 {
			t.Fatalf("motor did not drive: %+v", state)
		}
	})
	t.Run("upright", func(t *testing.T) {
		w := motionWorld(t)
		a, _ := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
		body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .4, Z: .2}})
		d := p.JointDesc{Kind: p.Upright, A: a, B: body, FrameA: p.Identity(), FrameB: p.Identity(), Hertz: 10, Damping: 1, MaxTorque: 100}
		if _, err := w.CreateJoint(d); err != nil {
			t.Fatal(err)
		}
		for frame := uint64(1); frame <= 20; frame++ {
			if err := w.ApplyTorque(body, p.Vec3{X: 2, Y: 2}); err != nil {
				t.Fatal(err)
			}
			step(t, w, frame)
		}
		state, _ := w.Body(body)
		if up := state.Pose.Rotation.Rotate(p.Vec3{Y: 1}); up.Y < .995 || state.AngularVelocity.Y < .5 {
			t.Fatalf("upright failed: %+v", state)
		}
	})
}
