package golem

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav"
)

type navAgentMover struct{ x, y float32 }

func (m *navAgentMover) Position() (float32, float32) { return m.x, m.y }
func (m *navAgentMover) SetPosition(x, y float32)     { m.x, m.y = x, y }

type navAgentBackend struct{ reachable bool }

func (b *navAgentBackend) FindPath(x, y, tx, ty float64) ([]nav.Point, bool) {
	if !b.reachable {
		return nil, false
	}
	return []nav.Point{{X: x, Y: y}, {X: tx, Y: ty}}, true
}

func TestNavAgentFailedDestinationStopsOldRoute(t *testing.T) {
	for _, failure := range []string{"unreachable", "disabled", "nil-server", "unbound"} {
		t.Run(failure, func(t *testing.T) {
			s := NewServer(ServerConfig{})
			backend := &navAgentBackend{reachable: true}
			s.SetNavBackend(backend)
			a := NavAgent{Speed: 2}
			a.Bind(&navAgentMover{})
			if !a.SetDestination(s, 10, 0) {
				t.Fatal("initial destination failed")
			}
			x, y := a.Step(1, 0, 0)
			switch failure {
			case "unreachable":
				backend.reachable = false
			case "disabled":
				s.SetNavBackend(nil)
			case "nil-server":
				s = nil
			case "unbound":
				a.Bind(nil)
			}
			if a.SetDestination(s, 100, 0) || a.HasPath() {
				t.Fatal("failed replacement retained a route")
			}
			if vx, vy := a.Velocity(); vx != 0 || vy != 0 {
				t.Fatalf("failed replacement retained velocity (%g,%g)", vx, vy)
			}
			if nx, ny := a.Step(1, x, y); nx != x || ny != y {
				t.Fatalf("failed replacement moved (%g,%g) to (%g,%g)", x, y, nx, ny)
			}
		})
	}
}

// navAgentEntity is a nav mover that is also a server entity.
type navAgentEntity struct {
	stubEntity
	x, y float32
}

func (e *navAgentEntity) Position() (float32, float32) { return e.x, e.y }
func (e *navAgentEntity) SetPosition(x, y float32)     { e.x, e.y = x, y }

func TestNavAgentUsesItsEntitySpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.SetNavBackend(&navAgentBackend{reachable: false})
	s.Space(3).SetNavBackend(&navAgentBackend{reachable: true})

	inCave := &navAgentEntity{stubEntity: stubEntity{id: 1}}
	if err := s.CreateEntityIn(3, inCave); err != nil {
		t.Fatal(err)
	}
	a := NavAgent{Speed: 1}
	a.Bind(inCave)
	if !a.SetDestination(s, 5, 0) {
		t.Fatal("space 3 entity did not path on space 3's backend")
	}

	outside := &navAgentEntity{stubEntity: stubEntity{id: 2}}
	if err := s.CreateEntity(outside); err != nil {
		t.Fatal(err)
	}
	a.Bind(outside)
	if a.SetDestination(s, 5, 0) {
		t.Fatal("space 0 entity used another space's backend")
	}

	lost := &navAgentEntity{stubEntity: stubEntity{id: 3}}
	if err := s.CreateEntityIn(6, lost); err != nil {
		t.Fatal(err)
	}
	a.Bind(lost)
	if a.SetDestination(s, 5, 0) {
		t.Fatal("entity in a space without nav backend found a path")
	}

	// Moving the entity changes the backend it paths on.
	if err := s.SetEntitySpace(2, 3); err != nil {
		t.Fatal(err)
	}
	a.Bind(outside)
	if !a.SetDestination(s, 5, 0) {
		t.Fatal("moved entity did not path in its new space")
	}
}

type navWalkableBackend struct {
	navAgentBackend
	blocked map[[2]float64]bool
}

func (b *navWalkableBackend) SetWalkable(x, y float64, walkable bool) {
	b.blocked[[2]float64{x, y}] = !walkable
}

func TestSpaceNavigationAPIsAreIsolated(t *testing.T) {
	s := NewServer(ServerConfig{})
	b0 := &navWalkableBackend{navAgentBackend{reachable: true}, map[[2]float64]bool{}}
	b3 := &navWalkableBackend{navAgentBackend{reachable: true}, map[[2]float64]bool{}}
	s.SetNavBackend(b0)
	s.Space(3).SetNavBackend(b3)
	s.Space(3).SetNavWalkable(1, 2, false)
	if !b3.blocked[[2]float64{1, 2}] || len(b0.blocked) != 0 {
		t.Fatal("Space(3).SetNavWalkable reached the wrong backend")
	}
	s.SetNavWalkable(4, 4, false)
	if !b0.blocked[[2]float64{4, 4}] || len(b3.blocked) != 1 {
		t.Fatal("Server.SetNavWalkable must act on space 0")
	}
	b0.reachable = false
	if _, ok := s.Space(3).FindPath(0, 0, 1, 1); !ok {
		t.Fatal("Space(3).FindPath failed")
	}
	if _, ok := s.FindPath(0, 0, 1, 1); ok {
		t.Fatal("Server.FindPath must use space 0's backend")
	}
	if _, ok := s.Space(0).FindPath(0, 0, 1, 1); ok {
		t.Fatal("Space(0).FindPath must match Server.FindPath")
	}
}
