package golemclient

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem-go-client/pb"
	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// fencedEntities mimics the generated managers' revision fence: an update
// older than the last applied revision is rejected; equal revisions apply.
// Full and compact records use the same test encoding: id, revision, value.
type fencedEntities struct {
	mu     sync.Mutex
	last   map[int64]uint64
	values map[int64]string
	log    []string
}

func newFencedEntities() *fencedEntities {
	return &fencedEntities{last: map[int64]uint64{}, values: map[int64]string{}}
}

func fencedRecord(id int64, revision uint64, value string) []byte {
	return append([]byte{byte(id), byte(revision)}, value...)
}

func (f *fencedEntities) apply(kind string, frame []byte) {
	id, revision, value := int64(frame[0]), uint64(frame[1]), string(frame[2:])
	f.mu.Lock()
	defer f.mu.Unlock()
	if last, ok := f.last[id]; ok && revision < last {
		f.log = append(f.log, fmt.Sprintf("reject %s %d@%d", kind, id, revision))
		return
	}
	f.last[id] = revision
	f.values[id] = value
	f.log = append(f.log, fmt.Sprintf("%s %d@%d=%s", kind, id, revision, value))
}

func (f *fencedEntities) ApplyUpdate(update any)          { f.apply("full", update.([]byte)) }
func (f *fencedEntities) ApplyCompactUpdate(frame []byte) { f.apply("compact", frame) }
func (f *fencedEntities) Get(int64) any                   { return nil }

func (f *fencedEntities) record(entry string) {
	f.mu.Lock()
	f.log = append(f.log, entry)
	f.mu.Unlock()
}

func (f *fencedEntities) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// spaceChannel is a fakeChannel whose stream sends may come from the inbound
// dispatcher goroutine.
type spaceChannel struct {
	fakeChannel
	mu     sync.Mutex
	frames [][]byte
	closed atomic.Bool
}

func (c *spaceChannel) Send(data []byte) error {
	c.mu.Lock()
	c.frames = append(c.frames, append([]byte(nil), data...))
	c.mu.Unlock()
	return nil
}

func (c *spaceChannel) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *spaceChannel) sentFrames() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.frames...)
}

func wrapServerField(field int, payload []byte) []byte {
	w := &pb.Writer{}
	w.Tag(field, 2).Bytes(payload)
	return w.Finish()
}

func spaceChangedField(space uint32, transition uint64) []byte {
	w := &pb.Writer{}
	if space != 0 {
		w.Tag(1, 0).Uint32(space)
	}
	if transition != 0 {
		w.Tag(2, 0).Uint64(transition)
	}
	return wrapServerField(serverMessageSpaceChangedField, w.Finish())
}

func spaceSettledField(transition uint64) []byte {
	w := &pb.Writer{}
	if transition != 0 {
		w.Tag(1, 0).Uint64(transition)
	}
	return wrapServerField(serverMessageSpaceSettledField, w.Finish())
}

func entityField(id int64, revision uint64, value string) []byte {
	return wrapServerField(serverMessageEntityField, fencedRecord(id, revision, value))
}

func concatFields(fields ...[]byte) []byte {
	var out []byte
	for _, field := range fields {
		out = append(out, field...)
	}
	return out
}

func compactBatch(id int64, revision uint64, value string) []byte {
	return appendLengthPrefixedFrame(nil, fencedRecord(id, revision, value))
}

func spaceAckBytes(transition uint64) []byte {
	out := []byte{0x00, 0x4f, 0x47, 0x53, 0x03, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint64(out[5:], transition)
	return out
}

func newSpaceClient(channels ...*spaceChannel) *GameClient {
	var next atomic.Int32
	return NewGameClient(GameClientOptions{
		EntityManager: newFencedEntities(),
		DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			channel := channels[next.Add(1)-1]
			channel.connected = true
			return channel, nil
		},
	})
}

func connectSpaceClient(t *testing.T, client *GameClient) {
	t.Helper()
	if err := client.Connect(context.Background(), ConnectOptions{Transport: TransportWebTransport, URL: "https://example"}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
}

func waitForCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForLog(t *testing.T, entities *fencedEntities, want ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := entities.snapshot()
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want %q", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSpaceMessageGoldenBytesDecodeByFieldNumber(t *testing.T) {
	if got := spaceChangedField(5, 1); !bytes.Equal(got, []byte{0x22, 0x04, 0x08, 0x05, 0x10, 0x01}) {
		t.Fatalf("SpaceChanged(5,1) = % x", got)
	}
	if got := spaceSettledField(1); !bytes.Equal(got, []byte{0x2a, 0x02, 0x08, 0x01}) {
		t.Fatalf("SpaceSettled(1) = % x", got)
	}
	cleared := (&pb.Writer{}).Tag(1, 2).String("terrain").Finish()
	frame := concatFields(
		spaceChangedField(0, 0),
		spaceSettledField(0),
		wrapServerField(serverMessageWorldClearedField, cleared),
		wrapServerField(9, []byte{1}),
	)
	messages, err := decodeServerMessages(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("decoded %d messages, want 3 (empty payloads kept, unknown field skipped)", len(messages))
	}
	for i, field := range []int{serverMessageSpaceChangedField, serverMessageSpaceSettledField, serverMessageWorldClearedField} {
		if messages[i].Field != field {
			t.Fatalf("message %d field = %d, want %d", i, messages[i].Field, field)
		}
	}
	if space, transition := decodeSpaceChanged(messages[0].Payload); space != 0 || transition != 0 {
		t.Fatalf("empty SpaceChanged = (%d,%d)", space, transition)
	}
	if name := decodeWorldCleared(messages[2].Payload); name != "terrain" {
		t.Fatalf("WorldCleared name = %q", name)
	}
	if space, transition := decodeSpaceChanged([]byte{0x08, 0x05, 0x10, 0x01}); space != 5 || transition != 1 {
		t.Fatalf("SpaceChanged = (%d,%d), want (5,1)", space, transition)
	}
}

func TestGameClientSpaceChangedNotifiesBeforeFollowingFramesAndAcks(t *testing.T) {
	channel := &spaceChannel{}
	client := newSpaceClient(channel)
	entities := client.Entities().(*fencedEntities)
	var calls atomic.Int32
	client.OnSpaceChanged(func(oldSpace, newSpace SpaceID) {
		calls.Add(1)
		if got := client.Space(); got != newSpace {
			t.Errorf("Space() in callback = %d, want %d", got, newSpace)
		}
		entities.record(fmt.Sprintf("space %d->%d", oldSpace, newSpace))
	})
	connectSpaceClient(t, client)
	defer client.Disconnect()

	// Exit, SpaceChanged, carried full state, entered full state and
	// SpaceSettled concatenated in one logical frame.
	channel.callbacks.onMessage(concatFields(
		entityField(1, 3, "exit"),
		spaceChangedField(5, 1),
		entityField(2, 4, "carried"),
		entityField(3, 1, "entered"),
		spaceSettledField(1),
	))
	waitForLog(t, entities, "full 1@3=exit", "space 0->5", "full 2@4=carried", "full 3@1=entered")
	waitForCondition(t, "SpaceAck", func() bool { return len(channel.sentFrames()) == 1 })
	if got := channel.sentFrames()[0]; !bytes.Equal(got, spaceAckBytes(1)) || !bytes.Equal(got, netwire.AppendSpaceAckControl(nil, 1)) {
		t.Fatalf("SpaceAck = % x", got)
	}
	if client.Space() != 5 || calls.Load() != 1 {
		t.Fatalf("Space() = %d calls = %d", client.Space(), calls.Load())
	}

	// A transition back to space 0 omits the space field.
	channel.callbacks.onMessage(concatFields(spaceChangedField(0, 2), spaceSettledField(2)))
	waitForCondition(t, "second SpaceAck", func() bool { return len(channel.sentFrames()) == 2 })
	if client.Space() != 0 || calls.Load() != 2 {
		t.Fatalf("Space() = %d calls = %d after returning to space 0", client.Space(), calls.Load())
	}
	if got := channel.sentFrames()[1]; !bytes.Equal(got, spaceAckBytes(2)) {
		t.Fatalf("second SpaceAck = % x", got)
	}
}

func TestGameClientHoldsDatagramsUntilSpaceSettled(t *testing.T) {
	channel := &spaceChannel{}
	client := newSpaceClient(channel)
	entities := client.Entities().(*fencedEntities)
	connectSpaceClient(t, client)
	defer client.Disconnect()

	channel.callbacks.onMessage(entityField(1, 8, "old"))
	channel.callbacks.onMessage(spaceChangedField(7, 3))
	// Datagrams racing the transition on every lane: one older than the carried
	// full state, one at its revision and one newer.
	channel.callbacks.onEventualStateMessage(compactBatch(1, 9, "late"))
	channel.callbacks.onReliableOrderedMessage(compactBatch(1, 10, "carried"))
	channel.callbacks.onUnreliableStateMessage(compactBatch(1, 11, "next"))
	// A stream marker proves the datagrams above were dispatched and held.
	channel.callbacks.onMessage(entityField(2, 1, "marker"))
	waitForLog(t, entities, "full 1@8=old", "full 2@1=marker")
	if len(channel.sentFrames()) != 0 {
		t.Fatal("SpaceAck sent before SpaceSettled")
	}

	channel.callbacks.onMessage(concatFields(entityField(1, 10, "carried"), spaceSettledField(3)))
	waitForLog(t, entities,
		"full 1@8=old",
		"full 2@1=marker",
		"full 1@10=carried",
		"reject compact 1@9",
		"compact 1@10=carried",
		"compact 1@11=next",
	)
	waitForCondition(t, "SpaceAck", func() bool { return len(channel.sentFrames()) == 1 })
	if got := channel.sentFrames()[0]; !bytes.Equal(got, spaceAckBytes(3)) {
		t.Fatalf("SpaceAck = % x", got)
	}

	// After the transition, datagrams apply immediately again.
	channel.callbacks.onEventualStateMessage(compactBatch(1, 12, "live"))
	waitForCondition(t, "live datagram", func() bool {
		log := entities.snapshot()
		return log[len(log)-1] == "compact 1@12=live"
	})
}

func TestGameClientNewerSpaceChangedSupersedesOpenTransition(t *testing.T) {
	channel := &spaceChannel{}
	client := newSpaceClient(channel)
	entities := client.Entities().(*fencedEntities)
	var mu sync.Mutex
	var changes []string
	client.OnSpaceChanged(func(oldSpace, newSpace SpaceID) {
		mu.Lock()
		changes = append(changes, fmt.Sprintf("%d->%d", oldSpace, newSpace))
		mu.Unlock()
	})
	connectSpaceClient(t, client)
	defer client.Disconnect()

	channel.callbacks.onMessage(spaceChangedField(1, 1))
	channel.callbacks.onEventualStateMessage(compactBatch(1, 1, "held"))
	channel.callbacks.onMessage(concatFields(spaceChangedField(2, 2), spaceSettledField(1)))
	channel.callbacks.onMessage(entityField(9, 1, "marker"))
	waitForLog(t, entities, "full 9@1=marker")
	if len(channel.sentFrames()) != 0 {
		t.Fatal("superseded SpaceSettled was acknowledged")
	}
	channel.callbacks.onMessage(spaceSettledField(2))
	waitForLog(t, entities, "full 9@1=marker", "compact 1@1=held")
	waitForCondition(t, "SpaceAck", func() bool { return len(channel.sentFrames()) == 1 })
	if got := channel.sentFrames()[0]; !bytes.Equal(got, spaceAckBytes(2)) {
		t.Fatalf("SpaceAck = % x, want transition 2", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(changes, []string{"0->1", "1->2"}) || client.Space() != 2 {
		t.Fatalf("changes = %v Space() = %d", changes, client.Space())
	}
}

func TestGameClientSpaceHoldBackOverflowClosesConnection(t *testing.T) {
	channel := &spaceChannel{}
	client := newSpaceClient(channel)
	entities := client.Entities().(*fencedEntities)
	disconnects := make(chan DisconnectInfo, 2)
	client.OnDisconnect(func(info DisconnectInfo) { disconnects <- info })
	connectSpaceClient(t, client)

	channel.callbacks.onMessage(spaceChangedField(4, 1))
	for i := 0; i <= maxHeldSpaceTransitionBatches; i++ {
		channel.callbacks.onEventualStateMessage(compactBatch(1, 1, "x"))
	}
	select {
	case info := <-disconnects:
		if info.WasClean || !errors.Is(info.Err, ErrSpaceTransitionOverflow) {
			t.Fatalf("disconnect = %+v, want unclean ErrSpaceTransitionOverflow", info)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow did not close the connection")
	}
	if !channel.closed.Load() {
		t.Fatal("channel was not closed")
	}
	if client.Space() != 0 || client.ConnectedTransport() != "" {
		t.Fatalf("Space() = %d transport = %q after overflow", client.Space(), client.ConnectedTransport())
	}
	if got := entities.snapshot(); len(got) != 0 {
		t.Fatalf("held batches were applied after overflow: %v", got)
	}
	client.Disconnect()
	select {
	case info := <-disconnects:
		t.Fatalf("extra disconnect %+v", info)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestGameClientSpaceResetsSilentlyOnDisconnectAndReconnect(t *testing.T) {
	first, second := &spaceChannel{}, &spaceChannel{}
	client := newSpaceClient(first, second)
	entities := client.Entities().(*fencedEntities)
	var calls atomic.Int32
	client.OnSpaceChanged(func(SpaceID, SpaceID) { calls.Add(1) })
	connectSpaceClient(t, client)

	first.callbacks.onMessage(spaceChangedField(3, 1))
	first.callbacks.onEventualStateMessage(compactBatch(1, 1, "held"))
	waitForCondition(t, "space 3", func() bool { return client.Space() == 3 })

	// A remote close resets the space without a notification.
	first.callbacks.onClose(DisconnectInfo{WasClean: false})
	if client.Space() != 0 {
		t.Fatalf("Space() = %d after remote close", client.Space())
	}
	connectSpaceClient(t, client)
	defer client.Disconnect()
	if client.Space() != 0 || calls.Load() != 1 {
		t.Fatalf("Space() = %d calls = %d after reconnect", client.Space(), calls.Load())
	}
	// The previous connection's open transition and held batch are gone.
	second.callbacks.onEventualStateMessage(compactBatch(1, 2, "fresh"))
	waitForLog(t, entities, "compact 1@2=fresh")

	second.callbacks.onMessage(spaceChangedField(6, 1))
	waitForCondition(t, "space 6", func() bool { return client.Space() == 6 })
	client.Disconnect()
	if client.Space() != 0 || calls.Load() != 2 {
		t.Fatalf("Space() = %d calls = %d after Disconnect", client.Space(), calls.Load())
	}
}

func TestGameClientWithoutSpaceMessagesIsUnchanged(t *testing.T) {
	channel := &spaceChannel{}
	client := newSpaceClient(channel)
	entities := client.Entities().(*fencedEntities)
	var calls atomic.Int32
	client.OnSpaceChanged(func(SpaceID, SpaceID) { calls.Add(1) })
	connectSpaceClient(t, client)
	defer client.Disconnect()

	channel.callbacks.onMessage(concatFields(entityField(1, 1, "a"), wrapServerField(9, []byte{7}), entityField(2, 1, "b")))
	channel.callbacks.onEventualStateMessage(compactBatch(1, 2, "c"))
	waitForLog(t, entities, "full 1@1=a", "full 2@1=b", "compact 1@2=c")
	if client.Space() != 0 || calls.Load() != 0 || len(channel.sentFrames()) != 0 {
		t.Fatalf("Space() = %d calls = %d sent = %d", client.Space(), calls.Load(), len(channel.sentFrames()))
	}
}

// recordingWorld logs world updates and, as clearingWorld, clears into the
// shared entity log so ordering with entity frames is visible.
type recordingWorld struct{ log *fencedEntities }

func (w recordingWorld) ApplyUpdate(update any) { w.log.record("world " + string(update.([]byte))) }

type clearingWorld struct{ recordingWorld }

func (w clearingWorld) ClearWorld(name string) { w.log.record("cleared " + name) }

func worldClearedField(name string) []byte {
	return wrapServerField(serverMessageWorldClearedField, (&pb.Writer{}).Tag(1, 2).String(name).Finish())
}

func TestGameClientWorldClearedCallsOptionalClearer(t *testing.T) {
	for _, clearer := range []bool{true, false} {
		channel := &spaceChannel{}
		entities := newFencedEntities()
		var world WorldManagerLike = recordingWorld{entities}
		if clearer {
			world = clearingWorld{recordingWorld{entities}}
		}
		client := NewGameClient(GameClientOptions{
			EntityManager: entities,
			DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
			WorldManager:  world,
			DecodeWorld:   func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
			CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
				channel.connected = true
				return channel, nil
			},
		})
		connectSpaceClient(t, client)
		channel.callbacks.onMessage(concatFields(
			spaceChangedField(4, 1),
			wrapServerField(serverMessageWorldField, []byte("fog")),
			worldClearedField("Light"),
			entityField(2, 4, "carried"),
			spaceSettledField(1),
		))
		if clearer {
			waitForLog(t, entities, "world fog", "cleared Light", "full 2@4=carried")
		} else {
			// Without WorldClearer the message is ignored.
			waitForLog(t, entities, "world fog", "full 2@4=carried")
		}
		client.Disconnect()
	}
}
