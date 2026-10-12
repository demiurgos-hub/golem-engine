//go:build !js || !wasm

package golemclient

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
	"github.com/quic-go/webtransport-go"
)

const (
	datagramAckCoalesceDelay          = 1 * time.Millisecond
	datagramSchedulerInterval         = 2 * time.Millisecond
	datagramReliableRetryBaseDelay    = 75 * time.Millisecond
	datagramReliableRetryMaxDelay     = 400 * time.Millisecond
	datagramReliableRetryLimit        = 8
	datagramReliableMessageTTL        = 3 * time.Second
	datagramReliableOrderedGapTimeout = 3 * time.Second
)

type pendingReliableDatagram struct {
	packet     datagramPacket
	queuedAt   time.Time
	lastSentAt time.Time
	nextSendAt time.Time
	attempts   int
}

type datagramProtocol struct {
	mu              sync.Mutex
	nextPacketSeq   uint16
	nextMessageID   uint16
	nextOrderedSeq  uint16
	recvPackets     netwire.SequenceWindow
	recvReliableMsg netwire.SequenceWindow
	orderedRecv     netwire.OrderedBuffer
	pendingReliable map[uint16]*pendingReliableDatagram
	ackDirty        bool
	ackDueAt        time.Time
	ackInterval     time.Duration
	write           func([]byte) error
}

func newDatagramProtocol(write func([]byte) error, ackInterval time.Duration) *datagramProtocol {
	if ackInterval <= 0 {
		ackInterval = datagramAckCoalesceDelay
	}
	return &datagramProtocol{
		pendingReliable: make(map[uint16]*pendingReliableDatagram),
		ackInterval:     ackInterval,
		write:           write,
	}
}

func datagramAckIntervalFromOptions(options ConnectOptions) time.Duration {
	if options.EventualAckIntervalMs <= 0 {
		return datagramAckCoalesceDelay
	}
	return time.Duration(options.EventualAckIntervalMs) * time.Millisecond
}

func (p *datagramProtocol) send(lane uint8, payload []byte) error {
	if lane == datagramLaneReliableUnordered || lane == datagramLaneReliableOrdered {
		return p.sendReliable(lane, payload, time.Now())
	}
	p.mu.Lock()
	packet := datagramPacket{
		Lane:    lane,
		Payload: append([]byte(nil), payload...),
	}
	p.stampPacketLocked(&packet)
	p.clearPendingACKLocked()
	p.mu.Unlock()
	data, err := encodeDatagramPacket(packet)
	if err != nil {
		return err
	}
	return p.write(data)
}

func (p *datagramProtocol) sendReliable(lane uint8, payload []byte, now time.Time) error {
	p.mu.Lock()
	packet := datagramPacket{
		Lane:      lane,
		MessageID: p.nextMessageID,
		Payload:   append([]byte(nil), payload...),
	}
	p.stampPacketLocked(&packet)
	p.nextMessageID++
	if lane == datagramLaneReliableOrdered {
		packet.OrderedSequence = p.nextOrderedSeq
		p.nextOrderedSeq++
	}
	p.clearPendingACKLocked()
	p.pendingReliable[packet.Sequence] = &pendingReliableDatagram{
		packet:     packet,
		queuedAt:   now,
		lastSentAt: now,
		nextSendAt: now.Add(datagramReliableRetryBaseDelay),
		attempts:   1,
	}
	p.mu.Unlock()
	data, err := encodeDatagramPacket(packet)
	if err != nil {
		return err
	}
	return p.write(data)
}

func (p *datagramProtocol) receive(data []byte, deliver func(uint8, []byte)) error {
	now := time.Now()
	packet, err := decodeDatagramPacket(data)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.applyPeerAcksLocked(packet.ACK.Sequence, packet.ACK.Mask)
	p.mu.Unlock()
	if packet.Flags&datagramFlagAckOnly != 0 {
		return nil
	}
	p.mu.Lock()
	accepted := p.recvPackets.Accept(packet.Sequence)
	if accepted {
		p.scheduleAckLocked(now)
	}
	p.mu.Unlock()
	if !accepted {
		return nil
	}
	switch packet.Lane {
	case datagramLaneReliableUnordered, datagramLaneReliableOrdered:
		p.mu.Lock()
		accepted = p.recvReliableMsg.Accept(packet.MessageID)
		p.mu.Unlock()
		if !accepted {
			return nil
		}
	}
	if packet.Lane == datagramLaneReliableOrdered {
		return p.deliverOrdered(packet, now, deliver)
	}
	if deliver != nil {
		deliver(packet.Lane, packet.Payload)
	}
	return nil
}

func (p *datagramProtocol) deliverOrdered(packet datagramPacket, now time.Time, deliver func(uint8, []byte)) error {
	p.mu.Lock()
	future := netwire.Newer(packet.OrderedSequence, p.orderedRecv.Next())
	// The decoder already owns the payload. Keep the client's replacement
	// policy for duplicate future sequences without copying those bytes again.
	deliveries := p.orderedRecv.AcceptOwned(packet.OrderedSequence, packet.Payload, now, netwire.ReplacePending)
	if future && now.Sub(p.orderedRecv.GapSince()) > datagramReliableOrderedGapTimeout {
		p.mu.Unlock()
		return fmt.Errorf("golem-go-client: reliable ordered datagram gap expired")
	}
	p.mu.Unlock()
	for _, payload := range deliveries {
		if deliver != nil {
			deliver(datagramLaneReliableOrdered, payload)
		}
	}
	return nil
}

func (p *datagramProtocol) scheduleAckLocked(now time.Time) {
	if p.ackDirty {
		return
	}
	// Coalesce from the first unacknowledged packet; sustained traffic must
	// not postpone the deadline until the peer declares delivery stalled.
	p.ackDirty = true
	p.ackDueAt = now.Add(p.ackInterval)
}

func (p *datagramProtocol) sendDueAck(now time.Time) error {
	p.mu.Lock()
	if !p.ackDirty || (!p.ackDueAt.IsZero() && now.Before(p.ackDueAt)) {
		p.mu.Unlock()
		return nil
	}
	packet := datagramPacket{
		Flags: datagramFlagAckOnly,
	}
	p.stampPacketLocked(&packet)
	p.clearPendingACKLocked()
	p.mu.Unlock()
	data, err := encodeDatagramPacket(packet)
	if err != nil {
		return err
	}
	return p.write(data)
}

func (p *datagramProtocol) drainRetries(now time.Time) error {
	var packets []datagramPacket
	p.mu.Lock()
	for seq, pending := range p.pendingReliable {
		if now.Sub(pending.queuedAt) > datagramReliableMessageTTL || pending.attempts >= datagramReliableRetryLimit {
			p.mu.Unlock()
			return fmt.Errorf("golem-go-client: reliable datagram delivery stalled")
		}
		if now.Before(pending.nextSendAt) {
			continue
		}
		delete(p.pendingReliable, seq)
		p.stampPacketLocked(&pending.packet)
		pending.attempts++
		pending.lastSentAt = now
		delay := datagramReliableRetryBaseDelay << min(pending.attempts-1, 3)
		if delay > datagramReliableRetryMaxDelay {
			delay = datagramReliableRetryMaxDelay
		}
		pending.nextSendAt = now.Add(delay)
		p.pendingReliable[pending.packet.Sequence] = pending
		packets = append(packets, pending.packet)
	}
	if len(packets) > 0 {
		p.clearPendingACKLocked()
	}
	p.mu.Unlock()
	for _, packet := range packets {
		data, err := encodeDatagramPacket(packet)
		if err != nil {
			return err
		}
		if err := p.write(data); err != nil {
			return err
		}
	}
	return nil
}

func (p *datagramProtocol) applyPeerAcksLocked(ackSeq uint16, ackMask ackMask128) {
	ack := datagramACK{Sequence: ackSeq, Mask: ackMask}
	for seq := range p.pendingReliable {
		if ack.Acknowledges(seq) {
			delete(p.pendingReliable, seq)
		}
	}
}

func (p *datagramProtocol) wrapStreamPayload(payload []byte) []byte {
	p.mu.Lock()
	if !p.ackDirty || len(payload)+netwire.ACKControlHeaderBytes > maxReliableMessageBytes {
		p.mu.Unlock()
		return payload
	}
	ack := p.snapshotACKLocked()
	p.clearPendingACKLocked()
	p.mu.Unlock()
	return netwire.AppendACKControl(nil, ack, payload)
}

// WebTransportChannel implements ReliableMessageChannel over WebTransport.
type WebTransportChannel struct {
	session   *webtransport.Session
	stream    *webtransport.Stream
	protocol  *datagramProtocol
	callbacks channelCallbacks
	writeMu   sync.Mutex
	connected atomic.Bool
	closeOnce sync.Once
	doneOnce  sync.Once
	readOnce  sync.Once
	done      chan struct{}
}

// DialWebTransport opens a WebTransport channel and client-initiated reliable stream.
func DialWebTransport(ctx context.Context, options ConnectOptions) (*WebTransportChannel, error) {
	redacted := RedactURL(options.URL)
	logAttempt := !intermediateTransportLogsSuppressed(ctx)
	if logAttempt {
		log.Printf("golem-go-client: dialing webtransport url=%q", redacted)
	}
	tlsConfig := options.TLSClientConfig
	if tlsConfig == nil && len(options.ServerCertificateHashes) > 0 {
		serverName, err := serverNameFromEndpointURL(options.URL)
		if err != nil {
			return nil, err
		}
		tlsConfig, err = TLSClientConfigFromCertificateHashes(serverName, options.ServerCertificateHashes)
		if err != nil {
			return nil, err
		}
	}
	dialer := &webtransport.Dialer{TLSClientConfig: tlsConfig}
	response, session, err := dialer.Dial(ctx, options.URL, nil)
	if err != nil {
		if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				err = fmt.Errorf("%w: %w", ErrRealtimeAuthorizationRejected, err)
			case http.StatusUpgradeRequired:
				err = fmt.Errorf("%w: %w", ErrRealtimeRevisionMismatch, err)
			}
		}
		err = sanitizeURLError(err)
		if logAttempt {
			log.Printf("golem-go-client: webtransport dial failed url=%q error=%v", redacted, err)
		}
		return nil, fmt.Errorf("golem-go-client: webtransport dial: %w", err)
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		err = sanitizeURLError(err)
		if logAttempt {
			log.Printf("golem-go-client: webtransport open stream failed url=%q error=%v", redacted, err)
		}
		_ = session.CloseWithError(0, "")
		return nil, fmt.Errorf("golem-go-client: webtransport open stream: %w", err)
	}
	// Flush the WebTransport stream header so the server can AcceptStream.
	// This does not write a golem reliable frame.
	if _, err := stream.Write(nil); err != nil {
		err = sanitizeURLError(err)
		if logAttempt {
			log.Printf("golem-go-client: webtransport stream header write failed url=%q error=%v", redacted, err)
		}
		_ = session.CloseWithError(0, "")
		return nil, fmt.Errorf("golem-go-client: webtransport stream header write: %w", err)
	}
	ch := &WebTransportChannel{session: session, stream: stream, done: make(chan struct{})}
	ch.protocol = newDatagramProtocol(ch.writeDatagram, datagramAckIntervalFromOptions(options))
	ch.connected.Store(true)
	go ch.datagramLoop(context.Background())
	go ch.schedulerLoop()
	return ch, nil
}

// Connected reports whether the channel is open.
func (c *WebTransportChannel) Connected() bool { return c.connected.Load() }

// MaxMessageBytes returns the reliable message cap.
func (c *WebTransportChannel) MaxMessageBytes() int { return maxReliableMessageBytes }

// MaxDatagramBytes returns the WebTransport datagram cap.
func (c *WebTransportChannel) MaxDatagramBytes() int { return maxWebTransportDatagramBytes }

// Send writes one reliable stream frame.
func (c *WebTransportChannel) Send(data []byte) error {
	if !c.Connected() {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return sanitizeURLError(writeReliableFrame(c.stream, c.protocol.wrapStreamPayload(data)))
}

// SendUnreliable sends one raw unreliable datagram.
func (c *WebTransportChannel) SendUnreliable(data []byte) error {
	return sanitizeURLError(c.protocol.send(datagramLaneUnreliable, data))
}

// SendReliableUnordered sends one reliable unordered datagram message.
func (c *WebTransportChannel) SendReliableUnordered(data []byte) error {
	return sanitizeURLError(c.protocol.send(datagramLaneReliableUnordered, data))
}

// SendReliableOrdered sends one reliable ordered datagram message.
func (c *WebTransportChannel) SendReliableOrdered(data []byte) error {
	return sanitizeURLError(c.protocol.send(datagramLaneReliableOrdered, data))
}

// Close closes the WebTransport session.
func (c *WebTransportChannel) Close() error {
	c.closeOnce.Do(func() {
		c.connected.Store(false)
		c.doneOnce.Do(func() { close(c.done) })
		_ = writeReliableFrame(c.stream, netwire.AppendCloseControl(nil))
		c.stream.CancelRead(0)
		c.stream.CancelWrite(0)
		_ = c.stream.Close()
		_ = c.session.CloseWithError(0, "client disconnect")
	})
	return nil
}

// OnOpen registers an open callback and fires immediately for an already-open connection.
func (c *WebTransportChannel) OnOpen(fn func()) {
	c.callbacks.onOpen = fn
	if fn != nil && c.Connected() {
		fn()
	}
}

// OnMessage registers a reliable message callback.
func (c *WebTransportChannel) OnMessage(fn func([]byte)) {
	c.callbacks.onMessage = fn
	c.readOnce.Do(func() { go c.readLoop(context.Background()) })
}

// OnUnreliableStateMessage registers a raw state datagram callback.
func (c *WebTransportChannel) OnUnreliableStateMessage(fn func([]byte)) {
	c.callbacks.onUnreliableStateMessage = fn
}

// OnReliableOrderedMessage registers a reliable ordered datagram callback.
func (c *WebTransportChannel) OnReliableOrderedMessage(fn func([]byte)) {
	c.callbacks.onReliableOrderedMessage = fn
}

// OnEventualStateMessage registers a state-aware datagram callback.
func (c *WebTransportChannel) OnEventualStateMessage(fn func([]byte)) {
	c.callbacks.onEventualStateMessage = fn
}

// OnClose registers a close callback.
func (c *WebTransportChannel) OnClose(fn func(DisconnectInfo)) { c.callbacks.onClose = fn }

func (c *WebTransportChannel) readLoop(ctx context.Context) {
	for {
		data, err := readReliableFrame(c.stream)
		if err != nil {
			c.closeWithError(err)
			return
		}
		if c.callbacks.onMessage != nil {
			c.callbacks.onMessage(data)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (c *WebTransportChannel) datagramLoop(ctx context.Context) {
	for {
		data, err := c.session.ReceiveDatagram(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				c.closeWithError(err)
			}
			return
		}
		if err := c.protocol.receive(data, c.deliverDatagram); err != nil {
			c.closeWithError(fmt.Errorf("golem-go-client: webtransport datagram protocol: %w", err))
			return
		}
	}
}

func (c *WebTransportChannel) schedulerLoop() {
	ticker := time.NewTicker(datagramSchedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			if err := c.protocol.checkOrderedGap(now); err != nil {
				c.closeWithError(err)
				return
			}
			if err := c.protocol.sendDueAck(now); err != nil {
				c.closeWithError(err)
				return
			}
			if err := c.protocol.drainRetries(now); err != nil {
				c.closeWithError(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *WebTransportChannel) deliverDatagram(lane uint8, payload []byte) {
	switch lane {
	case datagramLaneUnreliable:
		if c.callbacks.onUnreliableStateMessage != nil {
			c.callbacks.onUnreliableStateMessage(payload)
		}
	case datagramLaneReliableOrdered:
		if c.callbacks.onReliableOrderedMessage != nil {
			c.callbacks.onReliableOrderedMessage(payload)
		}
	case datagramLaneEventualState:
		if c.callbacks.onEventualStateMessage != nil {
			c.callbacks.onEventualStateMessage(payload)
		}
	}
}

func (c *WebTransportChannel) writeDatagram(data []byte) error {
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	return c.session.SendDatagram(data)
}

func (c *WebTransportChannel) closeWithError(err error) {
	if !c.connected.Swap(false) {
		return
	}
	err = sanitizeURLError(err)
	log.Printf("golem-go-client: webtransport channel closed error=%v", err)
	c.doneOnce.Do(func() { close(c.done) })
	if c.callbacks.onClose != nil {
		c.callbacks.onClose(DisconnectInfo{WasClean: false, Err: err})
	}
}

func encodeDatagramPacket(packet datagramPacket) ([]byte, error) {
	out, err := netwire.EncodePacketInto(nil, packet)
	if err != nil {
		return nil, fmt.Errorf("golem-go-client: %s", err)
	}
	return out, validateDatagramSize(out)
}

func decodeDatagramPacket(data []byte) (datagramPacket, error) {
	packet, err := netwire.DecodePacket(data)
	if err != nil {
		return datagramPacket{}, fmt.Errorf("golem-go-client: %s", err)
	}
	if packet.Flags&datagramFlagAckOnly == 0 {
		packet.Payload = append([]byte(nil), packet.Payload...)
	}
	return packet, nil
}

// stampPacketLocked assigns wire metadata without committing ACK consumption;
// a retry pass can still fail after restamping one or more pending packets.
func (p *datagramProtocol) stampPacketLocked(packet *datagramPacket) {
	packet.Sequence = p.nextPacketSeq
	p.nextPacketSeq++
	packet.ACK = p.snapshotACKLocked()
}

func (p *datagramProtocol) snapshotACKLocked() datagramACK {
	return p.recvPackets.Snapshot()
}

func (p *datagramProtocol) clearPendingACKLocked() {
	p.ackDirty = false
	p.ackDueAt = time.Time{}
}

// checkOrderedGap also runs on idle scheduler ticks, so missing initial or
// intermediate ordered packets cannot leave an otherwise idle channel stuck.
func (p *datagramProtocol) checkOrderedGap(now time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.orderedRecv.GapSince().IsZero() && now.Sub(p.orderedRecv.GapSince()) > datagramReliableOrderedGapTimeout {
		return fmt.Errorf("golem-go-client: reliable ordered datagram gap expired")
	}
	return nil
}
