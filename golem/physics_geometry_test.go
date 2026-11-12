package golem

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

type physicsGeometryTestWorld struct {
	*physicsTestWorld
	next      uint32
	shapes    map[p.ShapeHandle]p.ShapeDesc
	colliders map[p.ColliderHandle]p.ColliderDesc
	creating  func()
}

func (w *physicsGeometryTestWorld) handle() p.Handle {
	w.next++
	return p.Handle{World: 9, Index: w.next, Generation: 1}
}
func (w *physicsGeometryTestWorld) CreateShape(d p.ShapeDesc) (p.ShapeHandle, error) {
	h := p.ShapeHandle(w.handle())
	w.shapes[h] = d
	return h, nil
}
func (w *physicsGeometryTestWorld) DestroyShape(h p.ShapeHandle) error {
	if _, ok := w.shapes[h]; !ok {
		return p.ErrStale
	}
	delete(w.shapes, h)
	return nil
}
func (w *physicsGeometryTestWorld) CreateBody(d p.BodyDesc) (p.BodyHandle, error) {
	if w.creating != nil {
		w.creating()
	}
	h := p.BodyHandle(w.handle())
	w.bodies[h] = p.BodyState{Handle: h, ID: d.ID, Owner: d.Owner, Mode: d.Mode, Pose: d.Pose, Anchor: d.Pose.Compose(d.Anchor), Enabled: d.Enabled, Awake: d.Awake, Mass: d.Mass}
	return h, nil
}
func (w *physicsGeometryTestWorld) CreateCollider(d p.ColliderDesc) (p.ColliderHandle, error) {
	h := p.ColliderHandle(w.handle())
	w.colliders[h] = d
	return h, nil
}
func (w *physicsGeometryTestWorld) DestroyBody(h p.BodyHandle, reason p.CleanupReason) error {
	for ch, c := range w.colliders {
		if c.Body == h {
			delete(w.colliders, ch)
		}
	}
	return w.physicsTestWorld.DestroyBody(h, reason)
}
func (*physicsGeometryTestWorld) DestroyJoint(p.JointHandle) error { return nil }

func physicsGeometryFixture(t *testing.T) (*Server, *physicsGeometryTestWorld, *regionalTestBackend) {
	t.Helper()
	s := NewServer(ServerConfig{})
	w := &physicsGeometryTestWorld{physicsTestWorld: &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{}}, shapes: map[p.ShapeHandle]p.ShapeDesc{}, colliders: map[p.ColliderHandle]p.ColliderDesc{}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	backend := &regionalTestBackend{navigationTestBackend: &navigationTestBackend{revision: 1}, token: 1}
	if err := s.SetSurfaceNavigation(backend, NavigationConfig{BuildDebounce: time.Millisecond, BuildMaxWait: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx, s.life = ctx, lifecycleRunning
	s.navigation.start(ctx)
	t.Cleanup(func() { cancel(); s.navigation.stop(); s.physics.stop() })
	return s, w, backend
}

func physicsGeometrySource(t *testing.T, x float32) *content.Prepared {
	t.Helper()
	file, err := os.Open("../golem-unity/Tests/Fixtures/physics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	source, _, err := content.Decode(file, content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	source.Bodies[0].Position.X = x
	prepared, err := content.Prepare(source, content.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func awaitPhysicsGeometry(t *testing.T, s *Server, key string) NavigationBuildState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.navigation.prepare()
		state, ok := s.NavigationBuildStatus("physics/" + key)
		if ok && !state.Pending {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("physics geometry build did not finish")
	return NavigationBuildState{}
}

func geometryRequest(source *content.Prepared, revision uint64, backend *regionalTestBackend, commit func() error) PhysicsGeometryRequest {
	fingerprint := "unloaded"
	if source != nil {
		fingerprint = source.Fingerprint()
	}
	identity := PhysicsGeometryIdentity{Key: "cell", PhysicsFingerprint: fingerprint, NavigationFingerprint: "nav", Revision: revision}
	return PhysicsGeometryRequest{Identity: identity, Profiles: []surface.ProfileID{"human"}, Bounds: NavigationTraversalBounds{Min: surface.Vec3{X: 0, Y: 0, Z: -1}, Max: surface.Vec3{X: 2, Y: 4, Z: 1}},
		Quarantine: func() error { backend.edit(true, false); return nil },
		Prepare: func(context.Context) (PreparedPhysicsGeometry, error) {
			return PreparedPhysicsGeometry{Identity: identity, Source: source, ResolveOwner: func(string) (int64, error) { return 70, nil }, Navigation: &physicsCheckpointNavigation{commit: commit}}, nil
		}}
}

func TestPhysicsGeometryQueuePreservesUnrelatedBodiesAcrossReplaceAndUnload(t *testing.T) {
	s, w, backend := physicsGeometryFixture(t)
	d := p.DefaultBody(p.FreeDynamic)
	d.ID = "unrelated"
	d.Pose.Position.X = -10
	unrelated, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	w.creating = func() {
		if !backend.dirty {
			t.Error("physical geometry activated before quarantine")
		}
	}
	first := physicsGeometrySource(t, 1)
	request := geometryRequest(first, 1, backend, func() error {
		if len(w.bodies) != 2 {
			t.Error("navigation published before candidate physics")
		}
		backend.edit(false, true)
		return nil
	})
	if _, err = s.QueuePhysicsGeometry(request); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); !state.Committed || state.Err != nil {
		t.Fatalf("publication=%+v", state)
	}
	old, _ := s.PhysicsGeometry("cell")
	oldBody, _ := old.Body("actor")
	second := physicsGeometrySource(t, 1.2)
	request = geometryRequest(second, 2, backend, func() error {
		if len(w.bodies) != 3 {
			t.Error("previous geometry was removed before navigation publication")
		}
		backend.edit(false, true)
		return nil
	})
	if _, err = s.QueuePhysicsGeometry(request); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); !state.Committed {
		t.Fatalf("replacement=%+v", state)
	}
	if _, err = w.Body(oldBody); !errors.Is(err, p.ErrStale) {
		t.Fatal("old source body survived replacement")
	}
	if _, err = w.Body(unrelated); err != nil || len(w.bodies) != 2 {
		t.Fatal("replacement disturbed unrelated body")
	}
	request = geometryRequest(nil, 3, backend, func() error { backend.edit(false, true); return nil })
	if _, err = s.QueuePhysicsGeometry(request); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); !state.Committed {
		t.Fatalf("unload=%+v", state)
	}
	if _, exists := s.PhysicsGeometry("cell"); exists || len(w.bodies) != 1 || len(w.shapes) != 0 {
		t.Fatal("unload leaked source resources")
	}
}

func TestPhysicsGeometryNavigationFailureRollsBackCandidate(t *testing.T) {
	s, w, backend := physicsGeometryFixture(t)
	source := physicsGeometrySource(t, 1)
	initial := geometryRequest(source, 1, backend, func() error { backend.edit(false, true); return nil })
	if _, err := s.QueuePhysicsGeometry(initial); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); !state.Committed {
		t.Fatal(state.Err)
	}
	old, _ := s.PhysicsGeometry("cell")
	oldBody, _ := old.Body("actor")
	failure := errors.New("navigation commit failed")
	replacement := geometryRequest(physicsGeometrySource(t, 1.2), 2, backend, func() error { return failure })
	if _, err := s.QueuePhysicsGeometry(replacement); err != nil {
		t.Fatal(err)
	}
	state := awaitPhysicsGeometry(t, s, "cell")
	if !errors.Is(state.Err, failure) || state.Committed {
		t.Fatalf("state=%+v", state)
	}
	if live, _ := s.PhysicsGeometry("cell"); live != old || len(w.bodies) != 1 || len(w.shapes) != 1 {
		t.Fatal("failed replacement changed ownership or leaked candidate")
	}
	if _, err := w.Body(oldBody); err != nil {
		t.Fatal("failed replacement removed previous body")
	}
	if !backend.dirty {
		t.Fatal("failed replacement cleared quarantine")
	}
}

func TestPhysicsGeometryRejectsMissingQuarantineAndWrongBounds(t *testing.T) {
	s, w, backend := physicsGeometryFixture(t)
	source := physicsGeometrySource(t, 1)
	request := geometryRequest(source, 1, backend, nil)
	request.Quarantine = func() error { return nil }
	if _, err := s.QueuePhysicsGeometry(request); err == nil {
		t.Fatal("missing quarantine accepted")
	}
	request = geometryRequest(source, 2, backend, nil)
	request.Bounds.Min.X = .9
	request.Bounds.Max.X = 1.1
	if _, err := s.QueuePhysicsGeometry(request); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); state.Err == nil || state.Committed {
		t.Fatal("incomplete physical volume was published")
	}
	if len(w.bodies) != 0 || len(w.shapes) != 0 {
		t.Fatal("bounds rejection mutated physics")
	}
}

func TestPhysicsGeometryReplacementChecksCurrentDynamicBounds(t *testing.T) {
	s, w, backend := physicsGeometryFixture(t)
	source := physicsGeometrySource(t, 1)
	if _, err := s.QueuePhysicsGeometry(geometryRequest(source, 1, backend, func() error { backend.edit(false, true); return nil })); err != nil {
		t.Fatal(err)
	}
	if state := awaitPhysicsGeometry(t, s, "cell"); !state.Committed {
		t.Fatal(state.Err)
	}
	old, _ := s.PhysicsGeometry("cell")
	h, _ := old.Body("actor")
	state := w.bodies[h]
	state.Pose.Position.X = 100
	state.Anchor.Position.X = 100
	w.bodies[h] = state
	if _, err := s.QueuePhysicsGeometry(geometryRequest(source, 2, backend, nil)); err != nil {
		t.Fatal(err)
	}
	if status := awaitPhysicsGeometry(t, s, "cell"); status.Committed || status.Err == nil {
		t.Fatal("replacement ignored the moved body's actual volume")
	}
	if live, _ := s.PhysicsGeometry("cell"); live != old || len(w.bodies) != 1 || len(w.shapes) != 1 {
		t.Fatal("bounds rejection changed the accepted lease")
	}
}
