package golem

import (
	"context"
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

type progressLocation struct {
	navigationTestLocation
	polygon int
}

func (l progressLocation) SameSupport(other surface.Location) bool {
	r, ok := other.(progressLocation)
	return ok && l.revision == r.revision && l.polygon == r.polygon
}

type progressQuery struct {
	navigationTestQuery
	projections, repairs int
}

func (q *progressQuery) Project(ctx context.Context, p surface.Vec3, o surface.ProjectionOptions, f surface.Filter) surface.ProjectionResult {
	q.projections++
	r := q.navigationTestQuery.Project(ctx, p, o, f)
	if r.Location != nil {
		r.Location = progressLocation{r.Location.(navigationTestLocation), int(math.Floor(p.X / 5))}
	}
	return r
}
func (q *progressQuery) Move(ctx context.Context, l surface.Location, p surface.Vec3, f surface.Filter) surface.MoveResult {
	r := q.navigationTestQuery.Move(ctx, l, p, f)
	r.Location = progressLocation{r.Location.(navigationTestLocation), int(math.Floor(p.X / 5))}
	return r
}
func (q *progressQuery) RepairPath(_ context.Context, route surface.Route, location surface.Location, _ surface.Filter) surface.PathResult {
	q.repairs++
	corners := route.Corners()
	return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: location.Position()}, corners[len(corners)-1]}}}
}

func TestNavigationStraightProgressReusesSupportAndRepairsOnDeviation(t *testing.T) {
	s, b := regionalServer(t)
	// This test isolates movement continuity; regional behavior has its own
	// real-adapter tests and the progress locations are a separate backend type.
	s.navigation.backend = b.navigationTestBackend
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: .5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	q := &progressQuery{navigationTestQuery: navigationTestQuery{view: a.view.(*navigationTestView)}}
	a.query = q
	a.location = progressLocation{navigationTestLocation{surface.Vec3{X: .5, Z: 20}, 1}, 0}
	a.repairLocation = a.location
	for i := 0; i < 20; i++ {
		navigationTestTick(s)
	}
	if q.repairs != 0 {
		t.Fatalf("repaired unchanged straight corridor %d times", q.repairs)
	}
	if q.projections != 0 {
		t.Fatalf("body at accepted support was reprojected: %d", q.projections)
	}
	for i := 0; i < 30; i++ {
		navigationTestTick(s)
	}
	if q.repairs != 1 {
		t.Fatalf("polygon crossing repair count=%d", q.repairs)
	}
	// Even a tiny lateral motor correction must rebuild the funnel; small errors
	// cannot accumulate across accepted steps.
	a.config.Motor = regionalMotor(func(m NavigationMotion) (surface.Vec3, error) { m.Target.Z += .005; return m.Target, nil })
	navigationTestTick(s)
	if q.repairs != 2 {
		t.Fatalf("motor deviation did not repair: %d", q.repairs)
	}
	a.config.Motor = nil
	s.tick++
	s.navigation.prepare()
	s.navigation.advance(.05)
	p, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(float32(p.X), float32(p.Y), float32(p.Z+.005))
	s.navigation.reconcile()
	if q.repairs != 3 {
		t.Fatalf("collision deviation did not repair: %d", q.repairs)
	}
}

func TestNavigationSmallSupportChangesRepairImmediately(t *testing.T) {
	for _, crossing := range []bool{false, true} {
		t.Run(map[bool]string{false: "lateral-correction", true: "polygon-crossing"}[crossing], func(t *testing.T) {
			s, b := regionalServer(t)
			s.navigation.backend = b.navigationTestBackend
			from := surface.Vec3{X: 4.999, Z: 20}
			a := seedRegionalAgent(t, s, 1, from, surface.Vec3{X: 20, Z: 20})
			q := &progressQuery{navigationTestQuery: navigationTestQuery{view: a.view.(*navigationTestView)}}
			a.query = q
			a.location = progressLocation{navigationTestLocation{from, 1}, 0}
			a.repairLocation = a.location
			actual := from
			if crossing {
				actual.X += .002
				a.lastPosition = actual // ordinary straight progress crosses a seam
			} else {
				actual.Z += .002 // a tiny physical correction requires a new funnel
			}
			if !s.navigation.reconcileAgent(a, actual) || q.repairs != 1 {
				t.Fatalf("small support change was deferred: repairs=%d state=%+v", q.repairs, a.state)
			}
		})
	}
}
