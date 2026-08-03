package golem

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

func TestPostRejectsBeforeRun(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	err := srv.Post(func(*Server) {})
	if !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("Post before Run: %v, want ErrServerNotRunning", err)
	}
}

func TestPostRejectsNilCallback(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	ready := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	if err := srv.Post(nil); err == nil {
		t.Fatal("expected nil-callback validation error")
	}
	cancel()
	<-errCh
}

func TestPostRunsOnTickNotInline(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	gate := make(chan struct{})
	ready := make(chan struct{})
	var ran atomic.Bool

	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	if err := srv.Post(func(*Server) { ran.Store(true) }); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if ran.Load() {
		t.Fatal("Post invoked callback inline before drain")
	}
	close(gate)

	deadline := time.Now().Add(2 * time.Second)
	for !ran.Load() {
		if time.Now().After(deadline) {
			t.Fatal("callback did not run on tick drain")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-errCh
}

func TestPostCallbackRunsAtMostOnce(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	var n atomic.Int32
	done := make(chan struct{})

	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			if err := srv.Post(func(*Server) {
				if n.Add(1) == 1 {
					close(done)
				}
			}); err != nil {
				t.Errorf("Post: %v", err)
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for posted callback")
	}
	// Allow another tick so a double-fire would be visible.
	time.Sleep(5 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Fatalf("callback ran %d times, want 1", got)
	}
	cancel()
	<-errCh
}

func TestPostQueueFullWhileTickGated(t *testing.T) {
	const cap = 4
	srv := NewServer(ServerConfig{
		TickRate:              1000,
		PostQueueCapacity:     cap,
		AsyncCallbacksPerTick: 256,
	})
	gate := make(chan struct{})
	ready := make(chan struct{})
	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	for i := 0; i < cap; i++ {
		if err := srv.Post(func(*Server) {}); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	err := srv.Post(func(*Server) {})
	if !errors.Is(err, ErrPostQueueFull) {
		t.Fatalf("Post when full: %v, want ErrPostQueueFull", err)
	}
	close(gate)
	cancel()
	<-errCh
}

func TestPostBudgetLeavesRemainderForLaterTicks(t *testing.T) {
	const budget = 2
	srv := NewServer(ServerConfig{
		TickRate:              1000,
		PostQueueCapacity:     16,
		AsyncCallbacksPerTick: budget,
	})
	gate1 := make(chan struct{})
	gate2 := make(chan struct{})
	ready := make(chan struct{})
	tick2Start := make(chan struct{})
	var mu sync.Mutex
	var executed []int
	done := make(chan struct{})

	srv.OnTickStart(func(tick uint64) {
		switch tick {
		case 1:
			close(ready)
			<-gate1
		case 2:
			// Tick 1 finished (including its drain); hold before tick-2 drain.
			close(tick2Start)
			<-gate2
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	for i := 0; i < 5; i++ {
		n := i
		if err := srv.Post(func(*Server) {
			mu.Lock()
			executed = append(executed, n)
			if len(executed) == 5 {
				close(done)
			}
			mu.Unlock()
		}); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	close(gate1)

	select {
	case <-tick2Start:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick 2 gate")
	}
	mu.Lock()
	first := append([]int(nil), executed...)
	mu.Unlock()
	if len(first) != budget {
		t.Fatalf("after first drain: %v, want len %d", first, budget)
	}
	for i, v := range first {
		if v != i {
			t.Fatalf("after first drain order=%v, want FIFO prefix", first)
		}
	}

	close(gate2)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for remaining callbacks")
	}
	mu.Lock()
	final := append([]int(nil), executed...)
	mu.Unlock()
	if len(final) != 5 {
		t.Fatalf("final executed=%v, want 5 callbacks", final)
	}
	for i, v := range final {
		if v != i {
			t.Fatalf("order=%v, want FIFO 0..4", final)
		}
	}
	cancel()
	<-errCh
}

func TestPostRejectedDuringAndAfterShutdown(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	gate := make(chan struct{})
	ready := make(chan struct{})
	var ran atomic.Bool

	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	if err := srv.Post(func(*Server) { ran.Store(true) }); err != nil {
		t.Fatalf("Post while running: %v", err)
	}
	cancel()

	// Wait until cancellation linearizes stopping before releasing the tick.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := srv.Post(func(*Server) {})
		if errors.Is(err, ErrServerNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for shutdown rejection")
		}
		time.Sleep(time.Millisecond)
	}

	close(gate)

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to exit")
	}

	if ran.Load() {
		t.Fatal("queued Post ran after shutdown linearized")
	}
	err := srv.Post(func(*Server) {})
	if !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("Post after Run: %v, want ErrServerNotRunning", err)
	}
}

func TestRunRejectsConcurrentAndRepeatedCalls(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	started := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-started:
		default:
			close(started)
		}
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	err1 := make(chan error, 1)
	go func() { err1 <- srv.Run(ctx1) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first Run")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	err := srv.Run(ctx2)
	if !errors.Is(err, ErrServerAlreadyRun) {
		t.Fatalf("concurrent Run: %v, want ErrServerAlreadyRun", err)
	}

	cancel1()
	select {
	case err := <-err1:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first Run exit")
	}

	err = srv.Run(context.Background())
	if !errors.Is(err, ErrServerAlreadyRun) {
		t.Fatalf("repeated Run: %v, want ErrServerAlreadyRun", err)
	}
}

// TestRunAlreadyCancelledContextStopsSynchronously ensures an already-done
// caller context never briefly exposes lifecycleRunning via AfterFunc: no tick
// runs, and Post/SubmitTask cannot be accepted for that Run.
func TestRunAlreadyCancelledContextStopsSynchronously(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	var ticked atomic.Bool
	srv.OnTickStart(func(uint64) { ticked.Store(true) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var accepted atomic.Bool
	stopProbe := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopProbe:
				return
			default:
			}
			if err := srv.Post(func(*Server) {}); err == nil {
				accepted.Store(true)
			}
			if err := srv.SubmitTask(context.Background(), func(context.Context) error {
				return nil
			}, func(*Server, error) {}); err == nil {
				accepted.Store(true)
			}
		}
	}()

	err := srv.Run(ctx)
	close(stopProbe)
	wg.Wait()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v, want context.Canceled", err)
	}
	if ticked.Load() {
		t.Fatal("tick ran despite already-cancelled Run context")
	}
	if accepted.Load() {
		t.Fatal("Post/SubmitTask accepted despite already-cancelled Run context")
	}
	if err := srv.Post(func(*Server) {}); !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("Post after Run: %v, want ErrServerNotRunning", err)
	}
	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		return nil
	}, func(*Server, error) {}); !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("SubmitTask after Run: %v, want ErrServerNotRunning", err)
	}
	if err := srv.Run(context.Background()); !errors.Is(err, ErrServerAlreadyRun) {
		t.Fatalf("repeated Run: %v, want ErrServerAlreadyRun", err)
	}
}

func TestPostFromTickCallbackIsNotInline(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})

	srv.OnTick(func(_ float64, s *Server) {
		if s.Tick() != 1 {
			return
		}
		mu.Lock()
		order = append(order, "before")
		mu.Unlock()
		if err := s.Post(func(*Server) {
			mu.Lock()
			order = append(order, "posted")
			mu.Unlock()
			close(done)
		}); err != nil {
			t.Errorf("Post from OnTick: %v", err)
		}
		mu.Lock()
		order = append(order, "after")
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for nested Post")
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"before", "after", "posted"}
	if len(got) != len(want) {
		t.Fatalf("order=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v, want %v", got, want)
		}
	}
	cancel()
	<-errCh
}

func TestTaskConfigDefaultsAndValidation(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if srv.config.PostQueueCapacity != defaultPostQueueCapacity {
		t.Fatalf("PostQueueCapacity=%d, want %d", srv.config.PostQueueCapacity, defaultPostQueueCapacity)
	}
	if srv.config.AsyncCallbacksPerTick != defaultAsyncCallbacksPerTick {
		t.Fatalf("AsyncCallbacksPerTick=%d, want %d", srv.config.AsyncCallbacksPerTick, defaultAsyncCallbacksPerTick)
	}
	if srv.config.TaskWorkers != defaultTaskWorkers {
		t.Fatalf("TaskWorkers=%d, want %d", srv.config.TaskWorkers, defaultTaskWorkers)
	}
	if srv.config.TaskQueueCapacity != defaultTaskQueueCapacity {
		t.Fatalf("TaskQueueCapacity=%d, want %d", srv.config.TaskQueueCapacity, defaultTaskQueueCapacity)
	}
	if srv.config.TaskCompletionQueueCapacity != defaultTaskCompletionQueueCapacity {
		t.Fatalf("TaskCompletionQueueCapacity=%d, want %d", srv.config.TaskCompletionQueueCapacity, defaultTaskCompletionQueueCapacity)
	}
	if cap(srv.postQueue) != defaultPostQueueCapacity {
		t.Fatalf("postQueue cap=%d, want %d", cap(srv.postQueue), defaultPostQueueCapacity)
	}
	if cap(srv.completionQueue) != defaultTaskCompletionQueueCapacity {
		t.Fatalf("completionQueue cap=%d, want %d", cap(srv.completionQueue), defaultTaskCompletionQueueCapacity)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for negative PostQueueCapacity")
		}
	}()
	_ = NewServer(ServerConfig{PostQueueCapacity: -1})
}

func TestSubmitTaskRejectsBeforeAndAfterRun(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	err := srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, func(*Server, error) {})
	if !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("SubmitTask before Run: %v, want ErrServerNotRunning", err)
	}

	ready := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run exit")
	}

	err = srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, func(*Server, error) {})
	if !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("SubmitTask after Run: %v, want ErrServerNotRunning", err)
	}
}

func TestSubmitTaskRejectsNilArgs(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	ready := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	if err := srv.SubmitTask(nil, func(context.Context) error { return nil }, func(*Server, error) {}); err == nil {
		t.Fatal("expected nil context validation error")
	}
	if err := srv.SubmitTask(context.Background(), nil, func(*Server, error) {}); err == nil {
		t.Fatal("expected nil work validation error")
	}
	if err := srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, nil); err == nil {
		t.Fatal("expected nil complete validation error")
	}
	cancel()
	<-errCh
}

func TestSubmitTaskWorkOffTickCompletionOnTick(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000, TaskWorkers: 1})
	gate := make(chan struct{})
	ready := make(chan struct{})
	workStarted := make(chan struct{})
	var ticksWhileBlocked atomic.Uint64
	var completeTick atomic.Uint64
	done := make(chan struct{})

	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
			return
		}
		select {
		case <-workStarted:
			if completeTick.Load() == 0 {
				ticksWhileBlocked.Add(1)
			}
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	releaseWork := make(chan struct{})
	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		close(workStarted)
		<-releaseWork
		return nil
	}, func(s *Server, err error) {
		if err != nil {
			t.Errorf("complete err: %v", err)
		}
		completeTick.Store(s.Tick())
		close(done)
	}); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	close(gate)

	select {
	case <-workStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for work start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for ticksWhileBlocked.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ticks did not advance while work blocked")
		}
		time.Sleep(time.Millisecond)
	}
	if completeTick.Load() != 0 {
		t.Fatal("completion ran before work returned")
	}
	close(releaseWork)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for completion")
	}
	if completeTick.Load() == 0 {
		t.Fatal("completion did not observe tick")
	}
	cancel()
	<-errCh
}

func TestSubmitTaskCapturedMemoryVisibleInComplete(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	ready := make(chan struct{})
	done := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	var result int
	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		result = 42
		return nil
	}, func(_ *Server, err error) {
		if err != nil {
			t.Errorf("complete err: %v", err)
		}
		if result != 42 {
			t.Errorf("captured result=%d, want 42", result)
		}
		close(done)
	}); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for completion")
	}
	cancel()
	<-errCh
}

func TestSubmitTaskRespectsWorkerLimit(t *testing.T) {
	const workers = 2
	srv := NewServer(ServerConfig{
		TickRate:          1000,
		TaskWorkers:       workers,
		TaskQueueCapacity: 16,
	})
	ready := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	var running atomic.Int32
	var maxRunning atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	const tasks = 6
	wg.Add(tasks)
	for i := 0; i < tasks; i++ {
		if err := srv.SubmitTask(context.Background(), func(context.Context) error {
			n := running.Add(1)
			for {
				cur := maxRunning.Load()
				if n <= cur || maxRunning.CompareAndSwap(cur, n) {
					break
				}
			}
			<-release
			running.Add(-1)
			return nil
		}, func(*Server, error) { wg.Done() }); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for maxRunning.Load() < workers {
		if time.Now().After(deadline) {
			t.Fatalf("max running=%d, want %d", maxRunning.Load(), workers)
		}
		time.Sleep(time.Millisecond)
	}
	// Hold briefly so a third worker would be visible if the limit failed.
	time.Sleep(20 * time.Millisecond)
	if got := maxRunning.Load(); got != workers {
		t.Fatalf("max running=%d, want %d", got, workers)
	}
	close(release)
	wg.Wait()
	cancel()
	<-errCh
}

func TestSubmitTaskQueueFull(t *testing.T) {
	srv := NewServer(ServerConfig{
		TickRate:          1000,
		TaskWorkers:       1,
		TaskQueueCapacity: 1,
	})
	gate := make(chan struct{})
	ready := make(chan struct{})
	workStarted := make(chan struct{})
	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	block := make(chan struct{})
	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		close(workStarted)
		<-block
		return nil
	}, func(*Server, error) {}); err != nil {
		t.Fatalf("first SubmitTask: %v", err)
	}
	select {
	case <-workStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for worker")
	}
	// Fill the single queue slot.
	if err := srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, func(*Server, error) {}); err != nil {
		t.Fatalf("queued SubmitTask: %v", err)
	}
	err := srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, func(*Server, error) {})
	if !errors.Is(err, ErrTaskQueueFull) {
		t.Fatalf("SubmitTask when full: %v, want ErrTaskQueueFull", err)
	}
	close(block)
	close(gate)
	cancel()
	<-errCh
}

func TestSubmitTaskCompletionBackpressureAndShutdownEscape(t *testing.T) {
	srv := NewServer(ServerConfig{
		TickRate:                    1000,
		TaskWorkers:                 2,
		TaskQueueCapacity:           8,
		TaskCompletionQueueCapacity: 1,
		AsyncCallbacksPerTick:       256,
	})
	gate := make(chan struct{})
	ready := make(chan struct{})
	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	var completed atomic.Int32
	started := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		if err := srv.SubmitTask(context.Background(), func(context.Context) error {
			started <- struct{}{}
			return nil
		}, func(*Server, error) {
			completed.Add(1)
		}); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for workers to finish work")
		}
	}
	// One completion is queued; the other worker is blocked on backpressure.
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(srv.completionQueue) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(srv.completionQueue) != 1 {
		t.Fatalf("completionQueue len=%d, want 1 (backpressure)", len(srv.completionQueue))
	}
	if completed.Load() != 0 {
		t.Fatal("completion ran while tick gated")
	}

	cancel()
	deadline = time.Now().Add(2 * time.Second)
	for {
		err := srv.SubmitTask(context.Background(), func(context.Context) error { return nil }, func(*Server, error) {})
		if errors.Is(err, ErrServerNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for shutdown rejection")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for cooperative Run exit")
	}
	if completed.Load() != 0 {
		t.Fatal("completion ran after shutdown")
	}
}

func TestAsyncCallbackBudgetFairness(t *testing.T) {
	const budget = 4
	srv := NewServer(ServerConfig{
		TickRate:                    1000,
		PostQueueCapacity:           16,
		TaskCompletionQueueCapacity: 16,
		AsyncCallbacksPerTick:       budget,
	})
	gate1 := make(chan struct{})
	gate2 := make(chan struct{})
	ready := make(chan struct{})
	tick2 := make(chan struct{})
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})

	srv.OnTickStart(func(tick uint64) {
		switch tick {
		case 1:
			close(ready)
			<-gate1
		case 2:
			close(tick2)
			<-gate2
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		if len(order) == 6 {
			close(done)
		}
		mu.Unlock()
	}
	for i := 0; i < 3; i++ {
		srv.completionQueue <- taskCompletion{complete: func(*Server, error) { record("c") }}
		if err := srv.Post(func(*Server) { record("p") }); err != nil {
			t.Fatalf("Post: %v", err)
		}
	}
	close(gate1)

	select {
	case <-tick2:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick 2")
	}
	mu.Lock()
	first := append([]string(nil), order...)
	mu.Unlock()
	if len(first) != budget {
		t.Fatalf("first drain=%v, want len %d", first, budget)
	}
	wantFirst := []string{"c", "p", "c", "p"}
	for i := range wantFirst {
		if first[i] != wantFirst[i] {
			t.Fatalf("first drain=%v, want %v", first, wantFirst)
		}
	}
	close(gate2)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for remainder")
	}
	mu.Lock()
	final := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"c", "p", "c", "p", "c", "p"}
	if len(final) != len(want) {
		t.Fatalf("final=%v, want %v", final, want)
	}
	for i := range want {
		if final[i] != want[i] {
			t.Fatalf("final=%v, want %v", final, want)
		}
	}
	cancel()
	<-errCh
}

// TestAsyncCallbackBudget1PersistsFairnessAcrossTicks ensures AsyncCallbacksPerTick=1
// cannot starve Post when completions keep arriving: preference persists across
// ticks so drains alternate c, p, c, p, ...
func TestAsyncCallbackBudget1PersistsFairnessAcrossTicks(t *testing.T) {
	const (
		budget = 1
		pairs  = 4
		total  = pairs * 2
	)
	srv := NewServer(ServerConfig{
		TickRate:                    1000,
		PostQueueCapacity:           16,
		TaskCompletionQueueCapacity: 16,
		AsyncCallbacksPerTick:       budget,
	})
	ready := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	var tickN atomic.Uint64

	srv.OnTickStart(func(tick uint64) {
		tickN.Store(tick)
		if tick == 1 {
			close(ready)
			<-release
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		if len(order) == total {
			close(done)
		}
		mu.Unlock()
	}
	for i := 0; i < pairs; i++ {
		srv.completionQueue <- taskCompletion{complete: func(*Server, error) { record("c") }}
		if err := srv.Post(func(*Server) { record("p") }); err != nil {
			t.Fatalf("Post: %v", err)
		}
	}
	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for alternating drains")
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := make([]string, 0, total)
	for i := 0; i < pairs; i++ {
		want = append(want, "c", "p")
	}
	if len(got) != len(want) {
		t.Fatalf("order=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v, want %v (Post starved under budget=1)", got, want)
		}
	}
	if ticks := tickN.Load(); ticks < uint64(total) {
		t.Fatalf("ticks=%d, want at least %d for budget=1 drains", ticks, total)
	}
	cancel()
	<-errCh
}

func TestSubmitTaskWorkError(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	ready := make(chan struct{})
	done := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	wantErr := errors.New("boom")
	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		return wantErr
	}, func(_ *Server, err error) {
		if !errors.Is(err, wantErr) {
			t.Errorf("complete err=%v, want %v", err, wantErr)
		}
		close(done)
	}); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	<-errCh
}

func TestSubmitTaskCallerAndServerCancellation(t *testing.T) {
	t.Run("caller", func(t *testing.T) {
		srv := NewServer(ServerConfig{TickRate: 1000})
		ready := make(chan struct{})
		done := make(chan struct{})
		srv.OnTickStart(func(uint64) {
			select {
			case <-ready:
			default:
				close(ready)
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Run(ctx) }()
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Run")
		}

		taskCtx, taskCancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		if err := srv.SubmitTask(taskCtx, func(c context.Context) error {
			close(started)
			<-c.Done()
			return nil
		}, func(_ *Server, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("complete err=%v, want context.Canceled", err)
			}
			close(done)
		}); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for work")
		}
		taskCancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for completion")
		}
		cancel()
		<-errCh
	})

	t.Run("alreadyCancelledBeforeStart", func(t *testing.T) {
		srv := NewServer(ServerConfig{TickRate: 1000, TaskWorkers: 1})
		ready := make(chan struct{})
		done := make(chan struct{})
		srv.OnTickStart(func(uint64) {
			select {
			case <-ready:
			default:
				close(ready)
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Run(ctx) }()
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Run")
		}

		taskCtx, taskCancel := context.WithCancel(context.Background())
		taskCancel()
		var sawDoneImmediately atomic.Bool
		if err := srv.SubmitTask(taskCtx, func(c context.Context) error {
			select {
			case <-c.Done():
				sawDoneImmediately.Store(true)
			default:
				t.Error("already-cancelled caller context not visible synchronously to work")
			}
			return nil // nil-error precedence must surface context.Canceled
		}, func(_ *Server, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("complete err=%v, want context.Canceled", err)
			}
			close(done)
		}); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for completion")
		}
		if !sawDoneImmediately.Load() {
			t.Fatal("work did not observe cancelled context at start")
		}
		cancel()
		<-errCh
	})

	t.Run("cancelledWhileQueued", func(t *testing.T) {
		srv := NewServer(ServerConfig{
			TickRate:          1000,
			TaskWorkers:       1,
			TaskQueueCapacity: 4,
		})
		ready := make(chan struct{})
		done := make(chan struct{})
		srv.OnTickStart(func(uint64) {
			select {
			case <-ready:
			default:
				close(ready)
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Run(ctx) }()
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Run")
		}

		blockFirst := make(chan struct{})
		firstStarted := make(chan struct{})
		if err := srv.SubmitTask(context.Background(), func(context.Context) error {
			close(firstStarted)
			<-blockFirst
			return nil
		}, func(*Server, error) {}); err != nil {
			t.Fatalf("blocker SubmitTask: %v", err)
		}
		select {
		case <-firstStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for blocker worker")
		}

		taskCtx, taskCancel := context.WithCancel(context.Background())
		var sawDoneImmediately atomic.Bool
		if err := srv.SubmitTask(taskCtx, func(c context.Context) error {
			select {
			case <-c.Done():
				sawDoneImmediately.Store(true)
			default:
				t.Error("caller cancel while queued not visible synchronously when work starts")
			}
			return nil
		}, func(_ *Server, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("complete err=%v, want context.Canceled", err)
			}
			close(done)
		}); err != nil {
			t.Fatalf("queued SubmitTask: %v", err)
		}
		taskCancel()
		// Ensure cancel wins before the queued task becomes runnable.
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if taskCtx.Err() != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		close(blockFirst)

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for queued-cancelled completion")
		}
		if !sawDoneImmediately.Load() {
			t.Fatal("queued task did not observe cancelled caller context at start")
		}
		cancel()
		<-errCh
	})

	t.Run("server", func(t *testing.T) {
		srv := NewServer(ServerConfig{TickRate: 1000})
		ready := make(chan struct{})
		started := make(chan struct{})
		var completed atomic.Bool
		srv.OnTickStart(func(uint64) {
			select {
			case <-ready:
			default:
				close(ready)
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Run(ctx) }()
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Run")
		}

		if err := srv.SubmitTask(context.Background(), func(c context.Context) error {
			close(started)
			<-c.Done()
			return nil
		}, func(*Server, error) {
			completed.Store(true)
		}); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for work")
		}
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for cooperative pool exit")
		}
		if completed.Load() {
			t.Fatal("completion ran after shutdown")
		}
	})
}

func TestSubmitTaskPanicProducesCompletion(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	ready := make(chan struct{})
	done := make(chan struct{})
	srv.OnTickStart(func(uint64) {
		select {
		case <-ready:
		default:
			close(ready)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run")
	}

	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		panic("task-boom")
	}, func(_ *Server, err error) {
		if !errors.Is(err, ErrTaskPanicked) {
			t.Errorf("complete err=%v, want ErrTaskPanicked", err)
		}
		close(done)
	}); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for panic completion")
	}
	cancel()
	<-errCh
}

func TestSubmitTaskNoCompletionAfterLoopExit(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	gate := make(chan struct{})
	ready := make(chan struct{})
	var ran atomic.Bool
	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			close(ready)
			<-gate
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	if err := srv.SubmitTask(context.Background(), func(context.Context) error {
		return nil
	}, func(*Server, error) {
		ran.Store(true)
	}); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	// Wait until completion is queued (or worker finished) while still gated.
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.completionQueue) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	deadline = time.Now().Add(2 * time.Second)
	for {
		if errors.Is(srv.Post(func(*Server) {}), ErrServerNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for shutdown")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run exit")
	}
	if ran.Load() {
		t.Fatal("completion ran after loop exit")
	}
}

type phaseOrderEntity struct {
	id    int64
	mu    *sync.Mutex
	order *[]string
}

func (e *phaseOrderEntity) EntityID() int64              { return e.id }
func (e *phaseOrderEntity) SetEntityID(id int64)         { e.id = id }
func (e *phaseOrderEntity) TypeName() string             { return "phase" }
func (e *phaseOrderEntity) Position() (float32, float32) { return 0, 0 }
func (e *phaseOrderEntity) IsGlobal() bool               { return false }
func (e *phaseOrderEntity) FlushUpdate() ([]byte, error) { return []byte{1}, nil }
func (e *phaseOrderEntity) FullUpdate() ([]byte, error)  { return []byte{1}, nil }
func (e *phaseOrderEntity) Tick(float64) {
	e.mu.Lock()
	*e.order = append(*e.order, "entity")
	e.mu.Unlock()
}

type phaseCollisionBackend struct {
	mu    *sync.Mutex
	order *[]string
}

func (b *phaseCollisionBackend) Add(int64, collision.Shape, uint32, uint32, bool) {}
func (b *phaseCollisionBackend) Remove(int64)                                     {}
func (b *phaseCollisionBackend) Set(int64, collision.Shape, uint32, uint32, bool) {}
func (b *phaseCollisionBackend) Update(int64, float64, float64)                   {}
func (b *phaseCollisionBackend) Step(float64) []collision.Contact {
	b.mu.Lock()
	*b.order = append(*b.order, "collision")
	b.mu.Unlock()
	return nil
}
func (b *phaseCollisionBackend) ReadBack(func(int64, float64, float64)) {}

func TestTickPhaseOrderWithAsyncAndSession(t *testing.T) {
	srv := NewServer(ServerConfig{
		TickRate:              1000,
		AsyncCallbacksPerTick: 16,
		StateUpdateLane:       StateUpdateLaneStream,
	})
	var mu sync.Mutex
	var order []string
	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		mu.Unlock()
	}
	done := make(chan struct{})
	gate := make(chan struct{})
	ready := make(chan struct{})

	ent := &phaseOrderEntity{mu: &mu, order: &order}
	if err := srv.CreateEntity(ent); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	srv.SetCollisionBackend(&phaseCollisionBackend{mu: &mu, order: &order})
	srv.OnMessage(func(*Session, []byte) { record("session") })
	srv.OnTick(func(float64, *Server) { record("onTick") })
	srv.OnUpdates(func([][]byte) { record("replication") })
	srv.OnTickEnd(func(uint64, time.Duration) {
		record("onTickEnd")
		select {
		case <-done:
		default:
			close(done)
		}
	})
	srv.OnTickStart(func(tick uint64) {
		if tick == 1 {
			record("onTickStart")
			close(ready)
			<-gate
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick gate")
	}

	srv.completionQueue <- taskCompletion{complete: func(*Server, error) { record("completion") }}
	if err := srv.Post(func(*Server) { record("post") }); err != nil {
		t.Fatalf("Post: %v", err)
	}
	srv.msgQueue <- pendingMsg{kind: msgMessage, sess: &Session{ID: 1}, data: []byte{1}}
	close(gate)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tick end")
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"onTickStart", "completion", "post", "session", "entity", "onTick", "collision", "replication", "onTickEnd"}
	if len(got) != len(want) {
		t.Fatalf("order=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v, want %v", got, want)
		}
	}
	cancel()
	<-errCh
}
