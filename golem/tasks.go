package golem

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"

	"github.com/alitto/pond/v2"
)

// TaskStats holds independently sampled counters and live gauges for the
// background-task and Post pipelines. Fields are not a single cross-field
// consistent cut: atomics and gauges are read separately and may disagree
// slightly under concurrency. Cumulative counters are Golem-owned and
// monotonic for the Server's lifetime. Live gauges are approximate:
// QueuedTasks/RunningTasks come from the internal Pond pool when present;
// QueuedCompletions/QueuedPosts are instantaneous channel lengths and may
// race with producers and the tick drain.
type TaskStats struct {
	// QueuedTasks is the number of accepted tasks waiting in the worker-pool
	// queue (not yet executing). Approximate under concurrency.
	QueuedTasks uint64
	// RunningTasks is the number of active worker goroutines currently
	// executing accepted task wrappers. Approximate under concurrency.
	RunningTasks uint64
	// QueuedCompletions is the instantaneous length of the independent
	// worker→tick completion queue. Approximate under concurrency.
	QueuedCompletions uint64
	// QueuedPosts is the instantaneous length of the Post queue.
	// Approximate under concurrency.
	QueuedPosts uint64
	// TasksAccepted counts SubmitTask calls that successfully entered the
	// worker pool (not validation failures, not ErrServerNotRunning, not
	// ErrTaskQueueFull).
	TasksAccepted uint64
	// TasksFinished counts accepted tasks whose work function returned or
	// panicked. Distinct from TaskCompletionsExecuted: finished work may
	// still be awaiting tick drain, or its completion may be discarded on
	// shutdown without running the callback.
	TasksFinished uint64
	// TaskCompletionsExecuted counts completion callbacks that actually ran
	// on the tick goroutine. Shutdown discard of queued completions does not
	// increment this counter.
	TaskCompletionsExecuted uint64
	// TasksRejectedFull counts SubmitTask calls that returned ErrTaskQueueFull.
	TasksRejectedFull uint64
	// TasksCancelled counts each accepted task at most once when either
	// (1) queued-not-started work is discarded on shutdown without executing
	// user work, or (2) accepted work runs and its effective context has
	// already ended by the time work returns (caller or server cancellation).
	TasksCancelled uint64
	// TaskPanics counts accepted tasks whose work function panicked
	// (recovered into an ErrTaskPanicked completion error while running).
	TaskPanics uint64
	// PostsAccepted counts Post calls that successfully enqueued a callback.
	PostsAccepted uint64
	// PostsExecuted counts Post callbacks that actually ran on the tick
	// goroutine. Shutdown discard of queued posts does not increment this.
	PostsExecuted uint64
	// PostsRejectedFull counts Post calls that returned ErrPostQueueFull.
	PostsRejectedFull uint64
}

// TaskStats returns independently sampled pipeline counters and live gauges.
// Values are read separately (atomics and gauges are not frozen together), so
// the returned struct is not a cross-field consistent point-in-time cut under
// concurrency. See TaskStats field docs for counter semantics.
func (s *Server) TaskStats() TaskStats {
	s.lifeMu.Lock()
	pool := s.taskPool
	queuedCompletions := uint64(len(s.completionQueue))
	queuedPosts := uint64(len(s.postQueue))
	s.lifeMu.Unlock()

	stats := TaskStats{
		QueuedCompletions:       queuedCompletions,
		QueuedPosts:             queuedPosts,
		TasksAccepted:           s.tasksAccepted.Load(),
		TasksFinished:           s.tasksFinished.Load(),
		TaskCompletionsExecuted: s.taskCompletionsExecuted.Load(),
		TasksRejectedFull:       s.tasksRejectedFull.Load(),
		TasksCancelled:          s.tasksCancelled.Load(),
		TaskPanics:              s.taskPanics.Load(),
		PostsAccepted:           s.postsAccepted.Load(),
		PostsExecuted:           s.postsExecuted.Load(),
		PostsRejectedFull:       s.postsRejectedFull.Load(),
	}
	if pool != nil {
		stats.QueuedTasks = pool.WaitingTasks()
		if n := pool.RunningWorkers(); n > 0 {
			stats.RunningTasks = uint64(n)
		}
	}
	return stats
}

// Lifecycle, Post, and SubmitTask sentinel errors.
var (
	// ErrServerNotRunning is returned by Post and SubmitTask when the server
	// has not entered Run, has begun shutdown, or has already returned from Run.
	ErrServerNotRunning = errors.New("golem: server is not running")
	// ErrServerAlreadyRun is returned when Run is called concurrently with or
	// after another Run on the same Server. Run is single-use.
	ErrServerAlreadyRun = errors.New("golem: server has already been run")
	// ErrPostQueueFull is returned when Post cannot enqueue because the
	// bounded post queue is at capacity.
	ErrPostQueueFull = errors.New("golem: post queue is full")
	// ErrTaskQueueFull is returned when SubmitTask cannot accept work because
	// the bounded worker-pool queue is at capacity.
	ErrTaskQueueFull = errors.New("golem: task queue is full")
	// ErrTaskPanicked is wrapped into the completion error when work panics.
	// The completion callback still runs on the tick goroutine while the
	// server remains running.
	ErrTaskPanicked = errors.New("golem: task panicked")
)

// lifecycleState is the linearizable Run acceptance state.
type lifecycleState uint8

const (
	lifecycleCreated lifecycleState = iota
	lifecycleRunning
	lifecycleStopping
	lifecycleStopped
)

const (
	defaultPostQueueCapacity           = 1024
	defaultAsyncCallbacksPerTick       = 256
	defaultTaskWorkers                 = 4
	defaultTaskQueueCapacity           = 256
	defaultTaskCompletionQueueCapacity = 256
)

// taskCompletion is a worker-produced callback for the tick goroutine.
type taskCompletion struct {
	complete func(*Server, error)
	err      error
}

// Post enqueues fn to run on the tick goroutine after OnTickStart and before
// session-message drain. It never invokes fn inline, including when called
// from a tick callback; such posts run on a later tick drain.
//
// Post returns ErrServerNotRunning before Run, once shutdown is linearized,
// and after Run returns. A full queue returns ErrPostQueueFull immediately
// without blocking. A nil fn returns a non-nil validation error and is not
// enqueued.
//
// Accepted posts may be discarded without running when the server shuts down
// before they are drained. Posts and task completions share the per-tick
// AsyncCallbacksPerTick budget (round-robin, completion-first on the first
// drain, preference persisted across ticks).
//
// Pipeline counters are exposed by TaskStats / Server.TaskStats
// (PostsAccepted, PostsExecuted, PostsRejectedFull, QueuedPosts).
func (s *Server) Post(fn func(*Server)) error {
	if fn == nil {
		return errors.New("golem: Post requires non-nil callback")
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleRunning {
		return ErrServerNotRunning
	}
	select {
	case s.postQueue <- fn:
		s.postsAccepted.Add(1)
		return nil
	default:
		s.postsRejectedFull.Add(1)
		return ErrPostQueueFull
	}
}

// SubmitTask schedules work on the bounded background worker pool. work runs
// off the tick goroutine; complete runs later on the tick goroutine (after
// OnTickStart, before session-message drain), sharing the AsyncCallbacksPerTick
// budget with Post.
//
// Ownership: work must treat inputs as immutable/caller-owned and must not
// touch Server entities, registry, visibility, sessions, or other tick-owned
// state. Only complete (or Post) may mutate game/server state. Values captured
// by work and read in complete are safely visible: the worker's completion
// enqueue happens-before the tick drain receives and invokes complete.
//
// The effective work context is derived from ctx (so an already-cancelled or
// queue-cancelled caller context is visible synchronously when work starts)
// and cancelled when the server run context ends. A non-nil error returned by
// work is delivered unchanged. If work returns nil after the effective context
// has ended, complete receives that context error. A panic in work is
// recovered, logged with a stack, and delivered as an error wrapping
// ErrTaskPanicked; while the server remains running, complete still runs
// exactly once for that task.
//
// SubmitTask returns ErrServerNotRunning before Run, once shutdown is
// linearized, and after Run returns. A saturated pool queue returns
// ErrTaskQueueFull immediately without blocking. Nil ctx, work, or complete
// return a non-nil validation error and are not accepted.
//
// When the independent completion queue is full, a finished worker applies
// bounded backpressure (select on the run context) so completions are not
// dropped while the server remains running and workers never block past
// shutdown. On shutdown, queued-not-started tasks are not executed, in-flight
// work receives cancellation, queued completions/posts are discarded, and no
// complete/Post callback runs after the tick loop exits. Work that ignores
// context cancellation can delay Run's return indefinitely.
//
// Pipeline counters are exposed by TaskStats / Server.TaskStats. TasksAccepted
// is incremented under lifeMu before the per-submission start gate opens, so
// worker finish/cancel/panic counters cannot observe an accepted task before
// TasksAccepted reflects it. TasksFinished increments when work returns or
// panics; TaskCompletionsExecuted increments only when complete actually runs
// on the tick goroutine (so shutdown discard is observable as finished without
// executed). Rejected submissions never open the gate and are not counted as
// accepted.
func (s *Server) SubmitTask(ctx context.Context, work func(context.Context) error, complete func(*Server, error)) error {
	if ctx == nil {
		return errors.New("golem: SubmitTask requires non-nil context")
	}
	if work == nil {
		return errors.New("golem: SubmitTask requires non-nil work")
	}
	if complete == nil {
		return errors.New("golem: SubmitTask requires non-nil complete")
	}

	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life != lifecycleRunning {
		return ErrServerNotRunning
	}
	runCtx := s.runCtx
	pool := s.taskPool
	// Pond may run the wrapper on a worker before TrySubmitErr returns. Gate
	// user work until accept is counted so finish/cancel/panic cannot race
	// ahead of TasksAccepted. lifeMu is held until the gate opens, so shutdown
	// (which takes lifeMu before cancelling the pool) cannot StopAndWait a
	// worker blocked on the gate. Rejected TrySubmitErr never closes the gate
	// and never launches a waiter.
	startGate := make(chan struct{})
	_, ok := pool.TrySubmitErr(func() error {
		<-startGate
		return s.runSubmittedTask(runCtx, ctx, work, complete)
	})
	if !ok {
		// Holding lifeMu with life==running: pool stop cannot win the gate, so
		// a failed TrySubmitErr is queue saturation (or a defensive stopped race).
		if s.life != lifecycleRunning || pool.Stopped() {
			return ErrServerNotRunning
		}
		s.tasksRejectedFull.Add(1)
		return ErrTaskQueueFull
	}
	s.tasksAccepted.Add(1)
	close(startGate)
	return nil
}

// runSubmittedTask executes one accepted task on a Pond worker: derives the
// effective context from the caller (synchronous cancel visibility), links
// run-context cancellation via AfterFunc, recovers panics, and enqueues a tick
// completion or escapes on run cancellation.
func (s *Server) runSubmittedTask(runCtx, callerCtx context.Context, work func(context.Context) error, complete func(*Server, error)) error {
	effCtx, effCancel := context.WithCancel(callerCtx)
	stopRun := context.AfterFunc(runCtx, effCancel)
	defer stopRun()
	defer effCancel()

	var workErr error
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("golem: SubmitTask work panicked: %v\n%s", r, debug.Stack())
				workErr = fmt.Errorf("%w: %v", ErrTaskPanicked, r)
				panicked = true
			}
		}()
		workErr = work(effCtx)
		if workErr == nil && effCtx.Err() != nil {
			workErr = effCtx.Err()
		}
	}()

	// Work returned or panicked: finished is independent of whether the tick
	// completion callback later runs (shutdown may discard it).
	s.tasksFinished.Add(1)
	if panicked {
		s.taskPanics.Add(1)
	}
	if effCtx.Err() != nil {
		s.tasksCancelled.Add(1)
	}

	select {
	case s.completionQueue <- taskCompletion{complete: complete, err: workErr}:
	case <-runCtx.Done():
	}
	return workErr
}

// drainAsyncCallbacks runs queued task completions and Post callbacks on the
// tick goroutine under a single AsyncCallbacksPerTick budget. Selection is
// round-robin that starts with a completion on the first drain and persists
// preference across ticks so a budget of 1 cannot starve Post under sustained
// completions. The eligible count is snapshotted at drain start so callbacks
// that Post/complete cannot inflate the same drain. lifeMu is never held while
// invoking user code. Completion callback panics are not recovered.
func (s *Server) drainAsyncCallbacks() {
	s.lifeMu.Lock()
	if s.life != lifecycleRunning {
		s.lifeMu.Unlock()
		return
	}
	remainC := len(s.completionQueue)
	remainP := len(s.postQueue)
	n := remainC + remainP
	if budget := s.config.AsyncCallbacksPerTick; n > budget {
		n = budget
	}
	preferCompletion := s.asyncPreferCompletion
	s.lifeMu.Unlock()

	for i := 0; i < n; i++ {
		s.lifeMu.Lock()
		if s.life != lifecycleRunning {
			s.lifeMu.Unlock()
			return
		}
		var (
			comp taskCompletion
			post func(*Server)
			got  bool
		)
		if preferCompletion {
			if remainC > 0 {
				select {
				case comp = <-s.completionQueue:
					remainC--
					got = true
				default:
					remainC = 0
				}
			}
			if !got && remainP > 0 {
				select {
				case post = <-s.postQueue:
					remainP--
					got = true
				default:
					remainP = 0
				}
			}
		} else {
			if remainP > 0 {
				select {
				case post = <-s.postQueue:
					remainP--
					got = true
				default:
					remainP = 0
				}
			}
			if !got && remainC > 0 {
				select {
				case comp = <-s.completionQueue:
					remainC--
					got = true
				default:
					remainC = 0
				}
			}
		}
		if got {
			preferCompletion = !preferCompletion
			s.asyncPreferCompletion = preferCompletion
		}
		s.lifeMu.Unlock()
		if !got {
			return
		}
		if post != nil {
			s.postsExecuted.Add(1)
			post(s)
		} else {
			s.taskCompletionsExecuted.Add(1)
			comp.complete(s, comp.err)
		}
	}
}

// discardQueuedPostsLocked drops all queued Post callbacks without running
// them. Caller must hold lifeMu.
func (s *Server) discardQueuedPostsLocked() {
	for {
		select {
		case <-s.postQueue:
		default:
			return
		}
	}
}

// discardQueuedCompletionsLocked drops all queued task completions without
// running them. Caller must hold lifeMu.
func (s *Server) discardQueuedCompletionsLocked() {
	for {
		select {
		case <-s.completionQueue:
		default:
			return
		}
	}
}

// markLifecycleStopping transitions running → stopping under lifeMu so Post
// and SubmitTask stop accepting as soon as shutdown is linearized.
func (s *Server) markLifecycleStopping() {
	s.lifeMu.Lock()
	if s.life == lifecycleRunning {
		s.life = lifecycleStopping
	}
	s.lifeMu.Unlock()
}

// finalizeLifecycle closes acceptance, cancels the run context, waits for the
// Pond pool, discards queued callbacks, and marks stopped. Safe from return
// and panic paths via defer.
func (s *Server) finalizeLifecycle() {
	s.markLifecycleStopping()
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.taskPool != nil {
		s.taskPool.StopAndWait()
		// Pond skips user work for accepted tasks still queued when the pool
		// context ends; fold that into Golem's TasksCancelled (at most once
		// per accepted task; runSubmittedTask never ran for these).
		if n := s.taskPool.CanceledTasks(); n > 0 {
			s.tasksCancelled.Add(n)
		}
	}
	s.lifeMu.Lock()
	s.life = lifecycleStopped
	s.discardQueuedPostsLocked()
	s.discardQueuedCompletionsLocked()
	s.lifeMu.Unlock()
}

func normalizeTaskConfig(cfg *ServerConfig) {
	if cfg.PostQueueCapacity < 0 {
		panic("golem: PostQueueCapacity must not be negative")
	}
	if cfg.AsyncCallbacksPerTick < 0 {
		panic("golem: AsyncCallbacksPerTick must not be negative")
	}
	if cfg.TaskWorkers < 0 {
		panic("golem: TaskWorkers must not be negative")
	}
	if cfg.TaskQueueCapacity < 0 {
		panic("golem: TaskQueueCapacity must not be negative")
	}
	if cfg.TaskCompletionQueueCapacity < 0 {
		panic("golem: TaskCompletionQueueCapacity must not be negative")
	}
	if cfg.PostQueueCapacity == 0 {
		cfg.PostQueueCapacity = defaultPostQueueCapacity
	}
	if cfg.AsyncCallbacksPerTick == 0 {
		cfg.AsyncCallbacksPerTick = defaultAsyncCallbacksPerTick
	}
	if cfg.TaskWorkers == 0 {
		cfg.TaskWorkers = defaultTaskWorkers
	}
	if cfg.TaskQueueCapacity == 0 {
		cfg.TaskQueueCapacity = defaultTaskQueueCapacity
	}
	if cfg.TaskCompletionQueueCapacity == 0 {
		cfg.TaskCompletionQueueCapacity = defaultTaskCompletionQueueCapacity
	}
}

// startTaskPool constructs the Pond pool for this Run. Caller must hold
// lifeMu and set life to running only after this returns.
func (s *Server) startTaskPool(runCtx context.Context) {
	s.runCtx = runCtx
	s.taskPool = pond.NewPool(
		s.config.TaskWorkers,
		pond.WithQueueSize(s.config.TaskQueueCapacity),
		pond.WithContext(runCtx),
	)
}

// mapRunErr preserves the caller context error when the independent run
// context was cancelled because the caller context ended.
func mapRunErr(loopErr error, caller context.Context) error {
	if loopErr == nil {
		return nil
	}
	if errors.Is(loopErr, context.Canceled) || errors.Is(loopErr, context.DeadlineExceeded) {
		if err := caller.Err(); err != nil {
			return err
		}
	}
	return loopErr
}
