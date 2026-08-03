package golemclient

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
)

const gameClientInboundQueueSize = 1024

type inboundEventKind uint8

const (
	inboundEventStream inboundEventKind = iota + 1
	inboundEventCompactState
)

type inboundEvent struct {
	kind inboundEventKind
	data []byte
}

type inboundDispatcher struct {
	events  chan inboundEvent
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

// newInboundDispatcher starts a serialized inbound event dispatcher.
func newInboundDispatcher(c *GameClient) *inboundDispatcher {
	d := &inboundDispatcher{
		events:  make(chan inboundEvent, gameClientInboundQueueSize),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go d.run(c)
	return d
}

// enqueue copies and queues one inbound transport payload for serialized delivery.
func (d *inboundDispatcher) enqueue(kind inboundEventKind, data []byte) {
	if d == nil {
		return
	}
	event := inboundEvent{kind: kind, data: append([]byte(nil), data...)}
	select {
	case <-d.done:
	case d.events <- event:
	}
}

// stop prevents future queued events from reaching generated managers.
func (d *inboundDispatcher) stop() {
	if d == nil {
		return
	}
	d.once.Do(func() { close(d.done) })
}

// run processes inbound events until the dispatcher is stopped.
func (d *inboundDispatcher) run(c *GameClient) {
	defer close(d.stopped)
	for {
		select {
		case <-d.done:
			return
		case event := <-d.events:
			c.dispatchInboundEvent(d, event)
		}
	}
}

// GameClient owns the active transport and routes decoded updates to generated managers.
type GameClient struct {
	entities EntityManagerLike
	world    WorldManagerLike
	events   EventManagerLike

	decodeEntity  func([]byte) (any, error)
	decodeWorld   func([]byte) (any, error)
	encodeCommand func(any) ([]byte, error)
	encodePacket  func([][]byte) ([]byte, error)
	createChannel ChannelFactory

	mu         sync.Mutex
	dispatchMu sync.Mutex
	channel    ReliableMessageChannel
	inbound    *inboundDispatcher
	onOpen     func()
	onClose    func(DisconnectInfo)
	lastErr    error
	closedCh   chan struct{}
}

// NewGameClient creates a client wired to generated managers and codecs.
func NewGameClient(options GameClientOptions) *GameClient {
	create := options.CreateChannel
	if create == nil {
		create = DialChannel
	}
	return &GameClient{
		entities:      options.EntityManager,
		world:         options.WorldManager,
		events:        options.EventManager,
		decodeEntity:  options.DecodeEntity,
		decodeWorld:   options.DecodeWorld,
		encodeCommand: options.EncodeCommand,
		encodePacket:  options.EncodePacket,
		createChannel: create,
		closedCh:      make(chan struct{}),
	}
}

// Entities returns the generated entity manager.
func (c *GameClient) Entities() EntityManagerLike { return c.entities }

// World returns the generated world manager, if configured.
func (c *GameClient) World() WorldManagerLike { return c.world }

// Events returns the generated event manager, if configured.
func (c *GameClient) Events() EventManagerLike { return c.events }

// OnConnect registers a callback fired when the transport opens.
func (c *GameClient) OnConnect(fn func()) { c.onOpen = fn }

// OnDisconnect registers a callback fired when the transport closes (remote or
// local). Local GameClient.Disconnect emits exactly one clean notify for a live
// session; a subsequent Connect tears down any prior live session the same way
// before dialing (see Disconnect).
func (c *GameClient) OnDisconnect(fn func(DisconnectInfo)) { c.onClose = fn }

// ConnectURL opens a WebSocket connection to url.
func (c *GameClient) ConnectURL(ctx context.Context, url string) error {
	return c.Connect(ctx, ConnectOptions{Transport: TransportWebSocket, URL: url})
}

// Connect opens a transport connection.
// If a session is already live, Disconnect runs first and emits one clean
// OnDisconnect for that prior session before the new dial.
func (c *GameClient) Connect(ctx context.Context, options ConnectOptions) error {
	c.Disconnect()
	if options.Transport == "" {
		options.Transport = TransportWebSocket
	}
	redacted := RedactURL(options.URL)
	channel, err := c.createChannel(ctx, options)
	if err != nil {
		log.Printf("golem-go-client: connect failed transport=%s url=%q error=%v", options.Transport, redacted, err)
		return err
	}
	log.Printf("golem-go-client: connected transport=%s url=%q", options.Transport, redacted)
	inbound := newInboundDispatcher(c)
	c.mu.Lock()
	c.channel = channel
	c.inbound = inbound
	c.closedCh = make(chan struct{})
	c.mu.Unlock()

	channel.OnOpen(func() {
		if c.onOpen != nil {
			c.onOpen()
		}
	})
	channel.OnMessage(func(data []byte) {
		inbound.enqueue(inboundEventStream, data)
	})
	channel.OnUnreliableStateMessage(func(data []byte) {
		inbound.enqueue(inboundEventCompactState, data)
	})
	channel.OnReliableOrderedMessage(func(data []byte) {
		inbound.enqueue(inboundEventCompactState, data)
	})
	channel.OnEventualStateMessage(func(data []byte) {
		inbound.enqueue(inboundEventCompactState, data)
	})
	channel.OnClose(func(info DisconnectInfo) {
		var shouldNotify bool
		c.mu.Lock()
		if c.channel == channel {
			c.channel = nil
			if c.inbound == inbound {
				c.inbound = nil
			}
			shouldNotify = true
			c.lastErr = info.Err
			closeOnce(&c.closedCh)
		}
		c.mu.Unlock()
		inbound.stop()
		if shouldNotify {
			log.Printf("golem-go-client: disconnect was_clean=%v error=%v", info.WasClean, info.Err)
			if c.onClose != nil {
				c.onClose(info)
			}
		}
	})
	return nil
}

// Disconnect closes the active transport, if any.
//
// API contract: for a live local teardown, Disconnect emits exactly one
// OnDisconnect with WasClean=true. The channel pointer is cleared before
// Close so the transport OnClose path does not double-notify; a second
// Disconnect with no active session is a no-op (no extra notify). Connect
// calls Disconnect first, so replacing a live session also yields one clean
// OnDisconnect for the old session before the new dial.
func (c *GameClient) Disconnect() {
	c.mu.Lock()
	channel := c.channel
	inbound := c.inbound
	c.channel = nil
	c.inbound = nil
	shouldNotify := channel != nil
	onClose := c.onClose
	if shouldNotify {
		c.lastErr = nil
		closeOnce(&c.closedCh)
	}
	c.mu.Unlock()
	if inbound != nil {
		inbound.stop()
	}
	if channel != nil {
		_ = channel.Close()
	}
	if shouldNotify && onClose != nil {
		log.Printf("golem-go-client: disconnect was_clean=%v error=%v", true, nil)
		onClose(DisconnectInfo{WasClean: true})
	}
}

// Send encodes and sends a command over the reliable unordered datagram lane
// when available, or over the reliable stream otherwise.
func (c *GameClient) Send(command any) error {
	return c.sendCommand(command, false)
}

// SendOrdered encodes and sends a command over the reliable ordered datagram
// lane when available, or over the reliable stream otherwise.
func (c *GameClient) SendOrdered(command any) error {
	return c.sendCommand(command, true)
}

func (c *GameClient) sendCommand(command any, ordered bool) error {
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	if channel == nil || !channel.Connected() {
		return nil
	}

	if c.encodeCommand == nil {
		return errors.New("golem-go-client: command codec is not configured")
	}
	frame, err := c.encodeCommand(command)
	if err != nil {
		return err
	}

	if maxDatagramBytes := channel.MaxDatagramBytes(); maxDatagramBytes > 0 {
		maxPayloadBytes := maxDatagramBytes -
			datagramPacketHeaderBytes -
			datagramLaneHeaderBytes -
			datagramReliableMessageIDBytes
		laneName := "reliable unordered"
		if ordered {
			maxPayloadBytes -= datagramReliableOrderedSequenceBytes
			laneName = "reliable ordered"
		}
		if len(frame) > maxPayloadBytes {
			return fmt.Errorf(
				"golem-go-client: encoded %s command size %d exceeds max payload %d",
				laneName,
				len(frame),
				maxPayloadBytes,
			)
		}
		if ordered {
			return channel.SendReliableOrdered(frame)
		}
		return channel.SendReliableUnordered(frame)
	}

	if c.encodePacket == nil {
		return errors.New("golem-go-client: command codec is not configured")
	}
	packet, err := c.encodePacket([][]byte{frame})
	if err != nil {
		return err
	}
	if maxBytes := channel.MaxMessageBytes(); maxBytes > 0 && len(packet) > maxBytes {
		return fmt.Errorf("golem-go-client: encoded packet size %d exceeds max reliable message %d", len(packet), maxBytes)
	}
	return channel.Send(packet)
}

// dispatchInboundEvent applies one inbound event under the client dispatch lock.
func (c *GameClient) dispatchInboundEvent(dispatcher *inboundDispatcher, event inboundEvent) {
	select {
	case <-dispatcher.done:
		return
	default:
	}
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	select {
	case <-dispatcher.done:
		return
	default:
	}
	switch event.kind {
	case inboundEventStream:
		c.handleMessage(event.data)
	case inboundEventCompactState:
		c.handleCompactStateBatch(event.data)
	}
}

func (c *GameClient) handleMessage(data []byte) {
	messages, err := decodeServerMessages(data)
	if err != nil {
		c.setLastErr(err)
		return
	}
	for _, msg := range messages {
		if len(msg.EntityUpdate) > 0 && c.decodeEntity != nil && c.entities != nil {
			update, err := c.decodeEntity(msg.EntityUpdate)
			if err == nil {
				c.entities.ApplyUpdate(update)
			} else {
				c.setLastErr(err)
			}
		}
		if len(msg.WorldUpdate) > 0 && c.decodeWorld != nil && c.world != nil {
			update, err := c.decodeWorld(msg.WorldUpdate)
			if err == nil {
				c.world.ApplyUpdate(update)
			} else {
				c.setLastErr(err)
			}
		}
		if len(msg.ServerEvent) > 0 && c.events != nil {
			c.events.ApplyRaw(msg.ServerEvent)
		}
	}
}

func (c *GameClient) handleCompactStateBatch(data []byte) {
	if c.entities == nil {
		return
	}
	_ = decodeLengthPrefixedFrames(data, func(frame []byte) {
		c.entities.ApplyCompactUpdate(frame)
	})
}

// setLastErr records the last asynchronous client error.
func (c *GameClient) setLastErr(err error) {
	c.mu.Lock()
	c.lastErr = err
	c.mu.Unlock()
}

func closeOnce(ch *chan struct{}) {
	select {
	case <-*ch:
	default:
		close(*ch)
	}
}
