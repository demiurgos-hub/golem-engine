//go:build js && wasm

package golemclient

import (
	"context"
	"fmt"
)

const (
	datagramAckMaskWordCount             = 4
	datagramAckMaskWordBytes             = 4
	datagramAckMaskBytes                 = datagramAckMaskWordCount * datagramAckMaskWordBytes
	datagramPacketHeaderBytes            = 2 + 2 + datagramAckMaskBytes + 1
	datagramLaneHeaderBytes              = 1
	datagramReliableMessageIDBytes       = 2
	datagramReliableOrderedSequenceBytes = 2
)

// WebTransportChannel is unavailable in Go's js/wasm runtime.
type WebTransportChannel struct{}

// DialWebTransport reports that Go's js/wasm runtime has no built-in
// WebTransport implementation.
func DialWebTransport(context.Context, ConnectOptions) (*WebTransportChannel, error) {
	return nil, fmt.Errorf("%w: %s on js/wasm", ErrTransportUnsupported, TransportWebTransport)
}

// Connected reports false because WebTransport is unavailable on js/wasm.
func (*WebTransportChannel) Connected() bool { return false }

// MaxMessageBytes returns the reliable message cap.
func (*WebTransportChannel) MaxMessageBytes() int { return maxReliableMessageBytes }

// MaxDatagramBytes returns zero because WebTransport is unavailable.
func (*WebTransportChannel) MaxDatagramBytes() int { return 0 }

// Send reports that WebTransport is unavailable.
func (*WebTransportChannel) Send([]byte) error { return ErrTransportUnsupported }

// SendUnreliable reports that WebTransport is unavailable.
func (*WebTransportChannel) SendUnreliable([]byte) error { return ErrTransportUnsupported }

// SendReliableUnordered reports that WebTransport is unavailable.
func (*WebTransportChannel) SendReliableUnordered([]byte) error { return ErrTransportUnsupported }

// SendReliableOrdered reports that WebTransport is unavailable.
func (*WebTransportChannel) SendReliableOrdered([]byte) error { return ErrTransportUnsupported }

// Close is a no-op for the unavailable transport.
func (*WebTransportChannel) Close() error { return nil }

// OnOpen ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnOpen(func()) {}

// OnMessage ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnMessage(func([]byte)) {}

// OnUnreliableStateMessage ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnUnreliableStateMessage(func([]byte)) {}

// OnReliableOrderedMessage ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnReliableOrderedMessage(func([]byte)) {}

// OnEventualStateMessage ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnEventualStateMessage(func([]byte)) {}

// OnClose ignores callbacks for the unavailable transport.
func (*WebTransportChannel) OnClose(func(DisconnectInfo)) {}
