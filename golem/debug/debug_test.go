package debug

import (
	"context"
	"strings"
	"testing"
	"time"

	"golem-engine/golem"
)

func TestAttachSlowTickLoggerLogsRollingAverage(t *testing.T) {
	srv := golem.NewServer(golem.ServerConfig{TickRate: 1000})
	var lines []string
	AttachSlowTickLogger(srv, SlowTickLoggerOptions{
		Budget:   time.Hour,
		Interval: time.Hour,
		Window:   1,
		Logf: func(format string, args ...any) {
			lines = append(lines, format)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	srv.OnTickEnd(func(tick uint64, wall time.Duration) {
		if tick >= 2 {
			cancel()
		}
	})
	if err := srv.Run(ctx); err != context.Canceled {
		t.Fatalf("Run: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("expected slow tick log")
	}
	if !strings.Contains(lines[len(lines)-1], "avg%d") {
		t.Fatalf("last log format = %q, want rolling average", lines[len(lines)-1])
	}
}
