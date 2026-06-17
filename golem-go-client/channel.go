package golemclient

import (
	"context"
	"fmt"
)

// DialChannel opens the transport selected by options.
func DialChannel(ctx context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
	switch options.Transport {
	case "", TransportWebSocket:
		return DialWebSocket(ctx, options)
	case TransportWebTransport:
		return DialWebTransport(ctx, options)
	default:
		return nil, fmt.Errorf("golem-go-client: unknown transport %q", options.Transport)
	}
}

type channelCallbacks struct {
	onOpen                   func()
	onMessage                func([]byte)
	onUnreliableStateMessage func([]byte)
	onReliableOrderedMessage func([]byte)
	onEventualStateMessage   func([]byte)
	onClose                  func(DisconnectInfo)
}
