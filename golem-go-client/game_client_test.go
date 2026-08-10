package golemclient

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem-go-client/pb"
)

type recordingEntities struct {
	mu        sync.Mutex
	updates   int
	compact   int
	lastFrame []byte
	changed   chan struct{}
}

func newRecordingEntities() *recordingEntities {
	return &recordingEntities{changed: make(chan struct{}, 16)}
}

func (r *recordingEntities) ApplyUpdate(any) {
	r.mu.Lock()
	r.updates++
	r.mu.Unlock()
	r.notify()
}
func (r *recordingEntities) ApplyCompactUpdate(frame []byte) {
	r.mu.Lock()
	r.compact++
	r.lastFrame = append([]byte(nil), frame...)
	r.mu.Unlock()
	r.notify()
}
func (r *recordingEntities) Get(int64) any { return nil }

func (r *recordingEntities) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *recordingEntities) snapshot() (int, int, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates, r.compact, append([]byte(nil), r.lastFrame...)
}

type fakeChannel struct {
	callbacks             channelCallbacks
	sent                  [][]byte
	reliableUnorderedSent [][]byte
	reliableOrderedSent   [][]byte
	connected             bool
	maxMessageBytes       int
	maxDatagramBytes      int
	sendErr               error
	reliableUnorderedErr  error
	reliableOrderedErr    error
}

func (f *fakeChannel) Connected() bool { return f.connected }
func (f *fakeChannel) MaxMessageBytes() int {
	if f.maxMessageBytes > 0 {
		return f.maxMessageBytes
	}
	return maxReliableMessageBytes
}
func (f *fakeChannel) MaxDatagramBytes() int { return f.maxDatagramBytes }
func (f *fakeChannel) Send(data []byte) error {
	f.sent = append(f.sent, append([]byte(nil), data...))
	return f.sendErr
}
func (f *fakeChannel) SendUnreliable(data []byte) error { return f.Send(data) }
func (f *fakeChannel) SendReliableUnordered(data []byte) error {
	f.reliableUnorderedSent = append(f.reliableUnorderedSent, append([]byte(nil), data...))
	return f.reliableUnorderedErr
}
func (f *fakeChannel) SendReliableOrdered(data []byte) error {
	f.reliableOrderedSent = append(f.reliableOrderedSent, append([]byte(nil), data...))
	return f.reliableOrderedErr
}
func (f *fakeChannel) Close() error { f.connected = false; return nil }
func (f *fakeChannel) OnOpen(fn func()) {
	f.callbacks.onOpen = fn
	if fn != nil {
		fn()
	}
}
func (f *fakeChannel) OnMessage(fn func([]byte)) { f.callbacks.onMessage = fn }
func (f *fakeChannel) OnUnreliableStateMessage(fn func([]byte)) {
	f.callbacks.onUnreliableStateMessage = fn
}
func (f *fakeChannel) OnReliableOrderedMessage(fn func([]byte)) {
	f.callbacks.onReliableOrderedMessage = fn
}
func (f *fakeChannel) OnEventualStateMessage(fn func([]byte)) {
	f.callbacks.onEventualStateMessage = fn
}
func (f *fakeChannel) OnClose(fn func(DisconnectInfo)) { f.callbacks.onClose = fn }

func TestGameClientSendUsesWebTransportCommandLanes(t *testing.T) {
	channel := &fakeChannel{
		connected:        true,
		maxDatagramBytes: maxWebTransportDatagramBytes,
	}
	client := NewGameClient(GameClientOptions{
		EncodeCommand: func(command any) ([]byte, error) {
			return []byte(command.(string)), nil
		},
	})
	client.channel = channel

	if err := client.Send("unordered"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := client.SendOrdered("ordered"); err != nil {
		t.Fatalf("SendOrdered: %v", err)
	}

	if len(channel.sent) != 0 {
		t.Fatalf("stream sends = %d, want 0", len(channel.sent))
	}
	if len(channel.reliableUnorderedSent) != 1 || !bytes.Equal(channel.reliableUnorderedSent[0], []byte("unordered")) {
		t.Fatalf("reliable unordered sends = %q, want bare command frame", channel.reliableUnorderedSent)
	}
	if len(channel.reliableOrderedSent) != 1 || !bytes.Equal(channel.reliableOrderedSent[0], []byte("ordered")) {
		t.Fatalf("reliable ordered sends = %q, want bare command frame", channel.reliableOrderedSent)
	}
}

func TestGameClientSendUsesWebSocketStreamFallback(t *testing.T) {
	channel := &fakeChannel{connected: true}
	var packetCalls int
	client := NewGameClient(GameClientOptions{
		EncodeCommand: func(command any) ([]byte, error) {
			return []byte(command.(string)), nil
		},
		EncodePacket: func(frames [][]byte) ([]byte, error) {
			packetCalls++
			if len(frames) != 1 {
				t.Fatalf("packet frames = %d, want 1", len(frames))
			}
			return append([]byte("packet:"), frames[0]...), nil
		},
	})
	client.channel = channel

	if err := client.Send("unordered"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := client.SendOrdered("ordered"); err != nil {
		t.Fatalf("SendOrdered: %v", err)
	}

	if packetCalls != 2 {
		t.Fatalf("EncodePacket calls = %d, want 2", packetCalls)
	}
	want := [][]byte{[]byte("packet:unordered"), []byte("packet:ordered")}
	if !reflect.DeepEqual(channel.sent, want) {
		t.Fatalf("stream sends = %q, want %q", channel.sent, want)
	}
	if len(channel.reliableUnorderedSent) != 0 || len(channel.reliableOrderedSent) != 0 {
		t.Fatalf("datagram sends = unordered %d ordered %d, want 0", len(channel.reliableUnorderedSent), len(channel.reliableOrderedSent))
	}
}

func TestGameClientSendIsNoOpWhileDisconnected(t *testing.T) {
	channel := &fakeChannel{maxDatagramBytes: maxWebTransportDatagramBytes}
	client := NewGameClient(GameClientOptions{})
	client.channel = channel

	if err := client.Send("ignored"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := client.SendOrdered("ignored"); err != nil {
		t.Fatalf("SendOrdered: %v", err)
	}
	if len(channel.sent)+len(channel.reliableUnorderedSent)+len(channel.reliableOrderedSent) != 0 {
		t.Fatal("disconnected command was sent")
	}
}

func TestGameClientSendPropagatesSelectedLaneErrors(t *testing.T) {
	t.Run("command encoder", func(t *testing.T) {
		wantErr := errors.New("encode failed")
		channel := &fakeChannel{connected: true, maxDatagramBytes: maxWebTransportDatagramBytes}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return nil, wantErr },
		})
		client.channel = channel

		if err := client.Send("command"); !errors.Is(err, wantErr) {
			t.Fatalf("Send error = %v, want %v", err, wantErr)
		}
	})

	t.Run("packet encoder", func(t *testing.T) {
		wantErr := errors.New("packet failed")
		channel := &fakeChannel{connected: true}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("command"), nil },
			EncodePacket:  func([][]byte) ([]byte, error) { return nil, wantErr },
		})
		client.channel = channel

		if err := client.SendOrdered("command"); !errors.Is(err, wantErr) {
			t.Fatalf("SendOrdered error = %v, want %v", err, wantErr)
		}
	})

	t.Run("reliable stream channel", func(t *testing.T) {
		wantErr := errors.New("stream failed")
		channel := &fakeChannel{connected: true, sendErr: wantErr}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("command"), nil },
			EncodePacket:  func([][]byte) ([]byte, error) { return []byte("packet"), nil },
		})
		client.channel = channel

		if err := client.Send("command"); !errors.Is(err, wantErr) {
			t.Fatalf("Send error = %v, want %v", err, wantErr)
		}
	})

	t.Run("credential-bearing stream channel", func(t *testing.T) {
		const secret = "realtime-ticket-secret"
		wantErr := errors.New("stream failed for wss://example.test/ws?ticket=" + secret)
		channel := &fakeChannel{connected: true, sendErr: wantErr}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("command"), nil },
			EncodePacket:  func([][]byte) ([]byte, error) { return []byte("packet"), nil },
		})
		client.channel = channel

		err := client.Send("command")
		if !errors.Is(err, wantErr) {
			t.Fatalf("Send error = %v, want errors.Is channel error", err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "ticket=") {
			t.Fatalf("Send error leaked realtime credential: %q", err)
		}
	})

	t.Run("reliable unordered channel", func(t *testing.T) {
		wantErr := errors.New("unordered failed")
		channel := &fakeChannel{
			connected:            true,
			maxDatagramBytes:     maxWebTransportDatagramBytes,
			reliableUnorderedErr: wantErr,
		}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("command"), nil },
		})
		client.channel = channel

		if err := client.Send("command"); !errors.Is(err, wantErr) {
			t.Fatalf("Send error = %v, want %v", err, wantErr)
		}
		if len(channel.sent) != 0 {
			t.Fatalf("stream sends = %d, want no fallback", len(channel.sent))
		}
	})

	t.Run("reliable ordered channel", func(t *testing.T) {
		wantErr := errors.New("ordered failed")
		channel := &fakeChannel{
			connected:          true,
			maxDatagramBytes:   maxWebTransportDatagramBytes,
			reliableOrderedErr: wantErr,
		}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("command"), nil },
		})
		client.channel = channel

		if err := client.SendOrdered("command"); !errors.Is(err, wantErr) {
			t.Fatalf("SendOrdered error = %v, want %v", err, wantErr)
		}
		if len(channel.sent) != 0 {
			t.Fatalf("stream sends = %d, want no fallback", len(channel.sent))
		}
	})
}

func TestGameClientSendEnforcesSelectedLaneSizes(t *testing.T) {
	t.Run("reliable unordered datagram", func(t *testing.T) {
		const payloadBytes = 3
		channel := &fakeChannel{
			connected: true,
			maxDatagramBytes: datagramPacketHeaderBytes +
				datagramLaneHeaderBytes +
				datagramReliableMessageIDBytes +
				payloadBytes,
		}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("wide"), nil },
		})
		client.channel = channel

		err := client.Send("command")
		if err == nil || !strings.Contains(err.Error(), "reliable unordered command size 4 exceeds max payload 3") {
			t.Fatalf("Send error = %v, want reliable unordered payload size error", err)
		}
		if len(channel.sent)+len(channel.reliableUnorderedSent) != 0 {
			t.Fatal("oversized WebTransport command used a transport lane")
		}
	})

	t.Run("reliable ordered datagram", func(t *testing.T) {
		const payloadBytes = 3
		channel := &fakeChannel{
			connected: true,
			maxDatagramBytes: datagramPacketHeaderBytes +
				datagramLaneHeaderBytes +
				datagramReliableMessageIDBytes +
				datagramReliableOrderedSequenceBytes +
				payloadBytes,
		}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("wide"), nil },
		})
		client.channel = channel

		err := client.SendOrdered("command")
		if err == nil || !strings.Contains(err.Error(), "reliable ordered command size 4 exceeds max payload 3") {
			t.Fatalf("SendOrdered error = %v, want reliable ordered payload size error", err)
		}
		if len(channel.sent)+len(channel.reliableOrderedSent) != 0 {
			t.Fatal("oversized WebTransport command used a transport lane")
		}
	})

	t.Run("WebSocket stream", func(t *testing.T) {
		channel := &fakeChannel{connected: true, maxMessageBytes: 3}
		client := NewGameClient(GameClientOptions{
			EncodeCommand: func(any) ([]byte, error) { return []byte("x"), nil },
			EncodePacket:  func([][]byte) ([]byte, error) { return []byte("wide"), nil },
		})
		client.channel = channel

		err := client.SendOrdered("command")
		if err == nil || !strings.Contains(err.Error(), "exceeds max reliable message 3") {
			t.Fatalf("SendOrdered error = %v, want stream size error", err)
		}
		if len(channel.sent) != 0 {
			t.Fatal("oversized WebSocket packet was sent")
		}
	})
}

func TestGameClientCommandAPIExcludesLegacyMethods(t *testing.T) {
	clientType := reflect.TypeOf((*GameClient)(nil))
	for _, name := range []string{"Send", "SendOrdered"} {
		if _, ok := clientType.MethodByName(name); !ok {
			t.Errorf("GameClient.%s is missing", name)
		}
	}
	for _, name := range []string{"SendUnreliable", "SendReliableUnordered", "SendReliableOrdered"} {
		if _, ok := clientType.MethodByName(name); ok {
			t.Errorf("legacy GameClient.%s is still public", name)
		}
	}
}

func TestGameClientConnectSanitizesCredentialBearingURLErrors(t *testing.T) {
	const secret = "realtime-ticket-secret"
	endpoint := "wss://example.test/ws?ticket=" + secret + "#fragment"
	wantErr := errors.New("dial failed")
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(_ context.Context, options ConnectOptions) (ReliableMessageChannel, error) {
			return nil, &url.Error{Op: "dial", URL: options.URL, Err: wantErr}
		},
	})

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: endpoint})
	if err == nil {
		t.Fatal("Connect returned nil error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Connect error = %v, want errors.Is dial failure", err)
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("Connect error lost url.Error: %v", err)
	}
	for label, text := range map[string]string{
		"returned error": err.Error(),
		"url.Error URL":  uerr.URL,
		"log output":     logs.String(),
	} {
		if strings.Contains(text, secret) || strings.Contains(text, "ticket=") {
			t.Fatalf("%s leaked realtime credential: %q", label, text)
		}
	}
}

func TestGameClientConnectSanitizesGenericCredentialErrors(t *testing.T) {
	const secret = "realtime-ticket-secret"
	endpoint := "wss://example.test/ws?ticket=" + secret
	rawErr := errors.New("dial failed for " + endpoint)
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return nil, rawErr
		},
	})

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: endpoint})
	if err == nil {
		t.Fatal("Connect returned nil error")
	}
	if !errors.Is(err, rawErr) {
		t.Fatalf("Connect error = %v, want errors.Is raw transport error", err)
	}
	for label, text := range map[string]string{
		"returned error": err.Error(),
		"log output":     logs.String(),
	} {
		if strings.Contains(text, secret) || strings.Contains(text, "ticket=") {
			t.Fatalf("%s leaked realtime credential: %q", label, text)
		}
	}
}

func TestGameClientDisconnectSanitizesGenericCredentialErrors(t *testing.T) {
	const secret = "realtime-ticket-secret"
	endpoint := "wss://example.test/ws?ticket=" + secret
	rawErr := errors.New("read failed for " + endpoint)
	channel := &fakeChannel{connected: true}
	client := NewGameClient(GameClientOptions{
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	var got DisconnectInfo
	client.OnDisconnect(func(info DisconnectInfo) { got = info })

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: endpoint}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	channel.callbacks.onClose(DisconnectInfo{Err: rawErr})

	if got.Err == nil {
		t.Fatal("OnDisconnect error is nil")
	}
	if !errors.Is(got.Err, rawErr) {
		t.Fatalf("OnDisconnect error = %v, want errors.Is raw transport error", got.Err)
	}
	for label, text := range map[string]string{
		"disconnect error": got.Err.Error(),
		"last error":       client.lastErr.Error(),
		"log output":       logs.String(),
	} {
		if strings.Contains(text, secret) || strings.Contains(text, "ticket=") {
			t.Fatalf("%s leaked realtime credential: %q", label, text)
		}
	}
}

func TestGameClientRoutesStreamEntityUpdates(t *testing.T) {
	entities := newRecordingEntities()
	channel := &fakeChannel{connected: true}
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		DecodeEntity:  func(data []byte) (any, error) { return string(data), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: "ws://example"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Disconnect()
	w := &pb.Writer{}
	w.Tag(1, 2).Bytes([]byte("entity"))
	channel.callbacks.onMessage(w.Finish())
	waitForEntityCounts(t, entities, 1, 0)
}

func TestGameClientRoutesDatagramStateThroughDispatcher(t *testing.T) {
	entities := newRecordingEntities()
	channel := &fakeChannel{connected: true}
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebTransport, URL: "https://example"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Disconnect()
	frame := []byte("compact")
	channel.callbacks.onEventualStateMessage(appendLengthPrefixedFrame(nil, frame))
	waitForEntityCounts(t, entities, 0, 1)
	_, _, lastFrame := entities.snapshot()
	if string(lastFrame) != string(frame) {
		t.Fatalf("last frame = %q, want %q", lastFrame, frame)
	}
}

func TestGameClientRoutesCompactStateBatches(t *testing.T) {
	entities := newRecordingEntities()
	client := NewGameClient(GameClientOptions{EntityManager: entities})
	frame := []byte("compact")
	batch := appendLengthPrefixedFrame(nil, frame)
	client.handleCompactStateBatch(batch)
	_, compact, lastFrame := entities.snapshot()
	if compact != 1 {
		t.Fatalf("compact updates = %d, want 1", compact)
	}
	if string(lastFrame) != string(frame) {
		t.Fatalf("last frame = %q, want %q", lastFrame, frame)
	}
}

func TestGameClientSerializesConcurrentInboundCallbacks(t *testing.T) {
	const iterations = 64
	entities := &serialCheckingEntities{delivered: make(chan struct{}, iterations*2)}
	channel := &fakeChannel{connected: true}
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		DecodeEntity:  func(data []byte) (any, error) { return string(data), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebTransport, URL: "https://example"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Disconnect()

	streamFrame := encodeEntityUpdateMessage([]byte("entity"))
	compactBatch := appendLengthPrefixedFrame(nil, []byte("compact"))
	var wg sync.WaitGroup
	wg.Add(iterations * 2)
	for i := 0; i < iterations; i++ {
		go func() {
			defer wg.Done()
			channel.callbacks.onMessage(streamFrame)
		}()
		go func() {
			defer wg.Done()
			channel.callbacks.onEventualStateMessage(compactBatch)
		}()
	}
	wg.Wait()
	waitForSerialEntityCount(t, entities, iterations*2)
	if violations := atomic.LoadInt32(&entities.violations); violations != 0 {
		t.Fatalf("manager methods overlapped %d times", violations)
	}
}

func TestGameClientReconnectStopsPreviousDispatcher(t *testing.T) {
	entities := newRecordingEntities()
	channels := []*fakeChannel{{connected: true}, {connected: true}}
	var nextChannel int
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		DecodeEntity:  func(data []byte) (any, error) { return string(data), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			channel := channels[nextChannel]
			nextChannel++
			return channel, nil
		},
	})
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: "ws://example"}); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: "ws://example"}); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	defer client.Disconnect()

	channels[0].callbacks.onMessage(encodeEntityUpdateMessage([]byte("stale")))
	assertEntityCountsStay(t, entities, 0, 0)

	channels[1].callbacks.onMessage(encodeEntityUpdateMessage([]byte("current")))
	waitForEntityCounts(t, entities, 1, 0)
}

func TestDatagramProtocolEncodesStateAwareLane(t *testing.T) {
	var sent [][]byte
	protocol := newDatagramProtocol(func(data []byte) error {
		sent = append(sent, append([]byte(nil), data...))
		return nil
	}, 100*time.Millisecond)
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  7,
		lane:       datagramLaneEventualState,
		stateToken: 42,
		payload:    appendLengthPrefixedFrame(nil, []byte("state")),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	var delivered bool
	if err := protocol.receive(packet, func(lane uint8, payload []byte) {
		delivered = lane == datagramLaneEventualState && len(payload) > 0
	}); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !delivered {
		t.Fatal("state-aware datagram was not delivered")
	}
	if len(sent) != 0 {
		t.Fatalf("ack datagrams = %d, want 0 before coalescing interval", len(sent))
	}
}

func TestDatagramProtocolPiggybacksStateAckOnStreamPayload(t *testing.T) {
	var sent [][]byte
	protocol := newDatagramProtocol(func(data []byte) error {
		sent = append(sent, append([]byte(nil), data...))
		return nil
	}, time.Hour)
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  9,
		lane:       datagramLaneEventualState,
		stateToken: 1,
		payload:    appendLengthPrefixedFrame(nil, []byte("state")),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	if err := protocol.receive(packet, nil); err != nil {
		t.Fatalf("receive: %v", err)
	}
	payload := []byte{1, 2, 3}
	wrapped := protocol.wrapStreamPayload(payload)
	if len(sent) != 0 {
		t.Fatalf("ack datagrams = %d, want 0 after stream piggyback", len(sent))
	}
	if !bytes.HasPrefix(wrapped, clientReliableAckControlFrame) {
		t.Fatalf("wrapped payload missing ACK control frame prefix: %v", wrapped)
	}
	offset := len(clientReliableAckControlFrame)
	if ackSeq := binary.BigEndian.Uint16(wrapped[offset : offset+2]); ackSeq != 9 {
		t.Fatalf("ack seq = %d, want 9", ackSeq)
	}
	offset += 2 + datagramAckMaskBytes
	if !bytes.Equal(wrapped[offset:], payload) {
		t.Fatalf("wrapped payload suffix = %v, want %v", wrapped[offset:], payload)
	}
	if err := protocol.sendDueAck(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("sendDueAck: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("ack datagrams = %d, want 0 after piggyback cleared ACK", len(sent))
	}
}

func TestDatagramProtocolSendsDueAckOnlyPacket(t *testing.T) {
	var sent [][]byte
	protocol := newDatagramProtocol(func(data []byte) error {
		sent = append(sent, append([]byte(nil), data...))
		return nil
	}, 50*time.Millisecond)
	packet, err := encodeDatagramPacket(datagramPacket{
		packetSeq:  3,
		lane:       datagramLaneEventualState,
		stateToken: 1,
		payload:    appendLengthPrefixedFrame(nil, []byte("state")),
	})
	if err != nil {
		t.Fatalf("encodeDatagramPacket: %v", err)
	}
	if err := protocol.receive(packet, nil); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := protocol.sendDueAck(time.Now()); err != nil {
		t.Fatalf("sendDueAck before due: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("ack datagrams = %d, want 0 before coalescing interval", len(sent))
	}
	if err := protocol.sendDueAck(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("sendDueAck after due: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("ack datagrams = %d, want 1 after coalescing interval", len(sent))
	}
	ack, err := decodeDatagramPacket(sent[0])
	if err != nil {
		t.Fatalf("decode ACK packet: %v", err)
	}
	if ack.flags&datagramFlagAckOnly == 0 {
		t.Fatalf("ack flags = %d, want ACK-only", ack.flags)
	}
	if ack.ackSeq != 3 {
		t.Fatalf("ack seq = %d, want 3", ack.ackSeq)
	}
}

func TestDatagramProtocolRejectsMalformedDatagrams(t *testing.T) {
	protocol := newDatagramProtocol(func([]byte) error { return nil }, time.Millisecond)

	err := protocol.receive([]byte{1, 2, 3}, func(uint8, []byte) {
		t.Fatal("malformed datagram should not be delivered")
	})
	if err == nil {
		t.Fatal("receive returned nil error")
	}
}

type serialCheckingEntities struct {
	active     int32
	violations int32
	delivered  chan struct{}
}

func (s *serialCheckingEntities) ApplyUpdate(any) {
	s.enter()
	s.leave()
	s.delivered <- struct{}{}
}

func (s *serialCheckingEntities) ApplyCompactUpdate([]byte) {
	s.enter()
	s.leave()
	s.delivered <- struct{}{}
}

func (s *serialCheckingEntities) Get(int64) any { return nil }

func (s *serialCheckingEntities) enter() {
	if atomic.AddInt32(&s.active, 1) != 1 {
		atomic.AddInt32(&s.violations, 1)
	}
	time.Sleep(100 * time.Microsecond)
}

func (s *serialCheckingEntities) leave() {
	atomic.AddInt32(&s.active, -1)
}

func encodeEntityUpdateMessage(data []byte) []byte {
	w := &pb.Writer{}
	w.Tag(1, 2).Bytes(data)
	return w.Finish()
}

func waitForEntityCounts(t *testing.T, entities *recordingEntities, updates, compact int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		gotUpdates, gotCompact, _ := entities.snapshot()
		if gotUpdates == updates && gotCompact == compact {
			return
		}
		select {
		case <-entities.changed:
		case <-deadline:
			t.Fatalf("entity counts = updates %d compact %d, want updates %d compact %d", gotUpdates, gotCompact, updates, compact)
		}
	}
}

func assertEntityCountsStay(t *testing.T, entities *recordingEntities, updates, compact int) {
	t.Helper()
	deadline := time.After(50 * time.Millisecond)
	for {
		gotUpdates, gotCompact, _ := entities.snapshot()
		if gotUpdates != updates || gotCompact != compact {
			t.Fatalf("entity counts = updates %d compact %d, want to stay updates %d compact %d", gotUpdates, gotCompact, updates, compact)
		}
		select {
		case <-entities.changed:
		case <-deadline:
			return
		}
	}
}

func waitForSerialEntityCount(t *testing.T, entities *serialCheckingEntities, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for i := 0; i < want; i++ {
		select {
		case <-entities.delivered:
		case <-deadline:
			t.Fatalf("delivered updates = %d, want %d", i, want)
		}
	}
}

func TestGameClientDisconnectNotifiesOnClose(t *testing.T) {
	channel := &fakeChannel{connected: true}
	client := NewGameClient(GameClientOptions{
		EntityManager: newRecordingEntities(),
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			return channel, nil
		},
	})
	var got atomic.Int32
	client.OnDisconnect(func(info DisconnectInfo) {
		if !info.WasClean {
			t.Errorf("WasClean=%v want true", info.WasClean)
		}
		got.Add(1)
	})
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebSocket, URL: "ws://example"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client.Disconnect()
	if got.Load() != 1 {
		t.Fatalf("OnDisconnect calls=%d want 1", got.Load())
	}
	client.Disconnect() // idempotent; no second notify
	if got.Load() != 1 {
		t.Fatalf("second Disconnect notified again: %d", got.Load())
	}
}
