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

// NavigationTraversalUpdate supplies actual body position and completion intent.
type NavigationTraversalUpdate struct {
	Position surface.Vec3
	Complete bool
}

type navigationTraversal struct {
	link    surface.Link
	action  NavigationTraversalAction
	elapsed float64
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
	writer.SetPosition3D(float32(update.Position.X), float32(update.Position.Y), float32(update.Position.Z))
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
