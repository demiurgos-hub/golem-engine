package net

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golem-engine/golem/registry"
)

// testWSPair creates a WebSocket connection pair via httptest. The returned
// serverConn is suitable for building a Session. Call cleanup when done.
func testWSPair(t *testing.T) (serverConn *websocket.Conn, cleanup func()) {
	t.Helper()
	ch := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ch <- c
		<-r.Context().Done()
	}))

	ctx := context.Background()
	client, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}

	sc := <-ch
	return sc, func() {
		client.CloseNow()
		srv.Close()
	}
}

func testWSPairWithClient(t *testing.T) (serverConn *websocket.Conn, clientConn *websocket.Conn, cleanup func()) {
	t.Helper()
	ch := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ch <- c
		<-r.Context().Done()
	}))

	ctx := context.Background()
	client, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}

	sc := <-ch
	return sc, client, func() {
		client.CloseNow()
		srv.Close()
	}
}

func TestSessionSendOverflow(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	sess := newWebSocketSession(1, conn)

	for i := 0; i < sendBufSize; i++ {
		sess.Send([]byte("x"))
	}

	sess.Send([]byte("overflow"))
	if !sess.closed.Load() {
		t.Fatal("expected session to be marked closed after overflow")
	}

	for i := 0; i < 500; i++ {
		sess.Send([]byte("post-close"))
	}
}

func TestSessionSendOverflowLogsBufferOverflow(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	sess := newWebSocketSession(99, conn)

	var logBuf bytes.Buffer
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer log.SetOutput(prevWriter)
	defer log.SetFlags(prevFlags)

	for i := 0; i < sendBufSize; i++ {
		sess.Send([]byte("x"))
	}
	sess.Send([]byte("overflow"))

	if got := logBuf.String(); !strings.Contains(got, "send buffer overflow") {
		t.Fatalf("log output = %q, want substring %q", got, "send buffer overflow")
	}
}

func TestSessionShutdown(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	sess := newWebSocketSession(1, conn)
	sess.shutdown()

	if !sess.closed.Load() {
		t.Fatal("expected closed flag after shutdown")
	}

	for i := 0; i < 100; i++ {
		sess.Send([]byte("post-shutdown"))
	}
}

func TestSessionReadPumpClosesTransportWhenDatagramReadFails(t *testing.T) {
	reliable := newBlockingReadReliableChannel()
	sess := newSession(7, reliable, failingDatagramChannel{err: errors.New("datagram read failed")}, reliable.Close)

	done := make(chan struct{})
	go func() {
		sess.readPump(context.Background(), nil, nil, nil, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readPump did not return after datagram read failure")
	}
	if !sess.closed.Load() {
		t.Fatal("session was not marked closed")
	}
}

func TestSessionReadPumpInterceptsClientCloseControlFrame(t *testing.T) {
	reliable := &scriptedReliableChannel{
		messages: [][]byte{ClientCloseControlFrame(), []byte("after-close")},
	}
	sess := newSession(8, reliable, nil, reliable.Close)
	var onMsg atomic.Int32

	sess.readPump(context.Background(), func(*Session, []byte) {
		onMsg.Add(1)
	}, nil, nil, nil, nil)

	if got := onMsg.Load(); got != 0 {
		t.Fatalf("onMsg calls = %d, want 0", got)
	}
	if !sess.closed.Load() {
		t.Fatal("session was not marked closed")
	}
	if got := reliable.closeCount.Load(); got != 1 {
		t.Fatalf("reliable close count = %d, want 1", got)
	}
}

func TestSessionReadPumpCloseControlFrameRacesTransportFailure(t *testing.T) {
	reliable := &scriptedReliableChannel{
		messages: [][]byte{ClientCloseControlFrame()},
		block:    make(chan struct{}),
	}
	sess := newSession(9, reliable, failingDatagramChannel{err: errors.New("datagram read failed")}, reliable.Close)
	var onMsg atomic.Int32

	done := make(chan struct{})
	go func() {
		sess.readPump(context.Background(), func(*Session, []byte) {
			onMsg.Add(1)
		}, nil, nil, nil, nil)
		close(done)
	}()
	close(reliable.block)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readPump did not return")
	}
	if got := onMsg.Load(); got != 0 {
		t.Fatalf("onMsg calls = %d, want 0", got)
	}
	if got := reliable.closeCount.Load(); got != 1 {
		t.Fatalf("reliable close count = %d, want 1", got)
	}
}

func TestSessionReadPumpAppliesReliableAckControlFrame(t *testing.T) {
	payload := []byte("command")
	frame := make([]byte, clientReliableAckControlHeaderBytes+len(payload))
	copy(frame, clientReliableAckControlFrame)
	offset := len(clientReliableAckControlFrame)
	binary.BigEndian.PutUint16(frame[offset:offset+2], 5)
	offset += 2 + datagramAckMaskBytes
	copy(frame[offset:], payload)

	reliable := &scriptedReliableChannel{messages: [][]byte{frame}}
	sess := newSession(11, reliable, &captureDatagramChannel{}, reliable.Close)
	sess.protocol.inFlightEventual[5] = pendingEventualStateDatagram{
		token:  99,
		sentAt: time.Now(),
	}
	var gotMsg []byte
	var gotFeedback []EventualStateDelivery

	sess.readPump(context.Background(), func(_ *Session, data []byte) {
		gotMsg = append([]byte(nil), data...)
	}, nil, nil, nil, func(_ *Session, feedback []EventualStateDelivery) {
		gotFeedback = append([]EventualStateDelivery(nil), feedback...)
	})

	if !bytes.Equal(gotMsg, payload) {
		t.Fatalf("message payload = %q, want %q", gotMsg, payload)
	}
	if len(gotFeedback) != 1 || gotFeedback[0].Token != 99 || !gotFeedback[0].Delivered {
		t.Fatalf("feedback = %+v, want delivered token 99", gotFeedback)
	}
}

func TestSessionWritePumpSuppressesEventualStateStallLog(t *testing.T) {
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(10, reliable, datagrams, reliable.Close)
	sess.protocol.inFlightEventual[1] = pendingEventualStateDatagram{
		token:  1,
		sentAt: time.Now().Add(-datagramEventualStateTTL - time.Millisecond),
	}

	var logBuf bytes.Buffer
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer log.SetOutput(prevWriter)
	defer log.SetFlags(prevFlags)

	sess.writePump(context.Background())

	if got := logBuf.String(); strings.Contains(got, "eventual state datagram delivery stalled") {
		t.Fatalf("log output = %q, want no eventual-state stall log", got)
	}
}

func TestSessionNextOutboundWakeImmediateForQueuedWork(t *testing.T) {
	now := time.UnixMilli(1000)

	stream := newSession(1, &captureReliableChannel{}, nil, nil)
	stream.Send([]byte("stream"))
	assertWakeNow(t, "stream", stream, now)

	unreliable := newSession(2, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	if err := unreliable.SendUnreliable([]byte("unreliable")); err != nil {
		t.Fatalf("SendUnreliable: %v", err)
	}
	assertWakeNow(t, "unreliable", unreliable, now)

	reliable := newSession(3, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	if err := reliable.SendReliableOrdered([]byte("reliable")); err != nil {
		t.Fatalf("SendReliableOrdered: %v", err)
	}
	assertWakeNow(t, "reliable", reliable, now)

	eventual := newSession(4, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	if err := eventual.SendEventualState(1, []byte("eventual")); err != nil {
		t.Fatalf("SendEventualState: %v", err)
	}
	assertWakeNow(t, "eventual", eventual, now)
}

func TestSessionQueuePathsSignalWake(t *testing.T) {
	sess := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)

	sess.Send([]byte("stream"))
	assertWakeSignaled(t, "stream", sess)

	if err := sess.SendUnreliable([]byte("unreliable")); err != nil {
		t.Fatalf("SendUnreliable: %v", err)
	}
	assertWakeSignaled(t, "unreliable", sess)

	if err := sess.SendReliableOrdered([]byte("reliable")); err != nil {
		t.Fatalf("SendReliableOrdered: %v", err)
	}
	assertWakeSignaled(t, "reliable", sess)

	if err := sess.SendEventualState(1, []byte("eventual")); err != nil {
		t.Fatalf("SendEventualState: %v", err)
	}
	assertWakeSignaled(t, "eventual", sess)
}

func TestSessionConcurrentSendOverflow(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	sess := newWebSocketSession(1, conn)

	for i := 0; i < sendBufSize; i++ {
		sess.Send([]byte("x"))
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				sess.Send([]byte("concurrent"))
			}
		}()
	}
	wg.Wait()
}

func TestListenerSendAfterOverflow(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{})

	sess := newWebSocketSession(1, conn)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	for i := 0; i < sendBufSize; i++ {
		sess.Send([]byte("x"))
	}
	sess.Send([]byte("overflow"))

	if err := listener.Broadcast([][]byte{[]byte("a"), []byte("b")}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if err := listener.BroadcastRaw([]byte("raw")); err != nil {
		t.Fatalf("BroadcastRaw: %v", err)
	}
	if err := listener.SendWrapped(1, []byte("wrapped")); err != nil {
		t.Fatalf("SendWrapped: %v", err)
	}
	if err := listener.Send(1, []byte("direct")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := listener.SendBatch(1, [][]byte{[]byte("batch")}); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	if err := listener.BroadcastBatch([][]byte{[]byte("ba"), []byte("bb")}); err != nil {
		t.Fatalf("BroadcastBatch: %v", err)
	}
}

func TestSendBatchSingleChannel(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{})

	sess := newWebSocketSession(1, conn)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	if err := listener.SendBatch(1, [][]byte{[]byte("hello-batch")}); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}

	if err := listener.SendBatch(999, [][]byte{[]byte("nope")}); err == nil {
		t.Fatal("SendBatch returned nil for non-existent session")
	} else if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SendBatch error = %v, want ErrSessionNotFound", err)
	}
}

func TestListenerSendReliableOrderedBatchSplitsAtPayloadCap(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	frameA := bytes.Repeat([]byte("a"), 800)
	frameB := bytes.Repeat([]byte("b"), 800)
	if err := listener.SendReliableOrderedBatch(1, [][]byte{frameA, frameB}); err != nil {
		t.Fatalf("SendReliableOrderedBatch: %v", err)
	}

	budget := newDatagramDrainBudget()
	for i := 0; i < 2; i++ {
		sent, err := sess.writeOne(context.Background(), time.UnixMilli(0), &budget)
		if err != nil {
			t.Fatalf("writeOne %d: %v", i, err)
		}
		if !sent {
			t.Fatalf("writeOne %d returned sent=false", i)
		}
	}

	if len(datagrams.writes) != 2 {
		t.Fatalf("datagram writes len = %d, want 2", len(datagrams.writes))
	}
	for i, want := range [][]byte{frameA, frameB} {
		packet, err := decodeDatagramPacket(datagrams.writes[i])
		if err != nil {
			t.Fatalf("decodeDatagramPacket %d: %v", i, err)
		}
		if packet.lane != datagramLaneReliableOrdered {
			t.Fatalf("packet %d lane = %d, want %d", i, packet.lane, datagramLaneReliableOrdered)
		}
		got, err := readReliableFrame(bytes.NewReader(packet.payload))
		if err != nil {
			t.Fatalf("readReliableFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("packet %d payload mismatch", i)
		}
	}
}

func TestListenerSendReliableOrderedBatchRejectsOversizeFrame(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	frame := bytes.Repeat([]byte("x"), maxReliableOrderedDatagramPayloadBytes-reliableFrameHeaderBytes+1)
	if err := listener.SendReliableOrderedBatch(1, [][]byte{frame}); err == nil {
		t.Fatal("SendReliableOrderedBatch returned nil for oversized frame")
	}
}

func TestBroadcastBatchUsesOneSlotPerSession(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{})

	sess := newWebSocketSession(1, conn)
	listener.addSession(sess)
	defer listener.removeSession(sess)

	frames := make([][]byte, 500)
	for i := range frames {
		frames[i] = []byte("entity")
	}

	if err := listener.BroadcastBatch(frames); err != nil {
		t.Fatalf("BroadcastBatch: %v", err)
	}

	if sess.closed.Load() {
		t.Fatal("session should not be closed: 500 entities batched into 1 channel send")
	}
}

func TestSessionWritePumpSplitsBatchAtPayloadCap(t *testing.T) {
	conn, client, cleanup := testWSPairWithClient(t)
	defer cleanup()

	sess := newWebSocketSession(1, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sess.writePump(ctx)

	first := bytes.Repeat([]byte("a"), 20000)
	second := bytes.Repeat([]byte("b"), 15000)
	sess.SendBatch([][]byte{first, second})

	_, msg1, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	_, msg2, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}

	if len(msg1) != len(first) {
		t.Fatalf("first message len = %d, want %d", len(msg1), len(first))
	}
	if len(msg2) != len(second) {
		t.Fatalf("second message len = %d, want %d", len(msg2), len(second))
	}
}

func TestListenerBroadcastRawRejectsOversizeFrame(t *testing.T) {
	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{})

	oversized := bytes.Repeat([]byte("x"), maxWebSocketPayloadBytes+1)
	if err := listener.BroadcastRaw(oversized); err == nil {
		t.Fatal("BroadcastRaw returned nil for oversized frame")
	}
}

func TestSessionReadPumpRejectsOversizeMessage(t *testing.T) {
	conn, client, cleanup := testWSPairWithClient(t)
	defer cleanup()

	sess := newWebSocketSession(1, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := make(chan struct{}, 1)
	go sess.readPump(ctx, func(*Session, []byte) {
		called <- struct{}{}
	}, nil, nil, nil, nil)

	oversized := bytes.Repeat([]byte("z"), maxWebSocketPayloadBytes+1)
	if err := client.Write(ctx, websocket.MessageBinary, oversized); err != nil {
		t.Fatalf("client Write: %v", err)
	}

	deadline := time.After(time.Second)
	for !sess.closed.Load() {
		select {
		case <-deadline:
			t.Fatal("session was not closed after oversized inbound message")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	select {
	case <-called:
		t.Fatal("readPump delivered oversized message to callback")
	default:
	}
}

func TestListenerSendUnreliableUnsupportedOnWebSocket(t *testing.T) {
	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{})
	if err := listener.SendUnreliable(1, []byte("nope")); !errors.Is(err, ErrUnreliableNotSupported) {
		t.Fatalf("SendUnreliable error = %v, want ErrUnreliableNotSupported", err)
	}
	if err := listener.BroadcastUnreliable([]byte("nope")); !errors.Is(err, ErrUnreliableNotSupported) {
		t.Fatalf("BroadcastUnreliable error = %v, want ErrUnreliableNotSupported", err)
	}
}

func TestSessionSupportsReliableDatagramsOnlyOnWebTransport(t *testing.T) {
	conn, cleanup := testWSPair(t)
	defer cleanup()

	wsSession := newWebSocketSession(1, conn)
	if wsSession.SupportsReliableDatagrams() {
		t.Fatal("websocket session unexpectedly supports reliable datagrams")
	}
	if err := wsSession.SendReliableOrdered([]byte("x")); !errors.Is(err, ErrReliableDatagramsNotSupported) {
		t.Fatalf("SendReliableOrdered error = %v, want ErrReliableDatagramsNotSupported", err)
	}
}

func TestSessionReliableDatagramCallbacks(t *testing.T) {
	sess := &Session{
		ID:             1,
		reliable:       stubReliableChannel{},
		datagrams:      stubDatagramChannel{},
		protocol:       newDatagramProtocolState(),
		streamSend:     make(chan [][]byte, sendBufSize),
		unreliableSend: make(chan []byte, sendBufSize),
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
	}

	var gotUnordered, gotOrdered atomic.Int32
	rawUnordered, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 1,
		ackSeq:    0,
		ackMask:   ackMask128{},
		lane:      datagramLaneReliableUnordered,
		messageID: 1,
		payload:   []byte("unordered"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket unordered: %v", err)
	}
	rawOrdered, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  2,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  2,
		orderedSeq: 0,
		payload:    []byte("ordered"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ordered: %v", err)
	}
	deliveries, wake, err := sess.handleIncomingDatagram(time.Now(), rawUnordered)
	if err != nil || !wake || len(deliveries) != 1 || deliveries[0].lane != datagramLaneReliableUnordered {
		t.Fatalf("unordered deliveries = %+v wake=%v err=%v", deliveries, wake, err)
	}
	gotUnordered.Add(1)
	deliveries, wake, err = sess.handleIncomingDatagram(time.Now(), rawOrdered)
	if err != nil || !wake || len(deliveries) != 1 || deliveries[0].lane != datagramLaneReliableOrdered {
		t.Fatalf("ordered deliveries = %+v wake=%v err=%v", deliveries, wake, err)
	}
	gotOrdered.Add(1)
	if gotUnordered.Load() != 1 || gotOrdered.Load() != 1 {
		t.Fatalf("gotUnordered=%d gotOrdered=%d", gotUnordered.Load(), gotOrdered.Load())
	}
}

func TestSessionSendEventualStateCopiesPublicPayload(t *testing.T) {
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)

	payload := []byte("before")
	if err := sess.SendEventualState(9, payload); err != nil {
		t.Fatalf("SendEventualState: %v", err)
	}
	copy(payload, "after!")

	budget := newDatagramDrainBudget()
	sent, err := sess.writeOne(context.Background(), time.UnixMilli(0), &budget)
	if err != nil {
		t.Fatalf("writeOne: %v", err)
	}
	if !sent {
		t.Fatal("writeOne returned sent=false")
	}
	if len(datagrams.writes) != 1 {
		t.Fatalf("datagram writes len = %d, want 1", len(datagrams.writes))
	}
	packet, err := decodeDatagramPacket(datagrams.writes[0])
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if packet.lane != datagramLaneEventualState || packet.stateToken != 9 {
		t.Fatalf("packet = %+v, want eventual token 9", packet)
	}
	if !bytes.Equal(packet.payload, []byte("before")) {
		t.Fatalf("eventual payload = %q, want %q", packet.payload, "before")
	}
}

type stubReliableChannel struct{}

func (stubReliableChannel) WriteBatch(context.Context, [][]byte) error       { return nil }
func (stubReliableChannel) ReadMessages(context.Context, func([]byte)) error { return nil }
func (stubReliableChannel) Close() error                                     { return nil }

type stubDatagramChannel struct{}

func (stubDatagramChannel) WriteDatagram(context.Context, []byte) error       { return nil }
func (stubDatagramChannel) ReadDatagrams(context.Context, func([]byte)) error { return nil }

func TestSessionWriteSchedulingPrefersAckThenUnreliableThenReliableThenStream(t *testing.T) {
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)
	now := time.UnixMilli(0)

	sess.protocolMu.Lock()
	sess.protocol.ackDirty = true
	sess.protocol.ackDueAt = now
	sess.protocolMu.Unlock()

	if err := sess.SendUnreliable([]byte("u")); err != nil {
		t.Fatalf("SendUnreliable: %v", err)
	}
	if err := sess.SendReliableOrdered([]byte("o")); err != nil {
		t.Fatalf("SendReliableOrdered: %v", err)
	}
	if err := sess.SendReliableUnordered([]byte("r")); err != nil {
		t.Fatalf("SendReliableUnordered: %v", err)
	}
	sess.SendBatch([][]byte{[]byte("stream")})

	budget := newDatagramDrainBudget()
	for i := 0; i < 4; i++ {
		sent, err := sess.writeOne(context.Background(), now, &budget)
		if err != nil {
			t.Fatalf("writeOne %d: %v", i, err)
		}
		if !sent {
			t.Fatalf("writeOne %d returned sent=false", i)
		}
	}
	if len(datagrams.writes) != 4 {
		t.Fatalf("datagram writes len = %d, want 4", len(datagrams.writes))
	}
	if len(reliable.writes) != 0 {
		t.Fatalf("stream writes unexpectedly happened early: %d", len(reliable.writes))
	}
	packet0, err := decodeDatagramPacket(datagrams.writes[0])
	if err != nil || packet0.flags != datagramFlagAckOnly {
		t.Fatalf("first datagram = %+v err=%v, want ack-only", packet0, err)
	}
	packet1, err := decodeDatagramPacket(datagrams.writes[1])
	if err != nil || packet1.lane != datagramLaneUnreliable || !bytes.Equal(packet1.payload, []byte("u")) {
		t.Fatalf("second datagram = %+v err=%v, want unreliable", packet1, err)
	}
	packet2, err := decodeDatagramPacket(datagrams.writes[2])
	if err != nil || packet2.lane != datagramLaneReliableOrdered || !bytes.Equal(packet2.payload, []byte("o")) {
		t.Fatalf("third datagram = %+v err=%v, want reliable ordered", packet2, err)
	}
	packet3, err := decodeDatagramPacket(datagrams.writes[3])
	if err != nil || packet3.lane != datagramLaneReliableUnordered || !bytes.Equal(packet3.payload, []byte("r")) {
		t.Fatalf("fourth datagram = %+v err=%v, want reliable unordered", packet3, err)
	}

	sent, err := sess.writeOne(context.Background(), now, &budget)
	if err != nil {
		t.Fatalf("writeOne stream: %v", err)
	}
	if !sent || len(reliable.writes) != 1 || !bytes.Equal(reliable.writes[0][0], []byte("stream")) {
		t.Fatalf("stream writes = %+v", reliable.writes)
	}
}

func TestSessionDrainOutboundClearsOrderedBacklogBeyondLegacyBurst(t *testing.T) {
	const legacyBurst = 64

	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)

	want := legacyBurst + 32
	for i := 0; i < want; i++ {
		if err := sess.SendReliableOrdered([]byte{byte(i)}); err != nil {
			t.Fatalf("SendReliableOrdered %d: %v", i, err)
		}
	}

	if err := sess.drainOutbound(context.Background()); err != nil {
		t.Fatalf("drainOutbound: %v", err)
	}

	if got := len(datagrams.writes); got != want {
		t.Fatalf("ordered datagram writes = %d, want %d", got, want)
	}
	if got := len(reliable.writes); got != 0 {
		t.Fatalf("stream writes = %d, want 0", got)
	}
}

func TestSessionDrainOutboundDelaysStreamUntilNextGuardedPass(t *testing.T) {
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)

	for i := 0; i < maxDatagramDrainSendsPerWake; i++ {
		if err := sess.SendReliableOrdered([]byte{byte(i)}); err != nil {
			t.Fatalf("SendReliableOrdered %d: %v", i, err)
		}
	}
	sess.SendBatch([][]byte{[]byte("stream")})

	if err := sess.drainOutbound(context.Background()); err != nil {
		t.Fatalf("first drainOutbound: %v", err)
	}
	if got := len(datagrams.writes); got != maxDatagramDrainSendsPerWake {
		t.Fatalf("first drain ordered writes = %d, want %d", got, maxDatagramDrainSendsPerWake)
	}
	if got := len(reliable.writes); got != 0 {
		t.Fatalf("stream writes after first drain = %d, want 0", got)
	}

	if err := sess.drainOutbound(context.Background()); err != nil {
		t.Fatalf("second drainOutbound: %v", err)
	}
	if got := len(reliable.writes); got != 1 {
		t.Fatalf("stream writes after second drain = %d, want 1", got)
	}
	if !bytes.Equal(reliable.writes[0][0], []byte("stream")) {
		t.Fatalf("stream payload = %q, want %q", reliable.writes[0][0], []byte("stream"))
	}
}

func TestSessionWritePumpResumesGuardedDrainPromptly(t *testing.T) {
	reliable := &notifyReliableChannel{writes: make(chan time.Time, 1)}
	datagrams := &notifyDatagramChannel{writes: make(chan time.Time, maxDatagramDrainSendsPerWake)}
	sess := newSession(1, reliable, datagrams, nil)

	for i := 0; i < maxDatagramDrainSendsPerWake; i++ {
		if err := sess.SendReliableOrdered([]byte{byte(i)}); err != nil {
			t.Fatalf("SendReliableOrdered %d: %v", i, err)
		}
	}
	sess.SendBatch([][]byte{[]byte("stream")})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sess.writePump(ctx)

	var lastDatagramAt time.Time
	for i := 0; i < maxDatagramDrainSendsPerWake; i++ {
		select {
		case lastDatagramAt = <-datagrams.writes:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("timed out waiting for ordered datagram write %d", i+1)
		}
	}

	select {
	case streamAt := <-reliable.writes:
		if gap := streamAt.Sub(lastDatagramAt); gap > 50*time.Millisecond {
			t.Fatalf("stream resumed after %v, want <= 50ms", gap)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for stream write after guarded drain")
	}
}

func TestSessionWriteOnePrefersFreshReliableDatagramsOverRetries(t *testing.T) {
	reliable := &captureReliableChannel{}
	datagrams := &captureDatagramChannel{}
	sess := newSession(1, reliable, datagrams, nil)

	start := time.UnixMilli(0)
	sess.protocolMu.Lock()
	if err := sess.protocol.enqueueReliable(datagramLaneReliableOrdered, []byte("old"), start); err != nil {
		sess.protocolMu.Unlock()
		t.Fatalf("enqueueReliable old: %v", err)
	}
	sess.protocolMu.Unlock()

	budget := newDatagramDrainBudget()
	sent, err := sess.writeOne(context.Background(), start, &budget)
	if err != nil || !sent {
		t.Fatalf("writeOne initial = sent:%v err:%v", sent, err)
	}

	later := start.Add(datagramReliableRetryBaseDelay + time.Millisecond)
	sess.protocolMu.Lock()
	if err := sess.protocol.enqueueReliable(datagramLaneReliableOrdered, []byte("fresh"), later); err != nil {
		sess.protocolMu.Unlock()
		t.Fatalf("enqueueReliable fresh: %v", err)
	}
	sess.protocolMu.Unlock()

	sent, err = sess.writeOne(context.Background(), later, &budget)
	if err != nil || !sent {
		t.Fatalf("writeOne fresh-preferred = sent:%v err:%v", sent, err)
	}
	packet, err := decodeDatagramPacket(datagrams.writes[1])
	if err != nil {
		t.Fatalf("decodeDatagramPacket fresh: %v", err)
	}
	if !bytes.Equal(packet.payload, []byte("fresh")) {
		t.Fatalf("second payload = %q, want %q", packet.payload, []byte("fresh"))
	}

	sent, err = sess.writeOne(context.Background(), later, &budget)
	if err != nil || !sent {
		t.Fatalf("writeOne resend-fallback = sent:%v err:%v", sent, err)
	}
	packet, err = decodeDatagramPacket(datagrams.writes[2])
	if err != nil {
		t.Fatalf("decodeDatagramPacket resend: %v", err)
	}
	if !bytes.Equal(packet.payload, []byte("old")) {
		t.Fatalf("third payload = %q, want %q", packet.payload, []byte("old"))
	}
}

func TestSessionHandleIncomingDatagramAcksDeepOrderedBurstBeyondLegacyWindow(t *testing.T) {
	sess := newSession(1, &captureReliableChannel{}, &captureDatagramChannel{}, nil)
	now := time.UnixMilli(0)

	for i := 0; i < 64; i++ {
		if err := sess.SendReliableOrdered([]byte{byte(i)}); err != nil {
			t.Fatalf("SendReliableOrdered %d: %v", i, err)
		}
	}

	budget := newDatagramDrainBudget()
	lastPacketSeq := uint16(0)
	for i := 0; i < 64; i++ {
		sent, err := sess.writeOne(context.Background(), now, &budget)
		if err != nil {
			t.Fatalf("writeOne %d: %v", i, err)
		}
		if !sent {
			t.Fatalf("writeOne %d returned sent=false", i)
		}
		lastPacketSeq = uint16(i)
	}

	ackPacket, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 90,
		ackSeq:    lastPacketSeq,
		ackMask: func() ackMask128 {
			var mask ackMask128
			for bit := 0; bit < 63; bit++ {
				mask.setBit(bit)
			}
			return mask
		}(),
		flags: datagramFlagAckOnly,
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ack: %v", err)
	}

	deliveries, wake, err := sess.handleIncomingDatagram(now.Add(time.Millisecond), ackPacket)
	if err != nil {
		t.Fatalf("handleIncomingDatagram: %v", err)
	}
	if len(deliveries) != 0 || !wake {
		t.Fatalf("deliveries=%d wake=%v, want 0/true", len(deliveries), wake)
	}

	sess.protocolMu.Lock()
	defer sess.protocolMu.Unlock()
	if got := len(sess.protocol.pendingOrdered); got != 0 {
		t.Fatalf("pendingOrdered len = %d, want 0", got)
	}
}

type captureReliableChannel struct {
	writes [][][]byte
}

func (c *captureReliableChannel) WriteBatch(_ context.Context, frames [][]byte) error {
	cp := make([][]byte, len(frames))
	for i := range frames {
		cp[i] = append([]byte(nil), frames[i]...)
	}
	c.writes = append(c.writes, cp)
	return nil
}

func (c *captureReliableChannel) ReadMessages(context.Context, func([]byte)) error { return nil }
func (c *captureReliableChannel) Close() error                                     { return nil }

type captureDatagramChannel struct {
	writes [][]byte
}

func (c *captureDatagramChannel) WriteDatagram(_ context.Context, data []byte) error {
	c.writes = append(c.writes, append([]byte(nil), data...))
	return nil
}

func (c *captureDatagramChannel) ReadDatagrams(context.Context, func([]byte)) error { return nil }

type notifyReliableChannel struct {
	writes chan time.Time
}

func (c *notifyReliableChannel) WriteBatch(_ context.Context, _ [][]byte) error {
	c.writes <- time.Now()
	return nil
}

func (c *notifyReliableChannel) ReadMessages(context.Context, func([]byte)) error { return nil }
func (c *notifyReliableChannel) Close() error                                     { return nil }

type notifyDatagramChannel struct {
	writes chan time.Time
}

func (c *notifyDatagramChannel) WriteDatagram(_ context.Context, _ []byte) error {
	c.writes <- time.Now()
	return nil
}

func (c *notifyDatagramChannel) ReadDatagrams(context.Context, func([]byte)) error { return nil }

type blockingReadReliableChannel struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingReadReliableChannel() *blockingReadReliableChannel {
	return &blockingReadReliableChannel{closed: make(chan struct{})}
}

func (c *blockingReadReliableChannel) WriteBatch(context.Context, [][]byte) error { return nil }

func (c *blockingReadReliableChannel) ReadMessages(context.Context, func([]byte)) error {
	<-c.closed
	return errors.New("reliable channel closed")
}

func (c *blockingReadReliableChannel) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type failingDatagramChannel struct {
	err error
}

func (c failingDatagramChannel) WriteDatagram(context.Context, []byte) error { return nil }
func (c failingDatagramChannel) ReadDatagrams(context.Context, func([]byte)) error {
	return c.err
}

type scriptedReliableChannel struct {
	messages   [][]byte
	block      chan struct{}
	closeCount atomic.Int32
}

func (c *scriptedReliableChannel) WriteBatch(context.Context, [][]byte) error { return nil }

func (c *scriptedReliableChannel) ReadMessages(_ context.Context, onMsg func([]byte)) error {
	for _, msg := range c.messages {
		if onMsg != nil {
			onMsg(msg)
		}
	}
	if c.block != nil {
		<-c.block
	}
	return nil
}

func (c *scriptedReliableChannel) Close() error {
	c.closeCount.Add(1)
	return nil
}

func assertWakeNow(t *testing.T, name string, sess *Session, now time.Time) {
	t.Helper()
	wake, ok := sess.nextOutboundWake(now)
	if !ok {
		t.Fatalf("%s nextOutboundWake ok = false, want true", name)
	}
	if !wake.Equal(now) {
		t.Fatalf("%s nextOutboundWake = %v, want %v", name, wake, now)
	}
}

func assertWakeSignaled(t *testing.T, name string, sess *Session) {
	t.Helper()
	select {
	case <-sess.wake:
	default:
		t.Fatalf("%s enqueue did not signal wake", name)
	}
}
