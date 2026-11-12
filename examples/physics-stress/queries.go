package main

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

type projectile struct {
	position, velocity p.Vec3
	generation         uint64
}

func (s *scene) resetProjectile(i int, generation uint64, half float32) {
	s.projectiles[i] = projectile{position: p.Vec3{X: -half*.8 + float32((i*17+int(generation)%97)%1000)/1000*half*1.4, Y: 5 + float32(i%7), Z: -half*.8 + float32((i*31)%1000)/1000*half*1.6}, velocity: p.Vec3{X: 15, Y: -8, Z: 3}, generation: generation}
}

func (s *scene) sweepProjectiles(dt, half float32) (int, int, error) {
	var dst [1]p.Hit
	hits := 0
	for i := range s.projectiles {
		a := &s.projectiles[i]
		delta := a.velocity.Mul(dt)
		pose := p.Identity()
		pose.Position = a.position
		result, e := s.world.Query(p.Query{Kind: p.Sweep, Mode: p.Nearest, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .08}, Pose: pose, Translation: delta, Filter: p.QueryFilter{Mask: ^uint64(0)}, Initial: p.BlockInitial}, dst[:])
		if e != nil {
			return i, hits, e
		}
		if result.Count > 0 || a.position.Y < -.1 || !a.position.InBounds() {
			hits++
			s.resetProjectile(i, a.generation+1, half)
		} else {
			a.position = a.position.Add(delta)
			a.velocity.Y -= 9.81 * dt
		}
	}
	return len(s.projectiles), hits, nil
}

func (s *scene) gameplayQueries() (int, error) {
	if len(s.queryTasks) == 0 {
		s.queryTasks = make([]p.QueryTask, s.counts.Queries)
		s.queryOutcomes = make([]p.QueryOutcome, s.counts.Queries)
		s.queryHits = make([]p.Hit, s.counts.Queries*32)
	}
	for i := 0; i < s.counts.Queries; i++ {
		center := s.boxCenters[(i*7919)%len(s.boxCenters)]
		pose := p.Identity()
		pose.Position = center
		q := p.Query{Mode: p.Nearest, Pose: pose, Filter: p.QueryFilter{Mask: ^uint64(0)}, Initial: p.ReportInitial}
		switch i % 4 {
		case 0:
			q.Kind = p.Ray
			q.Pose.Position.Y = 10
			q.Translation = p.Vec3{Y: -12}
		case 1:
			q.Kind = p.Overlap
			q.Mode = p.All
			q.Shape = p.ShapeDesc{Kind: p.Sphere, Radius: .25}
		case 2:
			q.Kind = p.Sweep
			q.Shape = p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .2, Z: .2}}
			q.Pose.Position.Y = 2
			q.Translation = p.Vec3{Y: -3}
		case 3:
			q.Kind = p.Sweep
			q.Shape = p.ShapeDesc{Kind: p.Capsule, Radius: .2, PointB: p.Vec3{Y: .4}}
			q.Pose.Position.Y = 2
			q.Translation = p.Vec3{Y: -3}
		}
		s.queryTasks[i] = p.QueryTask{Query: q, Hits: s.queryHits[i*32 : (i+1)*32]}
	}
	if err := s.world.QueryBatch(s.queryTasks, s.queryOutcomes, s.queryWorkers); err != nil {
		return 0, err
	}
	for i, outcome := range s.queryOutcomes {
		if outcome.Err != nil {
			return i, outcome.Err
		}
		if outcome.Result.Incomplete {
			return i, fmt.Errorf("gameplay query %d overflow: %d hits, %d slots", i, outcome.Result.Total, 32)
		}
	}
	return s.counts.Queries, nil
}
