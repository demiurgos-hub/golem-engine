package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
	maxLoginBody     = 4 << 10
)

// sessionStore keeps opaque session ids server-side so logout and restarts
// invalidate sessions immediately; the cookie carries only the random id.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // sid -> expiry
}

func newSessionStore() *sessionStore { return &sessionStore{sessions: map[string]time.Time{}} }

func (s *sessionStore) create(maxAge time.Duration, now time.Time) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("hub: crypto/rand failed: " + err.Error())
	}
	sid := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.sessions[sid] = now.Add(maxAge)
	s.mu.Unlock()
	return sid
}

func (s *sessionStore) valid(sid string, now time.Time) bool {
	if sid == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[sid]
	if ok && now.After(exp) {
		delete(s.sessions, sid)
		return false
	}
	return ok
}

func (s *sessionStore) revoke(sid string) {
	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()
}

func (s *sessionStore) sweep(now time.Time) {
	s.mu.Lock()
	for sid, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, sid)
		}
	}
	s.mu.Unlock()
}

// loginLimiter counts failed logins per client IP in a fixed window. It keys on
// the TCP peer address and ignores forwarding headers, which clients control.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{failures: map[string][]time.Time{}} }

func (l *loginLimiter) blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(ip, now)) >= loginMaxFailures
}

func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	l.failures[ip] = append(l.pruneLocked(ip, now), now)
	l.mu.Unlock()
}

func (l *loginLimiter) pruneLocked(ip string, now time.Time) []time.Time {
	list := l.failures[ip]
	keep := list[:0]
	for _, t := range list {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.failures, ip)
		return nil
	}
	l.failures[ip] = keep
	return keep
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// originAllowed checks the browser Origin against PublicOrigins, or against
// the request Host when no public origins are configured.
func (h *Hub) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if len(h.cfg.PublicOrigins) > 0 {
		for _, o := range h.cfg.PublicOrigins {
			if strings.EqualFold(strings.TrimSuffix(o, "/"), origin) {
				return true
			}
		}
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (h *Hub) sessionID(r *http.Request) string {
	c, err := r.Cookie(h.cfg.CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// authenticated reports whether r carries a live session (always true with
// auth disabled) and returns its session id.
func (h *Hub) authenticated(r *http.Request) (string, bool) {
	if !h.cfg.AuthEnabled() {
		return "", true
	}
	sid := h.sessionID(r)
	return sid, h.sessions.valid(sid, time.Now())
}

func (h *Hub) handleSession(w http.ResponseWriter, r *http.Request) {
	_, ok := h.authenticated(r)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": ok, "authDisabled": !h.cfg.AuthEnabled()})
}

func (h *Hub) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !h.cfg.AuthEnabled() {
		writeError(w, http.StatusBadRequest, "auth_disabled")
		return
	}
	if !h.originAllowed(r) {
		h.metrics.inc("rejections_total", "surface", "login", "reason", "origin")
		writeError(w, http.StatusForbidden, "origin_not_allowed")
		return
	}
	ip, now := remoteIP(r), time.Now()
	if h.limiter.blocked(ip, now) {
		h.metrics.inc("rejections_total", "surface", "login", "reason", "rate_limited")
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(h.cfg.PasswordHash), []byte(body.Password)) != nil {
		h.limiter.fail(ip, now)
		h.metrics.inc("rejections_total", "surface", "login", "reason", "invalid_credentials")
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	sid := h.sessions.create(h.cfg.SessionMaxAge, now)
	http.SetCookie(w, &http.Cookie{
		Name: h.cfg.CookieName, Value: sid, Path: "/", HttpOnly: true, Secure: h.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(h.cfg.SessionMaxAge / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (h *Hub) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.cfg.AuthEnabled() && !h.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed")
		return
	}
	sid, ok := h.authenticated(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	if sid != "" {
		h.sessions.revoke(sid)
		h.closeClientsForSession(sid, "auth_revoked")
	}
	http.SetCookie(w, &http.Cookie{Name: h.cfg.CookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// agentTokenOK compares the bearer token in constant time.
func (h *Hub) agentTokenOK(r *http.Request) bool {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if len(got) <= len(prefix) || !strings.EqualFold(got[:len(prefix)], prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(h.cfg.AgentToken)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
