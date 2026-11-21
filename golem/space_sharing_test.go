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
