package golemclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/webtransport-go"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

const (
	datagramAckMaskWordCount             = 4
	datagramAckMaskWordBytes             = 4
	datagramAckMaskBytes                 = datagramAckMaskWordCount * datagramAckMaskWordBytes
	datagramPacketHeaderBytes            = 2 + 2 + datagramAckMaskBytes + 1
	datagramLaneHeaderBytes              = 1
	datagramReliableMessageIDBytes       = 2
	datagramReliableOrderedSequenceBytes = 2
	datagramEventualStateTokenBytes      = 8
	datagramPacketAckWindow              = datagramAckMaskWordCount * 32
	datagramAckCoalesceDelay             = 1 * time.Millisecond
	datagramSchedulerInterval            = 2 * time.Millisecond
	datagramReliableRetryBaseDelay       = 75 * time.Millisecond
	datagramReliableRetryMaxDelay        = 400 * time.Millisecond
	datagramReliableRetryLimit           = 8
	datagramReliableMessageTTL           = 3 * time.Second
	datagramReliableOrderedGapTimeout    = 3 * time.Second
)

const (
	datagramLaneUnreliable uint8 = iota + 1
	datagramLaneReliableUnordered
	datagramLaneReliableOrdered
	datagramLaneEventualState
)

const datagramFlagAckOnly uint8 = 1 << iota

var clientReliableAckControlFrame = []byte{0x00, 'O', 'G', 'S', 0x02}

type ackMask128 [datagramAckMaskWordCount]uint32

func (m *ackMask128) setBit(bit int) {
	if bit < 0 || bit >= datagramPacketAckWindow {
		return
	}
	m[bit/32] |= uint32(1) << uint(bit%32)
}

func (m *ackMask128) shiftLeft(bits int) {
	if bits <= 0 {
		return
	}
	if bits >= datagramPacketAckWindow {
		*m = ackMask128{}
		return
	}
	wordShift := bits / 32
	bitShift := uint(bits % 32)
	var shifted ackMask128
	for i := len(shifted) - 1; i >= 0; i-- {
		src := i - wordShift
		if src < 0 {
			continue
		}
		shifted[i] = m[src] << bitShift
		if bitShift > 0 && src > 0 {
			shifted[i] |= m[src-1] >> (32 - bitShift)
		}
	}
	*m = shifted
}

type sequenceWindow struct {
	init   bool
	latest uint16
	mask   ackMask128
}

func (w *sequenceWindow) accept(seq uint16) bool {
	if !w.init {
		w.init = true
		w.latest = seq
		return true
	}
	if seq == w.latest {
		return false
	}
	if sequenceNewer(seq, w.latest) {
		diff := sequenceDistance(w.latest, seq)
		w.mask.shiftLeft(diff)
		w.mask.setBit(diff - 1)
		w.latest = seq
		return true
	}
	diff := sequenceDistance(seq, w.latest)
	if diff == 0 || diff > datagramPacketAckWindow {
		return false
	}
	bit := diff - 1
	if w.mask[bit/32]&(uint32(1)<<uint(bit%32)) != 0 {
		return false
	}
	w.mask.setBit(bit)
	return true
}

func sequenceNewer(a, b uint16) bool {
	return a != b && uint16(a-b) < 0x8000
}

func sequenceDistance(older, newer uint16) int {
	return int(uint16(newer - older))
}

type datagramPacket struct {
	packetSeq  uint16
	ackSeq     uint16
	ackMask    ackMask128
	flags      uint8
	lane       uint8
	messageID  uint16
	orderedSeq uint16
	stateToken uint64
	payload    []byte
}

type pendingReliableDatagram struct {
	packet     datagramPacket
	queuedAt   time.Time
	lastSentAt time.Time
	nextSendAt time.Time
	attempts   int
}

type orderedReceiveState struct {
	nextSeq  uint16
	init     bool
	pending  map[uint16][]byte
	gapSince time.Time
}

type datagramProtocol struct {
	mu              sync.Mutex
	nextPacketSeq   uint16
	nextMessageID   uint16
	nextOrderedSeq  uint16
	recvPackets     sequenceWindow
	recvReliableMsg sequenceWindow
	orderedRecv     orderedReceiveState
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
		orderedRecv:     orderedReceiveState{pending: make(map[uint16][]byte)},
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
		packetSeq: p.nextPacketSeq,
		ackSeq:    p.recvPackets.latest,
		ackMask:   p.recvPackets.mask,
		lane:      lane,
		payload:   append([]byte(nil), payload...),
	}
	p.nextPacketSeq++
	p.ackDirty = false
	p.ackDueAt = time.Time{}
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
		packetSeq: p.nextPacketSeq,
		ackSeq:    p.recvPackets.latest,
		ackMask:   p.recvPackets.mask,
		lane:      lane,
		messageID: p.nextMessageID,
		payload:   append([]byte(nil), payload...),
	}
	p.nextPacketSeq++
	p.nextMessageID++
	if lane == datagramLaneReliableOrdered {
		packet.orderedSeq = p.nextOrderedSeq
		p.nextOrderedSeq++
	}
	p.ackDirty = false
	p.ackDueAt = time.Time{}
	p.pendingReliable[packet.packetSeq] = &pendingReliableDatagram{
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
	p.applyPeerAcksLocked(packet.ackSeq, packet.ackMask)
	p.mu.Unlock()
	if packet.flags&datagramFlagAckOnly != 0 {
		return nil
	}
	p.mu.Lock()
	accepted := p.recvPackets.accept(packet.packetSeq)
	if accepted {
		p.scheduleAckLocked(now)
	}
	p.mu.Unlock()
	if !accepted {
		return nil
	}
	switch packet.lane {
	case datagramLaneReliableUnordered, datagramLaneReliableOrdered:
		p.mu.Lock()
		accepted = p.recvReliableMsg.accept(packet.messageID)
		p.mu.Unlock()
		if !accepted {
			return nil
		}
	}
	if packet.lane == datagramLaneReliableOrdered {
		return p.deliverOrdered(packet, now, deliver)
	}
	if deliver != nil {
		deliver(packet.lane, packet.payload)
	}
	return nil
}

func (p *datagramProtocol) deliverOrdered(packet datagramPacket, now time.Time, deliver func(uint8, []byte)) error {
	var deliveries [][]byte
	p.mu.Lock()
	if !p.orderedRecv.init {
		p.orderedRecv.init = true
		p.orderedRecv.nextSeq = packet.orderedSeq
	}
	if packet.orderedSeq == p.orderedRecv.nextSeq {
		deliveries = append(deliveries, packet.payload)
		p.orderedRecv.nextSeq++
		for {
			payload, ok := p.orderedRecv.pending[p.orderedRecv.nextSeq]
			if !ok {
				break
			}
			delete(p.orderedRecv.pending, p.orderedRecv.nextSeq)
			deliveries = append(deliveries, payload)
			p.orderedRecv.nextSeq++
		}
		if len(p.orderedRecv.pending) == 0 {
			p.orderedRecv.gapSince = time.Time{}
		}
	} else if sequenceNewer(packet.orderedSeq, p.orderedRecv.nextSeq) {
		p.orderedRecv.pending[packet.orderedSeq] = append([]byte(nil), packet.payload...)
		if p.orderedRecv.gapSince.IsZero() {
			p.orderedRecv.gapSince = now
		}
		if now.Sub(p.orderedRecv.gapSince) > datagramReliableOrderedGapTimeout {
			p.mu.Unlock()
			return fmt.Errorf("golem-go-client: reliable ordered datagram gap expired")
		}
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
		packetSeq: p.nextPacketSeq,
		ackSeq:    p.recvPackets.latest,
		ackMask:   p.recvPackets.mask,
		flags:     datagramFlagAckOnly,
	}
	p.nextPacketSeq++
	p.ackDirty = false
	p.ackDueAt = time.Time{}
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
		pending.packet.packetSeq = p.nextPacketSeq
		pending.packet.ackSeq = p.recvPackets.latest
		pending.packet.ackMask = p.recvPackets.mask
		p.nextPacketSeq++
		pending.attempts++
		pending.lastSentAt = now
		delay := datagramReliableRetryBaseDelay << min(pending.attempts-1, 3)
		if delay > datagramReliableRetryMaxDelay {
			delay = datagramReliableRetryMaxDelay
		}
		pending.nextSendAt = now.Add(delay)
		p.pendingReliable[pending.packet.packetSeq] = pending
		packets = append(packets, pending.packet)
	}
	if len(packets) > 0 {
		p.ackDirty = false
		p.ackDueAt = time.Time{}
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
	for seq := range p.pendingReliable {
		if seq == ackSeq {
			delete(p.pendingReliable, seq)
			continue
		}
		diff := sequenceDistance(seq, ackSeq)
		if diff <= 0 || diff > datagramPacketAckWindow {
			continue
		}
		bit := diff - 1
		if ackMask[bit/32]&(uint32(1)<<uint(bit%32)) != 0 {
			delete(p.pendingReliable, seq)
		}
	}
}

func (p *datagramProtocol) wrapStreamPayload(payload []byte) []byte {
	p.mu.Lock()
	if !p.ackDirty || len(payload)+len(clientReliableAckControlFrame)+2+datagramAckMaskBytes > maxReliableMessageBytes {
		p.mu.Unlock()
		return payload
	}
	ackSeq := p.recvPackets.latest
	ackMask := p.recvPackets.mask
	p.ackDirty = false
	p.ackDueAt = time.Time{}
	p.mu.Unlock()
	out := make([]byte, 0, len(clientReliableAckControlFrame)+2+datagramAckMaskBytes+len(payload))
	out = append(out, clientReliableAckControlFrame...)
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], ackSeq)
	out = append(out, hdr[:]...)
	var word [4]byte
	for _, v := range ackMask {
		binary.BigEndian.PutUint32(word[:], v)
		out = append(out, word[:]...)
	}
	return append(out, payload...)
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
	log.Printf("golem-go-client: dialing webtransport url=%q", redacted)
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
	_, session, err := dialer.Dial(ctx, options.URL, nil)
	if err != nil {
		log.Printf("golem-go-client: webtransport dial failed url=%q error=%v", redacted, err)
		return nil, fmt.Errorf("golem-go-client: webtransport dial: %w", err)
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		log.Printf("golem-go-client: webtransport open stream failed url=%q error=%v", redacted, err)
		_ = session.CloseWithError(0, "")
		return nil, fmt.Errorf("golem-go-client: webtransport open stream: %w", err)
	}
	// Flush the WebTransport stream header so the server can AcceptStream.
	// This does not write a golem reliable frame.
	if _, err := stream.Write(nil); err != nil {
		log.Printf("golem-go-client: webtransport stream header write failed url=%q error=%v", redacted, err)
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
	return writeReliableFrame(c.stream, c.protocol.wrapStreamPayload(data))
}

// SendUnreliable sends one raw unreliable datagram.
func (c *WebTransportChannel) SendUnreliable(data []byte) error {
	return c.protocol.send(datagramLaneUnreliable, data)
}

// SendReliableUnordered sends one reliable unordered datagram message.
func (c *WebTransportChannel) SendReliableUnordered(data []byte) error {
	return c.protocol.send(datagramLaneReliableUnordered, data)
}

// SendReliableOrdered sends one reliable ordered datagram message.
func (c *WebTransportChannel) SendReliableOrdered(data []byte) error {
	return c.protocol.send(datagramLaneReliableOrdered, data)
}

// Close closes the WebTransport session.
func (c *WebTransportChannel) Close() error {
	c.closeOnce.Do(func() {
		c.connected.Store(false)
		c.doneOnce.Do(func() { close(c.done) })
		_ = writeReliableFrame(c.stream, golemnet.ClientCloseControlFrame())
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
	log.Printf("golem-go-client: webtransport channel closed error=%v", err)
	c.doneOnce.Do(func() { close(c.done) })
	if c.callbacks.onClose != nil {
		c.callbacks.onClose(DisconnectInfo{WasClean: false, Err: err})
	}
}

func encodeDatagramPacket(packet datagramPacket) ([]byte, error) {
	if packet.flags&datagramFlagAckOnly == 0 {
		switch packet.lane {
		case datagramLaneUnreliable, datagramLaneReliableUnordered, datagramLaneReliableOrdered, datagramLaneEventualState:
		default:
			return nil, fmt.Errorf("golem-go-client: unknown datagram lane %d", packet.lane)
		}
	}
	baseLen := datagramPacketHeaderBytes
	if packet.flags&datagramFlagAckOnly == 0 {
		baseLen += datagramLaneHeaderBytes + len(packet.payload)
		switch packet.lane {
		case datagramLaneReliableUnordered:
			baseLen += datagramReliableMessageIDBytes
		case datagramLaneReliableOrdered:
			baseLen += datagramReliableMessageIDBytes + datagramReliableOrderedSequenceBytes
		case datagramLaneEventualState:
			baseLen += datagramEventualStateTokenBytes
		}
	}
	out := make([]byte, baseLen)
	binary.BigEndian.PutUint16(out[0:2], packet.packetSeq)
	binary.BigEndian.PutUint16(out[2:4], packet.ackSeq)
	for i, word := range packet.ackMask {
		binary.BigEndian.PutUint32(out[4+i*4:8+i*4], word)
	}
	out[datagramPacketHeaderBytes-1] = packet.flags
	if packet.flags&datagramFlagAckOnly != 0 {
		return out, validateDatagramSize(out)
	}
	offset := datagramPacketHeaderBytes
	out[offset] = packet.lane
	offset++
	switch packet.lane {
	case datagramLaneReliableUnordered:
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.messageID)
		offset += 2
	case datagramLaneReliableOrdered:
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.messageID)
		offset += 2
		binary.BigEndian.PutUint16(out[offset:offset+2], packet.orderedSeq)
		offset += 2
	case datagramLaneEventualState:
		binary.BigEndian.PutUint64(out[offset:offset+8], packet.stateToken)
		offset += 8
	}
	copy(out[offset:], packet.payload)
	return out, validateDatagramSize(out)
}

func decodeDatagramPacket(data []byte) (datagramPacket, error) {
	if len(data) < datagramPacketHeaderBytes {
		return datagramPacket{}, fmt.Errorf("golem-go-client: datagram packet too small")
	}
	packet := datagramPacket{
		packetSeq: binary.BigEndian.Uint16(data[0:2]),
		ackSeq:    binary.BigEndian.Uint16(data[2:4]),
		flags:     data[datagramPacketHeaderBytes-1],
	}
	for i := range packet.ackMask {
		packet.ackMask[i] = binary.BigEndian.Uint32(data[4+i*4 : 8+i*4])
	}
	if packet.flags&datagramFlagAckOnly != 0 {
		return packet, nil
	}
	offset := datagramPacketHeaderBytes
	if len(data) < offset+datagramLaneHeaderBytes {
		return datagramPacket{}, fmt.Errorf("golem-go-client: datagram lane missing")
	}
	packet.lane = data[offset]
	offset++
	switch packet.lane {
	case datagramLaneUnreliable:
	case datagramLaneReliableUnordered:
		if len(data) < offset+datagramReliableMessageIDBytes {
			return datagramPacket{}, fmt.Errorf("golem-go-client: reliable datagram message id missing")
		}
		packet.messageID = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
	case datagramLaneReliableOrdered:
		if len(data) < offset+datagramReliableMessageIDBytes+datagramReliableOrderedSequenceBytes {
			return datagramPacket{}, fmt.Errorf("golem-go-client: ordered datagram header missing")
		}
		packet.messageID = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
		packet.orderedSeq = binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
	case datagramLaneEventualState:
		if len(data) < offset+datagramEventualStateTokenBytes {
			return datagramPacket{}, fmt.Errorf("golem-go-client: state datagram token missing")
		}
		packet.stateToken = binary.BigEndian.Uint64(data[offset : offset+8])
		offset += 8
	default:
		return datagramPacket{}, fmt.Errorf("golem-go-client: unknown datagram lane %d", packet.lane)
	}
	packet.payload = append([]byte(nil), data[offset:]...)
	return packet, nil
}
