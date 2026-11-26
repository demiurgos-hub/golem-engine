package host

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"time"
)

// Limits of GET /profile/{kind} on the admin listener. A timed capture (cpu,
// trace) takes ?seconds, defaulting to the Default value and refused above the
// Max value; the snapshot kinds (heap, allocs, goroutine) take no parameters.
const (
	AdminProfileCPUDefaultSeconds   = 30
	AdminProfileCPUMaxSeconds       = 60
	AdminProfileTraceDefaultSeconds = 5
	AdminProfileTraceMaxSeconds     = 15
)

// adminProfilePrefix is the path prefix of the profile endpoint.
const adminProfilePrefix = "/profile/"

// adminProfileWriteMargin is added to a timed capture's duration for the
// response's write deadline, which replaces the listener-wide WriteTimeout.
const adminProfileWriteMargin = 10 * time.Second

// errProfileBusy reports that the runtime is already producing a CPU profile
// or an execution trace, whether for this listener or for another caller.
var errProfileBusy = errors.New("profile already in progress")

// adminProfileKind describes one profile kind. Timed kinds have a duration;
// the others write a runtime/pprof profile in its compressed protobuf format.
type adminProfileKind struct {
	defaultSeconds, maxSeconds int
	start                      func(io.Writer) error
	stop                       func()
	filename                   string
}

func (k adminProfileKind) timed() bool { return k.maxSeconds > 0 }

var adminProfileKinds = map[string]adminProfileKind{
	"cpu": {defaultSeconds: AdminProfileCPUDefaultSeconds, maxSeconds: AdminProfileCPUMaxSeconds,
		start: pprof.StartCPUProfile, stop: pprof.StopCPUProfile, filename: "cpu.pb.gz"},
	"trace": {defaultSeconds: AdminProfileTraceDefaultSeconds, maxSeconds: AdminProfileTraceMaxSeconds,
		start: trace.Start, stop: trace.Stop, filename: "trace.out"},
	"heap":      {filename: "heap.pb.gz"},
	"allocs":    {filename: "allocs.pb.gz"},
	"goroutine": {filename: "goroutine.pb.gz"},
}

// handleProfile serves GET /profile/{kind}. The whole profile is buffered
// before the headers are written, so every failure is a JSON error.
func (a *adminServer) handleProfile(w http.ResponseWriter, r *http.Request, name string) {
	if !a.cfg.opts.Profiling {
		writeAdminError(w, http.StatusNotFound, "profiling_disabled", "profiling is not enabled on this server")
		return
	}
	kind, ok := adminProfileKinds[name]
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid_profile", "unknown profile kind; use cpu, trace, heap, allocs or goroutine")
		return
	}
	seconds, ok := profileSeconds(kind, r.URL.Query().Get("seconds"), r.URL.Query().Has("seconds"))
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid_profile",
			"seconds must be a whole number from 1 to "+strconv.Itoa(kind.maxSeconds)+" for "+name+" and is not accepted for other kinds")
		return
	}
	// Starting is fine: a slow start (such as a navigation bake) is worth profiling.
	if a.ctx.Err() != nil {
		writeAdminNotReady(w, "stopping")
		return
	}
	var buf bytes.Buffer
	if kind.timed() {
		select {
		case a.profileSem <- struct{}{}:
		default:
			writeAdminError(w, http.StatusConflict, "profile_in_progress", "a cpu or trace capture is already running")
			return
		}
		defer func() { <-a.profileSem }()
		duration := time.Duration(seconds) * time.Second
		// Not supported by every ResponseWriter (tests); the listener-wide
		// WriteTimeout then applies.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(duration + adminProfileWriteMargin))
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stopOnShutdown := context.AfterFunc(a.ctx, cancel)
		defer stopOnShutdown()
		err := captureTimedProfile(ctx, &buf, duration, kind)
		switch {
		case errors.Is(err, errProfileBusy):
			writeAdminError(w, http.StatusConflict, "profile_in_progress", "the runtime is already producing this kind of profile")
			return
		case a.ctx.Err() != nil:
			writeAdminNotReady(w, "stopping")
			return
		case err != nil:
			// The client went away; nobody is left to answer.
			return
		}
	} else if err := pprof.Lookup(name).WriteTo(&buf, 0); err != nil {
		log.Printf("golem/host: admin %s profile failed: %v", name, err)
		writeAdminError(w, http.StatusInternalServerError, "profile_failed", "the profile could not be written")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+kind.filename+`"`)
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// profileSeconds returns the capture duration for kind from the raw ?seconds
// value, and false when the value is malformed, out of range, or given for a
// kind that takes none.
func profileSeconds(kind adminProfileKind, raw string, present bool) (int, bool) {
	if !kind.timed() {
		return 0, !present
	}
	if !present {
		return kind.defaultSeconds, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > kind.maxSeconds {
		return 0, false
	}
	return n, true
}

// captureTimedProfile records kind into w for d, stopping early when ctx ends
// and returning ctx's error then. A runtime that is already recording the
// same kind is errProfileBusy.
func captureTimedProfile(ctx context.Context, w io.Writer, d time.Duration, kind adminProfileKind) error {
	if err := kind.start(w); err != nil {
		return errProfileBusy
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		kind.stop()
		return nil
	case <-ctx.Done():
		kind.stop()
		return ctx.Err()
	}
}
