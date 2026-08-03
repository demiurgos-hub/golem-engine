package golem

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"

	"github.com/alitto/pond/v2"
)

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
		return nil
	default:
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
	_, ok := pool.TrySubmitErr(func() error {
		return s.runSubmittedTask(runCtx, ctx, work, complete)
	})
	if !ok {
		// Holding lifeMu with life==running: pool stop cannot win the gate, so
		// a failed TrySubmitErr is queue saturation (or a defensive stopped race).
		if s.life != lifecycleRunning || pool.Stopped() {
			return ErrServerNotRunning
		}
		return ErrTaskQueueFull
	}
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
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("golem: SubmitTask work panicked: %v\n%s", r, debug.Stack())
				workErr = fmt.Errorf("%w: %v", ErrTaskPanicked, r)
			}
		}()
		workErr = work(effCtx)
		if workErr == nil && effCtx.Err() != nil {
			workErr = effCtx.Err()
		}
	}()

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
			post(s)
		} else {
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
