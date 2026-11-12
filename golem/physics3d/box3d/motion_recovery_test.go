package box3d_test

import (
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func recoveryWorld(t *testing.T, gravity p.Vec3) *box.World {
	t.Helper()
	config := p.DefaultConfig()
	config.Gravity = gravity
	w, err := box.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func recoveryBody(t *testing.T, w *box.World, mode p.MotionMode, pos p.Vec3, shape p.ShapeDesc, mass, restitution float32) p.BodyHandle {
	t.Helper()
	s, err := w.CreateShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(mode)
	d.Pose.Position = pos
	d.Mass = mass
	d.Continuous = true
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(body, s)
	c.Material.Restitution = restitution
	if _, err = w.CreateCollider(c); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestMotionRecoveryRestitutionDoesNotInjectEnergy(t *testing.T) {
	w := recoveryWorld(t, p.Vec3{Y: -9.81})
	recoveryBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 4, Y: .5, Z: 4}}, 1, .8)
	body := recoveryBody(t, w, p.FreeDynamic, p.Vec3{Y: 3}, p.ShapeDesc{Kind: p.Sphere, Radius: .5}, 1, .8)
	contact, bounced := false, false
	maximumPenetration := float32(0)
	for frame := uint64(1); frame <= 200; frame++ {
		r := step(t, w, frame)
		state, _ := w.Body(body)
		for _, event := range r.Events {
			if event.Pair.Kind == p.SolidContact {
				contact = true
			}
		}
		bounced = bounced || contact && state.LinearVelocity.Y > 1
		energy := 9.81*(state.Pose.Position.Y-.5) + .5*state.LinearVelocity.Dot(state.LinearVelocity)
		maximumPenetration = max(maximumPenetration, .5-state.Pose.Position.Y)
		// Native contact compliance permits transient overlap. This response test
		// checks energy and floor crossing, not placement/query depth tolerances.
		if energy > 25.1 || state.Pose.Position.Y < 0 {
			t.Fatalf("unbounded rebound frame%d energy=%g state=%+v", frame, energy, state)
		}
	}
	if !bounced {
		t.Fatal("restitution never produced a bounce")
	}
	state, _ := w.Body(body)
	if state.Awake || state.Pose.Position.Y > 1 {
		t.Fatalf("rebounding body did not settle on its support: %+v", state)
	}
	t.Logf("maximum observed native contact penetration: %g metres", maximumPenetration)
}

func TestMotionRecoveryMassRatioConservesMomentum(t *testing.T) {
	w := recoveryWorld(t, p.Vec3{})
	shape := p.ShapeDesc{Kind: p.Sphere, Radius: .5}
	light := recoveryBody(t, w, p.FreeDynamic, p.Vec3{X: -2}, shape, 1, 0)
	heavy := recoveryBody(t, w, p.FreeDynamic, p.Vec3{}, shape, 100, 0)
	if err := w.SetVelocity(light, p.Vec3{X: 10}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	for frame := uint64(1); frame <= 30; frame++ {
		step(t, w, frame)
	}
	a, _ := w.Body(light)
	z, _ := w.Body(heavy)
	momentum := a.LinearVelocity.X*a.Mass + z.LinearVelocity.X*z.Mass
	energy := .5*a.Mass*a.LinearVelocity.Dot(a.LinearVelocity) + .5*z.Mass*z.LinearVelocity.Dot(z.LinearVelocity)
	if math.Abs(float64(momentum-10)) > .01 || energy > 50.01 || a.Pose.Position.X >= z.Pose.Position.X {
		t.Fatalf("mass ratio response: momentum=%g energy=%g a=%+v b=%+v", momentum, energy, a, z)
	}
}

func TestMotionRecoveryStackSettlesAndSleeps(t *testing.T) {
	w := recoveryWorld(t, p.Vec3{Y: -9.81})
	recoveryBody(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 4, Y: .5, Z: 4}}, 1, 0)
	var bodies []p.BodyHandle
	for i := 0; i < 6; i++ {
		mass := float32(1)
		if i == 0 {
			mass = 100
		}
		bodies = append(bodies, recoveryBody(t, w, p.FreeDynamic, p.Vec3{Y: .505 + float32(i)*1.01}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}}, mass, 0))
	}
	for frame := uint64(1); frame <= 200; frame++ {
		if _, err := w.Simulate(p.Frame{ID: frame, Delta: .05}); err != nil {
			t.Fatalf("frame%d fresh endpoint depth=%g: %v", frame, box.MotionMaximumDepthForTest(w), err)
		}
		if err := w.Accept(frame); err != nil {
			t.Fatal(err)
		}
	}
	for i, body := range bodies {
		state, _ := w.Body(body)
		if state.Awake || math.Abs(float64(state.Pose.Position.Y-(.5+float32(i)))) > .08 {
			t.Fatalf("stack did not settle/sleep: index%d state=%+v", i, state)
		}
	}
}
