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

// TestNavAgentDropsPathAfterSpaceMove covers a path planned in one space and
// an entity moved to another before the next Tick, including a move back to
// space 0 that leaves no entity outside it.
func TestNavAgentDropsPathAfterSpaceMove(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to SpaceID
	}{{"space 3 to 0", 3, 0}, {"space 0 to 3", 0, 3}, {"space 3 to 4", 3, 4}} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(ServerConfig{})
			for _, space := range []SpaceID{0, 3, 4} {
				s.Space(space).SetNavBackend(&navAgentBackend{reachable: true})
			}
			e := &navAgentEntity{stubEntity: stubEntity{id: 1}}
			if err := s.CreateEntityIn(tc.from, e); err != nil {
				t.Fatal(err)
			}
			a := NavAgent{Speed: 1}
			a.Bind(e)
			if !a.SetDestination(s, 10, 0) {
				t.Fatal("destination failed")
			}
			a.Tick(1)
			if e.x != 1 || !a.HasPath() {
				t.Fatalf("agent did not follow its path before the move: x=%g", e.x)
			}
			if err := s.SetEntitySpace(1, tc.to); err != nil {
				t.Fatal(err)
			}
			a.Tick(1)
			if e.x != 1 || a.HasPath() {
				t.Fatalf("agent kept following its old space's path: x=%g, path %v", e.x, a.HasPath())
			}
			if vx, vy := a.Velocity(); vx != 0 || vy != 0 {
				t.Fatalf("moved agent kept velocity (%g,%g)", vx, vy)
			}
			if _, ok := a.NextWaypoint(); ok || a.RemainingDistance(1, 0) != 0 {
				t.Fatal("moved agent still reports its old path")
			}
			if !a.SetDestination(s, 10, 0) {
				t.Fatal("destination in the new space failed")
			}
			a.Tick(1)
			if e.x != 2 {
				t.Fatalf("agent did not follow its new space's path: x=%g", e.x)
			}
		})
	}
}

func TestNavAgentDropsPathAfterSpaceMoveDetectedByHasPath(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.SetNavBackend(&navAgentBackend{reachable: true})
	e := &navAgentEntity{stubEntity: stubEntity{id: 1}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	a := NavAgent{Speed: 1}
	a.Bind(e)
	if !a.SetDestination(s, 10, 0) {
		t.Fatal("destination failed")
	}
	if err := s.SetEntitySpace(1, 2); err != nil {
		t.Fatal(err)
	}
	if a.HasPath() {
		t.Fatal("HasPath reported the old space's path")
	}
}

func TestNavAgentSingleSpaceFollowsPathToGoal(t *testing.T) {
	s := NewServer(ServerConfig{})
	s.SetNavBackend(&navAgentBackend{reachable: true})
	e := &navAgentEntity{stubEntity: stubEntity{id: 1}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	a := NavAgent{Speed: 2}
	a.Bind(e)
	if !a.SetDestination(s, 5, 0) {
		t.Fatal("destination failed")
	}
	for i := 0; i < 2; i++ {
		a.Tick(1)
		if !a.HasPath() {
			t.Fatalf("path dropped early at tick %d", i)
		}
	}
	a.Tick(1)
	if e.x != 5 || a.HasPath() {
		t.Fatalf("agent ended at x=%g with path %v", e.x, a.HasPath())
	}
}
