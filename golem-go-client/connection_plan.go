package golemclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ConnectionPlanFromRealtimeConfig builds a credential-free primary/fallback
// plan from a realtime bootstrap response.
func ConnectionPlanFromRealtimeConfig(cfg RealtimeConfig) (ConnectionPlan, error) {
	primary, err := ConnectOptionsFromRealtimeConfig(cfg)
	if err != nil {
		return ConnectionPlan{}, err
	}
	plan := ConnectionPlan{Primary: primary}
	if cfg.Fallback != nil {
		fallback, err := connectOptionsFromRealtimeEndpoint(*cfg.Fallback)
		if err != nil {
			return ConnectionPlan{}, fmt.Errorf("golem-go-client: invalid realtime fallback: %w", err)
		}
		plan.Fallback = &fallback
	}
	if err := validateConnectionPlan(plan); err != nil {
		return ConnectionPlan{}, err
	}
	return cloneConnectionPlan(plan), nil
}

// ApplyConnectOptions applies the same URL/TLS customizers used by realtime
// bootstrap helpers to a fresh copy of base.
func ApplyConnectOptions(base ConnectOptions, opts ...ConnectOption) (ConnectOptions, error) {
	options := cloneConnectOptions(base)
	if err := validateConnectOptions(options); err != nil {
		return ConnectOptions{}, err
	}
	builder := &connectOptionsBuilder{options: &options}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(builder); err != nil {
			return ConnectOptions{}, err
		}
	}
	if err := applyConnectOptionQuery(&options, builder.query); err != nil {
		return ConnectOptions{}, err
	}
	return options, nil
}

// ConnectPlan attempts a validated connection plan sequentially. Unsupported
// WebTransport is skipped before resolving credentials, and pre-open
// WebTransport failures may fall back to WebSocket.
func (c *GameClient) ConnectPlan(ctx context.Context, plan ConnectionPlan) error {
	plan = cloneConnectionPlan(plan)
	if err := validateConnectionPlan(plan); err != nil {
		return err
	}
	planKey := connectionPlanAffinityKey(plan)
	ctx, generation, finish := c.beginConnect(ctx, false)
	defer finish()

	c.mu.Lock()
	if c.affinityKey != planKey {
		c.affinityKey = ""
		c.affinityTransport = ""
	}
	stickyWebSocket := plan.Fallback != nil &&
		c.affinityKey == planKey && c.affinityTransport == TransportWebSocket
	c.mu.Unlock()

	candidates := []ConnectOptions{plan.Primary}
	if plan.Fallback != nil {
		candidates = append(candidates, *plan.Fallback)
	}
	if stickyWebSocket {
		candidates = []ConnectOptions{*plan.Fallback}
	}

	var lastErr error
	for _, base := range candidates {
		if err := ctx.Err(); err != nil {
			return sanitizeURLError(err)
		}
		if !c.supportsTransport(base.Transport) {
			lastErr = fmt.Errorf("%w: %s", ErrTransportUnsupported, base.Transport)
			continue
		}

		resolved := cloneConnectOptions(base)
		if plan.ResolveOptions != nil {
			var err error
			resolved, err = plan.ResolveOptions(ctx, cloneConnectOptions(base))
			if err != nil {
				return fmt.Errorf("golem-go-client: resolving %s dial options: %w", base.Transport, sanitizeURLError(err))
			}
		}
		if err := ctx.Err(); err != nil {
			return sanitizeURLError(err)
		}
		resolved = cloneConnectOptions(resolved)
		if resolved.Transport != base.Transport {
			return fmt.Errorf("golem-go-client: dial options resolver changed the candidate transport")
		}
		if !sameEndpointIgnoringQuery(resolved.URL, base.URL) {
			return fmt.Errorf("golem-go-client: dial options resolver may only change URL query parameters")
		}
		if resolved.EventualAckIntervalMs != base.EventualAckIntervalMs ||
			!equalCertificateHashes(resolved.ServerCertificateHashes, base.ServerCertificateHashes) {
			return fmt.Errorf("golem-go-client: dial options resolver changed credential-free endpoint metadata")
		}
		if err := validateConnectOptions(resolved); err != nil {
			return fmt.Errorf("golem-go-client: invalid resolved %s options: %w", base.Transport, err)
		}

		dialCtx := ctx
		cancel := func() {}
		if base.Transport == TransportWebTransport {
			timeout := plan.WebTransportTimeout
			if timeout == 0 {
				timeout = defaultWebTransportConnectTimeout
			}
			dialCtx, cancel = context.WithTimeout(ctx, timeout)
			if plan.Fallback != nil {
				dialCtx = withSuppressedIntermediateTransportLogs(dialCtx)
			}
		}
		channel, err := c.dialCandidate(dialCtx, generation, resolved)
		cancel()
		if err != nil {
			lastErr = sanitizeURLError(err)
			if errors.Is(lastErr, ErrRealtimeRevisionMismatch) || errors.Is(lastErr, ErrRealtimeAuthorizationRejected) {
				return lastErr
			}
			if base.Transport == TransportWebTransport && plan.Fallback != nil && ctx.Err() == nil {
				continue
			}
			return lastErr
		}
		affinityPlanKey := ""
		if plan.Fallback != nil {
			affinityPlanKey = planKey
		}
		if err := c.installChannel(generation, channel, resolved, affinityPlanKey); err != nil {
			_ = channel.Close()
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("golem-go-client: connection plan has no supported candidates")
	}
	return lastErr
}

func sameEndpointIgnoringQuery(a, b string) bool {
	left, err := url.Parse(a)
	if err != nil {
		return false
	}
	right, err := url.Parse(b)
	if err != nil {
		return false
	}
	left.RawQuery = ""
	left.ForceQuery = false
	right.RawQuery = ""
	right.ForceQuery = false
	return left.String() == right.String()
}

func equalCertificateHashes(a, b []CertificateHash) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Algorithm != b[i].Algorithm || string(a[i].Value) != string(b[i].Value) {
			return false
		}
	}
	return true
}

func validateConnectionPlan(plan ConnectionPlan) error {
	if err := validateConnectOptions(plan.Primary); err != nil {
		return fmt.Errorf("golem-go-client: invalid primary connection: %w", err)
	}
	if plan.WebTransportTimeout < 0 {
		return fmt.Errorf("golem-go-client: WebTransport timeout cannot be negative")
	}
	if plan.Fallback == nil {
		return nil
	}
	if plan.Primary.Transport != TransportWebTransport || plan.Fallback.Transport != TransportWebSocket {
		return fmt.Errorf("golem-go-client: fallback must be websocket after a webtransport primary")
	}
	if err := validateConnectOptions(*plan.Fallback); err != nil {
		return fmt.Errorf("golem-go-client: invalid fallback connection: %w", err)
	}
	return nil
}

func validateConnectOptions(options ConnectOptions) error {
	switch options.Transport {
	case TransportWebSocket, TransportWebTransport:
	default:
		return fmt.Errorf("golem-go-client: unsupported transport %q", options.Transport)
	}
	if err := validateRealtimeEndpointURL(options.Transport, options.URL); err != nil {
		return err
	}
	if err := validateEventualAckIntervalMs(options.EventualAckIntervalMs); err != nil {
		return err
	}
	if err := validateCertificateHashes(options.ServerCertificateHashes); err != nil {
		return err
	}
	return nil
}

func validateRealtimeEndpointURL(transport TransportKind, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("golem-go-client: realtime config url is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("golem-go-client: parsing realtime endpoint URL: %w", sanitizeURLError(err))
	}
	if !parsed.IsAbs() || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("golem-go-client: %s endpoint URL must be absolute with a host and no userinfo", transport)
	}

	validScheme := false
	switch transport {
	case TransportWebTransport:
		validScheme = strings.EqualFold(parsed.Scheme, "https")
	case TransportWebSocket:
		validScheme = strings.EqualFold(parsed.Scheme, "ws") || strings.EqualFold(parsed.Scheme, "wss")
	}
	if !validScheme {
		return fmt.Errorf("golem-go-client: invalid URL scheme for %s endpoint", transport)
	}
	return nil
}

func cloneConnectionPlan(plan ConnectionPlan) ConnectionPlan {
	clone := plan
	clone.Primary = cloneConnectOptions(plan.Primary)
	if plan.Fallback != nil {
		fallback := cloneConnectOptions(*plan.Fallback)
		clone.Fallback = &fallback
	}
	return clone
}

func cloneConnectOptions(options ConnectOptions) ConnectOptions {
	clone := options
	clone.ServerCertificateHashes = cloneCertificateHashes(options.ServerCertificateHashes)
	if options.TLSClientConfig != nil {
		clone.TLSClientConfig = options.TLSClientConfig.Clone()
	}
	return clone
}

func connectionPlanAffinityKey(plan ConnectionPlan) string {
	hash := sha256.New()
	writeAffinityCandidate := func(options ConnectOptions) {
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00", options.Transport, options.URL, options.EventualAckIntervalMs)
		for _, certificateHash := range options.ServerCertificateHashes {
			fmt.Fprintf(hash, "%s:%s\x00", certificateHash.Algorithm, hex.EncodeToString(certificateHash.Value))
		}
	}
	writeAffinityCandidate(plan.Primary)
	if plan.Fallback != nil {
		writeAffinityCandidate(*plan.Fallback)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
