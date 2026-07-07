package golem

import (
	"bytes"
	"testing"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

type maskAwareEventualEntity struct {
	id            int64
	lastFlushMask uint64
	delta         []byte
	full          []byte
	deltaCalls    int
	fullCalls     int
	flushCalls    int
}

func (e *maskAwareEventualEntity) EntityID() int64              { return e.id }
func (e *maskAwareEventualEntity) TypeName() string             { return "mask-aware-eventual-test" }
func (e *maskAwareEventualEntity) Position() (float32, float32) { return 0, 0 }
func (e *maskAwareEventualEntity) IsGlobal() bool               { return false }
func (e *maskAwareEventualEntity) FlushUpdate() ([]byte, error) {
	e.flushCalls++
	return e.delta, nil
}
func (e *maskAwareEventualEntity) FullUpdate() ([]byte, error) {
	e.fullCalls++
	return e.full, nil
}
func (e *maskAwareEventualEntity) LastFlushMask() uint64 { return e.lastFlushMask }
func (e *maskAwareEventualEntity) MarshalDeltaMask(mask uint64) ([]byte, error) {
	e.deltaCalls++
	return append([]byte(nil), e.delta...), nil
}

func eventualFullChanges(ids ...int64) []eventualStateChange {
	changes := make([]eventualStateChange, len(ids))
	for i, id := range ids {
		changes[i] = eventualStateChange{id: id, full: true}
	}
	return changes
}

func TestEventualStateTrackerRequeuesLostEntities(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{3, 1})
	if got := tracker.dirtyIDs(); len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("dirtyIDs = %v, want insertion order [3 1]", got)
	}

	tracker.markSent(42, eventualFullChanges(1, 3))
	if got := tracker.dirtyIDs(); len(got) != 0 {
		t.Fatalf("dirtyIDs after markSent = %v, want empty", got)
	}

	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 42, Delivered: false}}, func(int64) bool { return true })
	if got := tracker.dirtyIDs(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("dirtyIDs after loss = %v, want requeue order [1 3]", got)
	}
}

func TestEventualStateTrackerSuppressesDuplicateDirtyIDs(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{2, 1, 2, 1, 3})

	if got := tracker.dirtyIDs(); len(got) != 3 || got[0] != 2 || got[1] != 1 || got[2] != 3 {
		t.Fatalf("dirtyIDs = %v, want unique insertion order [2 1 3]", got)
	}
}

func TestEventualStateTrackerReaddsClearedIDOnce(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{1, 2})
	tracker.markSent(1, eventualFullChanges(2))
	tracker.markDirty([]int64{2})

	if got := tracker.dirtyIDs(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("dirtyIDs after readd = %v, want [1 2]", got)
	}
}

func TestEventualStateTrackerCompactsStaleDirtyQueue(t *testing.T) {
	tracker := newEventualStateTracker()
	var ids []int64
	for id := int64(1); id <= 80; id++ {
		ids = append(ids, id)
	}
	tracker.markDirty(ids)
	tracker.markSent(1, eventualFullChanges(ids[:70]...))

	got := tracker.dirtyIDs()
	if len(got) != 10 || got[0] != 71 || got[9] != 80 {
		t.Fatalf("dirtyIDs before compaction = %v, want [71..80]", got)
	}
	tracker.compactDirtyQueue()
	if len(tracker.dirtyQueue) != 10 {
		t.Fatalf("dirtyQueue len after compaction = %d, want 10", len(tracker.dirtyQueue))
	}
	if got := tracker.dirtyIDs(); len(got) != 10 || got[0] != 71 || got[9] != 80 {
		t.Fatalf("dirtyIDs after compaction = %v, want [71..80]", got)
	}
}

func TestEventualStateTrackerClearsAckedEntities(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{5})
	tracker.markSent(8, eventualFullChanges(5))
	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 8, Delivered: true}}, func(int64) bool { return true })

	if got := tracker.dirtyIDs(); len(got) != 0 {
		t.Fatalf("dirtyIDs after ack = %v, want empty", got)
	}
	if len(tracker.inFlight) != 0 {
		t.Fatalf("inFlight len = %d, want 0", len(tracker.inFlight))
	}
}

func TestEventualStateTrackerReusesAckedInFlightSlice(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markSent(1, []eventualStateChange{{id: 1, mask: 0b001}, {id: 2, mask: 0b010}})
	first := tracker.inFlight[1]
	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 1, Delivered: true}}, func(int64) bool { return true })
	if len(tracker.inFlightFree) == 0 {
		t.Fatal("inFlightFree len = 0, want released slice")
	}

	tracker.markSent(2, []eventualStateChange{{id: 3, mask: 0b100}})
	second := tracker.inFlight[2]
	if len(second) != 1 || second[0].id != 3 {
		t.Fatalf("reused in-flight changes = %+v, want id 3", second)
	}
	if cap(second) != cap(first) {
		t.Fatalf("reused cap = %d, want %d", cap(second), cap(first))
	}
}

func TestEventualStateTrackerSkipsRemovedEntitiesOnLoss(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirty([]int64{5})
	tracker.markSent(8, eventualFullChanges(5))
	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 8, Delivered: false}}, func(int64) bool { return false })

	if got := tracker.dirtyIDs(); len(got) != 0 {
		t.Fatalf("dirtyIDs after removed loss = %v, want empty", got)
	}
	if len(tracker.inFlightFree) == 0 {
		t.Fatal("inFlightFree len = 0, want released lost slice")
	}
}

func TestEventualStateTrackerMergesDirtyMasks(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirtyMask(7, 0b001)
	tracker.markDirtyMask(7, 0b100)

	changes := tracker.dirtyChangesInto(nil)
	if len(changes) != 1 {
		t.Fatalf("dirty changes len = %d, want 1", len(changes))
	}
	if got, want := changes[0].mask, uint64(0b101); got != want {
		t.Fatalf("dirty mask = %03b, want %03b", got, want)
	}
}

func TestEventualStateTrackerLossRequeuesRemainingMask(t *testing.T) {
	tracker := newEventualStateTracker()
	old := []eventualStateChange{{id: 1, mask: 0b111}}
	newer := []eventualStateChange{{id: 1, mask: 0b010}}
	tracker.markDirtyChange(old[0])
	tracker.markSent(10, old)
	tracker.markDirtyChange(newer[0])
	tracker.markSent(11, newer)

	tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: 10, Delivered: false}}, func(int64) bool { return true })

	changes := tracker.dirtyChangesInto(nil)
	if len(changes) != 1 {
		t.Fatalf("dirty changes len = %d, want 1", len(changes))
	}
	if got, want := changes[0].mask, uint64(0b101); got != want {
		t.Fatalf("requeued mask = %03b, want %03b", got, want)
	}
}

func TestEventualStateTrackerClearEntityRemovesDirtyAndInFlight(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markDirtyMask(1, 0b001)
	tracker.markDirtyMask(2, 0b010)
	tracker.markSent(10, []eventualStateChange{{id: 2, mask: 0b010}})
	tracker.clearEntity(2)

	if got := tracker.dirtyIDs(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("dirty IDs = %v, want [1]", got)
	}
	if len(tracker.inFlight) != 0 {
		t.Fatalf("inFlight len = %d, want 0", len(tracker.inFlight))
	}
	if len(tracker.inFlightFree) == 0 {
		t.Fatal("inFlightFree len = 0, want released cleared slice")
	}
}

func TestEventualStateTrackerClearEntityKeepsPartialInFlightSlice(t *testing.T) {
	tracker := newEventualStateTracker()
	tracker.markSent(10, []eventualStateChange{
		{id: 1, mask: 0b001},
		{id: 2, mask: 0b010},
		{id: 3, mask: 0b100},
	})
	beforeCap := cap(tracker.inFlight[10])
	tracker.clearEntity(2)

	changes := tracker.inFlight[10]
	if len(changes) != 2 {
		t.Fatalf("in-flight len = %d, want 2", len(changes))
	}
	if changes[0].id != 1 || changes[1].id != 3 {
		t.Fatalf("in-flight changes = %+v, want ids [1 3]", changes)
	}
	if cap(changes) != beforeCap {
		t.Fatalf("in-flight cap = %d, want %d", cap(changes), beforeCap)
	}
	if len(tracker.inFlightFree) != 0 {
		t.Fatalf("inFlightFree len = %d, want 0 for partial clear", len(tracker.inFlightFree))
	}
}

func TestEventualStateFrameUsesMaskAwareDelta(t *testing.T) {
	srv := NewServer(ServerConfig{})
	entity := &maskAwareEventualEntity{
		id:            1,
		lastFlushMask: 0b101,
		delta:         []byte("selected-current-delta"),
		full:          []byte("full-state"),
	}
	if err := srv.CreateEntity(entity); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	change := srv.eventualChangeForDelta(entity.id)
	if change.full {
		t.Fatal("eventualChangeForDelta selected full state, want mask-aware delta")
	}
	if got, want := change.mask, entity.lastFlushMask; got != want {
		t.Fatalf("eventual mask = %b, want %b", got, want)
	}

	frame, live, err := newEventualStateTickCache().frame(srv, change)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if !live {
		t.Fatal("frame live = false, want true")
	}
	payloads := decodeWrappedEntityBatch(t, frame.stream)
	if len(payloads) != 1 {
		t.Fatalf("wrapped payload count = %d, want 1", len(payloads))
	}
	if !bytes.Equal(payloads[0], entity.delta) {
		t.Fatalf("eventual payload = %q, want %q", payloads[0], entity.delta)
	}
	if entity.deltaCalls != 1 {
		t.Fatalf("delta calls = %d, want 1", entity.deltaCalls)
	}
	if entity.fullCalls != 0 {
		t.Fatalf("full calls = %d, want 0", entity.fullCalls)
	}
}

func TestNonEventualLanesDoNotUseMaskAwareDeltaAPI(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ServerConfig
	}{
		{name: "stream", cfg: ServerConfig{StateUpdateLane: StateUpdateLaneStream}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(tc.cfg)
			entity := &maskAwareEventualEntity{
				id:            1,
				lastFlushMask: 0b001,
				delta:         []byte("flush-delta"),
				full:          []byte("full-state"),
			}
			if err := srv.CreateEntity(entity); err != nil {
				t.Fatalf("CreateEntity: %v", err)
			}
			if err := srv.runBroadcastTick(); err != nil {
				t.Fatalf("runBroadcastTick spawn: %v", err)
			}
			if entity.deltaCalls != 0 {
				t.Fatalf("delta mask calls after spawn = %d, want 0", entity.deltaCalls)
			}
			if err := srv.runBroadcastTick(); err != nil {
				t.Fatalf("runBroadcastTick delta: %v", err)
			}
			if entity.flushCalls == 0 {
				t.Fatal("FlushUpdate was not called")
			}
			if entity.deltaCalls != 0 {
				t.Fatalf("delta mask calls = %d, want 0", entity.deltaCalls)
			}
		})
	}
}
