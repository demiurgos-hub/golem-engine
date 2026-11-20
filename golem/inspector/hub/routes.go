package hub

import (
	"crypto/subtle"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Reserved path prefixes never fall back to the SPA.
var reservedPrefixes = []string{"/api", "/auth", "/ws", "/health", "/live", "/ready", "/metrics"}

func (h *Hub) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", h.handleAgentSocket)
	mux.HandleFunc("/ws/client", h.handleClientSocket)
	mux.HandleFunc("/auth/session", h.handleSession)
	mux.HandleFunc("/auth/login", h.handleLogin)
	mux.HandleFunc("/auth/logout", h.handleLogout)
	mux.HandleFunc("/api/targets", h.handleTargets)
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"live": true})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if h.shuttingDown.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"ready": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/metrics", h.handleMetrics)
	mux.Handle("/", h.staticHandler())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Referrer-Policy", "no-referrer")
		hdr.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (h *Hub) handleTargets(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticated(r); !ok {
		writeError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": h.Targets()})
}

func (h *Hub) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if h.cfg.MetricsToken == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.MetricsToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	h.metrics.write(w, h.gauges())
}

func (h *Hub) gauges() []gauge {
	h.mu.Lock()
	defer h.mu.Unlock()
	registered, connected, ready, viewers, entities := 0, 0, 0, 0, 0
	for _, t := range h.targets {
		registered++
		if t.agent != nil {
			connected++
		}
		if t.world.ready {
			ready++
		}
		viewers += len(t.viewers)
		entities += len(t.world.entities)
	}
	return []gauge{
		{name: "websocket_connections", help: "Open WebSocket connections by role.", series: map[string]float64{
			labelKey([]string{"role", "agent"}):  float64(len(h.agents)),
			labelKey([]string{"role", "client"}): float64(len(h.clients)),
		}},
		{name: "targets", help: "Known targets by state.", series: map[string]float64{
			labelKey([]string{"state", "registered"}): float64(registered),
			labelKey([]string{"state", "connected"}):  float64(connected),
			labelKey([]string{"state", "ready"}):      float64(ready),
		}},
		{name: "viewers", help: "Browser subscriptions across targets.", series: map[string]float64{"": float64(viewers)}},
		{name: "entities", help: "Entities held for observed targets.", series: map[string]float64{"": float64(entities)}},
	}
}

func isReserved(p string) bool {
	for _, prefix := range reservedPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// staticHandler serves the embedded UI: hashed /assets/* are immutable,
// index.html is never cached, unknown extension-less GETs that accept HTML get
// the SPA index, and everything else is a JSON 404.
func (h *Hub) staticHandler() http.Handler {
	webFS := h.cfg.WebFS
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReserved(r.URL.Path) || webFS == nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(webFS, name); err == nil && !info.IsDir() {
			serveFile(w, r, webFS, name)
			return
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "static_error")
			return
		}
		if path.Ext(name) == "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			serveFile(w, r, webFS, "index.html")
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, webFS fs.FS, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if name == "index.html" {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	}
	http.ServeFileFS(w, r, webFS, name)
}
