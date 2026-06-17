package golemclient

import (
	"context"
	"errors"
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

// OnDisconnect registers a callback fired when the transport closes.
func (c *GameClient) OnDisconnect(fn func(DisconnectInfo)) { c.onClose = fn }

// ConnectURL opens a WebSocket connection to url.
func (c *GameClient) ConnectURL(ctx context.Context, url string) error {
	return c.Connect(ctx, ConnectOptions{Transport: TransportWebSocket, URL: url})
}

// Connect opens a transport connection.
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
func (c *GameClient) Disconnect() {
	c.mu.Lock()
	channel := c.channel
	inbound := c.inbound
	c.channel = nil
	c.inbound = nil
	c.mu.Unlock()
	inbound.stop()
	if channel != nil {
		_ = channel.Close()
	}
}

// Send encodes and sends a command over the reliable stream.
func (c *GameClient) Send(command any) error {
	if c.encodeCommand == nil || c.encodePacket == nil {
		return errors.New("golem-go-client: command codec is not configured")
	}
	frame, err := c.encodeCommand(command)
	if err != nil {
		return err
	}
	packet, err := c.encodePacket([][]byte{frame})
	if err != nil {
		return err
	}
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	if channel == nil || !channel.Connected() {
		return nil
	}
	return channel.Send(packet)
}

// SendUnreliable sends a raw unreliable datagram payload if supported.
func (c *GameClient) SendUnreliable(data []byte) error {
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	if channel == nil || !channel.Connected() {
		return nil
	}
	return channel.SendUnreliable(data)
}

// SendReliableUnordered encodes and sends a command over reliable unordered datagrams.
func (c *GameClient) SendReliableUnordered(command any) error {
	frame, err := c.encodeDatagramCommand(command)
	if err != nil {
		return err
	}
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	if channel == nil || !channel.Connected() {
		return nil
	}
	return channel.SendReliableUnordered(frame)
}

// SendReliableOrdered encodes and sends a command over reliable ordered datagrams.
func (c *GameClient) SendReliableOrdered(command any) error {
	frame, err := c.encodeDatagramCommand(command)
	if err != nil {
		return err
	}
	c.mu.Lock()
	channel := c.channel
	c.mu.Unlock()
	if channel == nil || !channel.Connected() {
		return nil
	}
	return channel.SendReliableOrdered(frame)
}

func (c *GameClient) encodeDatagramCommand(command any) ([]byte, error) {
	if c.encodeCommand == nil {
		return nil, errors.New("golem-go-client: command codec is not configured")
	}
	return c.encodeCommand(command)
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
