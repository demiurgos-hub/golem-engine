//go:build unix

package host

import (
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
)

// TestRunAfterServerStopOnSignal delivers a real signal to the process.
// Windows cannot signal itself this way; there the signal path is the same
// context cancellation covered by the other ordering tests.
func TestRunAfterServerStopOnSignal(t *testing.T) {
	srv := newTestServer()
	var calls atomic.Int32
	h := startRun(t, RunOptions{
		Server:  srv,
		Signals: []os.Signal{syscall.SIGUSR1},
		AfterServerStop: func() error {
			calls.Add(1)
			if err := srv.Post(func(*golem.Server) {}); !errors.Is(err, golem.ErrServerNotRunning) {
				t.Errorf("Post inside AfterServerStop = %v, want ErrServerNotRunning", err)
			}
			return nil
		},
	})
	// Ticking means Run has installed its signal handler (it does so before
	// starting Server.Run).
	waitFor(t, "ticks", func() bool { return srv.TickStats().Tick > 3 })
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	if err := h.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil after a signal", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("AfterServerStop called %d times, want 1", n)
	}
}
