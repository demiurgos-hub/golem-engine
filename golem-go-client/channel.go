package golemclient

import (
	"context"
	"fmt"
)

type suppressIntermediateTransportLogsKey struct{}

func withSuppressedIntermediateTransportLogs(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressIntermediateTransportLogsKey{}, true)
}

func intermediateTransportLogsSuppressed(ctx context.Context) bool {
	return ctx != nil && ctx.Value(suppressIntermediateTransportLogsKey{}) == true
}

// DialChannel opens the transport selected by options.
func DialChannel(ctx context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
	switch options.Transport {
	case "", TransportWebSocket:
		channel, err := DialWebSocket(ctx, options)
		if err != nil {
			return nil, err
		}
		return channel, nil
	case TransportWebTransport:
		channel, err := DialWebTransport(ctx, options)
		if err != nil {
			return nil, err
		}
		return channel, nil
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
