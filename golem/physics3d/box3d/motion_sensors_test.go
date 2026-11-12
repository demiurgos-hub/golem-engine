package box3d_test

import (
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func motionSensor(t *testing.T, w *box.World, shape p.ShapeDesc, pos p.Vec3) p.ColliderHandle {
	t.Helper()
	s, err := w.CreateShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Static)
	d.Pose.Position = pos
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(body, s)
	c.Roles = p.Roles{Sensor: true, Query: true}
	c.Owner = 101
	h, err := w.CreateCollider(c)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestMotionSensorThinCrossingBetweenEverySample(t *testing.T) {
	w := motionWorld(t)
	sensor := motionSensor(t, w, p.ShapeDesc{Kind: p.Sphere, Radius: .01}, p.Vec3{X: .5})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .05})
	if err := w.SetVelocity(body, p.Vec3{X: 400}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	for _, path := range r.Paths {
		if path.Body != body {
			continue
		}
		for _, pose := range path.Poses {
			if math.Abs(float64(pose.Position.X-.5)) <= .06 {
				t.Fatal("fixture endpoint overlaps sensor")
			}
		}
	}
	var transitions []p.ContactEvent
	for _, event := range r.Events {
		if event.Pair.Kind == p.SensorContact && (event.Pair.A == sensor || event.Pair.B == sensor) {
			transitions = append(transitions, event)
		}
	}
	if len(transitions) != 2 || transitions[0].Phase != p.Enter || transitions[1].Phase != p.Exit || math.Abs(float64(transitions[0].Fraction-.122)) > 1e-5 || math.Abs(float64(transitions[1].Fraction-.128)) > 1e-5 {
		t.Fatalf("lost/reordered thin crossing: %+v", transitions)
	}
	var owners p.OwnerContactTracker
	aggregated := owners.Update(r.Events)
	if len(aggregated) != 2 || aggregated[0].Phase != p.Enter || aggregated[1].Phase != p.Exit || aggregated[1].ChildPairs != 0 {
		t.Fatalf("owner crossing collapsed: %+v", aggregated)
	}
	if events, err := w.CloseWithEvents(); err != nil || len(events) != 0 {
		t.Fatalf("transient sensor remained accepted: %+v %v", events, err)
	}
}

func TestMotionSensorRejectedFrameKeepsOnlyAcceptedHistory(t *testing.T) {
	w := motionWorld(t)
	shape := p.ShapeDesc{Kind: p.Sphere, Radius: .2}
	old := motionSensor(t, w, shape, p.Vec3{X: -2})
	newSensor := motionSensor(t, w, shape, p.Vec3{})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .1})
	step(t, w, 1)
	if err := w.SetVelocity(body, p.Vec3{X: 40}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r, err := w.Simulate(p.Frame{ID: 2, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range r.Events {
		found = found || event.Phase == p.Enter && (event.Pair.A == newSensor || event.Pair.B == newSensor)
	}
	if !found {
		t.Fatal("fixture did not enter pending sensor")
	}
	if err = w.Reject(2, nil); err != nil {
		t.Fatal(err)
	}
	events, err := w.CloseWithEvents()
	if err != nil || len(events) != 1 || events[0].Pair.A != old && events[0].Pair.B != old || events[0].Reason != p.Shutdown {
		t.Fatalf("pending history leaked into shutdown: %+v %v", events, err)
	}
}

func TestMotionSensorConcaveExitReentry(t *testing.T) {
	w := motionWorld(t)
	wall := p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .01, Y: 1, Z: 1}}
	sensor := motionSensor(t, w, p.ShapeDesc{Kind: p.Compound, Children: []p.ShapeChild{{Pose: motionPoseAt(-.5, 0, 0), Shape: wall}, {Pose: motionPoseAt(.5, 0, 0), Shape: wall}}}, p.Vec3{})
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .05})
	if err := w.SetVelocity(body, p.Vec3{X: 400}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	var transitions []p.ContactEvent
	for _, event := range r.Events {
		if event.Pair.Kind == p.SensorContact && (event.Pair.A == sensor || event.Pair.B == sensor) && event.Phase != p.Stay {
			transitions = append(transitions, event)
		}
	}
	if len(transitions) != 4 {
		t.Fatalf("concave transitions: %+v", transitions)
	}
	for i, event := range transitions {
		expected := p.Enter
		if i%2 == 1 {
			expected = p.Exit
		}
		if event.Phase != expected || i > 0 && event.Fraction <= transitions[i-1].Fraction {
			t.Fatalf("concave chronology: %+v", transitions)
		}
	}
}

func TestMotionSensorRotatingBoxContactLifecycle(t *testing.T) {
	w := motionWorld(t)
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 2, Y: .1, Z: .1}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.Kinematic)
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(body, shape)
	c.Roles = p.Roles{Sensor: true, Query: true}
	sensor, err := w.CreateCollider(c)
	if err != nil {
		t.Fatal(err)
	}
	add(t, w, p.FreeDynamic, p.Vec3{X: 1.5 * float32(math.Cos(.5)), Y: 1.5 * float32(math.Sin(.5))}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .05, Y: .05, Z: .05}})
	if err = w.SetVelocity(body, p.Vec3{}, p.Vec3{Z: 20}); err != nil {
		t.Fatal(err)
	}
	r := step(t, w, 1)
	var transitions []p.ContactEvent
	for _, event := range r.Events {
		if event.Pair.Kind == p.SensorContact && (event.Pair.A == sensor || event.Pair.B == sensor) && event.Phase != p.Stay {
			transitions = append(transitions, event)
		}
	}
	if len(transitions) != 2 || transitions[0].Phase != p.Enter || transitions[1].Phase != p.Exit || transitions[0].Fraction < .3 || transitions[1].Fraction > .7 {
		t.Fatalf("rotating sensor lifecycle: %+v", transitions)
	}
}
