package golemclient

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	golemnet "golem-engine/golem/net"
)

// WebSocketChannel implements ReliableMessageChannel over WebSocket.
type WebSocketChannel struct {
	conn      *websocket.Conn
	callbacks channelCallbacks
	connected atomic.Bool
	closeOnce sync.Once
	readOnce  sync.Once
}

// DialWebSocket opens a WebSocket channel.
func DialWebSocket(ctx context.Context, options ConnectOptions) (*WebSocketChannel, error) {
	redacted := RedactURL(options.URL)
	log.Printf("golem-go-client: dialing websocket url=%q", redacted)
	conn, _, err := websocket.Dial(ctx, options.URL, nil)
	if err != nil {
		log.Printf("golem-go-client: websocket dial failed url=%q error=%v", redacted, err)
		return nil, fmt.Errorf("golem-go-client: websocket dial: %w", err)
	}
	ch := &WebSocketChannel{conn: conn}
	ch.connected.Store(true)
	return ch, nil
}

// Connected reports whether the channel is open.
func (c *WebSocketChannel) Connected() bool { return c.connected.Load() }

// MaxMessageBytes returns the reliable message cap.
func (c *WebSocketChannel) MaxMessageBytes() int { return maxReliableMessageBytes }

// MaxDatagramBytes returns zero because WebSocket has no datagrams.
func (c *WebSocketChannel) MaxDatagramBytes() int { return 0 }

// Send writes one reliable binary message.
func (c *WebSocketChannel) Send(data []byte) error {
	if !c.Connected() {
		return nil
	}
	if err := c.conn.Write(context.Background(), websocket.MessageBinary, data); err != nil {
		return fmt.Errorf("golem-go-client: websocket send: %w", err)
	}
	return nil
}

// SendUnreliable returns nil because WebSocket has no datagram lane.
func (c *WebSocketChannel) SendUnreliable(_ []byte) error { return nil }

// SendReliableUnordered returns nil because WebSocket has no datagram lane.
func (c *WebSocketChannel) SendReliableUnordered(_ []byte) error { return nil }

// SendReliableOrdered returns nil because WebSocket has no datagram lane.
func (c *WebSocketChannel) SendReliableOrdered(_ []byte) error { return nil }

// Close closes the WebSocket connection.
func (c *WebSocketChannel) Close() error {
	c.closeOnce.Do(func() {
		_ = c.conn.Write(context.Background(), websocket.MessageBinary, golemnet.ClientCloseControlFrame())
		c.connected.Store(false)
		c.conn.CloseNow()
	})
	return nil
}

// OnOpen registers an open callback and fires immediately for an already-open connection.
func (c *WebSocketChannel) OnOpen(fn func()) {
	c.callbacks.onOpen = fn
	if fn != nil && c.Connected() {
		fn()
	}
}

// OnMessage registers a reliable message callback.
func (c *WebSocketChannel) OnMessage(fn func([]byte)) {
	c.callbacks.onMessage = fn
	c.readOnce.Do(func() { go c.readLoop(context.Background()) })
}

// OnUnreliableStateMessage registers a raw state datagram callback.
func (c *WebSocketChannel) OnUnreliableStateMessage(fn func([]byte)) {
	c.callbacks.onUnreliableStateMessage = fn
}

// OnReliableOrderedMessage registers a reliable ordered datagram callback.
func (c *WebSocketChannel) OnReliableOrderedMessage(fn func([]byte)) {
	c.callbacks.onReliableOrderedMessage = fn
}

// OnEventualStateMessage registers a state-aware datagram callback.
func (c *WebSocketChannel) OnEventualStateMessage(fn func([]byte)) {
	c.callbacks.onEventualStateMessage = fn
}

// OnClose registers a close callback.
func (c *WebSocketChannel) OnClose(fn func(DisconnectInfo)) { c.callbacks.onClose = fn }

func (c *WebSocketChannel) readLoop(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			c.connected.Store(false)
			err = fmt.Errorf("golem-go-client: websocket read: %w", err)
			log.Printf("golem-go-client: websocket read loop closed error=%v", err)
			if c.callbacks.onClose != nil {
				c.callbacks.onClose(DisconnectInfo{WasClean: false, Err: err})
			}
			return
		}
		if c.callbacks.onMessage != nil {
			c.callbacks.onMessage(data)
		}
	}
}
