package golem

import (
	"context"
	"errors"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// NavigationTraversalRequest identifies one selected, directed traversal.
// Begin is the eligibility/activation gate; the library does not choose gameplay
// actions or assume that disabling a link blocks ordinary walkable ground.
type NavigationTraversalRequest struct {
	EntityID   int64
	Generation uint64
	Profile    surface.ProfileID
	Link       surface.Link
}

// NavigationTraversal begins a game-owned action on the tick goroutine.
// Return an error to reject eligibility or a disabled link. Calls must not block.
type NavigationTraversal interface {
	BeginNavigationTraversal(NavigationTraversalRequest) (NavigationTraversalAction, error)
}

// NavigationTraversalAction owns body motion during traversal. Complete requires
// validated landing at the selected endpoint. Cancel must stop action ownership;
// it is called exactly once on interruption and never after successful completion.
type NavigationTraversalAction interface {
	AdvanceNavigationTraversal(dt float64) (NavigationTraversalUpdate, error)
	CancelNavigationTraversal(reason string)
}

// NavigationTraversalBounds contains every position a traversal may occupy,
// including its endpoints. Bounds are world-space and fixed for the action.
type NavigationTraversalBounds struct{ Min, Max surface.Vec3 }

// NavigationBoundedTraversal optionally declares an action's complete swept
// volume. Without it, any profile safety change cancels the action conservatively.
type NavigationBoundedTraversal interface {
	NavigationTraversalBounds() NavigationTraversalBounds
}

// NavigationTraversalUpdate supplies actual body position and completion intent.
type NavigationTraversalUpdate struct {
	Position surface.Vec3
	Complete bool
}

type navigationTraversal struct {
	link        surface.Link
	action      NavigationTraversalAction
	elapsed     float64
	bounded     bool
	bounds      NavigationTraversalBounds
	safetyToken uint64
}

func (n *navigationService) beginTraversal(a *navigationAgent, link surface.Link) {
	if a.config.Traversal == nil {
		n.failAgent(a, surface.Failed, errors.New("golem: route requires an unconfigured traversal handler"))
		return
	}
	if link.ID == "" || !link.From.Finite() || !link.To.Finite() {
		n.failAgent(a, surface.InvalidInput, errors.New("golem: route has invalid traversal endpoints"))
		return
	}
	from := a.location.Position()
	if !n.safeSegment(a, from, link.To) {
		return
	}
	if navDistance(from, link.From) > 0.1 {
		n.failAgent(a, surface.NoSurface, errors.New("golem: traversal entry is outside activation tolerance"))
		return
	}
	endpoint := a.query.Project(n.server.runCtx, link.To, surface.ProjectionOptions{Horizontal: 0.1, Up: 0.1, Down: 0.1}, a.config.Filter)
	if endpoint.Code != surface.Complete || endpoint.Location == nil || navDistance(endpoint.Location.Position(), link.To) > 0.1 {
		n.failAgent(a, surface.NoSurface, errors.New("golem: traversal destination is not available"))
		return
	}
	generation := a.state.Generation
	action, err := a.config.Traversal.BeginNavigationTraversal(NavigationTraversalRequest{EntityID: a.id, Generation: generation, Profile: a.config.Profile, Link: link})
	if !n.movementCurrent(a, generation) {
		if action != nil {
			action.CancelNavigationTraversal("traversal superseded during begin")
		}
		return
	}
	if err != nil || action == nil {
		if action != nil {
			action.CancelNavigationTraversal("traversal begin failed")
			if !n.current(a, generation) {
				return
			}
		}
		if err == nil {
			err = errors.New("golem: traversal handler returned no action")
		}
		n.failAgent(a, surface.Failed, err)
		return
	}
	a.traversal = &navigationTraversal{link: link, action: action}
	if safety := n.agentSafety(a); safety != nil {
		a.traversal.safetyToken = safety.Token()
	}
	if bounded, ok := action.(NavigationBoundedTraversal); ok {
		bounds := bounded.NavigationTraversalBounds()
		if !n.movementCurrent(a, generation) {
			return
		}
		if !validTraversalBounds(bounds) || !traversalBoundsContain(bounds, link.From) || !traversalBoundsContain(bounds, link.To) {
			n.failAgent(a, surface.InvalidInput, errors.New("golem: traversal bounds must contain both endpoints"))
			return
		}
		a.traversal.bounded, a.traversal.bounds = true, bounds
	}
	if safety := n.agentSafety(a); safety != nil {
		if !n.regionSafe(a, safety, from) {
			n.quarantineAgent(a, safety)
			return
		}
	}
	a.state.Mode = NavigationTraversing
	a.state.Velocity = surface.Vec3{}
	a.speed = 0
}

func (n *navigationService) advanceTraversal(a *navigationAgent, dt float64, writer registry.Position3DWriter) {
	if a.traversal == nil {
		n.failAgent(a, surface.Failed, errors.New("golem: missing traversal action"))
		return
	}
	a.traversal.elapsed += dt
	if a.traversal.elapsed >= a.config.TraversalTimeout.Seconds() {
		if !n.cancelTraversal(a, "traversal timed out") {
			return
		}
		n.failAgent(a, surface.Cancelled, context.DeadlineExceeded)
		return
	}
	generation := a.state.Generation
	from, _, exists := n.position(a.id)
	if !exists {
		return
	}
	update, err := a.traversal.action.AdvanceNavigationTraversal(dt)
	if !n.movementCurrent(a, generation) {
		return
	}
	if err != nil || !fitsNavigationEntity(update.Position) {
		if err == nil {
			err = errors.New("golem: traversal returned non-finite position")
		}
		if !n.cancelTraversal(a, "traversal failed") {
			return
		}
		n.failAgent(a, surface.Failed, err)
		return
	}
	if a.traversal.bounded && !traversalBoundsContain(a.traversal.bounds, update.Position) {
		n.failAgent(a, surface.InvalidInput, errors.New("golem: traversal left its declared bounds"))
		return
	}
	if !n.safeSegment(a, from, update.Position) {
		return
	}
	writer.SetPosition3D(float32(update.Position.X), float32(update.Position.Y), float32(update.Position.Z))
	a.lastPosition = update.Position
	if !update.Complete {
		return
	}
	tolerance := a.config.SupportTolerance
	landing := a.query.Project(n.server.runCtx, update.Position, surface.ProjectionOptions{Horizontal: tolerance, Up: tolerance, Down: tolerance}, a.config.Filter)
	if landing.Code != surface.Complete || landing.Location == nil {
		if !n.cancelTraversal(a, "landing surface unavailable") {
			return
		}
		n.failAgent(a, landing.Code, landing.Err)
		return
	}
	if navDistance(update.Position, landing.Location.Position()) > tolerance {
		if !n.cancelTraversal(a, "landing body is unsupported") {
			return
		}
		n.failAgent(a, surface.NoSurface, errors.New("golem: traversal landing body is outside support tolerance"))
		return
	}
	if navDistance(landing.Location.Position(), a.traversal.link.To) > 0.1 {
		if !n.cancelTraversal(a, "landing missed endpoint") {
			return
		}
		n.failAgent(a, surface.NoSurface, errors.New("golem: traversal landing missed its endpoint"))
		return
	}
	a.location = landing.Location
	a.locationSafetyChecked = false
	a.traversal = nil
	a.next++
	a.state.Mode = NavigationFollowing
}

func (n *navigationService) cancelTraversal(a *navigationAgent, reason string) bool {
	generation := a.state.Generation
	if a.traversal != nil {
		action := a.traversal.action
		a.traversal = nil
		action.CancelNavigationTraversal(reason)
	}
	return n.current(a, generation)
}

func (n *navigationService) current(a *navigationAgent, generation uint64) bool {
	return n.agents[a.id] == a && a.state.Generation == generation
}

// A game callback can change geometry without changing its agent's intent.
// Fence both identities before applying the callback's body-position result.
func (n *navigationService) movementCurrent(a *navigationAgent, generation uint64) bool {
	if !n.current(a, generation) || n.server.runCtx.Err() != nil {
		return false
	}
	if safety := n.agentSafety(a); safety != nil {
		position, _, ok := n.position(a.id)
		if !ok || !n.regionSafe(a, safety, position) {
			n.quarantineAgent(a, safety)
			return false
		}
		// Only retirement-aware quarantine can protect a callback's old-view
		// result after publication. Other backends wait for a view transfer.
		if a.view != nil && a.view.Revision() != safety.CommittedRevision() && !protectsRetiredViews(safety) {
			return false
		}
		return true
	}
	if n.pending(a.config.Profile) || a.view != nil && a.view.Revision() != n.backend.CurrentRevision(a.config.Profile) {
		if !n.cancelTraversal(a, "navigation changed during movement callback") {
			return false
		}
		n.invalidate(a)
		a.keepSupport = a.supportEstablished
		a.needsPath = a.state.HasDestination
		a.state.Mode, a.state.Status.Code = NavigationPending, surface.Stale
		return false
	}
	return true
}

func validTraversalBounds(b NavigationTraversalBounds) bool {
	return b.Min.Finite() && b.Max.Finite() && b.Min.X <= b.Max.X && b.Min.Y <= b.Max.Y && b.Min.Z <= b.Max.Z
}

func traversalBoundsContain(b NavigationTraversalBounds, p surface.Vec3) bool {
	return p.X >= b.Min.X && p.X <= b.Max.X && p.Y >= b.Min.Y && p.Y <= b.Max.Y && p.Z >= b.Min.Z && p.Z <= b.Max.Z
}
