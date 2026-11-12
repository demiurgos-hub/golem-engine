package golem

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

// ErrNavigationQueueFull reports bounded navigation work queue saturation.
var ErrNavigationQueueFull = errors.New("golem: navigation queue is full")

// NavigationConfig reserves independent path and rebuild capacity. Time budgets
// stop between operations; backend operations and commit callbacks must be bounded.
type NavigationConfig struct {
	PathWorkers, BuildWorkers               int
	PathQueueCapacity, BuildQueueCapacity   int
	BuildHistoryCapacity                    int
	BuildDispatchesPerTick, BuildRetryLimit int
	BuildDebounce, BuildMaxWait             time.Duration
	BuildRetryInitial, BuildRetryMax        time.Duration
	CompletionCapacity                      int
	PathResultsPerTick, BuildCommitsPerTick int
	PathDrainBudget, BuildDrainBudget       time.Duration
	MovementAgentsPerTick                   int
	MovementBudget                          time.Duration
	// RebaseAgentsPerTick and RebaseBudget bound fair snapshot transfers when
	// the backend protects movement from all still-retained older views.
	RebaseAgentsPerTick int
	RebaseBudget        time.Duration
	MaxStep             time.Duration
	// PathDeadline bounds active worker time, excluding queue wait (default 250 ms).
	PathDeadline     time.Duration
	Limits           surface.Limits
	RequiredProfiles []surface.ProfileID
	// ObserveTick receives navigation-only timings after reconciliation. It must
	// not block, mutate server state, or call navigation lifecycle APIs.
	ObserveTick func(NavigationTickMetrics)
	// ObservePathAdmission runs on the tick goroutine immediately after a valid
	// worker result becomes an owned following route. It must not block or mutate
	// server state. Observer execution is excluded from ObserveTick and included
	// in full tick time.
	ObservePathAdmission func(NavigationPathAdmissionMetrics)
}

// NavigationPathAdmissionMetrics timestamps successful route admission, after
// revision, support and regional-safety validation. Failed or superseded work
// does not emit admission. Callers measure intent latency from their own issue
// timestamp from NavigationNow; compare timestamps with Time.Sub, not time.Since.
// Generation identifies the accepted worker generation.
type NavigationPathAdmissionMetrics struct {
	EntityID                        int64
	Tick, Generation, WorldRevision uint64
	// AdmittedAt uses NavigationNow's process-local high-resolution clock.
	AdmittedAt time.Time
	Status     surface.Status
}

// NavigationTickMetrics measures navigation work on the tick goroutine. Worker
// work and observer execution are excluded; durations include policy callbacks.
type NavigationTickMetrics struct {
	Tick                         uint64
	Prepare, Movement, Reconcile time.Duration
	AgentsScheduled              int
	// Rebase is already included in Movement; do not add it again to tick cost.
	Rebase                        time.Duration
	RebaseAttempts, RebasedAgents int
}

// NavigationQueueStats reports independently sampled bounded pool counters.
type NavigationQueueStats struct {
	Delayed                                      int
	Queued, Running, Completions                 int
	Accepted, Rejected, Finished                 uint64
	QueueLatency, WorkLatency, CompletionLatency [8]uint64
	QueueWaitMax, WorkMax, CompletionLagMax      time.Duration
}

// NavigationLatencyUpperBounds returns seven inclusive histogram bounds;
// bucket eight contains larger durations. Samples are observations, not promises.
func NavigationLatencyUpperBounds() [7]time.Duration {
	return [7]time.Duration{100 * time.Microsecond, 500 * time.Microsecond, time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond, 250 * time.Millisecond}
}

// NavigationStats describes navigation queues independently of general tasks.
type NavigationStats struct {
	Paths, Builds NavigationQueueStats
}

// NavigationReadiness reports restoration and required-profile availability.
type NavigationReadiness struct {
	Ready, Restoring bool
	// Degraded means a committed regional world is usable outside quarantine.
	Degraded        bool
	MissingProfiles []surface.ProfileID
	PendingProfiles []surface.ProfileID
	Err             error
}

// NavigationMode is the server-controlled agent's current movement state.
type NavigationMode uint8

const (
	NavigationIdle NavigationMode = iota
	NavigationPending
	NavigationFollowing
	NavigationArrived
	NavigationBlocked
	NavigationUnsupported
	NavigationTraversing
	NavigationFailed
)

// NavigationState is a copied, tick-owned view of an agent's current intent.
type NavigationState struct {
	Mode           NavigationMode
	Status         surface.Status
	Destination    surface.Vec3
	HasDestination bool
	Velocity       surface.Vec3
	Generation     uint64
	WorldRevision  uint64
	// Quarantined means this agent is waiting for a regional safety change.
	Quarantined bool
	// PartialPath is a copied diagnostic prefix when partial following was rejected.
	PartialPath []surface.Corner
}

// NavigationMotion is a surface-constrained displacement offered to a body motor.
type NavigationMotion struct {
	EntityID      int64
	From, Target  surface.Vec3
	SurfaceNormal surface.Vec3
	Delta         float64
}

// NavigationMotor applies body collision policy on the tick goroutine and
// returns the actual position. The service writes that position to the entity.
// A nil motor selects navmesh-only movement; overlap detection is not a motor.
type NavigationMotor interface {
	MoveNavigation(NavigationMotion) (surface.Vec3, error)
}

// NavigationAgentConfig selects an already-baked profile and movement policy.
// Radius/height are deliberately absent: runtime steering cannot alter clearance.
type NavigationAgentConfig struct {
	Profile          surface.ProfileID
	Speed            float64
	Acceleration     float64
	StoppingDistance float64
	Projection       surface.ProjectionOptions
	Filter           surface.Filter
	Priority         int
	// SupportTolerance bounds the body's distance from its navigation surface.
	// Zero defaults to 0.05 world units. Choose it from bake/body height error,
	// below the separation of distinct floors and within the projection extents.
	SupportTolerance float64
	// AllowPartial explicitly permits following a reachable path prefix.
	AllowPartial bool
	// TraversalTimeout counts processed movement time; zero defaults to 30 s.
	// Values above 24 hours are rejected.
	TraversalTimeout time.Duration
	// MaxStallReplans defaults to three retries before reporting Unreachable.
	MaxStallReplans int
	Motor           NavigationMotor
	Traversal       NavigationTraversal
	// StagedPhysics defers movement to PrepareNavigationFrame and its matching
	// CommitNavigationFrame. It cannot be combined with a synchronous Motor.
	StagedPhysics bool
}

type navigationAgent struct {
	id                    int64
	attachment            uint64
	config                NavigationAgentConfig
	state                 NavigationState
	view                  surface.View
	query                 surface.Query
	location              surface.Location
	route                 surface.Route
	corners               []surface.Corner
	next                  int
	requesting            bool
	needsPath             bool
	admissionPending      bool
	speed                 float64
	lastTick              uint64
	lastPosition          surface.Vec3
	lastRepair            surface.Vec3
	stallTime             float64
	stallReplans          int
	keepSupport           bool
	initialProjection     bool
	supportEstablished    bool
	traversal             *navigationTraversal
	quarantined           bool
	safetyToken           uint64
	quarantineRevision    uint64
	resumeMode            NavigationMode
	resumeStatus          surface.Status
	routeSafetyChecked    bool
	routeSafetyToken      uint64
	locationSafetyChecked bool
	locationSafetyToken   uint64
	positionSafetyChecked bool
	positionSafetyToken   uint64
	safePosition          surface.Vec3
	segmentSafetyChecked  bool
	segmentSafetyToken    uint64
	safeSegmentFrom       surface.Vec3
	safeSegmentTo         surface.Vec3
	repairLocation        surface.Location
	corridorDeviated      bool
	reconciledPosition    surface.Vec3
	hasReconciledPosition bool
}

type navigationService struct {
	server                 *Server
	backend                surface.Backend
	config                 NavigationConfig
	paths, builds          *navigationPool
	agents                 map[int64]*navigationAgent
	deleting               map[int64]bool
	order                  []int64
	cursor                 int
	rebaseCursor           int
	rebaseRevisions        map[surface.ProfileID]uint64
	moved                  []int64
	attachment             uint64
	restoring              bool
	restoreErr             error
	buildGeneration        map[string]uint64
	buildSequence          uint64
	buildStates            map[string]NavigationBuildState
	buildRequests          map[string]*navigationBuildRequest
	buildClock             func() time.Time
	delayedBuilds          atomic.Int64
	tickMetrics            NavigationTickMetrics
	pathObservationElapsed time.Duration
	frame                  *NavigationFrame
	framePreparedTick      uint64
	framePrepared          bool
	frameCallbacks         []func()
}

// SetSurfaceNavigation installs a 3D navigation service before Run. Existing 2D
// navigation remains independent. Backend snapshots must be immutable; workers
// never read entity pointers or mutate a published navigation mesh.
func (s *Server) SetSurfaceNavigation(backend surface.Backend, cfg NavigationConfig) error {
	if backend == nil {
		return errors.New("golem: navigation backend is nil")
	}
	if err := normalizeNavigationConfig(&cfg); err != nil {
		return err
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleCreated {
		return errors.New("golem: configure navigation before Run")
	}
	if s.navigation != nil {
		return errors.New("golem: navigation is already configured")
	}
	s.navigation = &navigationService{server: s, backend: backend, config: cfg,
		paths:  newNavigationPool(cfg.PathWorkers, cfg.PathQueueCapacity, cfg.CompletionCapacity),
		builds: newNavigationPool(cfg.BuildWorkers, cfg.BuildQueueCapacity, cfg.CompletionCapacity),
		agents: make(map[int64]*navigationAgent), deleting: make(map[int64]bool), buildGeneration: make(map[string]uint64),
		buildStates: make(map[string]NavigationBuildState), buildRequests: make(map[string]*navigationBuildRequest), buildClock: NavigationNow}
	return nil
}

// AttachNavigation attaches a 3D position reader/writer by stable entity ID.
// Call during startup or on the tick goroutine. DeleteEntity detaches it before
// OnRemove. Attaching does not move or project an entity until it gets an intent.
func (s *Server) AttachNavigation(id int64, cfg NavigationAgentConfig) error {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return ErrServerNotRunning
	}
	n := s.navigation
	if n == nil {
		return errors.New("golem: surface navigation is not configured")
	}
	if n.deleting[id] {
		return errors.New("golem: cannot attach navigation while entity deletion is in progress")
	}
	if _, exists := n.agents[id]; exists {
		return errors.New("golem: entity already has navigation")
	}
	e, exists := s.Get(id)
	if !exists {
		return fmt.Errorf("golem: navigation entity %d does not exist", id)
	}
	if _, ok := e.(registry.Spatial3DEntity); !ok {
		return errors.New("golem: navigation requires Position3D")
	}
	if _, ok := e.(registry.Position3DWriter); !ok {
		return errors.New("golem: navigation requires SetPosition3D")
	}
	if err := validateNavigationAgent(&cfg); err != nil {
		return err
	}
	if s.physics != nil && s.physics.agents[id] != nil && !cfg.StagedPhysics {
		return errors.New("golem: a physics attachment requires staged navigation")
	}
	n.attachment++
	n.agents[id] = &navigationAgent{id: id, attachment: n.attachment, config: cfg,
		state: NavigationState{Mode: NavigationIdle}, lastTick: s.tick}
	n.order = append(n.order, id)
	return nil
}

// DetachNavigation cancels an entity's requests and releases its route. It is
// idempotent and runs on the tick goroutine (or before Run).
func (s *Server) DetachNavigation(id int64) {
	if n := s.navigation; n != nil {
		if a := n.agents[id]; a != nil {
			n.paths.cancelKey(strconv.FormatInt(id, 10))
			delete(n.agents, id)
			for i, existing := range n.order {
				if existing == id {
					n.order = append(n.order[:i], n.order[i+1:]...)
					if i < n.rebaseCursor {
						n.rebaseCursor--
					}
					break
				}
			}
			if n.cursor >= len(n.order) {
				n.cursor = 0
			}
			if n.rebaseCursor >= len(n.order) {
				n.rebaseCursor = 0
			}
			// Remove the old attachment before user cancellation can attach a replacement.
			n.cancelTraversal(a, "agent removed")
			n.releaseRoute(a)
		}
	}
}

// SetNavigationDestination replaces intent and stops the previous route
// immediately. Work is admitted on subsequent ticks; saturation remains visible
// as Pending/ResourceExhausted and retries fairly without blocking the tick.
func (s *Server) SetNavigationDestination(id int64, destination surface.Vec3) error {
	if !destination.Finite() {
		return errors.New("golem: navigation destination must be finite")
	}
	n, a, err := s.navigationAgent(id)
	if err != nil {
		return err
	}
	if !n.cancelTraversal(a, "destination changed") {
		return nil
	}
	n.invalidate(a)
	a.state.Destination, a.state.HasDestination = destination, true
	a.stallReplans = 0
	a.state.Mode = NavigationPending
	a.needsPath = true
	return nil
}

// StopNavigation cancels the destination, route and any active traversal.
func (s *Server) StopNavigation(id int64) error {
	n, a, err := s.navigationAgent(id)
	if err != nil {
		return err
	}
	if !n.cancelTraversal(a, "movement stopped") {
		return nil
	}
	n.invalidate(a)
	a.state.HasDestination, a.needsPath = false, false
	a.state.Mode = NavigationIdle
	a.state.Status = surface.Status{}
	return nil
}

// ReprojectNavigation explicitly resumes navigation after teleport, forced
// movement, or landing. Unsupported agents never snap to another floor on their own.
func (s *Server) ReprojectNavigation(id int64) error {
	n, a, err := s.navigationAgent(id)
	if err != nil {
		return err
	}
	if !n.cancelTraversal(a, "position externally changed") {
		return nil
	}
	n.invalidate(a)
	a.keepSupport = false
	a.supportEstablished = false
	a.stallReplans = 0
	a.needsPath = a.state.HasDestination
	if a.needsPath {
		a.state.Mode = NavigationPending
	} else {
		a.state.Mode = NavigationIdle
	}
	return nil
}

// NavigationAgentState returns a copied state on the tick goroutine or before Run.
func (s *Server) NavigationAgentState(id int64) (NavigationState, bool) {
	if n := s.navigation; n != nil {
		if a := n.agents[id]; a != nil {
			state := a.state
			state.PartialPath = append([]surface.Corner(nil), state.PartialPath...)
			for i := range state.PartialPath {
				if link := state.PartialPath[i].Link; link != nil {
					copy := *link
					state.PartialPath[i].Link = &copy
				}
			}
			return state, true
		}
	}
	return NavigationState{}, false
}

// NavigationStats reports concurrent-safe queue gauges and cumulative counters.
func (s *Server) NavigationStats() NavigationStats {
	s.lifeMu.Lock()
	n := s.navigation
	s.lifeMu.Unlock()
	if n == nil {
		return NavigationStats{}
	}
	stats := NavigationStats{Paths: n.paths.stats(), Builds: n.builds.stats()}
	stats.Builds.Delayed = int(n.delayedBuilds.Load())
	return stats
}

// NavigationReady reports availability on the tick or before Run. Committed
// regional worlds with pending edits or retiring-view quarantine remain Ready
// and report Degraded; missing profiles, restoration and profile-wide pending
// backends close readiness.
func (s *Server) NavigationReady() NavigationReadiness {
	n := s.navigation
	if n == nil {
		return NavigationReadiness{Err: errors.New("golem: navigation is not configured")}
	}
	r := NavigationReadiness{Restoring: n.restoring, Err: n.restoreErr}
	profiles := make(map[surface.ProfileID]bool)
	for _, profile := range n.config.RequiredProfiles {
		profiles[profile] = true
	}
	for _, agent := range n.agents {
		profiles[agent.config.Profile] = true
	}
	for profile := range profiles {
		if n.backend.CurrentRevision(profile) == 0 {
			r.MissingProfiles = append(r.MissingProfiles, profile)
		}
		if n.pending(profile) {
			r.PendingProfiles = append(r.PendingProfiles, profile)
		}
		if retained, ok := n.regionalSafety(profile).(surface.RetainedRegionalSafety); ok && retained.HasQuarantine() {
			r.Degraded = true
		}
	}
	blockingPending := false
	for _, profile := range r.PendingProfiles {
		if n.profileUnavailable(profile) {
			blockingPending = true
		} else {
			r.Degraded = true
		}
	}
	r.Ready = !r.Restoring && r.Err == nil && len(r.MissingProfiles) == 0 && !blockingPending
	sort.Slice(r.MissingProfiles, func(i, j int) bool { return r.MissingProfiles[i] < r.MissingProfiles[j] })
	sort.Slice(r.PendingProfiles, func(i, j int) bool { return r.PendingProfiles[i] < r.PendingProfiles[j] })
	return r
}

// BeginNavigationRestore closes the agent readiness barrier. Restore stable
// desired world state before calling CompleteNavigationRestore.
func (s *Server) BeginNavigationRestore() error {
	if s.navigation == nil {
		return errors.New("golem: navigation is not configured")
	}
	n := s.navigation
	if n.frame != nil {
		_ = s.AbortNavigationFrame(n.frame)
	}
	n.restoring, n.restoreErr = true, nil
	for _, a := range n.agents {
		if !n.cancelTraversal(a, "world restoration") {
			continue
		}
		n.invalidate(a)
		a.needsPath = a.state.HasDestination
		if a.needsPath {
			a.state.Mode = NavigationPending
		}
	}
	return nil
}

// CompleteNavigationRestore opens readiness only on success. A restoration
// failure remains explicit and blocks movement until a new restore succeeds.
func (s *Server) CompleteNavigationRestore(err error) error {
	if s.navigation == nil {
		return errors.New("golem: navigation is not configured")
	}
	s.navigation.restoring, s.navigation.restoreErr = false, err
	return nil
}

func (s *Server) navigationAgent(id int64) (*navigationService, *navigationAgent, error) {
	s.lifeMu.Lock()
	stopping := s.life == lifecycleStopping || s.life == lifecycleStopped
	s.lifeMu.Unlock()
	if stopping {
		return nil, nil, ErrServerNotRunning
	}
	n := s.navigation
	if n == nil {
		return nil, nil, errors.New("golem: navigation is not configured")
	}
	a := n.agents[id]
	if a == nil {
		return nil, nil, fmt.Errorf("golem: entity %d has no navigation", id)
	}
	return n, a, nil
}

func (n *navigationService) start(ctx context.Context) { n.paths.start(ctx); n.builds.start(ctx) }

func (n *navigationService) stop() {
	if n.frame != nil {
		_ = n.server.AbortNavigationFrame(n.frame)
	}
	n.stopBuildRequests()
	n.paths.stop()
	n.builds.stop()
	for _, a := range n.agents {
		n.cancelTraversal(a, "server stopped")
		n.releaseRoute(a)
	}
}

func (n *navigationService) releaseRoute(a *navigationAgent) {
	if a.view != nil {
		a.view.Release()
	}
	a.view, a.query, a.location, a.route, a.corners = nil, nil, nil, nil, nil
	a.routeSafetyChecked = false
	a.locationSafetyChecked = false
	a.positionSafetyChecked, a.segmentSafetyChecked = false, false
	a.repairLocation = nil
	a.corridorDeviated, a.hasReconciledPosition = false, false
	a.next, a.speed = 0, 0
	a.initialProjection = false
	a.admissionPending = false
	a.state.Velocity = surface.Vec3{}
}

func (n *navigationService) invalidate(a *navigationAgent) {
	a.state.Generation++
	a.state.PartialPath = nil
	n.paths.cancelKey(strconv.FormatInt(a.id, 10))
	a.requesting = false
	a.quarantined = false
	a.state.Quarantined = false
	n.releaseRoute(a)
}

func (n *navigationService) prepare() {
	n.tickMetrics = NavigationTickMetrics{Tick: n.server.tick}
	n.pathObservationElapsed = 0
	if n.config.ObserveTick != nil {
		started := NavigationNow()
		defer func() { n.tickMetrics.Prepare = navigationSince(started) - n.pathObservationElapsed }()
	}
	if n.server.runCtx == nil || n.server.runCtx.Err() != nil {
		return
	}
	started := NavigationNow()
	n.builds.drain(n.config.BuildCommitsPerTick, n.config.BuildDrainBudget)
	if remaining := n.config.BuildDrainBudget - navigationSince(started); remaining > 0 {
		n.dispatchBuilds(remaining)
	}
	n.paths.drain(n.config.PathResultsPerTick, n.config.PathDrainBudget)
}

func (n *navigationService) advance(dt float64) {
	if n.config.ObserveTick != nil {
		started := NavigationNow()
		observationBefore := n.pathObservationElapsed
		defer func() {
			n.tickMetrics.Movement = navigationSince(started) - (n.pathObservationElapsed - observationBefore)
		}()
	}
	n.moved = n.moved[:0]
	if n.server.runCtx == nil || n.server.runCtx.Err() != nil {
		return
	}
	if n.restoring || n.restoreErr != nil {
		return
	}
	barrier := false
	for _, profile := range n.config.RequiredProfiles {
		if n.profileUnavailable(profile) {
			barrier = true
			break
		}
	}
	if !barrier {
		n.rebaseRetired()
	}
	if n.restoring || n.restoreErr != nil {
		return
	}
	count := len(n.order)
	if count > n.config.MovementAgentsPerTick {
		count = n.config.MovementAgentsPerTick
	}
	started := NavigationNow()
	for i := 0; i < count && len(n.order) > 0; i++ {
		if n.restoring || n.restoreErr != nil {
			break
		}
		if i > 0 && navigationSince(started) >= n.config.MovementBudget {
			break
		}
		if n.cursor >= len(n.order) {
			n.cursor = 0
		}
		id := n.order[n.cursor]
		n.cursor++
		a := n.agents[id]
		if a == nil {
			continue
		}
		elapsed := dt * float64(n.server.tick-a.lastTick)
		a.lastTick = n.server.tick
		if barrier {
			// No agent advances while the barrier is closed; release old work within
			// the same bounded round-robin budget as ordinary movement.
			if !n.cancelTraversal(a, "required navigation profile unavailable") {
				continue
			}
			if a.view != nil || a.requesting {
				n.invalidate(a)
			}
			a.keepSupport = a.supportEstablished
			a.needsPath = a.state.HasDestination
			if a.needsPath {
				a.state.Mode, a.state.Status.Code = NavigationPending, surface.NotReady
			}
			continue
		}
		n.moved = append(n.moved, id)
		n.tickMetrics.AgentsScheduled++
		if elapsed <= 0 {
			elapsed = dt
		}
		if elapsed > n.config.MaxStep.Seconds() {
			elapsed = n.config.MaxStep.Seconds()
		}
		n.advanceAgent(a, elapsed)
	}
}

type navigationPathResult struct {
	revision    uint64
	startFailed bool
	view        surface.View
	start       surface.Location
	result      surface.PathResult
	from        surface.Vec3
}

func discardNavigationPath(value any) {
	if result, ok := value.(*navigationPathResult); ok && result != nil && result.view != nil {
		result.view.Release()
	}
}

func (n *navigationService) requestPath(a *navigationAgent, from surface.Vec3) {
	id, attachment, generation := a.id, a.attachment, a.state.Generation
	requestRevision := n.backend.CurrentRevision(a.config.Profile)
	profile, target, projection, filter := a.config.Profile, a.state.Destination, a.config.Projection, a.config.Filter.Clone()
	keepSupport, supportTolerance := a.keepSupport || a.supportEstablished, a.config.SupportTolerance
	projection.Hint = nil
	startProjection := projection
	if keepSupport {
		startProjection = surface.ProjectionOptions{Horizontal: supportTolerance, Up: supportTolerance, Down: supportTolerance}
	}
	err := n.paths.submit(strconv.FormatInt(id, 10), a.config.Priority, func(ctx context.Context) (value any, err error) {
		ctx, cancel := context.WithTimeout(ctx, n.config.PathDeadline)
		defer cancel()
		view, err := n.backend.Acquire(ctx, profile)
		if err != nil {
			return nil, err
		}
		retained := false
		defer func() {
			if !retained {
				view.Release()
			}
		}()
		query, err := view.NewQuery(n.config.Limits)
		if err != nil {
			return nil, err
		}
		start := query.Project(ctx, from, startProjection, filter)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if start.Code != surface.Complete || start.Location == nil {
			return &navigationPathResult{revision: view.Revision(), startFailed: true, result: surface.PathResult{Status: start.Status}}, nil
		}
		if keepSupport && navDistance(from, start.Location.Position()) > supportTolerance {
			return &navigationPathResult{revision: view.Revision(), startFailed: true, result: surface.PathResult{Status: surface.Status{Code: surface.NoSurface, Err: errors.New("golem: body is outside navigation support tolerance")}}}, nil
		}
		end := query.Project(ctx, target, projection, filter)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if end.Code != surface.Complete || end.Location == nil {
			return &navigationPathResult{revision: view.Revision(), result: surface.PathResult{Status: end.Status}}, nil
		}
		path := query.FindPath(ctx, start.Location, end.Location, filter)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if path.Route == nil || path.Code != surface.Complete && path.Code != surface.Partial {
			return &navigationPathResult{revision: view.Revision(), result: path}, nil
		}
		retained = true
		// Search scratch belongs to this job. Only immutable owned results and
		// their retained view cross back to the tick; followers get a fresh query.
		return &navigationPathResult{revision: view.Revision(), view: view, start: start.Location, result: path, from: from}, nil
	}, func(value any, err error) {
		current := n.agents[id]
		if current == nil || current.attachment != attachment || current.state.Generation != generation {
			discardNavigationPath(value)
			return
		}
		current.requesting = false
		result, _ := value.(*navigationPathResult)
		if err != nil {
			current.state.WorldRevision = requestRevision
			current.state.Mode, current.state.Status = NavigationFailed, surface.Status{Code: surface.Failed, Err: err}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				current.state.Mode, current.state.Status.Code = NavigationBlocked, surface.Cancelled
			}
			return
		}
		if result == nil {
			current.state.WorldRevision = requestRevision
			current.state.Mode = NavigationFailed
			return
		}
		if result.view == nil && result.revision != n.backend.CurrentRevision(profile) {
			current.needsPath = current.state.HasDestination
			current.state.Mode, current.state.Status.Code = NavigationPending, surface.Stale
			return
		}
		if n.restoring || n.restoreErr != nil || n.profileUnavailable(profile) {
			discardNavigationPath(value)
			current.needsPath = current.state.HasDestination
			current.state.Mode, current.state.Status.Code = NavigationPending, surface.Stale
			return
		}
		current.state.WorldRevision = result.revision
		current.state.Status = result.result.Status
		if result.result.Code == surface.Partial && !current.config.AllowPartial {
			if result.result.Route != nil {
				current.state.PartialPath = result.result.Route.Corners()
			}
			discardNavigationPath(value)
			current.state.Mode = NavigationBlocked
			return
		}
		if result.view == nil {
			current.state.Mode = NavigationBlocked
			if result.startFailed && (result.result.Code == surface.NoSurface || result.result.Code == surface.Ambiguous) {
				current.state.Mode = NavigationUnsupported
			}
			return
		}
		position, _, ok := n.position(current.id)
		if !ok || navDistance(position, result.from) > 0.01 {
			discardNavigationPath(value)
			current.needsPath = current.state.HasDestination
			current.state.Mode, current.state.Status.Code = NavigationPending, surface.Stale
			return
		}
		query, admitErr := result.view.NewQuery(n.config.Limits)
		if admitErr != nil {
			discardNavigationPath(value)
			n.failAgent(current, surface.Failed, admitErr)
			return
		}
		if result.revision != n.backend.CurrentRevision(profile) {
			latest, latestQuery, rebased, _ := n.rebaseHandles(current, result.view, result.start, result.result.Route, position, false)
			if latest == nil {
				discardNavigationPath(value)
				current.needsPath = current.state.HasDestination
				current.state.Mode, current.state.Status.Code = NavigationPending, surface.Stale
				return
			}
			result.view.Release()
			result.view, result.start, result.result.Route = latest, rebased.Location, rebased.Route
			result.result.Status = rebased.Status
			result.revision, query = latest.Revision(), latestQuery
			current.state.Status = rebased.Status
		}
		n.releaseRoute(current)
		current.view, current.query, current.location, current.route = result.view, query, result.start, result.result.Route
		current.routeSafetyChecked = false
		current.locationSafetyChecked = false
		current.quarantined = false
		current.state.Quarantined = false
		current.corners = navigationCorners(current.corners, result.result.Route)
		current.state.WorldRevision = result.view.Revision()
		current.state.Mode = NavigationFollowing
		current.keepSupport = false
		current.initialProjection = !keepSupport
		current.lastPosition = position
		current.lastRepair = position
		current.repairLocation = result.start
		current.stallTime = 0
		current.needsPath = false
		current.admissionPending = true
		// Keep an owned result that intersects quarantine. Its corridor is the
		// evidence needed to wait for safety changes without repeating the search.
		if safety := n.agentSafety(current); safety != nil && (!n.regionSafe(current, safety, position) || !safety.SafeSegment(position, result.start.Position())) {
			n.quarantineAgent(current, safety)
			return
		}
		n.observePathAdmission(current)
	}, discardNavigationPath)
	if err != nil {
		a.state.Status = surface.Status{Code: surface.ResourceExhausted, Err: err}
		return
	}
	a.requesting, a.needsPath = true, false
	a.state.Mode, a.state.Status = NavigationPending, surface.Status{Code: surface.NotReady}
}

func (n *navigationService) observePathAdmission(a *navigationAgent) {
	if !a.admissionPending {
		return
	}
	a.admissionPending = false
	if observe := n.config.ObservePathAdmission; observe != nil {
		admittedAt := NavigationNow()
		observe(NavigationPathAdmissionMetrics{EntityID: a.id, Tick: n.server.tick, Generation: a.state.Generation, WorldRevision: a.state.WorldRevision, AdmittedAt: admittedAt, Status: a.state.Status})
		n.pathObservationElapsed += navigationSince(admittedAt)
	}
}

func (n *navigationService) position(id int64) (surface.Vec3, registry.Position3DWriter, bool) {
	e, ok := n.server.Get(id)
	if !ok {
		return surface.Vec3{}, nil, false
	}
	reader, ok := e.(registry.Spatial3DEntity)
	if !ok {
		return surface.Vec3{}, nil, false
	}
	writer, ok := e.(registry.Position3DWriter)
	if !ok {
		return surface.Vec3{}, nil, false
	}
	x, y, z := reader.Position3D()
	return surface.Vec3{X: float64(x), Y: float64(y), Z: float64(z)}, writer, true
}

func navigationCorners(dst []surface.Corner, route surface.Route) []surface.Corner {
	if appendable, ok := route.(surface.CornerAppender); ok {
		return appendable.AppendCorners(dst[:0])
	}
	return route.Corners()
}

func normalizeNavigationConfig(cfg *NavigationConfig) error {
	if cfg.BuildDispatchesPerTick < 0 || cfg.BuildRetryLimit < 0 || cfg.BuildRetryLimit > 16 {
		return errors.New("golem: invalid build dispatch/retry limit")
	}
	if cfg.BuildDispatchesPerTick == 0 {
		cfg.BuildDispatchesPerTick = 2
	}
	if cfg.BuildRetryLimit == 0 {
		cfg.BuildRetryLimit = 3
	}
	buildTimes := []*time.Duration{&cfg.BuildDebounce, &cfg.BuildMaxWait, &cfg.BuildRetryInitial, &cfg.BuildRetryMax}
	for i, value := range buildTimes {
		if *value < 0 {
			return errors.New("golem: build scheduling durations must not be negative")
		}
		if *value == 0 {
			*value = []time.Duration{50 * time.Millisecond, 250 * time.Millisecond, 50 * time.Millisecond, time.Second}[i]
		}
	}
	if cfg.BuildMaxWait < cfg.BuildDebounce || cfg.BuildRetryMax < cfg.BuildRetryInitial {
		return errors.New("golem: build scheduling maximum is below its initial delay")
	}
	ints := []*int{&cfg.PathWorkers, &cfg.BuildWorkers, &cfg.PathQueueCapacity, &cfg.BuildQueueCapacity, &cfg.CompletionCapacity, &cfg.PathResultsPerTick, &cfg.BuildCommitsPerTick, &cfg.MovementAgentsPerTick, &cfg.Limits.Nodes, &cfg.Limits.PathPolygons, &cfg.Limits.Corners, &cfg.BuildHistoryCapacity, &cfg.RebaseAgentsPerTick}
	defaults := []int{2, 1, 256, 64, 128, 32, 2, 256, 4096, 512, 512, 256, 64}
	for i, value := range ints {
		if *value < 0 {
			return errors.New("golem: navigation limits must not be negative")
		}
		if *value == 0 {
			*value = defaults[i]
		}
	}
	durations := []*time.Duration{&cfg.PathDrainBudget, &cfg.BuildDrainBudget, &cfg.MovementBudget, &cfg.MaxStep, &cfg.PathDeadline, &cfg.RebaseBudget}
	for i, value := range durations {
		if *value < 0 {
			return errors.New("golem: navigation budgets must not be negative")
		}
		if *value == 0 {
			*value = []time.Duration{time.Millisecond, time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, time.Millisecond}[i]
		}
	}
	cfg.RequiredProfiles = append([]surface.ProfileID(nil), cfg.RequiredProfiles...)
	return nil
}

func validateNavigationAgent(cfg *NavigationAgentConfig) error {
	if cfg.StagedPhysics && cfg.Motor != nil {
		return errors.New("golem: staged navigation cannot use a synchronous motor")
	}
	if cfg.TraversalTimeout < 0 || cfg.TraversalTimeout > 24*time.Hour {
		return errors.New("golem: traversal timeout must be between zero and 24 hours")
	}
	if cfg.TraversalTimeout == 0 {
		cfg.TraversalTimeout = 30 * time.Second
	}
	if cfg.MaxStallReplans < 0 {
		return errors.New("golem: stall retry limit must not be negative")
	}
	if cfg.MaxStallReplans == 0 {
		cfg.MaxStallReplans = 3
	}
	if cfg.Profile == "" {
		return errors.New("golem: navigation requires a baked profile ID")
	}
	if !finiteNumber(cfg.Speed) || cfg.Speed <= 0 || !finiteNumber(cfg.Acceleration) || cfg.Acceleration < 0 || !finiteNumber(cfg.StoppingDistance) || cfg.StoppingDistance < 0 {
		return errors.New("golem: invalid navigation speed, acceleration or stopping distance")
	}
	p := cfg.Projection
	if !finiteNumber(p.Horizontal) || !finiteNumber(p.Vertical) || !finiteNumber(p.Up) || !finiteNumber(p.Down) || p.Horizontal <= 0 || p.Vertical < 0 || p.Up < 0 || p.Down < 0 {
		return errors.New("golem: navigation requires finite bounded projection extents")
	}
	if p.Up == 0 && p.Down == 0 && p.Vertical == 0 {
		return errors.New("golem: navigation requires a vertical projection bound")
	}
	if cfg.SupportTolerance == 0 {
		cfg.SupportTolerance = 0.05
	}
	vertical := math.Max(p.Up, p.Down)
	if vertical == 0 {
		vertical = p.Vertical
	}
	if !finiteNumber(cfg.SupportTolerance) || cfg.SupportTolerance <= 0 || cfg.SupportTolerance > p.Horizontal || cfg.SupportTolerance > vertical {
		return errors.New("golem: support tolerance must be finite, positive and within projection extents")
	}
	cfg.Projection.Hint = nil
	cfg.Filter = cfg.Filter.Clone()
	for _, cost := range cfg.Filter.AreaCosts {
		if !finiteNumber(cost) || cost <= 0 {
			return errors.New("golem: navigation area costs must be finite and positive")
		}
	}
	return nil
}

func finiteNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func navDistance(a, b surface.Vec3) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func (n *navigationService) pending(profile surface.ProfileID) bool {
	backend, ok := n.backend.(surface.PendingBackend)
	return ok && backend.Pending(profile)
}
