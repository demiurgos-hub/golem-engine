package physics3d

import (
	"reflect"
	"testing"
)

func TestContactTrackerScratchKeepsAcceptedAndReturnedStateIndependent(t *testing.T) {
	a, b := ColliderHandle{1, 1, 1}, ColliderHandle{1, 2, 1}
	contact := ContactEvent{Pair: ContactPair{a, b, SolidContact}, Depth: .002, Force: Vec3{Y: 1}}
	var accepted ContactTracker
	first := accepted.Update([]ContactEvent{contact, contact}, 1, 1, false)
	if len(first) != 1 || first[0].Phase != Enter {
		t.Fatal(first)
	}
	retained := append([]ContactEvent(nil), first...)
	private := accepted.Clone()
	for frame := uint64(2); frame < 6; frame++ {
		current := contact
		current.Depth = float32(frame)
		got := private.Update([]ContactEvent{current}, frame, frame, false)
		if len(got) != 1 || got[0].Phase != Stay || got[0].Depth != float32(frame) {
			t.Fatal(got)
		}
		got[0].Depth = 999
	}
	if !reflect.DeepEqual(first, retained) || !reflect.DeepEqual(accepted.Current(), retained) {
		t.Fatal("private updates or returned slice mutation changed accepted history")
	}
	if out := private.Remove(a, Replaced, 6); len(out) != 1 || out[0].Reason != Replaced || out[0].Depth != 5 {
		t.Fatal(out)
	}
	if out := private.Update(nil, 7, 7, false); len(out) != 0 {
		t.Fatalf("scratch revived removed pair: %+v", out)
	}
	a.Generation++
	contact.Pair.A = a
	if out := private.Update([]ContactEvent{contact}, 8, 8, true); len(out) != 1 || out[0].Phase != RestoredContact {
		t.Fatal(out)
	}
	if out := private.Drain(Shutdown, 9, 9); len(out) != 1 || out[0].Reason != Shutdown {
		t.Fatal(out)
	}
	if out := private.Update(nil, 10, 10, false); len(out) != 0 {
		t.Fatalf("scratch revived drained pair: %+v", out)
	}
}

func TestContactTrackerUpdateReusesPrivateMaps(t *testing.T) {
	current := make([]ContactEvent, 128)
	for i := range current {
		current[i] = ContactEvent{Pair: ContactPair{ColliderHandle{1, uint32(i*2 + 1), 1}, ColliderHandle{1, uint32(i*2 + 2), 1}, SolidContact}}
	}
	var tracker ContactTracker
	tracker.Update(current, 1, 1, false)
	tracker.Update(current, 2, 2, false)
	allocs := testing.AllocsPerRun(20, func() {
		out := tracker.Update(current, 3, 3, false)
		if len(out) != len(current) || out[0].Phase != Stay {
			panic("incomplete retained contacts")
		}
	})
	if allocs > 1 {
		t.Fatalf("update allocated %.0f times; want only its owned event slice", allocs)
	}
}
