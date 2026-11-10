package golem

import (
	"errors"
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func (n *navigationService) advanceAgent(a *navigationAgent, dt float64) {
	position, writer, ok := n.position(a.id)
	if !ok || !position.Finite() {
		n.failAgent(a, surface.InvalidInput, errors.New("golem: invalid navigation entity position"))
		return
	}
	if !a.state.HasDestination {
		return
	}
	if n.pending(a.config.Profile) {
		if a.view != nil || a.requesting {
			if !n.cancelTraversal(a, "geometry update pending") {
				return
			}
			n.invalidate(a)
		}
		a.keepSupport = a.supportEstablished
		a.state.Mode, a.state.Status.Code = NavigationPending, surface.NotReady
		a.needsPath = true
		return
	}
	revision := n.backend.CurrentRevision(a.config.Profile)
	if revision == 0 {
		if a.view != nil {
			if !n.cancelTraversal(a, "navigation unloaded") {
				return
			}
			n.invalidate(a)
		}
		a.state.Mode, a.state.Status.Code = NavigationPending, surface.Unloaded
		a.needsPath = true
		return
	}
	if a.state.WorldRevision != 0 && a.state.WorldRevision != revision {
		if !n.cancelTraversal(a, "navigation changed") {
			return
		}
		n.invalidate(a)
		a.keepSupport = a.supportEstablished
		a.needsPath = true
		a.state.WorldRevision = revision
		a.stallReplans = 0
		a.state.Mode, a.state.Status.Code = NavigationPending, surface.Stale
	}
	if a.needsPath && !a.requesting {
		n.requestPath(a, position)
	}
	if a.state.Mode == NavigationTraversing {
		n.advanceTraversal(a, dt, writer)
		return
	}
	if a.state.Mode != NavigationFollowing || a.view == nil {
		return
	}
	if a.initialProjection {
		actual := a.location.Position()
		if a.config.Motor != nil {
			generation := a.state.Generation
			var err error
			actual, err = a.config.Motor.MoveNavigation(NavigationMotion{EntityID: a.id, From: position, Target: actual, Delta: dt})
			if !n.movementCurrent(a, generation) {
				return
			}
			if err != nil {
				n.failAgent(a, surface.Failed, err)
				return
			}
		}
		if !fitsNavigationEntity(actual) {
			n.failAgent(a, surface.InvalidInput, errors.New("golem: projected position exceeds entity coordinates"))
			return
		}
		writer.SetPosition3D(float32(actual.X), float32(actual.Y), float32(actual.Z))
		position = actual
		a.initialProjection = false
	}
	if !n.reconcileAgent(a, position) {
		return
	}
	a.supportEstablished = true
	// The motor owns the body's position. Accepted height/position differences
	// must not turn the corridor's current-surface corner into a movement target.
	steering := a.location.Position()
	// Corridor repair starts with the current position. Skip reached ordinary
	// corners before calculating turn braking, retaining link activation corners.
	for a.next < len(a.corners) && a.corners[a.next].Link == nil && navDistance(steering, a.corners[a.next].Position) <= 0.01 {
		a.next++
	}

	remaining := n.remainingDistance(a, steering)
	if remaining <= a.config.StoppingDistance {
		n.finishRoute(a)
		return
	}
	targetSpeed := a.config.Speed
	if a.config.Acceleration > 0 {
		brakeDistance := math.Max(0, remaining-a.config.StoppingDistance)
		if a.next+1 < len(a.corners) {
			first, second := a.corners[a.next].Position, a.corners[a.next+1].Position
			ax, az, bx, bz := first.X-steering.X, first.Z-steering.Z, second.X-first.X, second.Z-first.Z
			denominator := math.Hypot(ax, az) * math.Hypot(bx, bz)
			if denominator > 1e-8 && (ax*bx+az*bz)/denominator < 0.5 {
				brakeDistance = math.Min(brakeDistance, navDistance(steering, first))
			}
		}
		targetSpeed = math.Min(targetSpeed, math.Sqrt(2*a.config.Acceleration*brakeDistance))
		change := a.config.Acceleration * dt
		if targetSpeed > a.speed {
			a.speed = math.Min(targetSpeed, a.speed+change)
		} else {
			a.speed = math.Max(targetSpeed, a.speed-change)
		}
	} else {
		a.speed = targetSpeed
	}
	budget := a.speed * dt
	initial := position
	for segment := 0; segment < 8 && budget > 0 && a.next < len(a.corners); segment++ {
		corner := a.corners[a.next]
		distance := navDistance(steering, corner.Position)
		if distance <= 0.01 {
			if corner.Link != nil {
				n.beginTraversal(a, *corner.Link)
				break
			}
			a.next++
			continue
		}
		step := math.Min(distance, budget)
		target := surface.Vec3{X: steering.X + (corner.Position.X-steering.X)*step/distance, Y: steering.Y + (corner.Position.Y-steering.Y)*step/distance, Z: steering.Z + (corner.Position.Z-steering.Z)*step/distance}
		moved := a.query.Move(n.server.runCtx, a.location, target, a.config.Filter)
		if moved.Location == nil || moved.Code != surface.Complete && moved.Code != surface.Partial {
			n.failAgent(a, moved.Code, moved.Err)
			return
		}
		actual := moved.Location.Position()
		if a.config.Motor != nil {
			generation := a.state.Generation
			var err error
			actual, err = a.config.Motor.MoveNavigation(NavigationMotion{EntityID: a.id, From: position, Target: actual, SurfaceNormal: moved.Normal, Delta: dt})
			if !n.movementCurrent(a, generation) {
				return
			}
			if err != nil {
				n.failAgent(a, surface.Failed, err)
				return
			}
		}
		if !fitsNavigationEntity(actual) {
			n.failAgent(a, surface.InvalidInput, errors.New("golem: navigation motor returned non-finite position"))
			return
		}
		writer.SetPosition3D(float32(actual.X), float32(actual.Y), float32(actual.Z))
		a.location = moved.Location
		progress := navDistance(position, actual)
		position = actual
		budget -= step
		if progress <= 1e-5 || navDistance(actual, moved.Location.Position()) > 0.01 {
			break
		}
		steering = moved.Location.Position()
	}
	if a.state.Mode == NavigationFollowing && a.next >= len(a.corners) {
		n.finishRoute(a)
	}
	if dt > 0 && a.state.Mode == NavigationFollowing {
		a.state.Velocity = surface.Vec3{X: (position.X - initial.X) / dt, Y: (position.Y - initial.Y) / dt, Z: (position.Z - initial.Z) / dt}
	}
	if navDistance(position, initial) < math.Min(1e-4, a.speed*dt*.01) && a.state.Mode == NavigationFollowing {
		a.stallTime += dt
		if a.stallTime >= 0.5 {
			if a.stallReplans >= a.config.MaxStallReplans {
				n.failAgent(a, surface.Unreachable, errors.New("golem: navigation stalled after bounded replanning"))
				return
			}
			a.stallReplans++
			n.invalidate(a)
			a.needsPath = true
			a.state.Mode = NavigationPending
		}
	} else {
		a.stallTime = 0
		if navDistance(position, initial) >= math.Min(1e-4, a.speed*dt*.01) && a.speed > 0 {
			a.stallReplans = 0
		}
	}
	a.lastPosition = position
}

func (n *navigationService) remainingDistance(a *navigationAgent, position surface.Vec3) float64 {
	var distance float64
	for i := a.next; i < len(a.corners); i++ {
		distance += navDistance(position, a.corners[i].Position)
		position = a.corners[i].Position
	}
	return distance
}

func (n *navigationService) finishRoute(a *navigationAgent) {
	code := a.state.Status.Code
	n.releaseRoute(a)
	if code == surface.Partial {
		a.state.Mode = NavigationBlocked
	} else {
		a.state.Mode = NavigationArrived
		a.state.Status.Code = surface.Complete
	}
}

func (n *navigationService) failAgent(a *navigationAgent, code surface.Code, err error) {
	if !n.cancelTraversal(a, "navigation failed") {
		return
	}
	n.releaseRoute(a)
	a.state.Mode, a.state.Status = NavigationBlocked, surface.Status{Code: code, Err: err}
	if code == surface.NoSurface || code == surface.Ambiguous {
		a.state.Mode = NavigationUnsupported
	}
	if code == surface.Failed || code == surface.InvalidInput {
		a.state.Mode = NavigationFailed
	}
}

func (n *navigationService) reconcile() {
	for _, id := range n.moved {
		a := n.agents[id]
		if a == nil || a.state.Mode != NavigationFollowing || a.view == nil {
			continue
		}
		position, _, ok := n.position(id)
		if ok {
			n.reconcileAgent(a, position)
		}
	}
}

func (n *navigationService) reconcileAgent(a *navigationAgent, position surface.Vec3) bool {
	projection := a.config.Projection
	projection.Hint = a.location
	projection.Horizontal = a.config.SupportTolerance
	projection.Vertical, projection.Up, projection.Down = a.config.SupportTolerance, a.config.SupportTolerance, a.config.SupportTolerance
	result := a.query.Project(n.server.runCtx, position, projection, a.config.Filter)
	if result.Code != surface.Complete || result.Location == nil {
		n.failAgent(a, result.Code, result.Err)
		return false
	}
	projected := result.Location.Position()
	if navDistance(position, projected) > a.config.SupportTolerance {
		n.failAgent(a, surface.NoSurface, errors.New("golem: body is no longer supported by its navigation surface"))
		return false
	}
	a.location = result.Location
	if repair, ok := a.query.(surface.CorridorQuery); ok && a.route != nil && navDistance(projected, a.lastRepair) > 0.01 {
		path := repair.RepairPath(n.server.runCtx, a.route, a.location, a.config.Filter)
		if path.Code != surface.Complete && path.Code != surface.Partial || path.Route == nil {
			n.invalidate(a)
			a.needsPath = a.state.HasDestination
			a.state.Mode, a.state.Status = NavigationPending, path.Status
			return false
		}
		a.route, a.corners, a.next, a.lastRepair = path.Route, path.Route.Corners(), 0, projected
		if path.Code == surface.Partial && !a.config.AllowPartial {
			a.state.PartialPath = path.Route.Corners()
			n.failAgent(a, surface.Partial, path.Err)
			a.state.Status = path.Status
			return false
		}
		a.state.Status = path.Status
	}
	return true
}

func fitsNavigationEntity(p surface.Vec3) bool {
	return p.Finite() && math.Abs(p.X) <= math.MaxFloat32 && math.Abs(p.Y) <= math.MaxFloat32 && math.Abs(p.Z) <= math.MaxFloat32
}
