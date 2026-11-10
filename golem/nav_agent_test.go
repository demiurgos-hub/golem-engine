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
