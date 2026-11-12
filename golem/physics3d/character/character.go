// Package character provides collision-resolved capsule motion without importing
// a server or a solver. Move never advances simulation time or applies impulses.
package character

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"math"
)

type Queries interface {
	Query(p.Query, []p.Hit) (p.QueryResult, error)
}
type Config struct {
	Radius, Height, StepHeight, GroundDistance, MaxSlopeDegrees, Skin, MaxDepenetration float32
	Iterations, MaxHits                                                                 int
}
type Support struct {
	Collider      p.ColliderHandle
	Feature       p.Feature
	Point, Normal p.Vec3
	Distance      float32
	Revision      uint64
}
type Result struct {
	Pose                       p.Pose
	Grounded, Blocked, Stepped bool
	Support                    Support
	Depenetration              p.Vec3
	Hits                       []p.Hit
}

// DefaultConfig uses explicit physical dimensions, independent of navmesh clearance.
func DefaultConfig() Config {
	return Config{Radius: .4, Height: 1.8, StepHeight: .3, GroundDistance: .05, MaxSlopeDegrees: 45, Skin: .001, MaxDepenetration: .5, Iterations: 8, MaxHits: 4096}
}

// Validate checks dimensions and bounded work limits.
func (c Config) Validate() error {
	for _, v := range []float32{c.Radius, c.Height, c.StepHeight, c.GroundDistance, c.MaxSlopeDegrees, c.Skin, c.MaxDepenetration} {
		if !p.Finite(v) || v < 0 {
			return p.ErrInvalid
		}
	}
	if c.Radius == 0 || c.Height < 2*c.Radius || c.MaxSlopeDegrees >= 90 || c.Iterations < 1 || c.Iterations > 64 || c.MaxHits < 1 || c.MaxHits > 1_000_000 {
		return p.ErrInvalid
	}
	return nil
}
func (c Config) shape() p.ShapeDesc {
	half := c.Height/2 - c.Radius
	return p.ShapeDesc{Kind: p.Capsule, Radius: c.Radius, PointA: p.Vec3{Y: -half}, PointB: p.Vec3{Y: half}}
}
func queryAll(world Queries, q p.Query, maxHits int) ([]p.Hit, error) {
	n := min(16, maxHits)
	for {
		dst := make([]p.Hit, n)
		r, e := world.Query(q, dst)
		if e != nil {
			return nil, e
		}
		if !r.Incomplete {
			return dst[:r.Count], nil
		}
		if r.Total > maxHits || n >= maxHits {
			return nil, fmt.Errorf("%w: character query overflow (%d hits)", p.ErrInvalid, r.Total)
		}
		n = min(maxHits, max(n*2, r.Total))
	}
}
func (c Config) query(world Queries, pose p.Pose, translation p.Vec3, filter p.QueryFilter) ([]p.Hit, error) {
	filter.RequireSolid = true
	filter.IncludeNonQuery = true
	filter.Sensors = p.IncludeSensors
	return queryAll(world, p.Query{Kind: p.Sweep, Mode: p.All, Shape: c.shape(), Pose: pose, Translation: translation, Filter: filter, Initial: p.ReportInitial}, c.MaxHits)
}
func firstBlocking(hits []p.Hit, delta p.Vec3, skin float32) (p.Hit, bool) {
	for _, h := range hits {
		if !h.Blocking {
			continue
		}
		if h.Initial && h.Normal.Dot(delta) >= 0 {
			continue
		}
		if h.Initial && h.Depth <= skin && h.Normal.Dot(delta) >= -1e-7 {
			continue
		}
		return h, true
	}
	return p.Hit{}, false
}
func (c Config) ground(world Queries, pose p.Pose, filter p.QueryFilter) (Support, bool, error) {
	hits, e := c.query(world, pose, p.Vec3{Y: -c.GroundDistance}, filter)
	if e != nil {
		return Support{}, false, e
	}
	cos := float32(math.Cos(float64(c.MaxSlopeDegrees) * math.Pi / 180))
	for _, h := range hits {
		if h.Blocking && h.Normal.Y >= cos && (!h.Penetrating || h.Depth <= c.Skin) {
			return Support{Collider: h.Collider, Feature: h.Feature, Point: h.Point, Normal: h.Normal, Distance: h.Distance, Revision: h.Revision}, true, nil
		}
	}
	return Support{}, false, nil
}

// Move sweeps, slides and bounds depenetration. Stepping is an up/forward/down
// action with a validated walkable landing; it never snaps to another floor.
func (c Config) Move(world Queries, start p.Pose, translation p.Vec3, filter p.QueryFilter) (Result, error) {
	r := Result{Pose: start}
	if err := c.Validate(); err != nil {
		return r, err
	}
	if world == nil || !start.Valid() || !translation.Valid() || !start.Position.Add(translation).InBounds() {
		return r, p.ErrInvalid
	}
	for i := 0; i < c.Iterations; i++ {
		hits, e := c.query(world, r.Pose, p.Vec3{}, filter)
		if e != nil {
			return r, e
		}
		var correction p.Vec3
		deepest := float32(0)
		for _, h := range hits {
			if h.Blocking && h.Penetrating && h.Depth > c.Skin && h.Depth > deepest {
				deepest = h.Depth
				correction = h.Normal.Mul(h.Depth + c.Skin)
			}
		}
		if deepest == 0 {
			break
		}
		if correction.Length() == 0 || r.Depenetration.Add(correction).Length() > c.MaxDepenetration {
			return r, fmt.Errorf("%w: depenetration bound", p.ErrInvalid)
		}
		r.Pose.Position = r.Pose.Position.Add(correction)
		r.Depenetration = r.Depenetration.Add(correction)
		if !r.Pose.Valid() {
			return r, p.ErrInvalid
		}
		if i == c.Iterations-1 {
			return r, fmt.Errorf("%w: depenetration did not converge", p.ErrInvalid)
		}
	}
	_, startedGrounded, e := c.ground(world, r.Pose, filter)
	if e != nil {
		return r, e
	}
	remaining := translation
	for i := 0; i < c.Iterations && remaining.Length() > 1e-7; i++ {
		hits, e := c.query(world, r.Pose, remaining, filter)
		if e != nil {
			return r, e
		}
		hit, blocked := firstBlocking(hits, remaining, c.Skin)
		if !blocked {
			r.Pose.Position = r.Pose.Position.Add(remaining)
			remaining = p.Vec3{}
			break
		}
		r.Hits = append(r.Hits, hit)
		r.Blocked = true
		if i == 0 && startedGrounded && c.StepHeight > 0 && hit.Normal.Y < float32(math.Cos(float64(c.MaxSlopeDegrees)*math.Pi/180)) {
			if pose, ok, e := c.step(world, r.Pose, remaining, filter); e != nil {
				return r, e
			} else if ok {
				r.Pose = pose
				r.Stepped = true
				remaining = p.Vec3{}
				break
			}
		}
		length := remaining.Length()
		distance := max(0, hit.Distance-c.Skin)
		fraction := min(1, distance/length)
		r.Pose.Position = r.Pose.Position.Add(remaining.Mul(fraction))
		remaining = remaining.Mul(1 - fraction)
		dot := remaining.Dot(hit.Normal)
		if dot < 0 {
			remaining = remaining.Sub(hit.Normal.Mul(dot))
		} else {
			remaining = p.Vec3{}
		}
	}
	r.Support, r.Grounded, e = c.ground(world, r.Pose, filter)
	return r, e
}
func (c Config) step(world Queries, pose p.Pose, delta p.Vec3, filter p.QueryFilter) (p.Pose, bool, error) {
	up := p.Vec3{Y: c.StepHeight}
	hits, e := c.query(world, pose, up, filter)
	if e != nil {
		return pose, false, e
	}
	if _, blocked := firstBlocking(hits, up, c.Skin); blocked {
		return pose, false, nil
	}
	raised := pose
	raised.Position = raised.Position.Add(up)
	horizontal := p.Vec3{X: delta.X, Z: delta.Z}
	hits, e = c.query(world, raised, horizontal, filter)
	if e != nil {
		return pose, false, e
	}
	if _, blocked := firstBlocking(hits, horizontal, c.Skin); blocked {
		return pose, false, nil
	}
	raised.Position = raised.Position.Add(horizontal)
	down := p.Vec3{Y: -c.StepHeight - c.GroundDistance}
	hits, e = c.query(world, raised, down, filter)
	if e != nil {
		return pose, false, e
	}
	h, blocked := firstBlocking(hits, down, c.Skin)
	if !blocked || h.Normal.Y < float32(math.Cos(float64(c.MaxSlopeDegrees)*math.Pi/180)) {
		return pose, false, nil
	}
	raised.Position.Y -= max(0, h.Distance-c.Skin)
	return raised, true, nil
}
