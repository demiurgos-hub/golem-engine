// Package golemclient provides the native Go client runtime for Golem Engine.
package golemclient

import (
	"context"
	"crypto/tls"
)

// TransportKind selects the network transport used by GameClient.Connect.
type TransportKind string

const (
	// TransportWebSocket connects over WebSocket.
	TransportWebSocket TransportKind = "websocket"
	// TransportWebTransport connects over WebTransport.
	TransportWebTransport TransportKind = "webtransport"
)

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

// ChannelFactory opens a transport channel.
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
}
