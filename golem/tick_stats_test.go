package golem

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTickStatsBeforeRun(t *testing.T) {
	s := NewServer(ServerConfig{TickRate: 50})
	stats := s.TickStats()
	if stats.Tick != 0 || stats.LastDuration != 0 || stats.MaxDuration != 0 || stats.Overruns != 0 {
		t.Fatalf("TickStats before Run = %+v, want zero timings", stats)
	}
	if stats.TickRate != 50 || stats.Budget != 20*time.Millisecond {
		t.Fatalf("TickStats rate/budget = %d/%s, want 50/20ms", stats.TickRate, stats.Budget)
	}
	if got := s.Transport(); got != TransportWebTransport {
		t.Fatalf("Transport() = %q, want default %q", got, TransportWebTransport)
	}
	if got := NewServer(ServerConfig{Transport: TransportWebSocket, StateUpdateLane: StateUpdateLaneStream}).Transport(); got != TransportWebSocket {
		t.Fatalf("Transport() = %q, want %q", got, TransportWebSocket)
	}
	var zero Server
	if stats := zero.TickStats(); stats.Budget != 0 {
		t.Fatalf("zero Server TickStats budget = %s, want 0", stats.Budget)
	}
}

// TestTickStatsAdvancesAndIsRaceFree reads TickStats from several goroutines
// while the loop runs; run with -race.
func TestTickStatsAdvancesAndIsRaceFree(t *testing.T) {
	s := NewServer(ServerConfig{TickRate: 200})
	slow := 3 * time.Millisecond
	s.OnTick(func(dt float64, srv *Server) {
		if srv.Tick() == 3 {
			time.Sleep(slow) // one slow tick must show up in MaxDuration
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	stopReaders := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var prev uint64
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				stats := s.TickStats()
				if stats.Tick < prev {
					t.Errorf("Tick went backwards: %d after %d", stats.Tick, prev)
					return
				}
				prev = stats.Tick
			}
		}()
	}

	deadline := time.Now().Add(5 * time.Second)
	for s.TickStats().Tick < 10 {
		if time.Now().After(deadline) {
			t.Fatalf("tick did not advance: %+v", s.TickStats())
		}
		time.Sleep(5 * time.Millisecond)
	}
	stats := s.TickStats()
	close(stopReaders)
	wg.Wait()
	cancel()
	<-done

	// LastDuration of an idle tick may read 0 on Windows, whose monotonic
	// clock advances at timer-interrupt resolution; the slow tick checks that
	// durations are measured.
	if stats.LastDuration < 0 {
		t.Fatalf("LastDuration = %s, want >= 0", stats.LastDuration)
	}
	if stats.MaxDuration < slow {
		t.Fatalf("MaxDuration = %s, want >= %s (slow tick 3 within the window)", stats.MaxDuration, slow)
	}
	if stats.MaxDuration < stats.LastDuration {
		t.Fatalf("MaxDuration %s < LastDuration %s", stats.MaxDuration, stats.LastDuration)
	}
	if stats.Budget != 5*time.Millisecond || stats.TickRate != 200 {
		t.Fatalf("budget/rate = %s/%d", stats.Budget, stats.TickRate)
	}
	after := s.TickStats()
	if after.Tick < stats.Tick {
		t.Fatalf("Tick after Run = %d, want >= %d", after.Tick, stats.Tick)
	}
}

func TestTickStatsRecorderWindow(t *testing.T) {
	var r tickStatsRecorder
	const rate = 4
	budget := 10 * time.Millisecond
	r.record(1, 30*time.Millisecond, budget, rate) // overrun, in window 1
	for tick := uint64(2); tick <= 4; tick++ {
		r.record(tick, time.Millisecond, budget, rate)
	}
	if got := time.Duration(r.max.Load()); got != 30*time.Millisecond {
		t.Fatalf("max after window 1 = %s, want 30ms", got)
	}
	// Window 2: the slow tick is still in the previous window.
	for tick := uint64(5); tick <= 8; tick++ {
		r.record(tick, 2*time.Millisecond, budget, rate)
		if got := time.Duration(r.max.Load()); got != 30*time.Millisecond {
			t.Fatalf("tick %d max = %s, want 30ms while the slow tick is in the previous window", tick, got)
		}
	}
	// Window 3: it has aged out.
	r.record(9, time.Millisecond, budget, rate)
	if got := time.Duration(r.max.Load()); got != 2*time.Millisecond {
		t.Fatalf("max after aging = %s, want 2ms", got)
	}
	if r.overruns.Load() != 1 || r.tick.Load() != 9 || time.Duration(r.last.Load()) != time.Millisecond {
		t.Fatalf("overruns=%d tick=%d last=%s", r.overruns.Load(), r.tick.Load(), time.Duration(r.last.Load()))
	}
}
