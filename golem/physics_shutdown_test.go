package golem

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

type physicsShutdownWorld struct {
	physicsTestWorld
	events []p.ContactEvent
	closes int
}

func (w *physicsShutdownWorld) CloseWithEvents() ([]p.ContactEvent, error) {
	w.closed = true
	w.closes++
	return w.events, nil
}

func TestPhysicsShutdownDispatchesImmutableTerminalEventsOnce(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &physicsShutdownWorld{events: []p.ContactEvent{{Phase: p.Exit, Reason: p.Shutdown}}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := s.OnPhysicsContact(func(events []p.ContactEvent) {
		calls++
		if !w.closed || events[0].Reason != p.Shutdown {
			t.Fatal("callback before shutdown", events)
		}
		if err := s.QueuePhysicsCommand(func(p.World) error { t.Fatal("post-shutdown command ran"); return nil }); err == nil {
			t.Fatal("shutdown accepted a command")
		}
		events[0].Reason = p.Deleted
	}); err != nil {
		t.Fatal(err)
	}
	s.physics.stop()
	s.physics.stop()
	if calls != 1 || w.closes != 1 || w.events[0].Reason != p.Shutdown {
		t.Fatal(calls, w.closes, w.events)
	}
}
