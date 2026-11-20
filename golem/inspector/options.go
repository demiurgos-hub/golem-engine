package inspector

import (
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
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
// ("k=v,k=v") and GOLEM_INSPECTOR_RATE_HZ. Unset variables leave defaults.
func OptionsFromEnv() Options {
	opts := Options{
		HubURL:   strings.TrimSpace(os.Getenv(EnvHubURL)),
		Token:    strings.TrimSpace(os.Getenv(EnvToken)),
		TargetID: strings.TrimSpace(os.Getenv(EnvTargetID)),
		Labels:   ParseLabels(os.Getenv(EnvLabels)),
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(EnvRateHz)), 64); err == nil && v > 0 {
		opts.RateHz = v
	}
	return opts
}

// ParseLabels parses "k=v,k=v" into a map, ignoring empty or malformed pairs.
func ParseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if ok && k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
