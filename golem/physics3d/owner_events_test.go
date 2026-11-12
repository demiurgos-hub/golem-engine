package physics3d

import "testing"

func ownerFixture(a, b uint32, phase ContactPhase, fraction float32) ContactEvent {
	return ContactEvent{Pair: ContactPair{A: ColliderHandle{1, a, 1}, B: ColliderHandle{1, b, 1}, Kind: SensorContact}, OwnerA: 10, OwnerB: 20, Phase: phase, Frame: 1, Revision: 2, Fraction: fraction}
}

func TestOwnerContactsKeepChildCountsAndCrossings(t *testing.T) {
	var tracker OwnerContactTracker
	first, second := ownerFixture(1, 2, Enter, .1), ownerFixture(3, 4, Enter, .1)
	if out := tracker.Update([]ContactEvent{first, second}); len(out) != 1 || out[0].Phase != Enter || out[0].ChildPairs != 2 {
		t.Fatal(out)
	}
	first.Phase, first.Fraction = Exit, .2
	if out := tracker.Update([]ContactEvent{first}); len(out) != 1 || out[0].Phase != Stay || out[0].ChildPairs != 1 {
		t.Fatal(out)
	}
	second.Phase, second.Fraction = Exit, .3
	second.Reason = Disabled
	if out := tracker.Update([]ContactEvent{second}); len(out) != 1 || out[0].Phase != Exit || out[0].ChildPairs != 0 || out[0].Reason != Disabled {
		t.Fatal(out)
	}
	a, b := ownerFixture(5, 6, Enter, .4), ownerFixture(5, 6, Exit, .6)
	if out := tracker.Update([]ContactEvent{b, a}); len(out) != 2 || out[0].Phase != Enter || out[1].Phase != Exit || out[0].Fraction != .4 {
		t.Fatal(out)
	}
}

func TestOwnerContactReplacementRestoreAndUnownedIdentity(t *testing.T) {
	var tracker OwnerContactTracker
	a := ownerFixture(1, 2, RestoredContact, 0)
	a.Reason = Restored
	if out := tracker.Update([]ContactEvent{a}); len(out) != 1 || out[0].Phase != RestoredContact {
		t.Fatal(out)
	}
	b := ownerFixture(3, 4, Enter, .5)
	a.Phase, a.Fraction = Exit, .5
	if out := tracker.Update([]ContactEvent{a, b}); len(out) != 1 || out[0].Phase != Stay || out[0].ChildPairs != 1 {
		t.Fatal(out)
	}
	tracker.Reset()
	a, b = ownerFixture(1, 2, Enter, 1), ownerFixture(3, 4, Enter, 1)
	a.OwnerB, b.OwnerB = 0, 0
	if out := tracker.Update([]ContactEvent{a, b}); len(out) != 2 {
		t.Fatal("unowned colliders merged", out)
	}
	b.Pair.B.Generation++
	if out := tracker.Update([]ContactEvent{b}); len(out) != 1 || out[0].Phase != Enter {
		t.Fatal("generation reused stale contact", out)
	}
}

func TestOwnerContactForcesFollowOwnerOrder(t *testing.T) {
	var tracker OwnerContactTracker
	a, b := ownerFixture(1, 2, Enter, 1), ownerFixture(3, 4, Enter, 1)
	a.OwnerA, a.OwnerB = 20, 10
	a.Force, b.Force = Vec3{X: -3}, Vec3{X: 2}
	out := tracker.Update([]ContactEvent{a, b})
	if len(out) != 1 || out[0].Force.X != 5 || out[0].ChildPairs != 2 {
		t.Fatal(out)
	}
}
