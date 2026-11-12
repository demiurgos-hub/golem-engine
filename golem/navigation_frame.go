package golem

import (
	"errors"
	"fmt"
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

// ErrNavigationFrameFault reports a rejected physical frame. No body positions
// from that frame were written by navigation; the physics host must reject the
// whole simulation frame rather than restore individual interacting bodies.
var ErrNavigationFrameFault = errors.New("golem: navigation physical frame rejected")

// NavigationMotionSegment is one proposed displacement, not a physics step.
// Delta apportions the proposal's elapsed time; Projection is a zero-time,
// bounded initial alignment that must pass the host's body-clearance validation.
type NavigationMotionSegment struct {
	From, Target  surface.Vec3
	SurfaceNormal surface.Vec3
	Delta         float64
	Projection    bool
}

// NavigationMotionProposal describes desired movement while the entity and
// navigation support remain at their accepted positions. The host advances time
// once per physics substep, never once per segment.
type NavigationMotionProposal struct {
	EntityID   int64
	Generation uint64
	Delta      float64
	Segments   []NavigationMotionSegment
	Traversal  bool
}

// NavigationMotionResult supplies the final physical anchor and its complete
// ordered path through this frame, including the starting and final positions.
// A stationary result may omit Path. Submit every changed staged attachment,
// including idle agents, and every agent with a movement proposal.
type NavigationMotionResult struct {
	EntityID int64
	Position surface.Vec3
	Path     []surface.Vec3
}

// NavigationStagedTraversalAction extends a traversal with transactional motion.
// Prepare may retain private proposal scratch but must not advance durable game
// state or mutate the world. Exactly one Commit or Discard follows a successful
// Prepare. Commit receives the accepted physical result, which may be clipped.
// The ordinary AdvanceNavigationTraversal method is unused in staged mode.
type NavigationStagedTraversalAction interface {
	PrepareNavigationTraversal(dt float64) (NavigationTraversalUpdate, error)
	CommitNavigationTraversal(NavigationTraversalUpdate)
	DiscardNavigationTraversal()
}

// NavigationFrame owns one tick's staged movement and captured regional safety.
// Prepare, validation, commit and abort are tick-owned. After preparation,
// Contains, SafeSegment and SafeBounds may be called concurrently by physics
// workers until commit or abort; callers must finish those reads first.
type NavigationFrame struct {
	service   *navigationService
	tick      uint64
	delta     float64
	agents    map[int64]navigationFrameAgent
	order     []int64
	plans     map[int64]*navigationFramePlan
	proposals []NavigationMotionProposal
	closed    bool
	barrier   bool
}

type navigationFrameAgent struct {
	agent      *navigationAgent
	generation uint64
	attachment uint64
	position   surface.Vec3
	view       surface.View
	safety     surface.RegionalSafety
	revision   uint64
	ready      bool
	pending    bool
	traversal  *navigationTraversal
	bounded    bool
	bounds     NavigationTraversalBounds
}

type navigationFramePlan struct {
	agent       *navigationAgent
	generation  uint64
	proposal    NavigationMotionProposal
	location    surface.Location
	next        int
	speed       float64
	projected   bool
	traversal   *navigationTraversal
	action      NavigationStagedTraversalAction
	update      NavigationTraversalUpdate
	actionReady bool
}

// PrepareNavigationFrame runs the normal fair movement admission once for this
// server tick. Legacy agents still move synchronously; StagedPhysics agents only
// produce proposals. The host must commit or abort before preparing another tick.
// A server without navigation returns a nil frame and no error.
func (s *Server) PrepareNavigationFrame(dt float64) (*NavigationFrame, error) {
	if !finiteNumber(dt) || dt <= 0 {
		return nil, errors.New("golem: navigation frame delta must be finite and positive")
	}
	n := s.navigation
	if n == nil {
		return nil, nil
	}
	if n.frame != nil {
		return nil, errors.New("golem: previous navigation frame is still open")
	}
	if n.framePrepared && n.framePreparedTick == s.tick {
		return nil, errors.New("golem: navigation movement was already prepared for this tick")
	}
	f := &NavigationFrame{service: n, tick: s.tick, delta: dt,
		agents: make(map[int64]navigationFrameAgent), plans: make(map[int64]*navigationFramePlan)}
	n.frame, n.framePrepared, n.framePreparedTick = f, true, s.tick
	n.advance(dt)
	if n.frame != f || f.closed {
		return nil, fmt.Errorf("%w: frame interrupted during preparation", ErrNavigationFrameFault)
	}
	for {
		if n.frame != f || f.closed {
			return nil, fmt.Errorf("%w: frame interrupted before sealing", ErrNavigationFrameFault)
		}
		f.agents = make(map[int64]navigationFrameAgent)
		f.order = f.order[:0]
		f.barrier = n.navigationFrameBarrier()
		// Traversal preparation can replace attachments or intents. Capture safety
		// after all callbacks, and drop any proposals superseded by a later callback.
		for _, id := range n.order {
			a := n.agents[id]
			if a == nil || !a.config.StagedPhysics {
				continue
			}
			position, _, ok := n.position(id)
			if !ok || !fitsNavigationEntity(position) {
				_ = s.AbortNavigationFrame(f)
				return nil, fmt.Errorf("%w: invalid starting position for entity %d", ErrNavigationFrameFault, id)
			}
			n.ensureStagedSupport(a, position)
			safety := n.agentSafety(a)
			revision := n.backend.CurrentRevision(a.config.Profile)
			pending := n.pending(a.config.Profile)
			ready := !f.barrier && revision != 0 && !a.quarantined
			if safety == nil {
				ready = ready && !pending
			} else {
				ready = ready && safety.SafeSegment(position, position)
			}
			capture := navigationFrameAgent{agent: a, generation: a.state.Generation,
				attachment: a.attachment, position: position, view: a.view, safety: safety,
				revision: revision, ready: ready, pending: pending, traversal: a.traversal}
			if a.traversal != nil {
				capture.bounded, capture.bounds = a.traversal.bounded, a.traversal.bounds
			}
			f.agents[id], f.order = capture, append(f.order, id)
		}
		dropped := false
		for _, id := range f.order {
			plan := f.plans[id]
			if plan == nil {
				continue
			}
			capture := f.agents[id]
			if !capture.ready || plan.agent != capture.agent || plan.generation != capture.generation || plan.traversal != nil && plan.traversal != capture.traversal || len(plan.proposal.Segments) > 0 && plan.proposal.Segments[0].From != capture.position {
				delete(f.plans, id)
				dropped = true
				plan.discard()
				continue
			}
		}
		for id, plan := range f.plans {
			if _, ok := f.agents[id]; !ok {
				delete(f.plans, id)
				dropped = true
				plan.discard()
			}
		}
		// Discard callbacks may supersede other prepared intents. Each pass removes
		// at least one plan, so recapture finishes without rerunning fair scheduling.
		if !dropped {
			break
		}
	}
	for _, id := range f.order {
		if plan := f.plans[id]; plan != nil {
			f.proposals = append(f.proposals, plan.proposal)
		}
	}
	return f, nil
}

// Proposals returns independent copies of this frame's movement proposals.
func (f *NavigationFrame) Proposals() []NavigationMotionProposal {
	if f == nil {
		return nil
	}
	out := make([]NavigationMotionProposal, len(f.proposals))
	for i, proposal := range f.proposals {
		out[i] = proposal
		out[i].Segments = append([]NavigationMotionSegment(nil), proposal.Segments...)
	}
	return out
}

// Contains reports whether the frame captured a staged navigation attachment.
func (f *NavigationFrame) Contains(id int64) bool {
	if f == nil {
		return false
	}
	_, ok := f.agents[id]
	return ok
}

// Frozen reports whether captured readiness requires this attachment to remain
// stationary throughout the solve. The host must impose a physical constraint
// before simulation, including for idle bodies, rather than restore one body
// after it has exchanged impulses. Unknown attachments are conservatively frozen.
func (f *NavigationFrame) Frozen(id int64) bool {
	if f == nil {
		return true
	}
	a, ok := f.agents[id]
	return !ok || !a.ready
}

// SafeSegment checks physical anchor motion against the immutable captured
// safety, including idle attachments and active traversal bounds. Remaining at
// the captured position is allowed while quarantined; leaving it is not.
func (f *NavigationFrame) SafeSegment(id int64, from, to surface.Vec3) bool {
	if f == nil || !fitsNavigationEntity(from) || !fitsNavigationEntity(to) {
		return false
	}
	a, ok := f.agents[id]
	if !ok {
		return false
	}
	if from == a.position && to == a.position {
		return true
	}
	if !a.ready || a.bounded && (!traversalBoundsContain(a.bounds, from) || !traversalBoundsContain(a.bounds, to)) {
		return false
	}
	return a.safety == nil || a.safety.SafeSegment(from, to)
}

// SafeBounds checks a physical swept volume against captured regional safety
// and any declared traversal bounds. Unknown attachments are rejected.
func (f *NavigationFrame) SafeBounds(id int64, min, max surface.Vec3) bool {
	if f == nil || !validTraversalBounds(NavigationTraversalBounds{Min: min, Max: max}) {
		return false
	}
	a, ok := f.agents[id]
	if !ok || !a.ready || a.bounded && (!traversalBoundsContain(a.bounds, min) || !traversalBoundsContain(a.bounds, max)) {
		return false
	}
	return a.safety == nil || a.safety.SafeBounds(min, max)
}

// ValidateNavigationFrame checks generation, lifetime and source positions before
// simulation. It neither writes entities nor commits proposed route progress.
func (s *Server) ValidateNavigationFrame(f *NavigationFrame) error {
	if f == nil {
		return nil
	}
	n := s.navigation
	if n == nil || f.service != n || n.frame != f || f.closed || f.tick != s.tick {
		return fmt.Errorf("%w: foreign, closed or stale frame", ErrNavigationFrameFault)
	}
	if n.navigationFrameBarrier() != f.barrier {
		return fmt.Errorf("%w: navigation restoration changed", ErrNavigationFrameFault)
	}
	for _, id := range f.order {
		capture := f.agents[id]
		a := n.agents[id]
		if a != capture.agent || a.attachment != capture.attachment || a.state.Generation != capture.generation || a.view != capture.view || a.traversal != capture.traversal {
			return fmt.Errorf("%w: attachment or intent changed for entity %d", ErrNavigationFrameFault, id)
		}
		position, _, ok := n.position(id)
		if !ok || position != capture.position {
			return fmt.Errorf("%w: source position changed for entity %d", ErrNavigationFrameFault, id)
		}
		if n.backend.CurrentRevision(a.config.Profile) != capture.revision {
			return fmt.Errorf("%w: navigation publication changed for entity %d", ErrNavigationFrameFault, id)
		}
		fresh := n.agentSafety(a)
		if (fresh == nil) != (capture.safety == nil) || fresh != nil && fresh.Token() != capture.safety.Token() {
			return fmt.Errorf("%w: regional safety changed for entity %d", ErrNavigationFrameFault, id)
		}
		if fresh == nil && n.pending(a.config.Profile) != capture.pending {
			return fmt.Errorf("%w: navigation pending state changed for entity %d", ErrNavigationFrameFault, id)
		}
	}
	return nil
}

func (n *navigationService) navigationFrameBarrier() bool {
	if n.restoring || n.restoreErr != nil {
		return true
	}
	for _, profile := range n.config.RequiredProfiles {
		if n.profileUnavailable(profile) {
			return true
		}
	}
	return false
}

// CommitNavigationFrame validates all physical paths before writing any entity.
// Invalid paths return ErrNavigationFrameFault and leave the frame open for Abort;
// the physics host must fault or restore the whole interacting frame. Loss of
// support after valid physical motion instead retains that pose and reports
// Unsupported through the ordinary navigation state.
func (s *Server) CommitNavigationFrame(f *NavigationFrame, results []NavigationMotionResult) error {
	accepted, err := s.validateNavigationResults(f, results)
	if err != nil {
		return err
	}
	if f != nil {
		s.commitNavigationResults(f, accepted)
	}
	return nil
}

func (s *Server) validateNavigationResults(f *NavigationFrame, results []NavigationMotionResult) (map[int64]surface.Vec3, error) {
	if f == nil {
		if len(results) != 0 {
			return nil, fmt.Errorf("%w: results without a navigation frame", ErrNavigationFrameFault)
		}
		return nil, nil
	}
	if err := s.ValidateNavigationFrame(f); err != nil {
		return nil, err
	}
	accepted := make(map[int64]surface.Vec3, len(results))
	for _, result := range results {
		capture, ok := f.agents[result.EntityID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown entity %d", ErrNavigationFrameFault, result.EntityID)
		}
		if _, duplicate := accepted[result.EntityID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate entity %d", ErrNavigationFrameFault, result.EntityID)
		}
		if !fitsNavigationEntity(result.Position) || len(result.Path) > 1024 {
			return nil, fmt.Errorf("%w: invalid physical result for entity %d", ErrNavigationFrameFault, result.EntityID)
		}
		position := navigationFramePosition(result.Position)
		if len(result.Path) == 0 {
			if position != capture.position {
				return nil, fmt.Errorf("%w: moving entity %d requires its physical path", ErrNavigationFrameFault, result.EntityID)
			}
		} else {
			if navigationFramePosition(result.Path[0]) != capture.position || navigationFramePosition(result.Path[len(result.Path)-1]) != position {
				return nil, fmt.Errorf("%w: physical path endpoints mismatch for entity %d", ErrNavigationFrameFault, result.EntityID)
			}
			previous := capture.position
			for _, point := range result.Path {
				if !fitsNavigationEntity(point) {
					return nil, fmt.Errorf("%w: invalid path point for entity %d", ErrNavigationFrameFault, result.EntityID)
				}
				point = navigationFramePosition(point)
				if !f.SafeSegment(result.EntityID, previous, point) {
					return nil, fmt.Errorf("%w: unsafe physical path for entity %d", ErrNavigationFrameFault, result.EntityID)
				}
				previous = point
			}
		}
		accepted[result.EntityID] = position
	}
	for id := range f.plans {
		if _, ok := accepted[id]; !ok {
			return nil, fmt.Errorf("%w: missing physical result for entity %d", ErrNavigationFrameFault, id)
		}
	}
	return accepted, nil
}

func (s *Server) commitNavigationResults(f *NavigationFrame, accepted map[int64]surface.Vec3) {
	// No game callback or pose setter runs until every result has passed. Physics
	// setters are required to be data-only, as with ordinary Position3DWriter.
	for _, id := range f.order {
		if position, ok := accepted[id]; ok {
			_, writer, _ := f.service.position(id)
			writer.SetPosition3D(float32(position.X), float32(position.Y), float32(position.Z))
		}
	}
	f.closed, f.service.frame = true, nil
	f.service.frameCallbacks = make([]func(), 0)
	for _, id := range f.order {
		position, changed := accepted[id]
		if !changed {
			continue
		}
		capture := f.agents[id]
		a := f.service.agents[id]
		if a != capture.agent || a.state.Generation != capture.generation {
			continue
		}
		if plan := f.plans[id]; plan != nil {
			f.acceptPlan(plan, capture.position, position)
		} else {
			f.reconcileAffected(a, position)
		}
	}
	for _, plan := range f.plans {
		plan.discard()
	}
	callbacks := f.service.frameCallbacks
	f.service.frameCallbacks = nil
	for _, callback := range callbacks {
		callback()
	}
}

func (n *navigationService) dispatchFrameCallback(callback func()) {
	if n.frameCallbacks != nil {
		n.frameCallbacks = append(n.frameCallbacks, callback)
		return
	}
	callback()
}

// AbortNavigationFrame discards prepared traversal state without changing body
// positions. It is idempotent for a frame belonging to this server.
func (s *Server) AbortNavigationFrame(f *NavigationFrame) error {
	if f == nil {
		return nil
	}
	if f.service != s.navigation {
		return errors.New("golem: foreign navigation frame")
	}
	if f.closed {
		return nil
	}
	if f.service.frame != f {
		return errors.New("golem: navigation frame is not current")
	}
	f.closed, f.service.frame = true, nil
	for _, plan := range f.plans {
		plan.discard()
	}
	return nil
}

func navigationFramePosition(p surface.Vec3) surface.Vec3 {
	return surface.Vec3{X: float64(float32(p.X)), Y: float64(float32(p.Y)), Z: float64(float32(p.Z))}
}

func (n *navigationService) ensureStagedSupport(a *navigationAgent, position surface.Vec3) {
	if n.server.runCtx == nil || a.view != nil || a.state.Mode != NavigationIdle && a.state.Mode != NavigationArrived || n.profileUnavailable(a.config.Profile) {
		return
	}
	view, err := n.backend.Acquire(n.server.runCtx, a.config.Profile)
	if err != nil || view == nil {
		return
	}
	query, err := view.NewQuery(n.config.Limits)
	if err != nil {
		view.Release()
		return
	}
	tolerance := a.config.SupportTolerance
	projection := query.Project(n.server.runCtx, position,
		surface.ProjectionOptions{Horizontal: tolerance, Up: tolerance, Down: tolerance}, a.config.Filter)
	if projection.Code != surface.Complete || projection.Location == nil || navDistance(position, projection.Location.Position()) > tolerance {
		view.Release()
		if a.supportEstablished {
			a.state.Mode, a.state.Status = NavigationUnsupported, projection.Status
		}
		return
	}
	a.view, a.query, a.location = view, query, projection.Location
	a.state.WorldRevision, a.supportEstablished = view.Revision(), true
	a.locationSafetyChecked, a.hasReconciledPosition = false, false
}

func (f *NavigationFrame) stage(a *navigationAgent, position surface.Vec3, dt float64) {
	n := f.service
	// Fair scheduling can accumulate legacy movement time across skipped ticks.
	// A shared solver may integrate only this frame's actual elapsed time.
	dt = math.Min(dt, f.delta)
	if a.state.Mode == NavigationTraversing {
		f.stageTraversal(a, position, dt)
		return
	}
	if a.state.Mode != NavigationFollowing || a.view == nil {
		return
	}
	plan := &navigationFramePlan{agent: a, generation: a.state.Generation, location: a.location,
		next: a.next, speed: a.speed, proposal: NavigationMotionProposal{EntityID: a.id, Generation: a.state.Generation, Delta: dt}}
	if a.initialProjection {
		target := navigationFramePosition(a.location.Position())
		if !fitsNavigationEntity(target) || !n.safeSegment(a, position, target) {
			return
		}
		plan.proposal.Segments = append(plan.proposal.Segments, NavigationMotionSegment{From: position, Target: target, Projection: true})
		plan.projected, position = true, target
	} else if !n.reconcileAgent(a, position) {
		return
	} else {
		plan.location, plan.next = a.location, a.next
	}
	steering := plan.location.Position()
	for plan.next < len(a.corners) && a.corners[plan.next].Link == nil && navDistance(steering, a.corners[plan.next].Position) <= .01 {
		plan.next++
	}
	draft := *a
	draft.next = plan.next
	remaining := n.remainingDistance(&draft, steering)
	if remaining <= a.config.StoppingDistance {
		if !plan.projected {
			n.finishRoute(a)
			return
		}
		f.plans[a.id] = plan
		return
	}
	targetSpeed := a.config.Speed
	if a.config.Acceleration > 0 {
		brake := math.Max(0, remaining-a.config.StoppingDistance)
		if plan.next+1 < len(a.corners) {
			first, second := a.corners[plan.next].Position, a.corners[plan.next+1].Position
			ax, az, bx, bz := first.X-steering.X, first.Z-steering.Z, second.X-first.X, second.Z-first.Z
			denominator := math.Hypot(ax, az) * math.Hypot(bx, bz)
			if denominator > 1e-8 && (ax*bx+az*bz)/denominator < .5 {
				brake = math.Min(brake, navDistance(steering, first))
			}
		}
		targetSpeed = math.Min(targetSpeed, math.Sqrt(2*a.config.Acceleration*brake))
		change := a.config.Acceleration * dt
		if targetSpeed > plan.speed {
			plan.speed = math.Min(targetSpeed, plan.speed+change)
		} else {
			plan.speed = math.Max(targetSpeed, plan.speed-change)
		}
	} else {
		plan.speed = targetSpeed
	}
	budget := plan.speed * dt
	for segment := 0; segment < 8 && budget > 0 && plan.next < len(a.corners); segment++ {
		corner := a.corners[plan.next]
		distance := navDistance(steering, corner.Position)
		if distance <= .01 {
			if corner.Link != nil {
				if len(plan.proposal.Segments) == 0 {
					n.beginTraversal(a, *corner.Link)
					return
				}
				break
			}
			plan.next++
			continue
		}
		step := math.Min(distance, budget)
		target := surface.Vec3{X: steering.X + (corner.Position.X-steering.X)*step/distance,
			Y: steering.Y + (corner.Position.Y-steering.Y)*step/distance,
			Z: steering.Z + (corner.Position.Z-steering.Z)*step/distance}
		if !n.safeSegment(a, position, target) {
			return
		}
		moved := a.query.Move(n.server.runCtx, plan.location, target, a.config.Filter)
		if moved.Location == nil || moved.Code != surface.Complete && moved.Code != surface.Partial {
			n.failAgent(a, moved.Code, moved.Err)
			return
		}
		actual := navigationFramePosition(moved.Location.Position())
		if !fitsNavigationEntity(actual) || !n.safeSegment(a, position, actual) {
			return
		}
		plan.proposal.Segments = append(plan.proposal.Segments, NavigationMotionSegment{
			From: position, Target: actual, SurfaceNormal: moved.Normal, Delta: step / plan.speed})
		plan.location = moved.Location
		progress := navDistance(position, actual)
		position, steering, budget = actual, moved.Location.Position(), budget-step
		if progress <= 1e-5 || !sameNavigationHorizontal(actual, target) {
			break
		}
	}
	if len(plan.proposal.Segments) > 0 {
		f.plans[a.id] = plan
	}
}

func (f *NavigationFrame) stageTraversal(a *navigationAgent, position surface.Vec3, dt float64) {
	n := f.service
	traversal := a.traversal
	if traversal == nil {
		n.failAgent(a, surface.Failed, errors.New("golem: missing staged traversal action"))
		return
	}
	action, ok := traversal.action.(NavigationStagedTraversalAction)
	if !ok {
		n.failAgent(a, surface.InvalidInput, errors.New("golem: staged traversal action is not transactional"))
		return
	}
	if traversal.elapsed+dt >= a.config.TraversalTimeout.Seconds() {
		n.failAgent(a, surface.Cancelled, errors.New("golem: staged traversal timed out"))
		return
	}
	generation := a.state.Generation
	update, err := action.PrepareNavigationTraversal(dt)
	if err != nil {
		if n.current(a, generation) {
			n.failAgent(a, surface.Failed, err)
		}
		return
	}
	plan := &navigationFramePlan{agent: a, generation: generation, traversal: traversal,
		action: action, actionReady: true, update: update,
		proposal: NavigationMotionProposal{EntityID: a.id, Generation: generation, Delta: dt, Traversal: true}}
	if !n.movementCurrent(a, generation) || !fitsNavigationEntity(update.Position) ||
		traversal.bounded && !traversalBoundsContain(traversal.bounds, update.Position) || !n.safeSegment(a, position, update.Position) {
		plan.discard()
		return
	}
	plan.proposal.Segments = []NavigationMotionSegment{{From: position, Target: navigationFramePosition(update.Position), Delta: dt}}
	f.plans[a.id] = plan
}

func (p *navigationFramePlan) discard() {
	if p.actionReady {
		p.actionReady = false
		p.action.DiscardNavigationTraversal()
	}
}

func (f *NavigationFrame) reconcileAffected(a *navigationAgent, position surface.Vec3) {
	n := f.service
	if a.view != nil && a.location != nil && a.state.Mode != NavigationTraversing {
		n.reconcileAgent(a, position)
	}
	a.lastPosition = position
}

func (f *NavigationFrame) acceptPlan(plan *navigationFramePlan, from, position surface.Vec3) {
	n, a := f.service, plan.agent
	if plan.traversal != nil {
		f.acceptTraversal(plan, position)
		return
	}
	a.speed = plan.speed
	a.initialProjection = false
	a.hasReconciledPosition = false
	segments := plan.proposal.Segments
	if len(segments) > 0 && position == segments[len(segments)-1].Target {
		a.location, a.next = plan.location, plan.next
		a.locationSafetyChecked = false
	} else {
		a.corridorDeviated = true
	}
	if !n.reconcileAgent(a, position) {
		a.lastPosition = position
		return
	}
	a.supportEstablished = true
	if n.remainingDistance(a, a.location.Position()) <= a.config.StoppingDistance || a.next >= len(a.corners) {
		n.finishRoute(a)
	}
	dt := plan.proposal.Delta
	if dt > 0 && a.state.Mode == NavigationFollowing {
		a.state.Velocity = surface.Vec3{X: (position.X - from.X) / dt, Y: (position.Y - from.Y) / dt, Z: (position.Z - from.Z) / dt}
	}
	if navDistance(position, from) < math.Min(1e-4, a.speed*dt*.01) && a.state.Mode == NavigationFollowing {
		a.stallTime += dt
		if a.stallTime >= .5 {
			if a.stallReplans >= a.config.MaxStallReplans {
				n.failAgent(a, surface.Unreachable, errors.New("golem: staged navigation stalled after bounded replanning"))
			} else {
				a.stallReplans++
				n.invalidate(a)
				a.needsPath, a.state.Mode = true, NavigationPending
			}
		}
	} else {
		a.stallTime = 0
		if navDistance(position, from) >= math.Min(1e-4, a.speed*dt*.01) && a.speed > 0 {
			a.stallReplans = 0
		}
	}
	a.lastPosition = position
}

func (f *NavigationFrame) acceptTraversal(plan *navigationFramePlan, position surface.Vec3) {
	n, a, traversal := f.service, plan.agent, plan.traversal
	traversal.elapsed += plan.proposal.Delta
	a.lastPosition = position
	update := NavigationTraversalUpdate{Position: position}
	if plan.update.Complete {
		tolerance := a.config.SupportTolerance
		landing := a.query.Project(n.server.runCtx, position,
			surface.ProjectionOptions{Horizontal: tolerance, Up: tolerance, Down: tolerance}, a.config.Filter)
		if landing.Code != surface.Complete || landing.Location == nil || navDistance(position, landing.Location.Position()) > tolerance || navDistance(landing.Location.Position(), traversal.link.To) > .1 {
			plan.actionReady = false
			n.dispatchFrameCallback(plan.action.DiscardNavigationTraversal)
			n.failAgent(a, surface.NoSurface, errors.New("golem: staged traversal landing is unsupported or missed its endpoint"))
			return
		}
		a.location, a.locationSafetyChecked = landing.Location, false
		a.traversal = nil
		a.next++
		a.state.Mode = NavigationFollowing
		update.Complete = true
	}
	plan.actionReady = false
	n.dispatchFrameCallback(func() { plan.action.CommitNavigationTraversal(update) })
}
