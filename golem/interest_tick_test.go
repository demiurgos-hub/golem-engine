package golem

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/webtransport-go"
	golemnet "golem-engine/golem/net"
	"golem-engine/golem/pb"
)

type interestTickEntity struct {
	id         int64
	x, y       float64
	global     bool
	flush      []byte
	compact    []byte
	full       []byte
	revision   uint64
	flushMask  uint64
	onFull     func()
	fullCalls  int
	flushCalls int
}

func (e *interestTickEntity) EntityID() int64              { return e.id }
func (e *interestTickEntity) TypeName() string             { return "interest-test" }
func (e *interestTickEntity) Position() (float32, float32) { return float32(e.x), float32(e.y) }
func (e *interestTickEntity) IsGlobal() bool               { return e.global }
func (e *interestTickEntity) FlushUpdate() ([]byte, error) {
	e.flushCalls++
	if e.flush == nil {
		e.flushMask = 0
		return nil, nil
	}
	e.flushMask = 1
	return e.flush, nil
}
func (e *interestTickEntity) FullUpdate() ([]byte, error) {
	e.fullCalls++
	if e.onFull != nil {
		e.onFull()
	}
	return e.full, nil
}
func (e *interestTickEntity) LastFlushMask() uint64 { return e.flushMask }
func (e *interestTickEntity) MarshalCompactDeltaMask(mask uint64) ([]byte, error) {
	if mask == 0 {
		return nil, nil
	}
	body := e.compact
	if body == nil {
		body = e.flush
	}
	if body == nil {
		return nil, nil
	}
	w := &pb.Writer{}
	w.Int64(e.id)
	w.Uint64(e.revision)
	w.Uint64(mask)
	w.Raw(body)
	return w.Finish(), nil
}

func decodeWrappedEntityBatch(t *testing.T, batch []byte) [][]byte {
	t.Helper()
	rd := pb.NewReader(batch)
	var payloads [][]byte
	for !rd.Done() {
		field, wire := rd.Tag()
		if field != 1 || wire != 2 {
			t.Fatalf("unexpected wrapped field/wire: %d/%d", field, wire)
		}
		payloads = append(payloads, rd.Bytes())
	}
	return payloads
}

func mustDialGameClient(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("Dial(%s): %v", url, err)
	}
	return conn
}

func mustReadWrappedMessages(t *testing.T, conn *websocket.Conn, count int) [][][]byte {
	t.Helper()
	batches := make([][][]byte, 0, count)
	for i := 0; i < count; i++ {
		_, msg, err := conn.Read(context.Background())
		if err != nil {
			t.Fatalf("Read message %d: %v", i+1, err)
		}
		batches = append(batches, decodeWrappedEntityBatch(t, msg))
	}
	return batches
}

func mustReadReliableFrame(t *testing.T, stream interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	var hdr [4]byte
	if _, err := ioReadFull(stream, hdr[:]); err != nil {
		t.Fatalf("read reliable frame header: %v", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	data := make([]byte, int(n))
	if _, err := ioReadFull(stream, data); err != nil {
		t.Fatalf("read reliable frame body: %v", err)
	}
	return data
}

func ioReadFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	var total int
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func freeUDPAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	return addr
}

func dialWebTransportSession(t *testing.T, transportURL string) *webtransport.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dialer := &webtransport.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	var (
		session *webtransport.Session
		err     error
	)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, session, err = dialer.Dial(ctx, transportURL, nil)
		if err == nil {
			return session
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Dial(%s): %v", transportURL, err)
	return nil
}

func waitForSessionIDs(t *testing.T, srv *Server, want int) []int64 {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ids := srv.listener.SessionIDs()
		if len(ids) == want {
			return ids
		}
		time.Sleep(10 * time.Millisecond)
	}
	ids := srv.listener.SessionIDs()
	t.Fatalf("connected sessions = %d, want %d", len(ids), want)
	return nil
}

func TestRunInterestTickCachesWrappedFramesPerTick(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d", entityID)), nil
	})

	anchorA := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchorA-full")}
	anchorB := &interestTickEntity{id: 2, x: 0, y: 0, full: []byte("anchorB-full")}
	shared := &interestTickEntity{id: 3, x: 1, y: 0, full: []byte("shared-full"), flush: []byte("shared-delta")}

	for _, e := range []*interestTickEntity{anchorA, anchorB, shared} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}

	var wrapCalls int
	srv.listener.SetMessageWrapper(func(data []byte) []byte {
		wrapCalls++
		return WrapEntityUpdate(data)
	})

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	client1 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client1.CloseNow()
	client2 := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client2.CloseNow()

	sessionIDs := waitForSessionIDs(t, srv, 2)

	srv.AssignFOI(sessionIDs[0], anchorA.id, 10, 1)
	srv.AssignFOI(sessionIDs[1], anchorB.id, 10, 1)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick tick1: %v", err)
	}
	if got, want := wrapCalls, 3; got != want {
		t.Fatalf("tick1 wrapCalls = %d, want %d", got, want)
	}

	_, msg1, err := client1.Read(context.Background())
	if err != nil {
		t.Fatalf("client1 Read tick1: %v", err)
	}
	_, msg2, err := client2.Read(context.Background())
	if err != nil {
		t.Fatalf("client2 Read tick1: %v", err)
	}
	payloads1 := decodeWrappedEntityBatch(t, msg1)
	payloads2 := decodeWrappedEntityBatch(t, msg2)
	if len(payloads1) != 3 || len(payloads2) != 3 {
		t.Fatalf("unexpected initial payload counts: got %d and %d, want 3 and 3", len(payloads1), len(payloads2))
	}

	shared.fullCalls = 0
	wrapCalls = 0
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick tick2: %v", err)
	}
	if got, want := wrapCalls, 1; got != want {
		t.Fatalf("tick2 wrapCalls = %d, want %d", got, want)
	}
	if shared.fullCalls != 0 {
		t.Fatalf("tick2 shared FullUpdate calls = %d, want 0", shared.fullCalls)
	}

	_, msg1, err = client1.Read(context.Background())
	if err != nil {
		t.Fatalf("client1 Read tick2: %v", err)
	}
	_, msg2, err = client2.Read(context.Background())
	if err != nil {
		t.Fatalf("client2 Read tick2: %v", err)
	}
	payloads1 = decodeWrappedEntityBatch(t, msg1)
	payloads2 = decodeWrappedEntityBatch(t, msg2)
	if len(payloads1) != 1 || len(payloads2) != 1 {
		t.Fatalf("unexpected delta payload counts: got %d and %d, want 1 and 1", len(payloads1), len(payloads2))
	}
	if !bytes.Equal(payloads1[0], shared.flush) || !bytes.Equal(payloads2[0], shared.flush) {
		t.Fatalf("delta payload mismatch: got %q and %q, want %q", payloads1[0], payloads2[0], shared.flush)
	}

	shared.x = 50
	anchorA.flush = nil
	anchorB.flush = nil
	shared.flush = nil
	wrapCalls = 0
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick tick3: %v", err)
	}
	if got, want := wrapCalls, 1; got != want {
		t.Fatalf("tick3 wrapCalls = %d, want %d", got, want)
	}

	_, msg1, err = client1.Read(context.Background())
	if err != nil {
		t.Fatalf("client1 Read tick3: %v", err)
	}
	_, msg2, err = client2.Read(context.Background())
	if err != nil {
		t.Fatalf("client2 Read tick3: %v", err)
	}
	payloads1 = decodeWrappedEntityBatch(t, msg1)
	payloads2 = decodeWrappedEntityBatch(t, msg2)
	if len(payloads1) != 1 || len(payloads2) != 1 {
		t.Fatalf("unexpected removal payload counts: got %d and %d, want 1 and 1", len(payloads1), len(payloads2))
	}
	if !bytes.Equal(payloads1[0], []byte("removed:3")) || !bytes.Equal(payloads2[0], []byte("removed:3")) {
		t.Fatalf("removal payload mismatch: got %q and %q", payloads1[0], payloads2[0])
	}
}

func TestRunInterestTickSplitsLargeBatchAtPayloadCap(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})

	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: bytes.Repeat([]byte("a"), 12000)}
	otherA := &interestTickEntity{id: 2, x: 1, y: 0, full: bytes.Repeat([]byte("b"), 12000)}
	otherB := &interestTickEntity{id: 3, x: 2, y: 0, full: bytes.Repeat([]byte("c"), 12000)}
	for _, e := range []*interestTickEntity{anchor, otherA, otherB} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()

	sessionIDs := waitForSessionIDs(t, srv, 1)
	srv.AssignFOI(sessionIDs[0], anchor.id, 10, 1)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick: %v", err)
	}

	batches := mustReadWrappedMessages(t, client, 2)
	if got, want := len(batches[0]), 2; got != want {
		t.Fatalf("first websocket message payload count = %d, want %d", got, want)
	}
	if got, want := len(batches[1]), 1; got != want {
		t.Fatalf("second websocket message payload count = %d, want %d", got, want)
	}
}

func TestRunInterestTickOversizeWrappedFrameFails(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})

	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-full")}
	oversized := &interestTickEntity{id: 2, x: 1, y: 0, full: bytes.Repeat([]byte("x"), 33000)}
	for _, e := range []*interestTickEntity{anchor, oversized} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	defer client.CloseNow()

	sessionIDs := waitForSessionIDs(t, srv, 1)
	srv.AssignFOI(sessionIDs[0], anchor.id, 10, 1)

	if err := srv.runInterestTick(); err == nil {
		t.Fatal("runInterestTick returned nil for oversized wrapped frame")
	}
}

func TestRunInterestTickCachesDatagramFullUpdatesPerTick(t *testing.T) {
	addr := freeUDPAddr(t)
	srv := NewServer(ServerConfig{
		Addr:              addr,
		Transport:         golemnet.TransportWebTransport,
		DevSelfSignedCert: true,
		CellSize:          4,
		StateUpdateLane:   StateUpdateLaneDatagram,
	})

	anchorA := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-a-full")}
	anchorB := &interestTickEntity{id: 2, x: 0, y: 0, full: []byte("anchor-b-full")}
	shared := &interestTickEntity{id: 3, x: 1, y: 0, full: []byte("shared-full"), flush: []byte("shared-delta")}
	for _, e := range []*interestTickEntity{anchorA, anchorB, shared} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.listener.ListenAndServe(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("ListenAndServe: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for listener shutdown")
		}
	}()

	wtSessionA := dialWebTransportSession(t, "https://"+addr+"/wt")
	defer wtSessionA.CloseWithError(0, "")
	streamA, err := wtSessionA.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync A: %v", err)
	}
	if _, err := streamA.Write(nil); err != nil {
		t.Fatalf("prime reliable stream A: %v", err)
	}

	wtSessionB := dialWebTransportSession(t, "https://"+addr+"/wt")
	defer wtSessionB.CloseWithError(0, "")
	streamB, err := wtSessionB.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync B: %v", err)
	}
	if _, err := streamB.Write(nil); err != nil {
		t.Fatalf("prime reliable stream B: %v", err)
	}

	sessionIDs := waitForSessionIDs(t, srv, 2)
	srv.AssignFOI(sessionIDs[0], anchorA.id, 10, 1)
	srv.AssignFOI(sessionIDs[1], anchorB.id, 10, 1)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick initial: %v", err)
	}

	shared.fullCalls = 0
	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick eventual: %v", err)
	}
	if got, want := shared.fullCalls, 1; got != want {
		t.Fatalf("shared FullUpdate calls = %d, want %d", got, want)
	}
}

func TestRunInterestTickDatagramOversizedFullUpdateFallsBackToStream(t *testing.T) {
	addr := freeUDPAddr(t)
	srv := NewServer(ServerConfig{
		Addr:              addr,
		Transport:         golemnet.TransportWebTransport,
		DevSelfSignedCert: true,
		CellSize:          4,
		StateUpdateLane:   StateUpdateLaneDatagram,
	})

	largeFull := bytes.Repeat([]byte("x"), golemnet.EventualStateDatagramPayloadBudget()+100)
	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-full")}
	shared := &interestTickEntity{id: 2, x: 1, y: 0, full: largeFull, flush: []byte("shared-delta")}
	for _, e := range []*interestTickEntity{anchor, shared} {
		if err := srv.CreateEntity(e); err != nil {
			t.Fatalf("CreateEntity(%d): %v", e.id, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.listener.ListenAndServe(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("ListenAndServe: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for listener shutdown")
		}
	}()

	wtSession := dialWebTransportSession(t, "https://"+addr+"/wt")
	defer wtSession.CloseWithError(0, "")
	stream, err := wtSession.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	sessionIDs := waitForSessionIDs(t, srv, 1)
	srv.AssignFOI(sessionIDs[0], anchor.id, 10, 1)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick initial: %v", err)
	}
	_ = mustReadReliableFrame(t, stream)
	_ = mustReadReliableFrame(t, stream)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick fallback: %v", err)
	}
	fallback := mustReadReliableFrame(t, stream)
	payloads := decodeWrappedEntityBatch(t, fallback)
	if got, want := len(payloads), 1; got != want {
		t.Fatalf("fallback payload count = %d, want %d", got, want)
	}
	if !bytes.Equal(payloads[0], largeFull) {
		t.Fatalf("fallback payload len = %d, want %d", len(payloads[0]), len(largeFull))
	}
	if tracker := srv.eventualTrackers[sessionIDs[0]]; tracker == nil {
		t.Fatal("eventual tracker was not created")
	} else if got := tracker.dirtyIDs(); len(got) != 0 {
		t.Fatalf("eventual dirty IDs after fallback = %v, want empty", got)
	}
}

func TestRunInterestTickDatagramOversizedDeltaFallsBackToStream(t *testing.T) {
	addr := freeUDPAddr(t)
	srv := NewServer(ServerConfig{
		Addr:              addr,
		Transport:         golemnet.TransportWebTransport,
		DevSelfSignedCert: true,
		CellSize:          4,
		StateUpdateLane:   StateUpdateLaneDatagram,
	})

	largeDelta := bytes.Repeat([]byte("d"), golemnet.EventualStateDatagramPayloadBudget()+100)
	entity := &maskAwareEventualEntity{
		id:            1,
		lastFlushMask: 0b001,
		delta:         largeDelta,
		full:          []byte("mask-aware-full"),
	}
	if err := srv.CreateEntity(entity); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.listener.ListenAndServe(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Fatalf("ListenAndServe: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for listener shutdown")
		}
	}()

	wtSession := dialWebTransportSession(t, "https://"+addr+"/wt")
	defer wtSession.CloseWithError(0, "")
	stream, err := wtSession.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync: %v", err)
	}
	if _, err := stream.Write(nil); err != nil {
		t.Fatalf("prime reliable stream: %v", err)
	}

	sessionIDs := waitForSessionIDs(t, srv, 1)
	srv.AssignFOI(sessionIDs[0], entity.id, 10, 1)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick initial: %v", err)
	}
	_ = mustReadReliableFrame(t, stream)

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick fallback: %v", err)
	}
	fallback := mustReadReliableFrame(t, stream)
	payloads := decodeWrappedEntityBatch(t, fallback)
	if got, want := len(payloads), 1; got != want {
		t.Fatalf("fallback payload count = %d, want %d", got, want)
	}
	if !bytes.Equal(payloads[0], largeDelta) {
		t.Fatalf("fallback payload len = %d, want %d", len(payloads[0]), len(largeDelta))
	}
	if entity.fullCalls != 1 {
		t.Fatalf("full calls = %d, want 1", entity.fullCalls)
	}
	if entity.deltaCalls != 1 {
		t.Fatalf("delta mask calls = %d, want 1", entity.deltaCalls)
	}
	if tracker := srv.eventualTrackers[sessionIDs[0]]; tracker == nil {
		t.Fatal("eventual tracker was not created")
	} else if got := tracker.dirtyIDs(); len(got) != 0 {
		t.Fatalf("eventual dirty IDs after fallback = %v, want empty", got)
	}
}

func TestRunInterestTickIgnoresDisconnectedSessionDuringTargetedSend(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
		CellSize:        4,
	})

	anchor := &interestTickEntity{id: 1, x: 0, y: 0, full: []byte("anchor-full")}
	if err := srv.CreateEntity(anchor); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	client := mustDialGameClient(t, "ws"+httpSrv.URL[4:])
	sessionIDs := waitForSessionIDs(t, srv, 1)
	srv.AssignFOI(sessionIDs[0], anchor.id, 10, 1)

	anchor.onFull = func() {
		client.CloseNow()
		waitForSessionIDs(t, srv, 0)
	}

	if err := srv.runInterestTick(); err != nil {
		t.Fatalf("runInterestTick: %v", err)
	}
}

func TestSendDatagramStateIgnoresDisconnectedSession(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	entity := &interestTickEntity{id: 1, full: []byte("eventual-full")}
	if err := srv.CreateEntity(entity); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{entity.id})

	batched, msgs, err := srv.sendEventualState(999, tracker, nil)
	if err != nil {
		t.Fatalf("sendEventualState: %v", err)
	}
	if batched != 0 || msgs != 0 {
		t.Fatalf("sendEventualState counts = %d/%d, want 0/0", batched, msgs)
	}
	if got := tracker.dirtyIDs(); len(got) != 1 || got[0] != entity.id {
		t.Fatalf("dirty IDs = %v, want [%d]", got, entity.id)
	}
	if len(tracker.inFlight) != 0 {
		t.Fatalf("in-flight tokens = %d, want 0", len(tracker.inFlight))
	}
}
