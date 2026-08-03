package golem

import (
	"errors"
)

// Lifecycle and Post sentinel errors.
var (
	// ErrServerNotRunning is returned by Post when the server has not entered
	// Run, has begun shutdown, or has already returned from Run.
	ErrServerNotRunning = errors.New("golem: server is not running")
	// ErrServerAlreadyRun is returned when Run is called concurrently with or
	// after another Run on the same Server. Run is single-use.
	ErrServerAlreadyRun = errors.New("golem: server has already been run")
	// ErrPostQueueFull is returned when Post cannot enqueue because the
	// bounded post queue is at capacity.
	ErrPostQueueFull = errors.New("golem: post queue is full")
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
	defaultPostQueueCapacity     = 1024
	defaultAsyncCallbacksPerTick = 256
)

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
// before they are drained.
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

// drainPosts runs queued Post callbacks on the tick goroutine. It snapshots
// how many callbacks are eligible at drain start (min(queued, budget)) so a
// callback that Posts cannot run in the same drain. Each dequeue re-checks
// lifecycle under lifeMu; once stopping/stopped, no further callback is
// started and leftovers are left for finalizeLifecycle discard. lifeMu is
// never held while invoking user code.
func (s *Server) drainPosts() {
	s.lifeMu.Lock()
	if s.life != lifecycleRunning {
		s.lifeMu.Unlock()
		return
	}
	n := len(s.postQueue)
	if budget := s.config.AsyncCallbacksPerTick; n > budget {
		n = budget
	}
	s.lifeMu.Unlock()

	for i := 0; i < n; i++ {
		s.lifeMu.Lock()
		if s.life != lifecycleRunning {
			s.lifeMu.Unlock()
			return
		}
		var fn func(*Server)
		select {
		case fn = <-s.postQueue:
		default:
			s.lifeMu.Unlock()
			return
		}
		s.lifeMu.Unlock()
		fn(s)
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

// markLifecycleStopping transitions running → stopping under lifeMu so Post
// stops accepting as soon as cancellation is observed.
func (s *Server) markLifecycleStopping() {
	s.lifeMu.Lock()
	if s.life == lifecycleRunning {
		s.life = lifecycleStopping
	}
	s.lifeMu.Unlock()
}

// finalizeLifecycle marks the server stopped and discards queued posts.
// Safe from return and panic paths via defer.
func (s *Server) finalizeLifecycle() {
	s.lifeMu.Lock()
	s.life = lifecycleStopped
	s.discardQueuedPostsLocked()
	s.lifeMu.Unlock()
}

func normalizeTaskConfig(cfg *ServerConfig) {
	if cfg.PostQueueCapacity < 0 {
		panic("golem: PostQueueCapacity must not be negative")
	}
	if cfg.AsyncCallbacksPerTick < 0 {
		panic("golem: AsyncCallbacksPerTick must not be negative")
	}
	if cfg.PostQueueCapacity == 0 {
		cfg.PostQueueCapacity = defaultPostQueueCapacity
	}
	if cfg.AsyncCallbacksPerTick == 0 {
		cfg.AsyncCallbacksPerTick = defaultAsyncCallbacksPerTick
	}
}
