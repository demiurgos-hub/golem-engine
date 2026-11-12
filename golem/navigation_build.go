package golem

import (
	"context"
	"errors"
	"sort"
	"time"
)

// NavigationBuildResult is a privately prepared replacement. Commit runs on the
// tick and must atomically publish or fail without changing live state. Discard
// may run on a worker or the tick; it only releases private resources and must
// not mutate tick-owned game state. A successful Commit transfers ownership.
type NavigationBuildResult interface {
	Commit() error
	Discard()
}

// NavigationBuildRetryable explicitly opts a transient error into bounded
// retry. Deterministic errors are terminal by default. Cancellation/deadline
// errors are never retried, including when wrapped by this interface.
type NavigationBuildRetryable interface {
	error
	RetryNavigationBuild() bool
}

// NavigationBuildState describes the newest desired generation. QueuedAt and
// CompletedAt include debounce, worker and retry delay. Pending plus a non-nil
// Err means a transient failure is waiting for NextAttempt.
// Timestamps use NavigationNow's process-local clock. Calculate remaining delay
// with NextAttempt.Sub(NavigationNow()), not time.Until; these values are not
// persistent civil timestamps or a clock-synchronization protocol.
type NavigationBuildState struct {
	Generation                         uint64
	Pending, Committed                 bool
	Attempts                           int
	QueuedAt, NextAttempt, CompletedAt time.Time
	Err                                error
}

type navigationBuildRequest struct {
	key                string
	generation         uint64
	priority, attempts int
	firstQueued, due   time.Time
	dispatched         bool
	work               func(context.Context) (NavigationBuildResult, error)
}

// QueueNavigationBuild accepts and debounces a stable transaction key. Delayed,
// queued and active keys share BuildQueueCapacity. The newest work replaces any
// prior generation; continuous edits cannot postpone eligibility beyond
// BuildMaxWait. Dispatch still observes worker capacity and tick budgets.
// Call on the tick goroutine; work captures immutable source inputs.
func (s *Server) QueueNavigationBuild(key string, priority int, work func(context.Context) (NavigationBuildResult, error)) (uint64, error) {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleRunning || s.runCtx == nil || s.runCtx.Err() != nil {
		return 0, ErrServerNotRunning
	}
	n := s.navigation
	if n == nil {
		return 0, errors.New("golem: navigation is not configured")
	}
	if key == "" || work == nil {
		return 0, errors.New("golem: navigation build requires a key and work")
	}
	old := n.buildRequests[key]
	if old == nil && len(n.buildRequests) >= n.config.BuildQueueCapacity {
		return 0, ErrNavigationQueueFull
	}
	if _, known := n.buildStates[key]; !known && len(n.buildStates) >= n.config.BuildHistoryCapacity {
		return 0, ErrNavigationQueueFull
	}
	now := n.buildClock()
	first := now
	if old != nil && !old.dispatched && old.attempts == 0 {
		first = old.firstQueued
	}
	due := now.Add(n.config.BuildDebounce)
	if maximum := first.Add(n.config.BuildMaxWait); maximum.Before(due) {
		due = maximum
	}
	n.builds.cancelKey(key)
	n.buildSequence++
	generation := n.buildSequence
	if old == nil || old.dispatched {
		n.delayedBuilds.Add(1)
	}
	n.buildRequests[key] = &navigationBuildRequest{key: key, generation: generation, priority: priority, firstQueued: first, due: due, work: work}
	n.buildGeneration[key] = generation
	n.buildStates[key] = NavigationBuildState{Generation: generation, Pending: true, QueuedAt: first, NextAttempt: due}
	return generation, nil
}

func discardNavigationBuild(value any) {
	if result, ok := value.(NavigationBuildResult); ok && result != nil {
		result.Discard()
	}
}

// dispatchBuilds is tick-owned and shares the build publication time budget.
// Due time plus generation provides deterministic admission; the worker queue
// applies the existing priority aging once requests have been admitted.
func (n *navigationService) dispatchBuilds(budget time.Duration) {
	started := NavigationNow()
	now := n.buildClock()
	due := make([]*navigationBuildRequest, 0, len(n.buildRequests))
	for _, request := range n.buildRequests {
		if !request.dispatched && !request.due.After(now) {
			due = append(due, request)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].due.Equal(due[j].due) {
			return due[i].generation < due[j].generation
		}
		return due[i].due.Before(due[j].due)
	})
	for i, request := range due {
		if i >= n.config.BuildDispatchesPerTick || navigationSince(started) >= budget {
			break
		}
		if n.server.runCtx.Err() != nil {
			return
		}
		if n.buildRequests[request.key] != request {
			continue
		}
		err := n.builds.submit(request.key, request.priority,
			func(ctx context.Context) (any, error) { return request.work(ctx) },
			func(value any, err error) { n.completeBuild(request, value, err) }, discardNavigationBuild)
		if err != nil {
			if errors.Is(err, ErrNavigationQueueFull) {
				break
			}
			n.removeBuildRequest(request)
			n.buildStates[request.key] = NavigationBuildState{Generation: request.generation, QueuedAt: request.firstQueued, CompletedAt: now, Attempts: request.attempts, Err: err}
			continue
		}
		request.dispatched = true
		request.attempts++
		n.delayedBuilds.Add(-1)
		state := n.buildStates[request.key]
		state.Attempts, state.NextAttempt = request.attempts, time.Time{}
		state.Err = nil
		n.buildStates[request.key] = state
	}
}

func (n *navigationService) currentBuild(request *navigationBuildRequest) bool {
	return n.buildRequests[request.key] == request && n.buildGeneration[request.key] == request.generation
}

func (n *navigationService) completeBuild(request *navigationBuildRequest, value any, err error) {
	if !n.currentBuild(request) {
		discardNavigationBuild(value)
		return
	}
	state := NavigationBuildState{Generation: request.generation, QueuedAt: request.firstQueued, Attempts: request.attempts, Err: err}
	result, ok := value.(NavigationBuildResult)
	if err == nil && (!ok || result == nil) {
		state.Err = errors.New("golem: navigation build returned no replacement")
	}
	if state.Err == nil {
		state.Err = result.Commit()
		state.Committed = state.Err == nil
	}
	if !state.Committed {
		discardNavigationBuild(value)
	}
	if !n.currentBuild(request) {
		return
	}
	retry := retryNavigationBuild(state.Err)
	if !n.currentBuild(request) {
		return
	}
	if retry && request.attempts <= n.config.BuildRetryLimit && n.server.runCtx.Err() == nil {
		request.dispatched = false
		request.due = n.buildClock().Add(n.buildBackoff(request.attempts))
		n.delayedBuilds.Add(1)
		state.Pending, state.NextAttempt = true, request.due
		n.buildStates[request.key] = state
		return
	}
	state.CompletedAt = n.buildClock()
	n.removeBuildRequest(request)
	n.buildStates[request.key] = state
}

func retryNavigationBuild(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var transient NavigationBuildRetryable
	return errors.As(err, &transient) && transient.RetryNavigationBuild()
}

func (n *navigationService) buildBackoff(attempt int) time.Duration {
	delay := n.config.BuildRetryInitial
	for i := 1; i < attempt; i++ {
		if delay > n.config.BuildRetryMax/2 {
			return n.config.BuildRetryMax
		}
		delay *= 2
	}
	if delay > n.config.BuildRetryMax {
		return n.config.BuildRetryMax
	}
	return delay
}

func (n *navigationService) removeBuildRequest(request *navigationBuildRequest) {
	if n.buildRequests[request.key] != request {
		return
	}
	if !request.dispatched {
		n.delayedBuilds.Add(-1)
	}
	delete(n.buildRequests, request.key)
}

// CancelNavigationBuild invalidates delayed, queued and active generations. It
// does not undo already committed geometry. Call on the tick (or before Run).
func (s *Server) CancelNavigationBuild(key string) {
	if n := s.navigation; n != nil {
		if _, known := n.buildStates[key]; !known {
			return
		}
		n.buildSequence++
		n.buildGeneration[key] = n.buildSequence
		n.builds.cancelKey(key)
		if request := n.buildRequests[key]; request != nil {
			n.removeBuildRequest(request)
		}
		state := n.buildStates[key]
		state.Generation, state.Pending, state.Committed = n.buildSequence, false, false
		state.NextAttempt, state.CompletedAt, state.Err = time.Time{}, n.buildClock(), context.Canceled
		n.buildStates[key] = state
	}
}

func (n *navigationService) stopBuildRequests() {
	for key := range n.buildRequests {
		n.server.CancelNavigationBuild(key)
	}
}

// ForgetNavigationBuild removes retained terminal status. Pending keys must
// first be cancelled. A reused key receives a fresh global generation.
func (s *Server) ForgetNavigationBuild(key string) error {
	if n := s.navigation; n != nil {
		if n.buildStates[key].Pending {
			return errors.New("golem: cannot forget a pending navigation build")
		}
		delete(n.buildStates, key)
		delete(n.buildGeneration, key)
	}
	return nil
}

// NavigationBuildStatus returns copied tick-owned state, including retry delay.
func (s *Server) NavigationBuildStatus(key string) (NavigationBuildState, bool) {
	if n := s.navigation; n != nil {
		state, ok := n.buildStates[key]
		return state, ok
	}
	return NavigationBuildState{}, false
}
