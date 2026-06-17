package golem

import "math"

// navMover is the minimal interface NavAgent needs to read and write an
// entity's position. All generated Synced* types satisfy it automatically
// via Position() (from Entity) and SetPosition() (from PositionWriter).
type navMover interface {
	Position() (x, y float32)
	SetPosition(x, y float32)
}

// NavAgent manages path-following movement for an NPC entity.
// Embed it in your NPC struct, call Bind once in OnSpawn, then call
// SetDestination whenever the goal changes — movement happens automatically
// each tick via the Ticker interface.
//
//	type NPC struct { Agent golem.NavAgent }
//
//	func (n *NPC) OnSpawn()        { n.Agent.Bind(n) }
//	func (n *NPC) Tick(dt float64) { n.Agent.Tick(dt) }
//
//	// Anywhere (command handler, OnTick, OnSpawn):
//	npc.Agent.SetDestination(s, targetX, targetY)
type NavAgent struct {
	// Speed is movement speed in world units per second.
	Speed float64
	// StoppingDistance is how close to the goal (and each intermediate
	// waypoint) the agent must be before it advances to the next waypoint.
	// Use a value around half a grid cell for smooth movement (e.g. 8.0 for
	// 16-pixel tiles). Zero relies entirely on overshoot protection to
	// snap the agent onto each waypoint position exactly.
	StoppingDistance float64

	entity navMover
	path   []NavPoint
	velX   float64
	velY   float64
}

// Bind attaches a position-aware entity to the agent. Call this from OnSpawn
// so that SetDestination and Tick can read and write the entity's position
// without extra arguments. Any generated Synced* type satisfies the required
// interface automatically.
func (a *NavAgent) Bind(e navMover) {
	a.entity = e
}

// SetDestination finds a path to (toX, toY) from the bound entity's current
// position and stores it as the agent's route. Returns false if the agent is
// not bound, no nav backend is configured, or no path exists.
// Any existing path is replaced.
func (a *NavAgent) SetDestination(s *Server, toX, toY float64) bool {
	if a.entity == nil {
		return false
	}
	fromX, fromY := a.entity.Position()
	path, ok := s.FindPath(float64(fromX), float64(fromY), toX, toY)
	if !ok {
		return false
	}
	a.path = path
	return true
}

// Tick advances the agent one tick along its current path, reading position
// from the bound entity and writing the updated position back via SetPosition.
// No-op if the agent is not bound or has no path. Call this from the entity's
// Tick method to drive automatic movement.
func (a *NavAgent) Tick(dt float64) {
	if a.entity == nil {
		return
	}
	x, y := a.entity.Position()
	nx, ny := a.Step(dt, float64(x), float64(y))
	a.entity.SetPosition(float32(nx), float32(ny))
}

// Step advances the agent one tick along its current path. x and y are the
// entity's current world-space position; returns the new position after
// applying one tick of movement at the configured Speed.
//
// When the agent reaches the goal or has no path, (x, y) is returned
// unchanged and velocity is zeroed. Overshoot is prevented: the agent snaps
// to a waypoint rather than moving past it in a single tick.
//
// Use Step when you need manual control without binding, or when position
// management lives outside the entity (e.g. a shared physics body):
//
//	npc.X, npc.Y = npc.Agent.Step(dt, npc.X, npc.Y)
//	npc.SetPosition(npc.X, npc.Y)
func (a *NavAgent) Step(dt, x, y float64) (float64, float64) {
	for len(a.path) > 0 {
		next := a.path[0]
		dx, dy := next.X-x, next.Y-y
		d := math.Sqrt(dx*dx + dy*dy)

		if d <= a.StoppingDistance {
			a.path = a.path[1:]
			continue
		}

		speed := a.Speed * dt
		dir := 1.0 / d
		a.velX = dx * dir * a.Speed
		a.velY = dy * dir * a.Speed

		if speed >= d {
			// Would overshoot: snap to waypoint and advance.
			a.path = a.path[1:]
			return next.X, next.Y
		}

		return x + dx*dir*speed, y + dy*dir*speed
	}

	a.velX = 0
	a.velY = 0
	return x, y
}

// HasPath reports whether the agent has a path to follow.
func (a *NavAgent) HasPath() bool {
	return len(a.path) > 0
}

// ResetPath clears the current path, stopping all movement immediately.
func (a *NavAgent) ResetPath() {
	a.path = nil
	a.velX = 0
	a.velY = 0
}

// Velocity returns the agent's movement velocity in world units per second—
// the direction toward the next waypoint multiplied by Speed—as computed
// during the most recent call to Step or Tick. Returns (0, 0) when not moving.
// Useful for driving animation facing direction.
func (a *NavAgent) Velocity() (float64, float64) {
	return a.velX, a.velY
}

// RemainingDistance returns the total length of the path from (x, y) to the
// goal, summing straight-line distances to each remaining waypoint.
// Returns 0 when the agent has no path.
func (a *NavAgent) RemainingDistance(x, y float64) float64 {
	if len(a.path) == 0 {
		return 0
	}
	total := navPointDist(x, y, a.path[0].X, a.path[0].Y)
	for i := 1; i < len(a.path); i++ {
		total += navPointDist(a.path[i-1].X, a.path[i-1].Y, a.path[i].X, a.path[i].Y)
	}
	return total
}

// NextWaypoint returns the next world-space position the agent is heading
// toward, and true. Returns the zero value and false when no path is set.
func (a *NavAgent) NextWaypoint() (NavPoint, bool) {
	if len(a.path) == 0 {
		return NavPoint{}, false
	}
	return a.path[0], true
}

func navPointDist(x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	return math.Sqrt(dx*dx + dy*dy)
}
