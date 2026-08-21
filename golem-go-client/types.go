// Package golemclient provides the native Go client runtime for Golem Engine.
package golemclient

import (
	"context"
	"crypto/tls"
	"errors"
	"time"
)

// TransportKind selects the network transport used by GameClient.Connect.
type TransportKind string

const (
	// TransportWebSocket connects over WebSocket.
	TransportWebSocket TransportKind = "websocket"
	// TransportWebTransport connects over WebTransport.
	TransportWebTransport TransportKind = "webtransport"
)

const defaultWebTransportConnectTimeout = 5 * time.Second

// ErrTransportUnsupported reports that the current runtime cannot dial a
// requested transport.
var ErrTransportUnsupported = errors.New("golem-go-client: transport unsupported")

// ErrRealtimeRevisionMismatch reports an HTTP 426 response from a realtime
// endpoint. Connection plans treat it as terminal and do not change transport.
var ErrRealtimeRevisionMismatch = errors.New("golem-go-client: realtime revision mismatch")

// ErrRealtimeAuthorizationRejected reports an HTTP 401 or 403 response from a
// realtime endpoint. Connection plans treat it as terminal.
var ErrRealtimeAuthorizationRejected = errors.New("golem-go-client: realtime authorization rejected")

// CertificateHash identifies a WebTransport certificate hash.
type CertificateHash struct {
	Algorithm string
	Value     []byte
}

// ConnectOptions configures a GameClient connection.
type ConnectOptions struct {
	// Transport selects the built-in channel implementation.
	Transport TransportKind
	// URL is the WebSocket or WebTransport endpoint URL.
	URL string
	// ServerCertificateHashes pins WebTransport TLS certificates when
	// TLSClientConfig is nil.
	ServerCertificateHashes []CertificateHash
	// EventualAckIntervalMs controls how long WebTransport waits to coalesce
	// datagram ACKs before sending a standalone ACK packet. Zero uses the
	// default interval.
	EventualAckIntervalMs int
	// TLSClientConfig configures native Go TLS validation for WebTransport.
	TLSClientConfig *tls.Config
}

// DialOptionsResolver lazily adds per-dial credentials or TLS settings to a
// fresh copy of a credential-free connection candidate. It may change URL
// query parameters and TLSClientConfig, but must preserve the transport,
// endpoint, certificate hashes, and ACK metadata.
type DialOptionsResolver func(context.Context, ConnectOptions) (ConnectOptions, error)

// ConnectionPlan describes a primary connection and an optional sequential
// fallback. Version 1 supports WebTransport primary to WebSocket fallback.
type ConnectionPlan struct {
	Primary  ConnectOptions
	Fallback *ConnectOptions

	// ResolveOptions runs immediately before each physical dial. A resolver
	// failure aborts the logical connection attempt without trying fallback.
	ResolveOptions DialOptionsResolver
	// WebTransportTimeout bounds primary WebTransport establishment. Zero uses
	// the five-second default.
	WebTransportTimeout time.Duration
}

// EntityLifecycle is implemented by generated or custom entity wrappers that
// want spawn/remove callbacks from the generated EntityManager.
type EntityLifecycle interface {
	OnSpawn()
	OnRemove()
}

// EntityManagerLike is the contract generated Go EntityManagers satisfy.
type EntityManagerLike interface {
	ApplyUpdate(any)
	ApplyCompactUpdate([]byte)
	Get(int64) any
}

// clearableEntityManager is optionally implemented by generated EntityManagers.
type clearableEntityManager interface {
	Clear()
}

// WorldManagerLike is the contract generated Go WorldManagers satisfy.
type WorldManagerLike interface {
	ApplyUpdate(any)
}

// EventManagerLike is the contract generated Go EventManagers satisfy.
type EventManagerLike interface {
	ApplyRaw([]byte)
}

// ReliableMessageChannel is the transport-neutral channel used by GameClient.
type ReliableMessageChannel interface {
	Connected() bool
	MaxMessageBytes() int
	MaxDatagramBytes() int
	Send([]byte) error
	SendUnreliable([]byte) error
	SendReliableUnordered([]byte) error
	SendReliableOrdered([]byte) error
	Close() error
	OnOpen(func())
	OnMessage(func([]byte))
	OnUnreliableStateMessage(func([]byte))
	OnReliableOrderedMessage(func([]byte))
	OnEventualStateMessage(func([]byte))
	OnClose(func(DisconnectInfo))
}

// ChannelFactory opens a transport channel. Built-in factories return when ctx
// is canceled. Custom factories should do the same; GameClient waits for a late
// factory result and closes any returned channel before advancing a connection
// plan, so cleanup can extend ConnectPlan beyond its establishment deadline.
type ChannelFactory func(context.Context, ConnectOptions) (ReliableMessageChannel, error)

// DisconnectInfo describes a transport close event.
type DisconnectInfo struct {
	Code     int
	Reason   string
	WasClean bool
	Err      error
}

// GameClientOptions wires generated codecs and managers into a GameClient.
type GameClientOptions struct {
	DecodeEntity  func([]byte) (any, error)
	EncodeCommand func(any) ([]byte, error)
	EncodePacket  func([][]byte) ([]byte, error)

	EntityManager EntityManagerLike

	DecodeWorld  func([]byte) (any, error)
	WorldManager WorldManagerLike
	EventManager EventManagerLike

	CreateChannel ChannelFactory

	// SupportsTransport overrides runtime capability detection. It is useful
	// for custom channel factories; nil uses the built-in platform predicate.
	SupportsTransport func(TransportKind) bool
}
