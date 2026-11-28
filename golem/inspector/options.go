package inspector

import (
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

// Environment variables read by OptionsFromEnv.
const (
	EnvHubURL   = "GOLEM_INSPECTOR_HUB_URL"
	EnvToken    = "GOLEM_INSPECTOR_TOKEN"
	EnvTargetID = "GOLEM_INSPECTOR_TARGET_ID"
	EnvLabels   = "GOLEM_INSPECTOR_LABELS"
	EnvRateHz   = "GOLEM_INSPECTOR_RATE_HZ"
)

// Options configures an inspector agent.
type Options struct {
	// HubURL is the hub's agent endpoint, e.g. wss://inspector.example.com/ws/agent.
	// An empty HubURL disables the agent.
	HubURL string
	// Token authenticates the agent; it is sent as an Authorization bearer header.
	Token string
	// TargetID identifies this server in the hub. Defaults to the hostname.
	TargetID string
	// Labels are free-form key/value metadata shown and grouped in the UI.
	Labels map[string]string
	// RateHz caps how often coalesced changes are sent while observed. Default 10.
	RateHz float64
	// MaxFrameBytes is the soft size at which snapshots and batches are split.
	// Default 256 KiB.
	MaxFrameBytes int
	// QueueFrames bounds queued snapshots and batches (each possibly several
	// frames) waiting for the network. When full the agent drops the stream and
	// restarts it with a fresh snapshot once the queue drains. Default 128.
	QueueFrames int
	// EngineVersion is reported to the hub. Defaults to the golem-engine module
	// version from build info.
	EngineVersion string
	// HTTPClient is used to dial the hub (for custom TLS roots). Optional.
	HTTPClient *http.Client
	// Logger receives connection diagnostics. Defaults to log.Default().
	Logger *log.Logger
	// MinBackoff and MaxBackoff bound reconnect delays. Defaults 500ms and 15s.
	MinBackoff, MaxBackoff time.Duration
	// PingInterval is the heartbeat period. Default 15s.
	PingInterval time.Duration
}

// OptionsFromEnv reads agent options from GOLEM_INSPECTOR_HUB_URL,
// GOLEM_INSPECTOR_TOKEN, GOLEM_INSPECTOR_TARGET_ID, GOLEM_INSPECTOR_LABELS
// ("k=v,k=v") and GOLEM_INSPECTOR_RATE_HZ (a finite positive number).
// Blank variables leave defaults. Malformed values are logged by name and
// ignored. Valid label pairs are kept when another pair is malformed.
func OptionsFromEnv() Options {
	opts := Options{
		HubURL:   strings.TrimSpace(os.Getenv(EnvHubURL)),
		Token:    strings.TrimSpace(os.Getenv(EnvToken)),
		TargetID: strings.TrimSpace(os.Getenv(EnvTargetID)),
	}
	if opts.HubURL != "" {
		if err := validateHubURL(opts.HubURL); err != nil {
			logIgnoredEnv(EnvHubURL, err.Error())
			opts.HubURL = ""
		}
	}
	if !httpguts.ValidHeaderFieldValue(opts.Token) {
		logIgnoredEnv(EnvToken, "want a valid Authorization header value")
		opts.Token = ""
	}
	if len(opts.TargetID) > 128 || !utf8.ValidString(opts.TargetID) || strings.ContainsFunc(opts.TargetID, unicode.IsControl) {
		logIgnoredEnv(EnvTargetID, "want a UTF-8 identifier of at most 128 bytes without control characters")
		opts.TargetID = ""
	}
	var malformedLabels bool
	opts.Labels, malformedLabels = parseLabels(os.Getenv(EnvLabels))
	if malformedLabels {
		logIgnoredEnv(EnvLabels, "want comma-separated key=value pairs")
	}
	if raw := strings.TrimSpace(os.Getenv(EnvRateHz)); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 && !math.IsInf(v, 0) {
			opts.RateHz = v
		} else {
			logIgnoredEnv(EnvRateHz, "want a finite positive number")
		}
	}
	return opts
}

func validateHubURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("inspector: hub URL must be an absolute ws:// or wss:// URL")
	}
	if u.User != nil || u.Query().Has("token") {
		return errors.New("inspector: pass the hub token in Options.Token, not the URL")
	}
	return nil
}

// Log the variable name only. A malformed URL or another field can contain a secret.
func logIgnoredEnv(name, reason string) {
	log.Printf("golem/inspector: ignoring %s: %s", name, reason)
}

// ParseLabels parses "k=v,k=v" into a map, ignoring empty or malformed pairs.
func ParseLabels(s string) map[string]string {
	labels, _ := parseLabels(s)
	return labels
}

func parseLabels(s string) (map[string]string, bool) {
	out := map[string]string{}
	malformed := false
	for _, pair := range strings.Split(s, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if ok && k != "" && utf8.ValidString(pair) {
			out[k] = v
		} else {
			malformed = true
		}
	}
	if len(out) == 0 {
		return nil, malformed
	}
	return out, malformed
}

func (o Options) withDefaults() Options {
	if o.TargetID == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			o.TargetID = h
		} else {
			o.TargetID = "golem-server"
		}
	}
	if o.RateHz <= 0 {
		o.RateHz = 10
	}
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = 256 << 10
	}
	if o.QueueFrames <= 0 {
		o.QueueFrames = 128
	}
	if o.EngineVersion == "" {
		o.EngineVersion = engineVersion()
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = 500 * time.Millisecond
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = 15 * time.Second
		if o.MaxBackoff < o.MinBackoff {
			o.MaxBackoff = o.MinBackoff
		}
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 15 * time.Second
	}
	return o
}

func engineVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	const module = "github.com/demiurgos-hub/golem-engine"
	if info.Main.Path == module {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == module {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return ""
}
