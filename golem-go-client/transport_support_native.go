//go:build !js || !wasm

package golemclient

func builtinSupportsTransport(transport TransportKind) bool {
	return transport == TransportWebSocket || transport == TransportWebTransport
}
