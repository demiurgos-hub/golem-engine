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
	Pose p.Pose
	// Path retains every swept waypoint, including start, depenetration, slide
	// and up/forward/down step phases. Hosts must integrate this path, not its chord.
	Path                       []p.Pose
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

// Probe returns the nearest walkable support within GroundDistance. It never
// changes pose or snaps down to a more distant floor.
func (c Config) Probe(world Queries, pose p.Pose, filter p.QueryFilter) (Support, bool, error) {
	if err := c.Validate(); err != nil {
		return Support{}, false, err
	}
	if world == nil || !pose.Valid() {
		return Support{}, false, p.ErrInvalid
	}
	return c.ground(world, pose, filter)
}

// Move sweeps, slides and bounds depenetration. Stepping is an up/forward/down
// action with a validated walkable landing; it never snaps to another floor.
func (c Config) Move(world Queries, start p.Pose, translation p.Vec3, filter p.QueryFilter) (Result, error) {
	r := Result{Pose: start, Path: []p.Pose{start}}
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
		r.Path = append(r.Path, r.Pose)
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
	var planes []p.Vec3
	cosSlope := float32(math.Cos(float64(c.MaxSlopeDegrees) * math.Pi / 180))
	for i := 0; i < c.Iterations && remaining.Length() > 1e-7; i++ {
		hits, e := c.query(world, r.Pose, remaining, filter)
		if e != nil {
			return r, e
		}
		hit, blocked := firstBlocking(hits, remaining, c.Skin)
		if !blocked {
			r.Pose.Position = r.Pose.Position.Add(remaining)
			r.Path = append(r.Path, r.Pose)
			remaining = p.Vec3{}
			break
		}
		r.Hits = append(r.Hits, hit)
		r.Blocked = true
		if i == 0 && startedGrounded && c.StepHeight > 0 && hit.Normal.Y < float32(math.Cos(float64(c.MaxSlopeDegrees)*math.Pi/180)) {
			if pose, path, ok, e := c.step(world, r.Pose, remaining, filter); e != nil {
				return r, e
			} else if ok {
				r.Pose = pose
				r.Path = append(r.Path, path...)
				r.Stepped = true
				remaining = p.Vec3{}
				break
			}
		}
		length := remaining.Length()
		distance := max(0, hit.Distance-c.Skin)
		fraction := min(1, distance/length)
		r.Pose.Position = r.Pose.Position.Add(remaining.Mul(fraction))
		if r.Pose != r.Path[len(r.Path)-1] {
			r.Path = append(r.Path, r.Pose)
		}
		remaining = remaining.Mul(1 - fraction)
		normal := hit.Normal
		if normal.Y > 0 && normal.Y < cosSlope {
			// Horizontal intent must not turn a steep surface or stair edge into
			// an upward ramp. Downward motion can still slide along that surface.
			horizontalNormal := p.Vec3{X: normal.X, Z: normal.Z}.Unit()
			horizontal := p.Vec3{X: remaining.X, Z: remaining.Z}
			vertical := p.Vec3{Y: remaining.Y}
			if dot := horizontal.Dot(horizontalNormal); dot < 0 {
				horizontal = horizontal.Sub(horizontalNormal.Mul(dot))
			}
			if dot := vertical.Dot(normal); dot < 0 {
				vertical = vertical.Sub(normal.Mul(dot))
			}
			remaining = horizontal.Add(vertical)
			normal = horizontalNormal
		}
		planes = append(planes, normal)
		for pass := 0; pass < len(planes)+1; pass++ {
			changed := false
			for _, plane := range planes {
				if dot := remaining.Dot(plane); dot < -1e-7 {
					remaining = remaining.Sub(plane.Mul(dot))
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		for _, plane := range planes {
			if remaining.Dot(plane) < -1e-6 {
				remaining = p.Vec3{}
				break
			}
		}
	}
	r.Support, r.Grounded, e = c.ground(world, r.Pose, filter)
	return r, e
}
func (c Config) step(world Queries, pose p.Pose, delta p.Vec3, filter p.QueryFilter) (p.Pose, []p.Pose, bool, error) {
	up := p.Vec3{Y: c.StepHeight}
	hits, e := c.query(world, pose, up, filter)
	if e != nil {
		return pose, nil, false, e
	}
	if _, blocked := firstBlocking(hits, up, c.Skin); blocked {
		return pose, nil, false, nil
	}
	raised := pose
	raised.Position = raised.Position.Add(up)
	path := []p.Pose{raised}
	horizontal := p.Vec3{X: delta.X, Z: delta.Z}
	hits, e = c.query(world, raised, horizontal, filter)
	if e != nil {
		return pose, nil, false, e
	}
	if _, blocked := firstBlocking(hits, horizontal, c.Skin); blocked {
		return pose, nil, false, nil
	}
	raised.Position = raised.Position.Add(horizontal)
	path = append(path, raised)
	down := p.Vec3{Y: -c.StepHeight - c.GroundDistance}
	hits, e = c.query(world, raised, down, filter)
	if e != nil {
		return pose, nil, false, e
	}
	h, blocked := firstBlocking(hits, down, c.Skin)
	if !blocked || h.Normal.Y < float32(math.Cos(float64(c.MaxSlopeDegrees)*math.Pi/180)) {
		return pose, nil, false, nil
	}
	raised.Position.Y -= max(0, h.Distance-c.Skin)
	path = append(path, raised)
	return raised, path, true, nil
}
