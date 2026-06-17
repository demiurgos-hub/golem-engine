package net

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golem-engine/golem/registry"
)

func decodeReliableBatchPayload(t *testing.T, payload []byte) [][]byte {
	t.Helper()
	rd := bytes.NewReader(payload)
	var frames [][]byte
	for rd.Len() > 0 {
		frame, err := readReliableFrame(rd)
		if err != nil {
			t.Fatalf("readReliableFrame: %v", err)
		}
		frames = append(frames, frame)
	}
	return frames
}

// webTransportTestEntity is a minimal registry entity used by WT listener tests.
type webTransportTestEntity struct {
	id    int64
	state []byte
}

func (e *webTransportTestEntity) EntityID() int64              { return e.id }
func (e *webTransportTestEntity) TypeName() string             { return "TestEntity" }
func (e *webTransportTestEntity) Position() (float32, float32) { return 0, 0 }
func (e *webTransportTestEntity) IsGlobal() bool               { return false }
func (e *webTransportTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *webTransportTestEntity) FullUpdate() ([]byte, error)  { return e.state, nil }

// startTestWebTransportListener starts a WT listener on an ephemeral UDP port.
func startTestWebTransportListener(t *testing.T, listener *Listener) string {
	t.Helper()

	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}

	h3 := &http3.Server{Handler: listener.newServeMux()}
	server := listener.WebTransportServer(h3)

	certificate, _, cleanupCert, err := prepareWebTransportTLS(Config{
		Addr:              packetConn.LocalAddr().String(),
		DevSelfSignedCert: true,
	})
	if err != nil {
		_ = packetConn.Close()
		t.Fatalf("prepareWebTransportTLS: %v", err)
	}
	h3.TLSConfig = newWebTransportServerTLSConfig(certificate)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(packetConn)
	}()

	t.Cleanup(func() {
		cleanupCert()
		_ = server.Close()
		_ = packetConn.Close()

		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("Serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for WebTransport test server shutdown")
		}
	})

	return "https://" + packetConn.LocalAddr().String() + listener.config.Path
}

func TestListenerWebTransportGoClientMustPrimeOpenedStream(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})
	listener.SetWorldSnapshotFunc(func() ([][]byte, error) {
		return [][]byte{[]byte("world-snapshot")}, nil
	})

	transportURL := startTestWebTransportListener(t, listener)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test-only local self-signed certificate.
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	if _, err := readReliableFrame(stream); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read without priming error = %v, want deadline exceeded", err)
	}
}

func TestListenerWebTransportPrimedClientReceivesSnapshotBeforeOnConnectSend(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Add(&webTransportTestEntity{id: 7, state: []byte("entity-snapshot")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	listener := NewListener(reg, Config{Transport: TransportWebTransport})
	listener.SetWorldSnapshotFunc(func() ([][]byte, error) {
		return [][]byte{[]byte("world-snapshot")}, nil
	})

	onConnectErr := make(chan error, 1)
	listener.OnConnect(func(sess *Session) {
		onConnectErr <- listener.Send(sess.ID, []byte("on-connect"))
	})

	transportURL := startTestWebTransportListener(t, listener)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test-only local self-signed certificate.
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	gotWorld, err := readReliableFrame(stream)
	if err != nil {
		t.Fatalf("read world snapshot: %v", err)
	}
	gotEntity, err := readReliableFrame(stream)
	if err != nil {
		t.Fatalf("read entity snapshot: %v", err)
	}
	gotOnConnect, err := readReliableFrame(stream)
	if err != nil {
		t.Fatalf("read on-connect frame: %v", err)
	}

	if !bytes.Equal(gotWorld, []byte("world-snapshot")) {
		t.Fatalf("world snapshot = %q, want %q", gotWorld, []byte("world-snapshot"))
	}
	if !bytes.Equal(gotEntity, []byte("entity-snapshot")) {
		t.Fatalf("entity snapshot = %q, want %q", gotEntity, []byte("entity-snapshot"))
	}
	if !bytes.Equal(gotOnConnect, []byte("on-connect")) {
		t.Fatalf("on-connect frame = %q, want %q", gotOnConnect, []byte("on-connect"))
	}

	select {
	case err := <-onConnectErr:
		if err != nil {
			t.Fatalf("OnConnect send: %v", err)
		}
	default:
		t.Fatal("OnConnect was not called")
	}
}

func TestListenerWebTransportSendBatchDeliversFramesInOrder(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})

	onConnectErr := make(chan error, 1)
	listener.OnConnect(func(sess *Session) {
		onConnectErr <- listener.SendBatch(sess.ID, [][]byte{
			[]byte("frame-one"),
			[]byte("frame-two"),
			[]byte("frame-three"),
		})
	})

	transportURL := startTestWebTransportListener(t, listener)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test-only local self-signed certificate.
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer stream.SetReadDeadline(time.Time{})

	for i, want := range [][]byte{
		[]byte("frame-one"),
		[]byte("frame-two"),
		[]byte("frame-three"),
	} {
		got, err := readReliableFrame(stream)
		if err != nil {
			t.Fatalf("readReliableFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}

	select {
	case err := <-onConnectErr:
		if err != nil {
			t.Fatalf("OnConnect SendBatch: %v", err)
		}
	default:
		t.Fatal("OnConnect was not called")
	}
}

func TestListenerWebTransportSendReliableOrderedBatchDeliversFramedPayloads(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})

	onConnectErr := make(chan error, 1)
	listener.OnConnect(func(sess *Session) {
		onConnectErr <- listener.SendReliableOrderedBatch(sess.ID, [][]byte{
			[]byte("frame-one"),
			[]byte("frame-two"),
			[]byte("frame-three"),
		})
	})

	transportURL := startTestWebTransportListener(t, listener)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	rawDatagram, err := wtSession.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatalf("ReceiveDatagram: %v", err)
	}
	packet, err := decodeDatagramPacket(rawDatagram)
	if err != nil {
		t.Fatalf("decodeDatagramPacket: %v", err)
	}
	if packet.lane != datagramLaneReliableOrdered {
		t.Fatalf("packet lane = %d, want %d", packet.lane, datagramLaneReliableOrdered)
	}
	frames := decodeReliableBatchPayload(t, packet.payload)
	if got, want := len(frames), 3; got != want {
		t.Fatalf("frames len = %d, want %d", got, want)
	}
	for i, want := range [][]byte{
		[]byte("frame-one"),
		[]byte("frame-two"),
		[]byte("frame-three"),
	} {
		if !bytes.Equal(frames[i], want) {
			t.Fatalf("frame %d = %q, want %q", i, frames[i], want)
		}
	}

	select {
	case err := <-onConnectErr:
		if err != nil {
			t.Fatalf("OnConnect SendReliableOrderedBatch: %v", err)
		}
	default:
		t.Fatal("OnConnect was not called")
	}
}

func TestListenerWebTransportReliableDatagramCallbacksPreserveOrder(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})

	var sessionID int64
	orderedSeen := make(chan []byte, 4)
	unorderedSeen := make(chan []byte, 4)
	listener.OnConnect(func(sess *Session) {
		sessionID = sess.ID
	})
	listener.OnReliableOrdered(func(_ *Session, data []byte) {
		orderedSeen <- append([]byte(nil), data...)
	})
	listener.OnReliableUnordered(func(_ *Session, data []byte) {
		unorderedSeen <- append([]byte(nil), data...)
	})

	transportURL := startTestWebTransportListener(t, listener)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for sessionID == 0 {
		select {
		case <-deadline:
			t.Fatal("session not connected")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	unorderedPacket, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 1,
		ackSeq:    0,
		ackMask:   ackMask128{},
		lane:      datagramLaneReliableUnordered,
		messageID: 1,
		payload:   []byte("u1"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket unordered: %v", err)
	}
	orderedSecond, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  2,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  3,
		orderedSeq: 1,
		payload:    []byte("o2"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ordered second: %v", err)
	}
	orderedFirst, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  3,
		ackSeq:     0,
		ackMask:    ackMask128{},
		lane:       datagramLaneReliableOrdered,
		messageID:  2,
		orderedSeq: 0,
		payload:    []byte("o1"),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ordered first: %v", err)
	}

	if err := wtSession.SendDatagram(unorderedPacket); err != nil {
		t.Fatalf("SendDatagram unordered: %v", err)
	}
	if err := wtSession.SendDatagram(orderedSecond); err != nil {
		t.Fatalf("SendDatagram ordered second: %v", err)
	}
	if err := wtSession.SendDatagram(orderedFirst); err != nil {
		t.Fatalf("SendDatagram ordered first: %v", err)
	}

	select {
	case got := <-unorderedSeen:
		if !bytes.Equal(got, []byte("u1")) {
			t.Fatalf("unordered callback = %q, want %q", got, []byte("u1"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for unordered callback")
	}

	for i, want := range [][]byte{[]byte("o1"), []byte("o2")} {
		select {
		case got := <-orderedSeen:
			if !bytes.Equal(got, want) {
				t.Fatalf("ordered callback %d = %q, want %q", i, got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for ordered callback %d", i)
		}
	}
}

func TestListenerWebTransportExpandedAckWindowStopsDeepOrderedResends(t *testing.T) {
	listener := NewListener(registry.NewRegistry(), Config{Transport: TransportWebTransport})

	onConnectErr := make(chan error, 1)
	listener.OnConnect(func(sess *Session) {
		for i := 0; i < 64; i++ {
			if err := listener.SendReliableOrdered(sess.ID, []byte{byte(i)}); err != nil {
				onConnectErr <- err
				return
			}
		}
		onConnectErr <- nil
	})

	transportURL := startTestWebTransportListener(t, listener)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	_, wtSession, err := dialer.Dial(ctx, transportURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wtSession.CloseWithError(0, "")

	stream, err := wtSession.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	var lastPacketSeq uint16
	for i := 0; i < 64; i++ {
		rawDatagram, err := wtSession.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatalf("ReceiveDatagram %d: %v", i, err)
		}
		packet, err := decodeDatagramPacket(rawDatagram)
		if err != nil {
			t.Fatalf("decodeDatagramPacket %d: %v", i, err)
		}
		if packet.flags&datagramFlagAckOnly != 0 {
			i--
			continue
		}
		if packet.lane != datagramLaneReliableOrdered {
			t.Fatalf("packet %d lane = %d, want %d", i, packet.lane, datagramLaneReliableOrdered)
		}
		lastPacketSeq = packet.packetSeq
	}

	select {
	case err := <-onConnectErr:
		if err != nil {
			t.Fatalf("OnConnect SendReliableOrdered: %v", err)
		}
	default:
		t.Fatal("OnConnect was not called")
	}

	var ackMask ackMask128
	for bit := 0; bit < 63; bit++ {
		ackMask.setBit(bit)
	}
	ackPacket, err := encodeDatagramPacket(datagramPacket{
		packetSeq: 1,
		ackSeq:    lastPacketSeq,
		ackMask:   ackMask,
		flags:     datagramFlagAckOnly,
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket ack: %v", err)
	}
	if err := wtSession.SendDatagram(ackPacket); err != nil {
		t.Fatalf("SendDatagram ack: %v", err)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer dcancel()
	for {
		rawDatagram, err := wtSession.ReceiveDatagram(dctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return
			}
			t.Fatalf("ReceiveDatagram after ack: %v", err)
		}
		packet, err := decodeDatagramPacket(rawDatagram)
		if err != nil {
			t.Fatalf("decodeDatagramPacket after ack: %v", err)
		}
		if packet.flags&datagramFlagAckOnly != 0 {
			continue
		}
		t.Fatalf("unexpected datagram after deep ack: lane=%d packetSeq=%d", packet.lane, packet.packetSeq)
	}
}
