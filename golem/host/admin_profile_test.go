package host

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// profileDo issues GET path against a live admin handler and returns the
// status, headers and raw body.
func profileDo(t *testing.T, url, token string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, body
}

func TestAdminProfileDisabledByDefault(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken}, nil)
	a.ready.Store(true)
	if resp := serveAdmin(t, a, http.MethodGet, "/profile/heap", ""); resp.status != http.StatusUnauthorized {
		t.Fatalf("without token = %d %s", resp.status, resp.raw)
	}
	resp := serveAdmin(t, a, http.MethodGet, "/profile/heap", testToken)
	if resp.status != http.StatusNotFound || resp.body["error"] != "profiling_disabled" {
		t.Fatalf("disabled = %d %s", resp.status, resp.raw)
	}
	if info := serveAdmin(t, a, http.MethodGet, "/info", testToken); info.body["profiling"] != false {
		t.Fatalf("/info profiling = %v", info.body["profiling"])
	}
}

func TestAdminProfileRejectsBadRequests(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Profiling: true}, nil)
	for _, tc := range []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/profile/block", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/cmdline", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/cpu?seconds=0", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/cpu?seconds=61", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/cpu?seconds=1.5", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/cpu?seconds=", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/trace?seconds=16", http.StatusBadRequest, "invalid_profile"},
		{http.MethodGet, "/profile/heap?seconds=5", http.StatusBadRequest, "invalid_profile"},
		{http.MethodPost, "/profile/heap", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/profile/", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/profile/heap/x", http.StatusNotFound, "not_found"},
	} {
		resp := serveAdmin(t, a, tc.method, tc.path, testToken)
		if resp.status != tc.status || resp.body["error"] != tc.code {
			t.Errorf("%s %s = %d %s, want %d %s", tc.method, tc.path, resp.status, resp.raw, tc.status, tc.code)
		}
	}
}

func TestAdminProfileSnapshotKinds(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Profiling: true}, nil)
	// Not ready yet: profiles are still served while the server starts.
	ts := httptest.NewServer(a)
	defer ts.Close()
	for _, kind := range []string{"heap", "allocs", "goroutine"} {
		status, h, body := profileDo(t, ts.URL+"/profile/"+kind, testToken)
		if status != http.StatusOK {
			t.Fatalf("%s = %d %s", kind, status, body)
		}
		if ct := h.Get("Content-Type"); ct != "application/octet-stream" {
			t.Fatalf("%s Content-Type = %q", kind, ct)
		}
		if cd := h.Get("Content-Disposition"); !strings.Contains(cd, kind+".pb.gz") {
			t.Fatalf("%s Content-Disposition = %q", kind, cd)
		}
		if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s headers = %v", kind, h)
		}
		if !bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
			t.Fatalf("%s is not gzip: % x", kind, body[:min(len(body), 8)])
		}
	}
	if info := serveAdmin(t, a, http.MethodGet, "/info", testToken); info.body["profiling"] != true {
		t.Fatalf("/info profiling = %v", info.body["profiling"])
	}
}

func TestAdminProfileTimedCaptureIsExclusive(t *testing.T) {
	a, _ := newTestAdmin(t, AdminOptions{Token: testToken, Profiling: true}, nil)
	a.ready.Store(true)
	ts := httptest.NewServer(a)
	defer ts.Close()
	type result struct {
		status int
		body   []byte
	}
	first := make(chan result, 1)
	go func() {
		status, _, body := profileDo(t, ts.URL+"/profile/cpu?seconds=1", testToken)
		first <- result{status, body}
	}()
	waitFor(t, "the first capture to start", func() bool { return len(a.profileSem) == 1 })
	for _, path := range []string{"/profile/cpu?seconds=1", "/profile/trace?seconds=1"} {
		resp := serveAdmin(t, a, http.MethodGet, path, testToken)
		if resp.status != http.StatusConflict || resp.body["error"] != "profile_in_progress" {
			t.Fatalf("concurrent %s = %d %s", path, resp.status, resp.raw)
		}
	}
	r := <-first
	if r.status != http.StatusOK || !bytes.HasPrefix(r.body, []byte{0x1f, 0x8b}) {
		t.Fatalf("first capture = %d % x", r.status, r.body[:min(len(r.body), 8)])
	}
	// The slot is free again.
	status, _, body := profileDo(t, ts.URL+"/profile/trace?seconds=1", testToken)
	if status != http.StatusOK || len(body) == 0 {
		t.Fatalf("trace after cpu = %d %s", status, body)
	}
}

func TestAdminProfileStopsOnShutdown(t *testing.T) {
	a, stop := newTestAdmin(t, AdminOptions{Token: testToken, Profiling: true}, nil)
	a.ready.Store(true)
	ts := httptest.NewServer(a)
	defer ts.Close()
	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		status, _, body := profileDo(t, ts.URL+"/profile/cpu?seconds=60", testToken)
		done <- result{status, body}
	}()
	waitFor(t, "the capture to start", func() bool { return len(a.profileSem) == 1 })
	stop()
	select {
	case r := <-done:
		if r.status != http.StatusServiceUnavailable || !strings.Contains(string(r.body), "stopping") {
			t.Fatalf("capture during shutdown = %d %s", r.status, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the capture did not stop when the server stopped")
	}
	if resp := serveAdmin(t, a, http.MethodGet, "/profile/heap", testToken); resp.status != http.StatusServiceUnavailable {
		t.Fatalf("profile after shutdown = %d %s", resp.status, resp.raw)
	}
}
