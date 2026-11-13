package box3d

import (
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func TestStationarySensorReplayRequiresIdenticalObservedEndpoints(t *testing.T) {
	var cache stationarySensorReplay
	empty := b.MotionStepSample{}
	if cache.unchanged(empty) {
		t.Fatal("unobserved static world reused contacts")
	}
	cache.remember(empty)
	if !cache.unchanged(empty) {
		t.Fatal("unchanged static world was not reusable")
	}
	one := b.MotionStepSample{Bodies: []b.MotionBodySample{{}}}
	if cache.unchanged(one) {
		t.Fatal("new observed body reused contacts")
	}
	cache.remember(one)
	if !cache.unchanged(one) {
		t.Fatal("unchanged observed body was not reusable")
	}
	one.Bodies[0].End.P.X = 1
	if cache.unchanged(one) {
		t.Fatal("moving counterpart reused contacts")
	}
	one.Bodies[0].Start = one.Bodies[0].End
	if cache.unchanged(one) {
		t.Fatal("discontinuous body placement reused contacts")
	}
	cache.remember(one)
	if cache.unchanged(empty) {
		t.Fatal("removed body reused contacts")
	}
}

func stationarySensorWorld(t *testing.T) *World {
	t.Helper()
	config := p.DefaultConfig()
	config.Gravity = p.Vec3{}
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func stationarySensorBody(t *testing.T, w *World, mode p.MotionMode, position p.Vec3, geometry p.ShapeDesc, sensor bool) (p.BodyHandle, p.ColliderHandle) {
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
	c := p.DefaultCollider(body, shape)
	if sensor {
		c.Roles = p.Roles{Sensor: true, Query: true}
	}
	collider, err := w.CreateCollider(c)
	if err != nil {
		t.Fatal(err)
	}
	return body, collider
}

func stationarySensorStep(t *testing.T, w *World, id uint64) p.FrameResult {
	t.Helper()
	result, err := w.Simulate(p.Frame{ID: id, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(id); err != nil {
		t.Fatal(err)
	}
	return result
}

func sensorPhases(result p.FrameResult, phase p.ContactPhase) int {
	count := 0
	for _, event := range result.Events {
		if event.Pair.Kind == p.SensorContact && event.Phase == phase {
			count++
		}
	}
	return count
}

func TestStationarySensorMeshContactsQueriesAndMutations(t *testing.T) {
	w := stationarySensorWorld(t)
	_, sensor := stationarySensorBody(t, w, p.Static, p.Vec3{Y: .25}, p.ShapeDesc{Kind: p.Sphere, Radius: .5}, true)
	ground, _ := stationarySensorBody(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.TriangleMesh,
		Vertices: []p.Vec3{{X: -2, Z: -2}, {X: -2, Z: 2}, {X: 2, Z: -2}, {X: 2, Z: 2}}, Indices: []uint32{0, 1, 2, 2, 1, 3}}, false)
	first := stationarySensorStep(t, w, 1)
	if sensorPhases(first, p.Enter) != 1 || !w.sensorReplay.ready {
		t.Fatalf("initial static sensor observation: %+v", first.Events)
	}
	second := stationarySensorStep(t, w, 2)
	if sensorPhases(second, p.Enter) != 0 || sensorPhases(second, p.Exit) != 0 || sensorPhases(second, p.Stay) != 4 {
		t.Fatalf("cached static contact lifecycle: %+v", second.Events)
	}
	var hits [8]p.Hit
	query := p.Query{Kind: p.Overlap, Mode: p.All, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: 1}, Pose: p.Identity(), Filter: p.QueryFilter{Mask: ^uint64(0), Sensors: p.OnlySensors}}
	result, err := w.Query(query, hits[:])
	if err != nil || result.Incomplete || result.Count != 1 || hits[0].Collider != sensor {
		t.Fatalf("cached simulation changed public queries: %+v %v", result, err)
	}
	if err = w.SetOwner(ground, 23); err != nil || w.sensorReplay.ready {
		t.Fatalf("ownership did not invalidate replay: %v", err)
	}
	stationarySensorStep(t, w, 3)
	if err = w.SetEnabled(ground, false); err != nil || w.sensorReplay.ready {
		t.Fatalf("disable did not invalidate replay: %v", err)
	}
	if result := stationarySensorStep(t, w, 4); sensorPhases(result, p.Exit) != 1 || sensorPhases(result, p.Stay) != 0 {
		t.Fatalf("disable kept cached overlap: %+v", result.Events)
	}
	if err = w.SetEnabled(ground, true); err != nil {
		t.Fatal(err)
	}
	if result := stationarySensorStep(t, w, 5); sensorPhases(result, p.Enter) != 1 {
		t.Fatalf("enable lost overlap: %+v", result.Events)
	}
	pose := p.Identity()
	pose.Position.X = 10
	if err = w.SetPose(ground, pose); err != nil || w.sensorReplay.ready {
		t.Fatalf("pose did not invalidate replay: %v", err)
	}
	if result := stationarySensorStep(t, w, 6); sensorPhases(result, p.Exit) != 1 || sensorPhases(result, p.Stay) != 0 {
		t.Fatalf("moved static retained overlap: %+v", result.Events)
	}
}

func TestStationarySensorReplayStillDetectsMovingCounterpart(t *testing.T) {
	w := stationarySensorWorld(t)
	stationarySensorBody(t, w, p.Static, p.Vec3{X: .5}, p.ShapeDesc{Kind: p.Sphere, Radius: .01}, true)
	body, _ := stationarySensorBody(t, w, p.FreeDynamic, p.Vec3{X: -2}, p.ShapeDesc{Kind: p.Sphere, Radius: .05}, false)
	stationarySensorStep(t, w, 1)
	stationarySensorStep(t, w, 2)
	if !w.sensorReplay.ready {
		t.Fatal("stationary endpoint was not recorded")
	}
	// A force does not invalidate geometry; actual native sample motion must
	// invalidate reuse even without a SetPose/SetVelocity mutation.
	if err := w.ApplyForce(body, p.Vec3{X: 8000}, p.Vec3{X: -2}); err != nil {
		t.Fatal(err)
	}
	if !w.sensorReplay.ready {
		t.Fatal("force fixture invalidated the stationary endpoint before motion")
	}
	result := stationarySensorStep(t, w, 3)
	var transitions []p.ContactEvent
	for _, event := range result.Events {
		if event.Pair.Kind == p.SensorContact && event.Phase != p.Stay {
			transitions = append(transitions, event)
		}
	}
	if len(transitions) != 2 || transitions[0].Phase != p.Enter || transitions[1].Phase != p.Exit || transitions[0].Fraction <= 0 || transitions[1].Fraction >= 1 || transitions[0].Fraction >= transitions[1].Fraction {
		t.Fatalf("cached endpoint hid swept crossing: %+v", transitions)
	}
}

func TestStationarySensorReplayRetainsAcceptedCleanupAndNewColliderIdentity(t *testing.T) {
	w := stationarySensorWorld(t)
	stationarySensorBody(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: 1}, true)
	body, collider := stationarySensorBody(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .5}, false)
	stationarySensorStep(t, w, 1)
	stationarySensorStep(t, w, 2)
	if err := w.DestroyBody(body, p.Deleted); err != nil || w.sensorReplay.ready {
		t.Fatalf("removal did not invalidate replay: %v", err)
	}
	_, replacement := stationarySensorBody(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .5}, false)
	if replacement == collider {
		t.Fatal("fixture did not replace generational collider identity")
	}
	result := stationarySensorStep(t, w, 3)
	if sensorPhases(result, p.Enter) != 1 || sensorPhases(result, p.Exit) != 1 {
		t.Fatalf("replacement contact identity lost: %+v", result.Events)
	}
	if _, err := w.Simulate(p.Frame{ID: 4, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if err := w.Reject(4, nil); err != nil {
		t.Fatal(err)
	}
	events, err := w.CloseWithEvents()
	if err != nil || len(events) != 1 || events[0].Phase != p.Exit || events[0].Reason != p.Shutdown || events[0].Pair.A != replacement && events[0].Pair.B != replacement {
		t.Fatalf("cached pending frame changed accepted shutdown history: %+v %v", events, err)
	}
}
