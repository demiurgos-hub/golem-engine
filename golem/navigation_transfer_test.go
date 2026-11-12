package golem

import (
	"context"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

type regionalTransferQuery struct{ *regionalTestQuery }

func (q *regionalTransferQuery) TransferPath(_ context.Context, route surface.Route, location surface.Location, position surface.Vec3, tolerance float64, _ surface.Filter) surface.RebaseResult {
	q.owner.mu.Lock()
	defer q.owner.mu.Unlock()
	q.owner.transfers++
	if q.owner.changedSupport || navDistance(location.Position(), position) > tolerance {
		return surface.RebaseResult{Status: surface.Status{Code: surface.Stale}}
	}
	status := surface.Status{Code: surface.Complete}
	if q.owner.rebaseStatus != nil {
		status = *q.owner.rebaseStatus
	}
	return surface.RebaseResult{Status: status, Location: progressLocation{navigationTestLocation{position, q.view.revision}, 1}, Route: navigationTestRoute{route.Corners()}}
}

func seedTransferAgent(t *testing.T) (*Server, *regionalTestBackend, *navigationAgent) {
	t.Helper()
	s, b := regionalServer(t)
	b.transferEnabled = true
	from, to := surface.Vec3{X: 5, Z: 20}, surface.Vec3{X: 20, Z: 20}
	a := seedRegionalAgent(t, s, 1, from, to)
	a.location = progressLocation{navigationTestLocation{from, 1}, 1}
	a.repairLocation = a.location
	a.route = navigationTestRoute{[]surface.Corner{{Position: surface.Vec3{Z: 20}}, {Position: surface.Vec3{X: 4, Z: 20}}, {Position: to}}}
	a.corners, a.next = a.route.Corners(), 2
	a.reconciledPosition, a.hasReconciledPosition = from, true
	a.state.Velocity, a.speed = surface.Vec3{X: 2}, 2
	return s, b, a
}

func TestNavigationUnchangedTransferKeepsForwardCursorAndOwnedHandles(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			s, b, a := seedTransferAgent(t)
			if partial {
				status := surface.Status{Code: surface.Partial, Reason: surface.Unreachable}
				b.rebaseStatus, a.config.AllowPartial = &status, true
			}
			oldView, oldQuery, oldRoute := a.view.(*regionalTestView), a.query, a.route
			firstCorner, destination, velocity := &a.corners[0], a.state.Destination, a.state.Velocity
			b.edit(false, true)
			if !s.navigation.rebaseAgent(a, a.location.Position()) {
				t.Fatal("unchanged transfer failed")
			}
			if b.transfers != 1 || b.repairs != 0 || a.next != 2 || firstCorner != &a.corners[0] {
				t.Fatalf("transfer rebuilt or rewound corridor: transfers=%d repairs=%d next=%d", b.transfers, b.repairs, a.next)
			}
			if a.view.Revision() != 2 || a.query == oldQuery || a.location.(progressLocation).revision != 2 || !oldView.released.Load() || oldRoute.Corners()[0] != a.route.Corners()[0] {
				t.Fatal("transfer did not replace all handles before releasing old view")
			}
			if a.state.Destination != destination || a.state.Velocity != velocity || a.speed != 2 || partial && a.state.Status != *b.rebaseStatus {
				t.Fatalf("transfer changed intent, velocity or partial status: %+v", a.state)
			}
			s.navigation.advanceAgent(a, .05)
			position, _, _ := s.navigation.position(a.id)
			if position.X <= 5 {
				t.Fatalf("transferred cursor moved backwards: %v", position)
			}
		})
	}
}

func TestNavigationTransferRequiresReconciledUndeviatedSupport(t *testing.T) {
	for _, reason := range []string{"lateral-deviation", "external-displacement", "polygon-change", "unreconciled", "initial-projection", "unsupported-backend"} {
		t.Run(reason, func(t *testing.T) {
			s, b, a := seedTransferAgent(t)
			position := a.location.Position()
			switch reason {
			case "lateral-deviation":
				a.corridorDeviated = true
			case "external-displacement":
				position.Z += .001
			case "polygon-change":
				a.repairLocation = progressLocation{navigationTestLocation{position, 1}, 0}
			case "unreconciled":
				a.hasReconciledPosition = false
			case "initial-projection":
				a.initialProjection = true
			case "unsupported-backend":
				b.transferEnabled = false
			}
			b.edit(false, true)
			if !s.navigation.rebaseAgent(a, position) || b.transfers != 0 || b.repairs != 1 || a.next != 0 {
				t.Fatalf("pending repair was hidden: transfers=%d repairs=%d next=%d", b.transfers, b.repairs, a.next)
			}
		})
	}
}

func TestNavigationRejectedTransferKeepsOldLeaseAndCursor(t *testing.T) {
	s, b, a := seedTransferAgent(t)
	old := a.view.(*regionalTestView)
	b.changedSupport = true
	b.edit(false, true)
	if s.navigation.rebaseAgent(a, a.location.Position()) || b.transfers != 1 || b.repairs != 0 || a.view != old || old.released.Load() || a.next != 2 || b.releases.Load() != 1 {
		t.Fatal("rejected transfer changed current handles or leaked candidate lease")
	}
}
