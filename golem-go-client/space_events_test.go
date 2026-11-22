package golemclient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// loggingEvents records applied server events in the entity log, so tests see
// events and state in one order.
type loggingEvents struct{ log *fencedEntities }

func (e loggingEvents) ApplyRaw(data []byte) { e.log.record("event " + string(data)) }

func eventBatch(transition uint64, events ...string) []byte {
	var batch []byte
	for _, event := range events {
		batch = netwire.AppendEventRecordTransition(batch, []byte(event), transition)
	}
	return batch
}

func TestGameClientDropsEventRecordsOfOlderTransitions(t *testing.T) {
	channel := &spaceChannel{}
	entities := newFencedEntities()
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		EventManager:  loggingEvents{entities},
		DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			channel.connected = true
			return channel, nil
		},
	})
	connectSpaceClient(t, client)
	defer client.Disconnect()

	// Untagged events apply before any switch.
	channel.callbacks.onUnreliableStateMessage(eventBatch(0, "pre"))
	waitForLog(t, entities, "event pre")

	// Held during the transition: the untagged (pre-switch) event is dropped
	// on replay, the current transition's event applies.
	channel.callbacks.onMessage(spaceChangedField(7, 1))
	channel.callbacks.onUnreliableStateMessage(eventBatch(0, "held-old"))
	channel.callbacks.onReliableOrderedMessage(eventBatch(1, "held-current"))
	channel.callbacks.onMessage(spaceSettledField(1))
	waitForLog(t, entities, "event pre", "event held-current")

	// Live after the settle: older records are dropped, current ones apply.
	channel.callbacks.onUnreliableStateMessage(eventBatch(0, "live-old"))
	channel.callbacks.onReliableUnorderedMessage(eventBatch(1, "live-current"))
	waitForLog(t, entities, "event pre", "event held-current", "event live-current")

	// A later transition makes transition 1 stale, live or held.
	channel.callbacks.onMessage(concatFields(spaceChangedField(8, 2), spaceSettledField(2)))
	channel.callbacks.onUnreliableStateMessage(eventBatch(1, "stale"))
	// A malformed tagged record is dropped, never read as compact state.
	channel.callbacks.onUnreliableStateMessage(appendLengthPrefixedFrame(nil, []byte{0, 0, 0x80}))
	channel.callbacks.onUnreliableStateMessage(eventBatch(2, "fresh"))
	waitForLog(t, entities, "event pre", "event held-current", "event live-current", "event fresh")
}

// failingSendChannel is a spaceChannel whose sends fail.
type failingSendChannel struct {
	spaceChannel
	err error
}

func (c *failingSendChannel) Send([]byte) error { return c.err }

func TestGameClientFailedControlSendsCloseConnection(t *testing.T) {
	for _, control := range []string{"space-ack", "entity-resync"} {
		t.Run(control, func(t *testing.T) {
			sendErr := errors.New("write failed")
			channel := &failingSendChannel{err: sendErr}
			entities := &resyncEntities{fencedEntities: newFencedEntities()}
			client := NewGameClient(GameClientOptions{
				EntityManager: entities,
				DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
				CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
					channel.connected = true
					return channel, nil
				},
			})
			var once sync.Once
			entities.onCompact = func([]byte) { once.Do(func() { entities.resync(1) }) }
			disconnects := make(chan DisconnectInfo, 2)
			client.OnDisconnect(func(info DisconnectInfo) { disconnects <- info })
			connectSpaceClient(t, client)
			defer client.Disconnect()

			if control == "space-ack" {
				channel.callbacks.onMessage(concatFields(spaceChangedField(3, 1), spaceSettledField(1)))
			} else {
				// The resync hook runs on the dispatcher, like an eviction.
				channel.callbacks.onEventualStateMessage(compactBatch(1, 1, "x"))
			}
			select {
			case info := <-disconnects:
				if info.WasClean || !errors.Is(info.Err, sendErr) {
					t.Fatalf("disconnect = %+v, want unclean with the send error", info)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("failed control send did not close the connection")
			}
			if !channel.closed.Load() || client.ConnectedTransport() != "" {
				t.Fatalf("closed = %v transport = %q", channel.closed.Load(), client.ConnectedTransport())
			}
		})
	}
}

// Events on a reliable datagram lane are reliable only within one space: a
// record sent before a space change that arrives after it, such as a
// retransmission, is dropped like any record of an older transition. An event
// that must survive a space change belongs on the stream, which stays ordered
// with the transition, or the server prepares the change
// (Server.PrepareSpaceChange) so no such record is still on its way.
func TestGameClientDropsLateReliableLaneEventsButKeepsStreamEvents(t *testing.T) {
	channel := &spaceChannel{}
	entities := newFencedEntities()
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		EventManager:  loggingEvents{entities},
		DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			channel.connected = true
			return channel, nil
		},
	})
	connectSpaceClient(t, client)
	defer client.Disconnect()

	channel.callbacks.onMessage(concatFields(spaceChangedField(7, 1), spaceSettledField(1)))
	channel.callbacks.onReliableOrderedMessage(eventBatch(0, "inventory-ordered-late"))
	channel.callbacks.onReliableUnorderedMessage(eventBatch(0, "inventory-unordered-late"))
	channel.callbacks.onMessage(wrapServerField(serverMessageEventField, []byte("inventory-stream")))
	channel.callbacks.onReliableOrderedMessage(eventBatch(1, "current"))
	waitForLog(t, entities, "event inventory-stream", "event current")
}
