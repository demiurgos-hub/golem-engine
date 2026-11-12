package box3d_test

import (
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func TestNativeContinuationReboundRetainsSensorCrossingsAndImpulse(t *testing.T) {
	config := p.DefaultConfig()
	config.Gravity, config.Substeps = p.Vec3{}, 1
	w, err := box.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	sensor := motionSensor(t, w, p.ShapeDesc{Kind: p.Sphere, Radius: .01}, p.Vec3{X: -1})
	makeBody := func(mode p.MotionMode, position p.Vec3, geometry p.ShapeDesc) (p.BodyHandle, p.ColliderHandle) {
		t.Helper()
		shape, err := w.CreateShape(geometry)
		if err != nil {
			t.Fatal(err)
		}
		desc := p.DefaultBody(mode)
		desc.Pose.Position = position
		body, err := w.CreateBody(desc)
		if err != nil {
			t.Fatal(err)
		}
		collider := p.DefaultCollider(body, shape)
		collider.Material.Friction, collider.Material.Restitution = 0, .7
		handle, err := w.CreateCollider(collider)
		if err != nil {
			t.Fatal(err)
		}
		return body, handle
	}
	makeBody(p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .01, Y: 2, Z: 2}})
	body, collider := makeBody(p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .01})
	if err = w.SetVelocity(body, p.Vec3{X: 400}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	const delta = float32(1.0 / 80)
	r, err := w.Simulate(p.Frame{ID: 1, Delta: delta})
	if err != nil {
		t.Fatal(err)
	}
	if r.Microsteps != 1 || r.Elapsed != delta || r.TOIContinuationEvents == 0 {
		t.Fatalf("fixture did not continue one native impact: %+v", r)
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	state, err := w.Body(body)
	if err != nil || state.Pose.Position.X >= -1.05 || math.Abs(float64(state.LinearVelocity.X+280)) > .1 {
		t.Fatalf("fixture did not rebound past the sensor: %+v, %v", state, err)
	}
	var transitions []p.ContactEvent
	var impulse float32
	for _, event := range r.Events {
		if event.Pair.Kind == p.SensorContact && (event.Pair.A == sensor || event.Pair.B == sensor) && event.Phase != p.Stay {
			transitions = append(transitions, event)
		}
		if event.Pair.Kind == p.SolidContact {
			if event.Pair.A == collider {
				impulse -= event.Force.X * delta
			} else if event.Pair.B == collider {
				impulse += event.Force.X * delta
			}
		}
	}
	if len(transitions) != 4 {
		t.Fatalf("outward/return sensor crossings were lost: %+v", transitions)
	}
	for i, event := range transitions {
		phase := p.Enter
		if i%2 != 0 {
			phase = p.Exit
		}
		if event.Phase != phase || i > 0 && event.Fraction <= transitions[i-1].Fraction {
			t.Fatalf("sensor chronology changed: %+v", transitions)
		}
	}
	if math.Abs(float64(impulse-state.Mass*(state.LinearVelocity.X-400))) > .02 {
		t.Fatalf("contact impulse replayed or lost: reported %g, actual %g", impulse, state.Mass*(state.LinearVelocity.X-400))
	}
}
