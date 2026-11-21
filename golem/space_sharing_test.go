package golem

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// expectSharedPanic runs fn and requires the panic of a service that already
// serves space owner.
func expectSharedPanic(t *testing.T, name string, owner SpaceID, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		msg, _ := r.(string)
		if r == nil || !strings.Contains(msg, fmt.Sprintf("already serves space %d;", owner)) {
			t.Fatalf("%s: panic = %v", name, r)
		}
	}()
	fn()
}

func TestSpaceSettersRejectServicesOfAnotherSpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	b2, c3 := newRecordingBackend2D(), newRecordingBackend3D()
	l2, l3 := NewCollisionLayers().Bind(b2).Define("Player"), NewCollisionLayers3D().Bind(c3).Define("Player")
	grid := &navAgentBackend{reachable: true}

	// Space 0 through the Server setters, then again through both paths.
	for i := 0; i < 2; i++ {
		s.SetCollisionBackend(b2)
		s.SetCollision3DBackend(c3)
		s.SetCollisionLayers(l2)
		s.SetCollisionLayers3D(l3)
		s.SetNavBackend(grid)
		s.Space(0).SetCollisionBackend(b2)
		s.Space(0).SetCollision3DBackend(c3)
		s.Space(0).SetCollisionLayers(l2)
		s.Space(0).SetCollisionLayers3D(l3)
		s.Space(0).SetNavBackend(grid)
	}
	sp := s.Space(2)
	expectSharedPanic(t, "collision backend", 0, func() { sp.SetCollisionBackend(b2) })
	expectSharedPanic(t, "3D collision backend", 0, func() { sp.SetCollision3DBackend(c3) })
	expectSharedPanic(t, "collision layers", 0, func() { sp.SetCollisionLayers(l2) })
	expectSharedPanic(t, "3D collision layers", 0, func() { sp.SetCollisionLayers3D(l3) })
	expectSharedPanic(t, "nav backend", 0, func() { sp.SetNavBackend(grid) })
	if rt := s.runtime(2); rt != nil {
		t.Fatal("rejected setters created a runtime for space 2")
	}

	// Space 2's own services, re-installed into space 2, then refused by space 0.
	own2, own3 := newRecordingBackend2D(), newRecordingBackend3D()
	ownL2, ownL3 := NewCollisionLayers().Bind(own2).Define("Player"), NewCollisionLayers3D().Bind(own3).Define("Player")
	ownGrid := &navAgentBackend{reachable: true}
	for i := 0; i < 2; i++ {
		sp.SetCollisionBackend(own2)
		sp.SetCollision3DBackend(own3)
		sp.SetCollisionLayers(ownL2)
		sp.SetCollisionLayers3D(ownL3)
		sp.SetNavBackend(ownGrid)
	}
	expectSharedPanic(t, "Server collision backend", 2, func() { s.SetCollisionBackend(own2) })
	expectSharedPanic(t, "Server 3D collision backend", 2, func() { s.SetCollision3DBackend(own3) })
	expectSharedPanic(t, "Server collision layers", 2, func() { s.SetCollisionLayers(ownL2) })
	expectSharedPanic(t, "Server 3D collision layers", 2, func() { s.SetCollisionLayers3D(ownL3) })
	expectSharedPanic(t, "Server nav backend", 2, func() { s.SetNavBackend(ownGrid) })
	expectSharedPanic(t, "space 5 nav backend", 2, func() { s.Space(5).SetNavBackend(ownGrid) })
	if s.space0.collision != b2 || s.space0.layers != l2 || s.space0.navBackend != grid {
		t.Fatal("a rejected setter replaced space 0's services")
	}

	// Clearing a space releases its services for another space.
	sp.SetNavBackend(nil)
	s.Space(5).SetNavBackend(ownGrid)
	if s.runtime(5).navBackend != ownGrid {
		t.Fatal("released nav backend could not move to space 5")
	}
}

func TestSpaceSettersRejectSharedPhysicsAndSurfaceNavigation(t *testing.T) {
	s := NewServer(ServerConfig{})
	world := &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{}}
	if err := s.SetPhysicsWorld(world); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(4).SetPhysicsWorld(world); err == nil || !strings.Contains(err.Error(), "already serves space 0") {
		t.Fatalf("shared physics world = %v", err)
	}
	backend := &navigationTestBackend{revision: 1}
	if err := s.Space(4).SetSurfaceNavigation(backend, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(backend, NavigationConfig{}); err == nil || !strings.Contains(err.Error(), "already serves space 4") {
		t.Fatalf("shared surface navigation backend = %v", err)
	}
	if s.navigation != nil || s.runtime(4).physics != nil {
		t.Fatal("rejected setters installed a service")
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
}

// expectPanicContains runs fn and requires a string panic containing want.
func expectPanicContains(t *testing.T, name, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		msg, _ := r.(string)
		if r == nil || !strings.Contains(msg, want) {
			t.Fatalf("%s: panic = %v, want %q", name, r, want)
		}
	}()
	fn()
}

// TestSpaceSettersRejectHelpersSharingABackend covers sharing through a layer
// helper's backend: a second helper bound to one backend, a helper bound to
// another space's backend, and a backend another space's helper is bound to.
func TestSpaceSettersRejectHelpersSharingABackend(t *testing.T) {
	s := NewServer(ServerConfig{})
	b0, c0 := newRecordingBackend2D(), newRecordingBackend3D()
	s.SetCollisionBackend(b0)
	s.SetCollision3DBackend(c0)
	s.SetCollisionLayers(NewCollisionLayers().Bind(b0).Define("Player"))
	s.SetCollisionLayers3D(NewCollisionLayers3D().Bind(c0).Define("Player"))

	// A different helper bound to space 0's backend.
	sp := s.Space(2)
	expectSharedPanic(t, "helper on space 0 backend", 0, func() { sp.SetCollisionLayers(NewCollisionLayers().Bind(b0).Define("Player")) })
	expectSharedPanic(t, "3D helper on space 0 backend", 0, func() { sp.SetCollisionLayers3D(NewCollisionLayers3D().Bind(c0).Define("Player")) })
	if s.runtime(2) != nil {
		t.Fatal("rejected helpers created a runtime for space 2")
	}

	// Space 3 has helpers bound to backends it never installed.
	bx, cx := newRecordingBackend2D(), newRecordingBackend3D()
	s.Space(3).SetCollisionLayers(NewCollisionLayers().Bind(bx).Define("Player"))
	s.Space(3).SetCollisionLayers3D(NewCollisionLayers3D().Bind(cx).Define("Player"))
	const helperOf3 = "is bound to the collision layers of space 3;"
	expectPanicContains(t, "two helpers on one backend", helperOf3, func() { sp.SetCollisionLayers(NewCollisionLayers().Bind(bx).Define("Player")) })
	expectPanicContains(t, "two 3D helpers on one backend", helperOf3, func() { sp.SetCollisionLayers3D(NewCollisionLayers3D().Bind(cx).Define("Player")) })
	expectPanicContains(t, "backend of space 3 helper", helperOf3, func() { sp.SetCollisionBackend(bx) })
	expectPanicContains(t, "3D backend of space 3 helper", helperOf3, func() { sp.SetCollision3DBackend(cx) })
	expectPanicContains(t, "Server backend of space 3 helper", helperOf3, func() { s.SetCollisionBackend(bx) })
	expectPanicContains(t, "Server 3D backend of space 3 helper", helperOf3, func() { s.SetCollision3DBackend(cx) })
	if s.runtime(2) != nil || s.space0.collision != b0 || s.space0.collision3D != c0 {
		t.Fatal("a rejected setter installed something")
	}

	// Space 3 may install its helpers' backends and reinstall everything.
	for i := 0; i < 2; i++ {
		s.Space(3).SetCollisionBackend(bx)
		s.Space(3).SetCollision3DBackend(cx)
		s.Space(3).SetCollisionLayers(s.runtime(3).layers)
		s.Space(3).SetCollisionLayers3D(s.runtime(3).layers3D)
	}
}

func TestCollisionSettersTreatTypedNilBackendsAsNil(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.SetCollisionBackend((*recordingBackend2D)(nil))
	s.SetCollision3DBackend((*recordingBackend3D)(nil))
	if s.space0.collision != nil || s.space0.collision3D != nil {
		t.Fatalf("typed nil installed: %v %v", s.space0.collision, s.space0.collision3D)
	}
	s.Space(4).SetCollisionBackend((*recordingBackend2D)(nil))
	s.Space(4).SetCollision3DBackend((*recordingBackend3D)(nil))
	if s.runtime(4) != nil {
		t.Fatal("typed-nil backends created a runtime for space 4")
	}
	expectPanicContains(t, "MustCollisionBackend", "backend must be non-nil", func() { MustCollisionBackend((*recordingBackend2D)(nil)) })
	expectPanicContains(t, "MustCollisionBackend3D", "backend must be non-nil", func() { MustCollisionBackend3D((*recordingBackend3D)(nil)) })
}

// TestSpaceTransferRejectsHelperReboundToForeignBackend rebinds a space's
// helper to another space's backend after installation; a move into that space
// is rejected before anything changes.
func TestSpaceTransferRejectsHelperReboundToForeignBackend(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	createIn(t, s, 0, newSpaceCollider(1))
	s.runtime(3).layers.Bind(b0)
	err := s.SetEntitySpace(1, 3)
	if err == nil || !strings.Contains(err.Error(), "space 3 collision layers are bound to a backend other than the space's collision backend") {
		t.Fatalf("move = %v", err)
	}
	if space, _ := s.EntitySpace(1); space != 0 || len(b3.updated) != 0 {
		t.Fatalf("entity in space %d, space 3 backend updated %v", space, b3.updated)
	}
	// A space without its own backend still refuses another space's backend.
	s.Space(3).SetCollisionBackend(nil)
	err = s.SetEntitySpace(1, 3)
	if err == nil || !strings.Contains(err.Error(), "bound to the collision backend of space 0") {
		t.Fatalf("move without own backend = %v", err)
	}
}

// TestCreateEntityRejectsHelperReboundToForeignBackend rebinds a space's helper
// after installation: CreateEntity panics before registering any shape, like
// an unbound helper.
func TestCreateEntityRejectsHelperReboundToForeignBackend(t *testing.T) {
	s, b0, b3 := twoSpaceCollisionServer(t)
	s.runtime(3).layers.Bind(b0)
	expectPanicContains(t, "foreign backend", "space 3 collision layers are bound to a backend other than the space's collision backend", func() {
		_ = s.CreateEntityIn(3, newSpaceCollider(1))
	})
	if _, ok := b0.shapes[1]; ok || len(b3.shapes) != 0 {
		t.Fatalf("a shape was registered: space 0 %v, space 3 %v", b0.shapes, b3.shapes)
	}
	// Without its own backend, the space still refuses another space's backend.
	s.Space(3).SetCollisionBackend(nil)
	expectPanicContains(t, "other space's backend", "space 3 collision layers are bound to the collision backend of space 0", func() {
		_ = s.CreateEntityIn(3, newSpaceCollider(2))
	})
	// Its own correctly bound helper registers normally.
	s.Space(3).SetCollisionBackend(b3)
	s.runtime(3).layers.Bind(b3)
	createIn(t, s, 3, newSpaceCollider(3))
	if _, ok := b3.shapes[3]; !ok {
		t.Fatal("collider not registered on the space's own backend")
	}
}
