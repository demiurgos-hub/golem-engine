package net

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/webtransport-go"
)

const sendBufSize = 512

// Session represents a single connected client on the active integrated transport.
type Session struct {
	ID         int64
	Data       any       // game-specific state; set from OnUpgrade return value before OnConnect fires
	RemoteAddr string    // client address from the upgrade request; set before OnConnect
	Transport  Transport // websocket or webtransport; set before OnConnect

	reliable       reliableMessageChannel
	datagrams      datagramChannel
	protocol       *datagramProtocolState
	protocolMu     sync.Mutex
	closeTransport func() error
	streamSend     chan [][]byte
	unreliableSend chan []byte
	rawStateSend   chan []byte
	eventualSend   chan eventualStateDatagram
	wake           chan struct{}
	packetScratch  []byte

	closed    atomic.Bool
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once

	// Cumulative successful writes (for LogReplicationStats deltas).
	wireDatagramOK atomic.Uint64
	streamBatchOK  atomic.Uint64
}

func newSession(id int64, reliable reliableMessageChannel, datagrams datagramChannel, closeTransport func() error) *Session {
	sess := &Session{
		ID:             id,
		reliable:       reliable,
		datagrams:      datagrams,
		closeTransport: closeTransport,
		streamSend:     make(chan [][]byte, sendBufSize),
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	if datagrams != nil {
		sess.unreliableSend = make(chan []byte, sendBufSize)
		sess.rawStateSend = make(chan []byte, sendBufSize)
		sess.eventualSend = make(chan eventualStateDatagram, sendBufSize)
		sess.protocol = newDatagramProtocolState()
	}
	return sess
}

type datagramDrainBudget struct {
	freshReliableSendReserve int
	resendBudget             int
}

// newDatagramDrainBudget returns the per-drain resend policy budget.
func newDatagramDrainBudget() datagramDrainBudget {
	return datagramDrainBudget{
		freshReliableSendReserve: datagramFreshReliableSendReserve,
		resendBudget:             datagramResendBudgetPerDrain,
	}
}

// newWebSocketSession wraps a WebSocket connection in the transport-neutral Session API.
func newWebSocketSession(id int64, conn *websocket.Conn) *Session {
	reliable := newWebSocketReliableChannel(conn)
	return newSession(id, reliable, nil, reliable.Close)
}

// newWebTransportSession wraps a WebTransport session in the transport-neutral Session API.
func newWebTransportSession(id int64, session *webtransport.Session, stream *webtransport.Stream) *Session {
	return newSession(
		id,
		newWebTransportReliableChannel(stream),
		newWebTransportDatagramChannel(session),
		func() error { return session.CloseWithError(0, "") },
	)
}

// writePump drains the session's per-lane queues using ack preemption and
// guarded bulk drains. Exits when the done channel is closed, ctx is cancelled, or a write fails.
func (s *Session) writePump(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() { stopTimer(timer) }()
	defer s.closeNow()
	for {
		if s.closed.Load() {
			return
		}
		if err := s.drainOutbound(ctx); err != nil {
			if !s.isExpectedSessionCloseError(err) {
				log.Printf("golem/net: session %d write pump closing reason=%s remote=%q transport=%q error=%v", s.ID, classifySessionError(err), s.RemoteAddr, s.Transport, err)
			}
			return
		}
		if wakeAt, ok := s.nextOutboundWake(time.Now()); ok {
			delay := time.Until(wakeAt)
			if delay <= 0 {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				resetTimer(timer, delay)
			}
			timerC = timer.C
		} else {
			stopTimer(timer)
			timerC = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-s.wake:
		case <-timerC:
		}
	}
}

func (s *Session) isExpectedSessionCloseError(err error) bool {
	return s.closed.Load() || errors.Is(err, context.Canceled) || errors.Is(err, errEventualStateDatagramStalled)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, d time.Duration) {
	stopTimer(timer)
	timer.Reset(d)
}

func (s *Session) drainOutbound(ctx context.Context) error {
	budget := newDatagramDrainBudget()
	deadline := time.Now().Add(maxDatagramDrainDuration)
	for sends := 0; sends < maxDatagramDrainSendsPerWake; sends++ {
		now := time.Now()
		if now.After(deadline) {
			return nil
		}
		sent, err := s.writeOne(ctx, now, &budget)
		if err != nil {
			return err
		}
		if !sent {
			return nil
		}
	}
	return nil
}

func (s *Session) writeOne(ctx context.Context, now time.Time, budget *datagramDrainBudget) (bool, error) {
	if s.protocol != nil {
		s.protocolMu.Lock()
		err := s.protocol.checkOrderedGap(now)
		if err != nil {
			s.protocolMu.Unlock()
			return false, err
		}
		if packet, ok, err := s.protocol.nextAckPacketInto(now, s.packetScratch[:0]); err != nil {
			s.protocolMu.Unlock()
			return false, err
		} else if ok {
			s.protocolMu.Unlock()
			s.packetScratch = packet
			return true, s.writeProtocolDatagram(ctx, packet)
		}
		s.protocolMu.Unlock()
	}
	if payload, ok := s.tryRecvUnreliable(); ok {
		return true, s.writeUnreliable(ctx, payload)
	}
	if payload, ok := s.tryRecvRawState(); ok {
		return true, s.writeRawStateDatagram(ctx, payload)
	}
	if s.protocol != nil {
		packet, ok, err := s.nextEventualStatePacketForWrite(now, s.packetScratch[:0])
		if err != nil {
			return false, err
		}
		if ok {
			s.packetScratch = packet
			return true, s.writeProtocolDatagram(ctx, packet)
		}
	}
	if s.protocol != nil {
		preferFresh := budget != nil && budget.freshReliableSendReserve > 0
		allowResend := budget == nil || budget.resendBudget > 0
		packet, ok, resend, err := s.nextReliablePacketForWrite(now, allowResend, preferFresh, s.packetScratch[:0])
		if err != nil {
			return false, err
		}
		if ok {
			s.packetScratch = packet
			if budget != nil {
				if budget.freshReliableSendReserve > 0 {
					budget.freshReliableSendReserve--
				}
				if resend && budget.resendBudget > 0 {
					budget.resendBudget--
				}
			}
			return true, s.writeProtocolDatagram(ctx, packet)
		}
	}
	if frames, ok := s.tryRecvStream(); ok {
		return true, s.writeBatch(ctx, frames)
	}
	return false, nil
}

func (s *Session) nextOutboundWake(now time.Time) (time.Time, bool) {
	if len(s.streamSend) > 0 {
		return now, true
	}
	if len(s.unreliableSend) > 0 {
		return now, true
	}
	if len(s.rawStateSend) > 0 {
		return now, true
	}
	if len(s.eventualSend) > 0 {
		return now, true
	}
	if s.protocol == nil {
		return time.Time{}, false
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	return s.protocol.nextWakeTime(now)
}

// nextReliablePacketForWrite selects the next reliable ordered/unordered packet,
// preferring fresh sends before retries when a drain still has fresh-reserve left.
func (s *Session) nextReliablePacketForWrite(now time.Time, allowResend bool, preferFresh bool, dst []byte) ([]byte, bool, bool, error) {
	passes := []bool{allowResend}
	if preferFresh {
		passes = passes[:0]
		passes = append(passes, false)
		if allowResend {
			passes = append(passes, true)
		}
	}
	for _, passAllowResend := range passes {
		s.protocolMu.Lock()
		packet, ok, resend, err := s.protocol.nextReliablePacketInto(datagramLaneReliableOrdered, now, passAllowResend, dst)
		if err == nil && !ok {
			packet, ok, resend, err = s.protocol.nextReliablePacketInto(datagramLaneReliableUnordered, now, passAllowResend, dst)
		}
		s.protocolMu.Unlock()
		if err != nil || ok {
			return packet, ok, resend, err
		}
	}
	return nil, false, false, nil
}

func (s *Session) nextEventualStatePacketForWrite(now time.Time, dst []byte) ([]byte, bool, error) {
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()

	for {
		select {
		case msg := <-s.eventualSend:
			if err := s.protocol.enqueueEventualState(msg.token, msg.payload, msg.queuedAt); err != nil {
				return nil, false, err
			}
		default:
			return s.protocol.nextEventualStatePacketInto(now, dst)
		}
	}
}

func (s *Session) tryRecvStream() ([][]byte, bool) {
	select {
	case frames := <-s.streamSend:
		return frames, true
	default:
		return nil, false
	}
}

func (s *Session) tryRecvUnreliable() ([]byte, bool) {
	select {
	case data := <-s.unreliableSend:
		return data, true
	default:
		return nil, false
	}
}

func (s *Session) tryRecvRawState() ([]byte, bool) {
	select {
	case data := <-s.rawStateSend:
		return data, true
	default:
		return nil, false
	}
}

func (s *Session) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Session) writeProtocolDatagram(ctx context.Context, data []byte) error {
	if s.datagrams == nil {
		return ErrReliableDatagramsNotSupported
	}
	if err := s.datagrams.WriteDatagram(ctx, data); err != nil {
		return fmt.Errorf("protocol datagram write: %w", err)
	}
	s.wireDatagramOK.Add(1)
	return nil
}

// writeRawStateDatagram drains one queued state payload directly to WebTransport.
func (s *Session) writeRawStateDatagram(ctx context.Context, data []byte) error {
	if s.datagrams == nil {
		return ErrUnreliableNotSupported
	}
	if err := s.datagrams.WriteDatagram(ctx, data); err != nil {
		return fmt.Errorf("raw state datagram write: %w", err)
	}
	s.wireDatagramOK.Add(1)
	return nil
}

// writeBatch drains one queued reliable batch to the active reliable channel.
func (s *Session) writeBatch(ctx context.Context, frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}
	if err := s.reliable.WriteBatch(ctx, frames); err != nil {
		return fmt.Errorf("stream write: %w", err)
	}
	s.streamBatchOK.Add(1)
	return nil
}

// writeUnreliable drains one queued raw unreliable payload through the datagram protocol.
func (s *Session) writeUnreliable(ctx context.Context, data []byte) error {
	if s.datagrams == nil || s.protocol == nil {
		return ErrUnreliableNotSupported
	}
	s.protocolMu.Lock()
	packet, err := s.protocol.newUnreliablePacketInto(data, s.packetScratch[:0])
	s.protocolMu.Unlock()
	if err != nil {
		log.Printf("golem/net: session %d dropping oversized queued datagram: %v", s.ID, err)
		return err
	}
	s.packetScratch = packet
	return s.writeProtocolDatagram(ctx, packet)
}

// readPump reads reliable messages and protocol datagrams until the transport closes.
func (s *Session) readPump(
	ctx context.Context,
	onMsg func(*Session, []byte),
	onDatagram func(*Session, []byte),
	onReliableUnordered func(*Session, []byte),
	onReliableOrdered func(*Session, []byte),
	onEventualStateFeedback func(*Session, []EventualStateDelivery),
) {
	defer s.closeNow()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var readPumpReason string
	var readPumpErr error
	defer func() {
		if readPumpReason == "" {
			return
		}
		if readPumpErr != nil && s.isExpectedSessionCloseError(readPumpErr) {
			return
		}
		if readPumpErr != nil {
			log.Printf("golem/net: session %d read pump closing reason=%s remote=%q transport=%q error=%v", s.ID, classifySessionError(readPumpErr), s.RemoteAddr, s.Transport, readPumpErr)
			return
		}
		log.Printf("golem/net: session %d read pump closing reason=%q remote=%q transport=%q", s.ID, readPumpReason, s.RemoteAddr, s.Transport)
	}()

	var wg sync.WaitGroup
	if s.datagrams != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.datagrams.ReadDatagrams(ctx, func(data []byte) {
				deliveries, wake, err := s.handleIncomingDatagram(time.Now(), data)
				feedback := s.drainEventualStateFeedback()
				if wake {
					s.signalWake()
				}
				if err != nil {
					readPumpErr = err
					s.closeNow()
					cancel()
					return
				}
				if len(feedback) > 0 && onEventualStateFeedback != nil {
					onEventualStateFeedback(s, feedback)
				}
				for _, delivery := range deliveries {
					switch delivery.lane {
					case datagramLaneUnreliable:
						if onDatagram != nil {
							onDatagram(s, delivery.data)
						}
					case datagramLaneReliableUnordered:
						if onReliableUnordered != nil {
							onReliableUnordered(s, delivery.data)
						}
					case datagramLaneReliableOrdered:
						if onReliableOrdered != nil {
							onReliableOrdered(s, delivery.data)
						}
					case datagramLaneEventualState:
					}
				}
			}); err != nil && !errors.Is(err, context.Canceled) {
				readPumpErr = err
				s.closeNow()
				cancel()
			}
		}()
	}

	_ = s.reliable.ReadMessages(ctx, func(data []byte) {
		if s.closed.Load() {
			return
		}
		if isClientCloseControlFrame(data) {
			readPumpReason = "client initiated close"
			s.Close()
			cancel()
			return
		}
		var feedback []EventualStateDelivery
		var wake bool
		var err error
		data, feedback, wake, err = s.handleReliableAckControlFrame(time.Now(), data)
		if wake {
			s.signalWake()
		}
		if err != nil {
			readPumpErr = err
			s.closeNow()
			cancel()
			return
		}
		if len(feedback) > 0 && onEventualStateFeedback != nil {
			onEventualStateFeedback(s, feedback)
		}
		if len(data) == 0 {
			return
		}
		if onMsg != nil {
			onMsg(s, data)
		}
	})
	cancel()
	s.closeNow()
	wg.Wait()
}

func classifySessionError(err error) string {
	switch {
	case err == nil:
		return "unknown"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, errEventualStateDatagramStalled):
		return "eventual_state_stalled"
	case errors.Is(err, errReliableDatagramStalled):
		return "reliable_datagram_stalled"
	case errors.Is(err, errReliableOrderedDatagramGap):
		return "reliable_ordered_gap"
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "stream write") || strings.Contains(text, "reliable write"):
		return "stream_write"
	case strings.Contains(text, "stream read") || strings.Contains(text, "reliable read"):
		return "stream_read"
	case strings.Contains(text, "datagram write"):
		return "datagram_write"
	case strings.Contains(text, "datagram read"):
		return "datagram_read"
	case strings.Contains(text, "protocol") || strings.Contains(text, "frame") || strings.Contains(text, "payload"):
		return "protocol_error"
	default:
		return "transport_error"
	}
}

func (s *Session) drainEventualStateFeedback() []EventualStateDelivery {
	if s.protocol == nil {
		return nil
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	return s.protocol.drainEventualFeedback()
}

// handleReliableAckControlFrame applies ACK state piggybacked on a stream frame.
func (s *Session) handleReliableAckControlFrame(now time.Time, data []byte) ([]byte, []EventualStateDelivery, bool, error) {
	payload, ackSeq, ackMask, ok, err := decodeClientReliableAckControlFrame(data)
	if err != nil || !ok || s.protocol == nil {
		return payload, nil, false, err
	}
	s.protocolMu.Lock()
	wake, err := s.protocol.applyPeerAcks(ackSeq, ackMask, now)
	feedback := s.protocol.drainEventualFeedback()
	s.protocolMu.Unlock()
	return payload, feedback, wake, err
}

func (s *Session) handleIncomingDatagram(now time.Time, data []byte) ([]datagramDelivery, bool, error) {
	if s.protocol == nil {
		return []datagramDelivery{{lane: datagramLaneUnreliable, data: append([]byte(nil), data...)}}, false, nil
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	return s.protocol.handleIncoming(now, data)
}

// SupportsUnreliable reports whether the active transport exposes a datagram lane.
func (s *Session) SupportsUnreliable() bool {
	return s.datagrams != nil
}

// SupportsReliableDatagrams reports whether the active transport supports reliable datagram lanes.
func (s *Session) SupportsReliableDatagrams() bool {
	return s.protocol != nil
}

// Send queues a single reliable binary message for delivery. If the session is already
// closing, the message is silently dropped.
func (s *Session) Send(data []byte) {
	s.SendBatch([][]byte{data})
}

// SendBatch queues a reliable batch for delivery on the reliable stream lane.
func (s *Session) SendBatch(frames [][]byte) {
	if len(frames) == 0 {
		return
	}
	s.queueStream(append([][]byte(nil), frames...))
}

// SendUnreliable queues a single lossy datagram for delivery when supported.
func (s *Session) SendUnreliable(data []byte) error {
	if s.datagrams == nil {
		return ErrUnreliableNotSupported
	}
	if len(data) == 0 || s.closed.Load() {
		return nil
	}
	if err := validateUnreliableDatagramPayloadSize(data); err != nil {
		return err
	}
	s.queueUnreliable(append([]byte(nil), data...))
	return nil
}

// sendUnreliableState queues one raw state datagram that bypasses the datagram
// reliability protocol entirely.
func (s *Session) sendUnreliableState(data []byte) error {
	cp := append([]byte(nil), data...)
	return s.sendUnreliableStateOwned(cp)
}

func (s *Session) sendUnreliableStateOwned(data []byte) error {
	if s.datagrams == nil {
		return ErrUnreliableNotSupported
	}
	if len(data) == 0 || s.closed.Load() {
		return nil
	}
	if err := validateDatagramSize(data); err != nil {
		return err
	}
	s.queueRawState(data)
	return nil
}

// SendReliableUnordered queues one reliable unordered datagram when supported.
func (s *Session) SendReliableUnordered(data []byte) error {
	return s.queueReliable(datagramLaneReliableUnordered, data)
}

// SendReliableOrdered queues one reliable ordered datagram when supported.
func (s *Session) SendReliableOrdered(data []byte) error {
	return s.queueReliable(datagramLaneReliableOrdered, data)
}

// SendEventualState queues one state datagram payload that reports ACK/loss by token.
func (s *Session) SendEventualState(token uint64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	cp := append([]byte(nil), data...)
	return s.sendEventualStateOwned(token, cp)
}

func (s *Session) sendEventualStateOwned(token uint64, data []byte) error {
	if s.protocol == nil {
		return ErrReliableDatagramsNotSupported
	}
	if len(data) == 0 || s.closed.Load() {
		return nil
	}
	if err := validateEventualStateDatagramPayloadSize(data); err != nil {
		return err
	}
	s.queueEventualState(eventualStateDatagram{
		token:    token,
		payload:  data,
		queuedAt: time.Now(),
	})
	return nil
}

func (s *Session) queueReliable(lane datagramLane, data []byte) error {
	if s.protocol == nil {
		return ErrReliableDatagramsNotSupported
	}
	if len(data) == 0 || s.closed.Load() {
		return nil
	}
	s.protocolMu.Lock()
	err := s.protocol.enqueueReliable(lane, data, time.Now())
	s.protocolMu.Unlock()
	if err != nil {
		if s.closed.Swap(true) {
			return nil
		}
		log.Printf("golem/net: session %d send buffer overflow while queueing %s datagram; closing slow session", s.ID, laneName(lane))
		s.closeNow()
		return err
	}
	s.signalWake()
	return nil
}

func (s *Session) queueStream(frames [][]byte) {
	if s.closed.Load() {
		return
	}
	for {
		select {
		case s.streamSend <- frames:
			s.signalWake()
			return
		default:
			if s.closed.Swap(true) {
				return
			}
			log.Printf("golem/net: session %d send buffer overflow while queueing reliable batch (buffer=%d); closing slow session", s.ID, len(s.streamSend))
			s.closeNow()
			return
		}
	}
}

func (s *Session) queueUnreliable(data []byte) {
	if s.closed.Load() {
		return
	}
	for {
		select {
		case s.unreliableSend <- data:
			s.signalWake()
			return
		default:
			if s.closed.Swap(true) {
				return
			}
			log.Printf("golem/net: session %d send buffer overflow while queueing unreliable datagram (buffer=%d); closing slow session", s.ID, len(s.unreliableSend))
			s.closeNow()
			return
		}
	}
}

func (s *Session) queueRawState(data []byte) {
	if s.closed.Load() {
		return
	}
	for {
		select {
		case s.rawStateSend <- data:
			s.signalWake()
			return
		default:
			if s.closed.Swap(true) {
				return
			}
			log.Printf("golem/net: session %d send buffer overflow while queueing raw state datagram (buffer=%d); closing slow session", s.ID, len(s.rawStateSend))
			s.closeNow()
			return
		}
	}
}

func (s *Session) queueEventualState(msg eventualStateDatagram) {
	if s.closed.Load() {
		return
	}
	for {
		select {
		case s.eventualSend <- msg:
			s.signalWake()
			return
		default:
			if s.closed.Swap(true) {
				return
			}
			log.Printf("golem/net: session %d send buffer overflow while queueing eventual state datagram (buffer=%d); closing slow session", s.ID, len(s.eventualSend))
			s.closeNow()
			return
		}
	}
}

func laneName(lane datagramLane) string {
	switch lane {
	case datagramLaneReliableOrdered:
		return "reliable-ordered"
	case datagramLaneReliableUnordered:
		return "reliable-unordered"
	case datagramLaneUnreliable:
		return "unreliable"
	case datagramLaneEventualState:
		return "eventual-state"
	default:
		return fmt.Sprintf("lane-%d", lane)
	}
}

// Close terminates the session transport and unblocks any waiting writePump.
// Exported so generated routers and custom middleware can reject bad clients.
func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.closeNow()
	s.shutdown()
}

// shutdown signals writePump to exit. Called from Listener.removeSession
// after the session is deleted from the session map.
func (s *Session) shutdown() {
	s.closed.Store(true)
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Session) closeNow() {
	s.closed.Store(true)
	s.closeOnce.Do(func() {
		if s.closeTransport != nil {
			_ = s.closeTransport()
		}
	})
}
