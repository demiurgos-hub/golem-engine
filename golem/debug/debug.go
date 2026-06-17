package debug

import (
	"context"
	"log"
	"net/http"
	httppprof "net/http/pprof"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"time"

	"golem-engine/golem"
)

// StartPprof starts a pprof debug HTTP server on addr (e.g. "127.0.0.1:6060").
// All pprof handlers are mounted on a dedicated ServeMux so http.DefaultServeMux
// is not affected. Call from main when a debug flag is set; never import this
// package in production binaries.
func StartPprof(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	log.Printf("golem/debug: pprof listening on http://%s/debug/pprof/", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("golem/debug: pprof server stopped: %v", err)
		}
	}()
	return nil
}

// TickProfilerOptions configures AttachTickProfiler.
type TickProfilerOptions struct {
	Context context.Context
	Trace   bool
}

// AttachTickProfiler labels pprof samples by tick and optionally wraps each
// tick in a runtime/trace region.
func AttachTickProfiler(srv *golem.Server, opts TickProfilerOptions) {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	var tickRegion *trace.Region
	srv.OnTickStart(func(tick uint64) {
		lctx := pprof.WithLabels(opts.Context, pprof.Labels("tick", strconv.FormatUint(tick, 10)))
		pprof.SetGoroutineLabels(lctx)
		if opts.Trace {
			tickRegion = trace.StartRegion(lctx, "tick")
		}
	})
	srv.OnTickEnd(func(uint64, time.Duration) {
		if tickRegion != nil {
			tickRegion.End()
			tickRegion = nil
		}
	})
}

// SlowTickLoggerOptions configures AttachSlowTickLogger.
type SlowTickLoggerOptions struct {
	Budget        time.Duration
	Interval      time.Duration
	Window        int
	Logf          func(format string, args ...any)
	MessagePrefix string
}

// AttachSlowTickLogger logs slow ticks and periodic rolling wall-time samples.
func AttachSlowTickLogger(srv *golem.Server, opts SlowTickLoggerOptions) {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.Window <= 0 {
		opts.Window = 10
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.MessagePrefix == "" {
		opts.MessagePrefix = "golem/debug"
	}
	var lastLog time.Time
	recentMs := make([]float64, 0, opts.Window)
	srv.OnTickEnd(func(tick uint64, wall time.Duration) {
		slow := opts.Budget > 0 && wall > opts.Budget
		if !slow && time.Since(lastLog) < opts.Interval {
			return
		}
		ms := float64(wall.Nanoseconds()) / 1e6
		recentMs = append(recentMs, ms)
		if len(recentMs) > opts.Window {
			recentMs = recentMs[len(recentMs)-opts.Window:]
		}
		lastLog = time.Now()
		if len(recentMs) >= opts.Window {
			var sum float64
			for _, v := range recentMs {
				sum += v
			}
			opts.Logf("%s: tick %d wall=%.2fms avg%d=%.2fms", opts.MessagePrefix, tick, ms, opts.Window, sum/float64(opts.Window))
			return
		}
		opts.Logf("%s: tick %d wall=%.2fms", opts.MessagePrefix, tick, ms)
	})
}
