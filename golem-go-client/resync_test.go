package golemclient

import (
	"bytes"
	"context"
	"testing"

	"github.com/demiurgos-hub/golem-engine/internal/netwire"
)

// resyncEntities is a fencedEntities that exposes the optional resync hook
// and can run a callback for each compact frame.
type resyncEntities struct {
	*fencedEntities
	resync    func(int64)
	onCompact func([]byte)
}

func (r *resyncEntities) OnResyncNeeded(fn func(int64)) { r.resync = fn }

func (r *resyncEntities) ApplyCompactUpdate(frame []byte) {
	r.fencedEntities.ApplyCompactUpdate(frame)
	if r.onCompact != nil {
		r.onCompact(frame)
	}
}

func TestGameClientSendsEntityResyncForEvictedRecords(t *testing.T) {
	entities := &resyncEntities{fencedEntities: newFencedEntities()}
	channel := &spaceChannel{}
	client := NewGameClient(GameClientOptions{
		EntityManager: entities,
		CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
			channel.connected = true
			return channel, nil
		},
	})
	if entities.resync == nil {
		t.Fatal("NewGameClient did not register the resync hook")
	}
	entities.resync(5) // disconnected: nothing to send
	if len(channel.sentFrames()) != 0 {
		t.Fatal("resync sent without a connection")
	}
	connectSpaceClient(t, client)
	defer client.Disconnect()
	entities.resync(0x0102030405060708)
	want := []byte{0x00, 0x4f, 0x47, 0x53, 0x04, 1, 2, 3, 4, 5, 6, 7, 8}
	if got := channel.sentFrames(); len(got) != 1 || !bytes.Equal(got[0], want) || !bytes.Equal(got[0], netwire.AppendEntityResyncControl(nil, 0x0102030405060708)) {
		t.Fatalf("EntityResync frames = % x", got)
	}
}

// A callback that disconnects stops the rest of the compact batch, both for a
// live batch and for batches held during a space transition.
func TestGameClientDisconnectInCompactCallbackStopsBatch(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "live"
		if held {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			entities := &resyncEntities{fencedEntities: newFencedEntities()}
			channel := &spaceChannel{}
			client := NewGameClient(GameClientOptions{
				EntityManager: entities,
				DecodeEntity:  func(data []byte) (any, error) { return append([]byte(nil), data...), nil },
				CreateChannel: func(context.Context, ConnectOptions) (ReliableMessageChannel, error) {
					channel.connected = true
					return channel, nil
				},
			})
			disconnected := make(chan struct{})
			client.OnDisconnect(func(DisconnectInfo) { close(disconnected) })
			entities.onCompact = func([]byte) { client.Disconnect() }
			connectSpaceClient(t, client)
			defer client.Disconnect()
			client.mu.Lock()
			dispatcher := client.inbound
			client.mu.Unlock()

			batch := append(compactBatch(1, 1, "first"), compactBatch(2, 1, "second")...)
			batch = append(batch, compactBatch(3, 1, "third")...)
			if held {
				channel.callbacks.onMessage(spaceChangedField(4, 1))
			}
			channel.callbacks.onEventualStateMessage(batch)
			if held {
				channel.callbacks.onMessage(spaceSettledField(1))
			}
			<-disconnected
			<-dispatcher.stopped // the batch in progress has finished
			if got := entities.snapshot(); len(got) != 1 || got[0] != "compact 1@1=first" {
				t.Fatalf("applied after disconnect: %q", got)
			}
			if held && len(channel.sentFrames()) != 0 {
				t.Fatal("SpaceAck sent after a callback disconnected")
			}
		})
	}
}
