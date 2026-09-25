package main

import (
	"math"
	"testing"

	"example.com/golem-threlte/internal/generated"
)

func TestPulseCommandRequiresActorOwnership(t *testing.T) {
	d := newDemo()
	actor := generated.NewSyncedActor(0, 0, 0, "test", 0, 0)
	if err := d.server.CreateEntity(actor, 42); err != nil {
		t.Fatal(err)
	}
	message := (&generated.ClientMessage{Payload: &generated.ClientMessage_PulseCommand{
		PulseCommand: &generated.PulseCommand{EntityId: actor.EntityID()},
	}}).Marshal()
	if err := d.runtime.Commands.Dispatch(7, message); err != nil {
		t.Fatal(err)
	}
	if actor.PulseCount() != 0 || d.pulses != 0 {
		t.Fatal("another session changed the actor")
	}
	if err := d.runtime.Commands.Dispatch(42, message); err != nil {
		t.Fatal(err)
	}
	if actor.PulseCount() != 1 || d.pulses != 1 {
		t.Fatal("owner command did not update actor and shared world")
	}
}

func TestBeaconRemovalAndRestoreUsesNewEntity(t *testing.T) {
	d := newDemo()
	oldID := d.beacon.EntityID()
	d.toggleBeacon()
	if _, exists := d.server.Get(oldID); exists || d.beacon != nil {
		t.Fatal("removed beacon is still registered")
	}
	d.toggleBeacon()
	if d.beacon == nil || d.beacon.EntityID() == oldID {
		t.Fatal("restored beacon did not get a fresh entity identity")
	}
}

func TestActorMovementUsesElapsedSeconds(t *testing.T) {
	d := newDemo()
	actor := generated.NewSyncedActor(0, 0, 0, "test", 0, 0)
	d.actors[1] = &actorState{entity: actor}
	d.tick(1, d.server)
	x, y, z := actor.Position3D()
	if math.Abs(float64(x)-math.Cos(0.35)*2.8) > 0.00001 || y != 0.55 || z <= 0 {
		t.Fatalf("unexpected position after one second: %v, %v, %v", x, y, z)
	}
	if d.stats.UptimeSeconds() != 1 {
		t.Fatal("headless session statistics did not update")
	}
}
