package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// MinBcryptCost is the minimum bcrypt cost accepted for production password hashes.
const MinBcryptCost = 12

// DevAgentToken is the agent token used outside production when none is set.
const DevAgentToken = "dev-token"

var placeholderPattern = regexp.MustCompile(`(?i)change.?me|replace|placeholder|example|default`)

// Config configures a Hub. Zero values take the defaults documented per field.
type Config struct {
	// AgentToken authenticates game-server agents (Authorization: Bearer).
	// Defaults to DevAgentToken outside production; required in production.
	AgentToken string
	// PasswordHash is a bcrypt hash of the shared UI password. Empty disables
	// login (development only).
	PasswordHash string
	// SessionMaxAge bounds a login session. Default 10h.
	SessionMaxAge time.Duration
	// CookieName names the session cookie. Default "golem_inspector_session".
	CookieName string
	// CookieSecure marks the session cookie Secure (set when serving TLS or
	// behind a TLS-terminating proxy).
	CookieSecure bool
	// PublicOrigins lists browser origins allowed to log in and open the UI
	// socket. When empty, the request Origin must match the request Host.
	PublicOrigins []string
	// ObserveGrace delays stopping an agent after its last viewer leaves. Default 30s.
	ObserveGrace time.Duration
	// Connection limits. Defaults: 256 agents, 32 pending, 4 pending per IP, 128 clients.
	MaxAgents, MaxPendingAgents, MaxPendingAgentsPerIP, MaxClients int
	// AgentMaxFrameBytes bounds a single agent frame. Default 16 MiB.
	AgentMaxFrameBytes int64
	// AgentRegisterTimeout bounds the wait for Register. Default 10s.
	AgentRegisterTimeout time.Duration
	// AgentHeartbeatTimeout purges silent agents and offline targets. Default 45s.
	AgentHeartbeatTimeout time.Duration
	// ClientHeartbeatTimeout closes silent browser sockets. Default 15s.
	ClientHeartbeatTimeout time.Duration
	// JanitorInterval is the heartbeat sweep period. Default 5s.
	JanitorInterval time.Duration
	// ClientQueueItems bounds queued items per browser. Default 64.
	ClientQueueItems int
	// MetricsToken enables GET /metrics behind this bearer token.
	MetricsToken string
	// Production enforces deployment rules in Validate.
	Production bool
	// TLS reports that the hub's HTTP server terminates TLS; BehindProxy that a
	// proxy does. Production requires one of them.
	TLS, BehindProxy bool
	// WebFS holds the built UI (index.html at its root). Optional.
	WebFS fs.FS
	// Logger receives operational logs. Defaults to log.Default().
	Logger *log.Logger
}

func (c Config) withDefaults() Config {
	if c.AgentToken == "" && !c.Production {
		c.AgentToken = DevAgentToken
	}
	if c.SessionMaxAge <= 0 {
		c.SessionMaxAge = 10 * time.Hour
	}
	if c.CookieName == "" {
		c.CookieName = "golem_inspector_session"
	}
	if c.ObserveGrace < 0 {
		c.ObserveGrace = 0
	} else if c.ObserveGrace == 0 {
		c.ObserveGrace = 30 * time.Second
	}
	if c.MaxAgents <= 0 {
		c.MaxAgents = 256
	}
	if c.MaxPendingAgents <= 0 {
		c.MaxPendingAgents = 32
	}
	if c.MaxPendingAgentsPerIP <= 0 {
		c.MaxPendingAgentsPerIP = 4
	}
	if c.MaxClients <= 0 {
		c.MaxClients = 128
	}
	if c.AgentMaxFrameBytes <= 0 {
		c.AgentMaxFrameBytes = 16 << 20
	}
	if c.AgentRegisterTimeout <= 0 {
		c.AgentRegisterTimeout = 10 * time.Second
	}
	if c.AgentHeartbeatTimeout <= 0 {
		c.AgentHeartbeatTimeout = 45 * time.Second
	}
	if c.ClientHeartbeatTimeout <= 0 {
		c.ClientHeartbeatTimeout = 15 * time.Second
	}
	if c.JanitorInterval <= 0 {
		c.JanitorInterval = 5 * time.Second
	}
	if c.ClientQueueItems <= 0 {
		c.ClientQueueItems = 64
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	return c
}

// AuthEnabled reports whether the UI requires the shared password.
func (c Config) AuthEnabled() bool { return c.PasswordHash != "" }

// Validate checks cross-field rules and, in production, deployment rules.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if c.PasswordHash != "" {
		cost, err := bcrypt.Cost([]byte(c.PasswordHash))
		if err != nil {
			add("password hash is not a bcrypt hash")
		} else if c.Production && cost < MinBcryptCost {
			add("password hash bcrypt cost %d is below %d", cost, MinBcryptCost)
		}
	}
	for _, o := range c.PublicOrigins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
			add("public origin %q must be scheme://host[:port]", o)
		}
	}
	if c.MetricsToken != "" {
		if len(c.MetricsToken) < 32 || placeholderPattern.MatchString(c.MetricsToken) {
			add("metrics token must be at least 32 characters and not a placeholder")
		}
		if c.MetricsToken == c.AgentToken {
			add("metrics token must differ from the agent token")
		}
	}
	if c.MaxPendingAgentsPerIP > 0 && c.MaxPendingAgents > 0 && c.MaxPendingAgentsPerIP > c.MaxPendingAgents {
		add("pending agents per IP exceeds the pending agent limit")
	}
	if c.Production {
		if !c.TLS && !c.BehindProxy {
			add("production requires TLS or an explicit TLS-terminating proxy")
		}
		if c.PasswordHash == "" {
			add("production requires a password hash")
		}
		if !c.CookieSecure {
			add("production requires secure cookies")
		}
		if len(c.AgentToken) < 32 || placeholderPattern.MatchString(c.AgentToken) || c.AgentToken == DevAgentToken {
			add("production requires an agent token of at least 32 characters that is not a placeholder")
		}
		if len(c.PublicOrigins) != 1 || !strings.HasPrefix(c.PublicOrigins[0], "https://") {
			add("production requires exactly one https public origin")
		}
		if c.WebFS == nil {
			add("production requires the embedded web UI")
		}
	}
	return errors.Join(errs...)
}

// ValidateTLSFiles loads a certificate/key pair and checks that the key
// matches, the certificate is currently valid and, when host is set, that it
// covers host.
func ValidateTLSFiles(certFile, keyFile, host string, now time.Time) error {
	if (certFile == "") != (keyFile == "") {
		return errors.New("TLS certificate and key must be set together")
	}
	if certFile == "" {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("TLS key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("TLS certificate: %w", err)
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("TLS certificate is not valid at %s (valid %s to %s)", now.Format(time.RFC3339), leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339))
	}
	if host != "" {
		if err := leaf.VerifyHostname(host); err != nil {
			return fmt.Errorf("TLS certificate: %w", err)
		}
	}
	return nil
}
