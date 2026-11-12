package golem

import (
	"sync"
	"testing"
	"time"
)

func TestNavigationClock(t *testing.T) {
	previous := NavigationNow()
	advanced := false
	for i := 0; i < 10000; i++ {
		now := NavigationNow()
		if now.Before(previous) {
			t.Fatalf("clock moved backward: %s", now.Sub(previous))
		}
		advanced = advanced || now.After(previous)
		previous = now
	}
	if !advanced {
		t.Fatal("clock did not advance across repeated reads")
	}
	start := NavigationNow()
	time.Sleep(20 * time.Millisecond)
	if elapsed := navigationSince(start); elapsed < 10*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("unexpected elapsed sleep interval: %s", elapsed)
	}
	t.Run("Allocations", func(t *testing.T) {
		if allocations := testing.AllocsPerRun(1000, func() { NavigationNow() }); allocations != 0 {
			t.Fatalf("clock allocated on read: %g", allocations)
		}
	})
}

func TestNavigationClockConcurrentReads(t *testing.T) {
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			previous := NavigationNow()
			for i := 0; i < 1000; i++ {
				now := NavigationNow()
				if now.Before(previous) {
					t.Error("clock moved backward within a reader")
					return
				}
				previous = now
			}
		}()
	}
	workers.Wait()
}
