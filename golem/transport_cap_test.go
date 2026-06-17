package golem

import (
	"bytes"
	"testing"

	golemnet "golem-engine/golem/net"
)

type oversizedWorldData struct {
	name string
	data []byte
}

func (d *oversizedWorldData) WorldName() string              { return d.name }
func (d *oversizedWorldData) MarshalUpdate() ([]byte, error) { return d.data, nil }

func TestPushWorldDataRejectsOversizeWrappedFrame(t *testing.T) {
	srv := NewServer(ServerConfig{})
	srv.World.Set(&oversizedWorldData{
		name: "zone",
		data: bytes.Repeat([]byte("w"), 33000),
	})

	if err := srv.PushWorldData("zone"); err == nil {
		t.Fatal("PushWorldData returned nil for oversized wrapped frame")
	}
}

func TestBroadcastEventRejectsOversizeWrappedFrame(t *testing.T) {
	srv := NewServer(ServerConfig{})
	data := WrapServerEvent(bytes.Repeat([]byte("e"), 33000))

	if err := srv.BroadcastEvent(data); err == nil {
		t.Fatal("BroadcastEvent returned nil for oversized wrapped frame")
	}
}

func TestNewServerDefaultsToDatagramStateUpdatesOnWebTransport(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if srv.config.Transport != golemnet.TransportWebTransport {
		t.Fatalf("Transport = %q, want %q", srv.config.Transport, golemnet.TransportWebTransport)
	}
	if srv.config.StateUpdateLane != StateUpdateLaneDatagram {
		t.Fatalf("StateUpdateLane = %q, want %q", srv.config.StateUpdateLane, StateUpdateLaneDatagram)
	}
}

func TestNewServerRejectsDatagramStateUpdatesOnWebSocket(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewServer did not panic for invalid datagram state update config")
		}
	}()
	_ = NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
}

func TestNewServerAcceptsDatagramStateUpdatesOnWebTransport(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	if srv.config.StateUpdateLane != StateUpdateLaneDatagram {
		t.Fatalf("StateUpdateLane = %q, want %q", srv.config.StateUpdateLane, StateUpdateLaneDatagram)
	}
}

func TestNewServerAcceptsStreamStateUpdatesOnWebSocket(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	if srv.config.StateUpdateLane != StateUpdateLaneStream {
		t.Fatalf("StateUpdateLane = %q, want %q", srv.config.StateUpdateLane, StateUpdateLaneStream)
	}
}
