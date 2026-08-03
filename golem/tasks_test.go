package golem

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	if cap(srv.postQueue) != defaultPostQueueCapacity {
		t.Fatalf("postQueue cap=%d, want %d", cap(srv.postQueue), defaultPostQueueCapacity)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for negative PostQueueCapacity")
		}
	}()
	_ = NewServer(ServerConfig{PostQueueCapacity: -1})
}
