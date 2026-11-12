package golem

import (
	"strconv"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func (n *navigationService) regionalSafety(profile surface.ProfileID) surface.RegionalSafety {
	if regional, ok := n.backend.(surface.RegionalBackend); ok {
		return regional.Safety(profile)
	}
	return nil
}

func (n *navigationService) agentSafety(a *navigationAgent) surface.RegionalSafety {
	safety := n.regionalSafety(a.config.Profile)
	if scoped, ok := safety.(surface.ViewRegionalSafety); ok && a.view != nil {
		return scoped.ForView(a.view)
	}
	return safety
}

func (n *navigationService) profileUnavailable(profile surface.ProfileID) bool {
	if safety := n.regionalSafety(profile); safety != nil {
		return safety.CommittedRevision() == 0
	}
	return n.backend.CurrentRevision(profile) == 0 || n.pending(profile)
}

// Quarantine keeps the destination, support and corridor. It cancels work once,
// then waits for a different safety token instead of filling the path queue.
func (n *navigationService) quarantineAgent(a *navigationAgent, safety surface.RegionalSafety) bool {
	if !n.cancelTraversal(a, "navigation region quarantined") {
		return false
	}
	if !a.quarantined {
		a.resumeMode = a.state.Mode
		a.resumeStatus = a.state.Status
		if a.resumeMode == NavigationTraversing {
			a.resumeMode = NavigationFollowing
		}
		if a.requesting {
			a.state.Generation++
			n.paths.cancelKey(strconv.FormatInt(a.id, 10))
			a.requesting, a.needsPath = false, a.state.HasDestination
		}
	}
	a.quarantined, a.safetyToken = true, safety.Token()
	a.quarantineRevision = a.state.WorldRevision
	a.state.Quarantined = true
	a.keepSupport = a.supportEstablished
	a.state.Mode, a.state.Status = NavigationPending, surface.Status{Code: surface.NotReady}
	a.speed = 0
	a.state.Velocity = surface.Vec3{}
	return true
}

func (n *navigationService) regionSafe(a *navigationAgent, safety surface.RegionalSafety, position surface.Vec3) bool {
	if !n.pointSafe(a, safety, position) {
		return false
	}
	if !n.supportSafe(a, safety) {
		return false
	}
	if a.route != nil && (!a.routeSafetyChecked || a.routeSafetyToken != safety.Token()) {
		if !safety.SafeRoute(a.route) {
			return false
		}
		// Routes and safety captures are immutable. Recheck on either change,
		// rather than walking the same corridor at every local movement stage.
		a.routeSafetyChecked, a.routeSafetyToken = true, safety.Token()
	}
	if a.traversal != nil {
		if !a.traversal.bounded {
			return safety.Token() == a.traversal.safetyToken
		}
		return safety.SafeBounds(a.traversal.bounds.Min, a.traversal.bounds.Max)
	}
	return true
}

func (n *navigationService) supportSafe(a *navigationAgent, safety surface.RegionalSafety) bool {
	if a.location != nil && (!a.locationSafetyChecked || a.locationSafetyToken != safety.Token()) {
		if !safety.SafeLocation(a.location) {
			return false
		}
		// Locations are immutable. Assignments and view transfers clear this
		// cache; full physical movement segments are still checked every time.
		a.locationSafetyChecked, a.locationSafetyToken = true, safety.Token()
	}
	return true
}

func (n *navigationService) safeSegment(a *navigationAgent, from, to surface.Vec3) bool {
	if safety := n.agentSafety(a); safety != nil {
		if !n.regionSafe(a, safety, from) || !n.segmentSafe(a, safety, from, to) {
			n.quarantineAgent(a, safety)
			return false
		}
	}
	return true
}

func (n *navigationService) pointSafe(a *navigationAgent, safety surface.RegionalSafety, position surface.Vec3) bool {
	if a.positionSafetyChecked && a.positionSafetyToken == safety.Token() && a.safePosition == position {
		return true
	}
	if !safety.SafeSegment(position, position) {
		return false
	}
	a.positionSafetyChecked, a.positionSafetyToken, a.safePosition = true, safety.Token(), position
	return true
}

func (n *navigationService) segmentSafe(a *navigationAgent, safety surface.RegionalSafety, from, to surface.Vec3) bool {
	if from == to {
		return n.pointSafe(a, safety, from)
	}
	if a.segmentSafetyChecked && a.segmentSafetyToken == safety.Token() && a.safeSegmentFrom == from && a.safeSegmentTo == to {
		return true
	}
	if !safety.SafeSegment(from, to) {
		return false
	}
	// A fresh capture is still required after every host callback. Reusing the
	// identical directed segment within that immutable token cannot hide edits,
	// motor deviations or collision changes: each changes the token or endpoints.
	a.segmentSafetyChecked, a.segmentSafetyToken = true, safety.Token()
	a.safeSegmentFrom, a.safeSegmentTo = from, to
	return true
}

func (n *navigationService) rebaseHandles(a *navigationAgent, old surface.View, location surface.Location, route surface.Route, position surface.Vec3, transfer bool) (surface.View, surface.Query, surface.RebaseResult, bool) {
	view, err := n.backend.Acquire(n.server.runCtx, a.config.Profile)
	if err != nil {
		return nil, nil, surface.RebaseResult{Status: surface.Status{Code: surface.NotReady, Err: err}}, false
	}
	query, err := view.NewQuery(n.config.Limits)
	if err != nil {
		view.Release()
		return nil, nil, surface.RebaseResult{Status: surface.Status{Code: surface.Failed, Err: err}}, false
	}
	if location == nil || old == nil {
		view.Release()
		return nil, nil, surface.RebaseResult{Status: surface.Status{Code: surface.Stale}}, false
	}
	var result surface.RebaseResult
	preserved := false
	if unchanged, ok := query.(surface.CorridorTransferQuery); transfer && ok {
		result = unchanged.TransferPath(n.server.runCtx, route, location, position, a.config.SupportTolerance, a.config.Filter)
		preserved = true
	} else if rebase, ok := query.(surface.RebaseQuery); ok {
		result = rebase.RebasePath(n.server.runCtx, route, location, position, a.config.SupportTolerance, a.config.Filter)
	} else {
		view.Release()
		return nil, nil, surface.RebaseResult{Status: surface.Status{Code: surface.Stale}}, false
	}
	if result.Code != surface.Complete && !(result.Code == surface.Partial && a.config.AllowPartial) || result.Location == nil || route != nil && result.Route == nil {
		view.Release()
		return nil, nil, result, false
	}
	return view, query, result, preserved
}

func canTransferNavigationCorridor(a *navigationAgent, position surface.Vec3) bool {
	if a.route == nil || a.location == nil || a.traversal != nil || a.initialProjection || a.corridorDeviated || !a.hasReconciledPosition || !sameNavigationHorizontal(position, a.reconciledPosition) {
		return false
	}
	identity, ok := a.location.(surface.SupportIdentity)
	// Only straight, reconciled progress on the last repaired polygon may keep
	// its funnel and cursor. A deviation must still run ordinary corridor repair.
	return ok && identity.SameSupport(a.repairLocation)
}

func protectsRetiredViews(safety surface.RegionalSafety) bool {
	retained, ok := safety.(surface.RetainedRegionalSafety)
	return ok && retained.ProtectsRetiredViews()
}

// Retirement has its own fair cursor: rebuilding does not spend the whole
// movement budget on the first agents while other safe agents lose cadence.
func (n *navigationService) rebaseRetired() {
	if n.rebaseRevisions == nil {
		n.rebaseRevisions = make(map[surface.ProfileID]uint64)
	}
	clear(n.rebaseRevisions)
	started, attempts, visits := NavigationNow(), 0, len(n.order)
	for i := 0; i < visits && len(n.order) > 0; i++ {
		if n.restoring || n.restoreErr != nil {
			return
		}
		if attempts >= n.config.RebaseAgentsPerTick || i > 0 && navigationSince(started) >= n.config.RebaseBudget {
			return
		}
		if n.rebaseCursor >= len(n.order) {
			n.rebaseCursor = 0
		}
		a := n.agents[n.order[n.rebaseCursor]]
		n.rebaseCursor++
		if a == nil || a.view == nil {
			continue
		}
		revision, known := n.rebaseRevisions[a.config.Profile]
		if !known {
			revision = n.backend.CurrentRevision(a.config.Profile)
			n.rebaseRevisions[a.config.Profile] = revision
		}
		if revision == 0 || a.view.Revision() == revision {
			continue
		}
		safety := n.agentSafety(a)
		if !protectsRetiredViews(safety) || safety.CommittedRevision() == 0 || a.view.Revision() == safety.CommittedRevision() {
			continue
		}
		position, _, ok := n.position(a.id)
		if !ok {
			continue
		}
		attempts++
		n.refreshAgentRevision(a, position, safety, safety.CommittedRevision())
	}
}

func (n *navigationService) refreshAgentRevision(a *navigationAgent, position surface.Vec3, safety surface.RegionalSafety, revision uint64) bool {
	rebasePosition := position
	canRebase := a.traversal == nil
	if safety != nil && a.traversal != nil && a.traversal.bounded && safety.SafeBounds(a.traversal.bounds.Min, a.traversal.bounds.Max) {
		canRebase = true
		rebasePosition = a.location.Position()
	}
	if safety != nil && a.view != nil && canRebase && n.rebaseAgent(a, rebasePosition) {
		return true
	}
	if !n.cancelTraversal(a, "navigation changed") {
		return false
	}
	n.invalidate(a)
	a.keepSupport = a.supportEstablished
	a.needsPath = a.state.HasDestination
	a.state.WorldRevision = revision
	a.stallReplans = 0
	a.state.Mode, a.state.Status.Code = NavigationPending, surface.Stale
	return true
}

// Every transfer moves straight to the latest generation and skips intermediates.
func (n *navigationService) rebaseAgent(a *navigationAgent, position surface.Vec3) bool {
	n.tickMetrics.RebaseAttempts++
	if n.config.ObserveTick != nil {
		started := NavigationNow()
		defer func() { n.tickMetrics.Rebase += navigationSince(started) }()
	}
	view, query, result, preserved := n.rebaseHandles(a, a.view, a.location, a.route, position, canTransferNavigationCorridor(a, position))
	if view == nil {
		return false
	}
	var corners []surface.Corner
	next := 0
	if preserved {
		corners, next = a.corners, a.next
	} else if a.traversal != nil {
		if result.Route != nil {
			// Keep the retained corners intact until the action's directed link
			// has been found in the replacement corridor.
			corners = navigationCorners(nil, result.Route)
		}
		next = -1
		for i, corner := range corners {
			if corner.Link != nil && *corner.Link == a.traversal.link {
				next = i
				break
			}
		}
		if next < 0 {
			view.Release()
			return false
		}
	} else if result.Route != nil {
		corners = navigationCorners(a.corners, result.Route)
	}
	old := a.view
	a.view, a.query, a.location, a.route = view, query, result.Location, result.Route
	a.routeSafetyChecked = false
	a.locationSafetyChecked = false
	a.positionSafetyChecked, a.segmentSafetyChecked = false, false
	if result.Route != nil {
		a.corners, a.next = corners, next
		if a.quarantined {
			a.resumeStatus = result.Status
		} else {
			a.state.Status = result.Status
		}
	}
	a.lastRepair = result.Location.Position()
	a.repairLocation = result.Location
	a.corridorDeviated, a.hasReconciledPosition = false, false
	a.state.WorldRevision = view.Revision()
	old.Release()
	n.tickMetrics.RebasedAgents++
	return true
}
