package golem

import (
	"bytes"
	"testing"

	golemnet "golem-engine/golem/net"
)

type benchmarkEventualEntity struct {
	id      int64
	payload []byte
}

func (e benchmarkEventualEntity) EntityID() int64              { return e.id }
func (e benchmarkEventualEntity) TypeName() string             { return "benchmark" }
func (e benchmarkEventualEntity) Position() (x, y float32)     { return 0, 0 }
func (e benchmarkEventualEntity) IsGlobal() bool               { return false }
func (e benchmarkEventualEntity) FlushUpdate() ([]byte, error) { return e.payload, nil }
func (e benchmarkEventualEntity) FullUpdate() ([]byte, error)  { return e.payload, nil }
func (e benchmarkEventualEntity) LastFlushMask() uint64        { return 1 }
func (e benchmarkEventualEntity) MarshalCompactDeltaMask(uint64) ([]byte, error) {
	return e.payload, nil
}

func BenchmarkSendEventualState(b *testing.B) {
	s := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	ids := make([]int64, 8)
	for i := range ids {
		id := int64(i + 1)
		ids[i] = id
		if err := s.reg.Add(benchmarkEventualEntity{
			id:      id,
			payload: bytes.Repeat([]byte{byte('a' + i)}, 96),
		}); err != nil {
			b.Fatal(err)
		}
	}
	tracker := newEventualStateTracker()
	cache := newEventualStateTickCache()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		clear(tracker.dirtySet)
		tracker.dirtyQueue = tracker.dirtyQueue[:0]
		tracker.markDirty(ids)
		batched, msgs, err := s.sendEventualState(-1, tracker, cache)
		if err != nil {
			b.Fatal(err)
		}
		if batched != 0 || msgs != 0 {
			b.Fatalf("sendEventualState disconnected result = %d/%d, want 0/0", batched, msgs)
		}
	}
}

func BenchmarkEventualStateDirtyIDsInto(b *testing.B) {
	tracker := newEventualStateTracker()
	ids := make([]int64, 512)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	tracker.markDirty(ids)
	dst := make([]int64, 0, len(ids))

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		got := tracker.dirtyIDsInto(dst[:0])
		if len(got) != len(ids) {
			b.Fatalf("dirtyIDs len = %d, want %d", len(got), len(ids))
		}
	}
}

func BenchmarkEventualStateTrackerMarkSentFeedback(b *testing.B) {
	tracker := newEventualStateTracker()
	changes := make([]eventualStateChange, 32)
	for i := range changes {
		changes[i] = eventualStateChange{id: int64(i + 1), mask: 1}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		token := uint64(i + 1)
		tracker.markSent(token, changes)
		tracker.applyFeedback([]golemnet.EventualStateDelivery{{Token: token, Delivered: true}}, func(int64) bool { return true })
	}
}

func BenchmarkSendEventualStateChangesDirect(b *testing.B) {
	s := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	changes := make([]eventualStateChange, 64)
	for i := range changes {
		id := int64(i + 1)
		changes[i] = eventualStateChange{id: id, mask: 1}
		if err := s.reg.Add(benchmarkEventualEntity{
			id:      id,
			payload: bytes.Repeat([]byte{byte('a' + i%26)}, 48),
		}); err != nil {
			b.Fatal(err)
		}
	}
	tracker := newEventualStateTracker()
	cache := newEventualStateTickCache()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		batched, msgs, err := s.sendEventualStateChanges(-1, tracker, cache, changes)
		if err != nil {
			b.Fatal(err)
		}
		if batched != 0 || msgs != 0 {
			b.Fatalf("sendEventualStateChanges disconnected result = %d/%d, want 0/0", batched, msgs)
		}
	}
}

func BenchmarkSendPreparedEventualStateFramesDirect(b *testing.B) {
	s := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebTransport,
		StateUpdateLane: StateUpdateLaneDatagram,
	})
	prepared := make([]eventualPreparedFrame, 64)
	for i := range prepared {
		id := int64(i + 1)
		if err := s.reg.Add(benchmarkEventualEntity{
			id:      id,
			payload: bytes.Repeat([]byte{byte('a' + i%26)}, 48),
		}); err != nil {
			b.Fatal(err)
		}
		ch := eventualStateChange{id: id, mask: 1}
		pf, err := s.eventualPreparedFrameForChange(ch)
		if err != nil {
			b.Fatal(err)
		}
		prepared[i] = pf
	}
	tracker := newEventualStateTracker()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		batched, msgs, err := s.sendPreparedEventualStateFrames(-1, tracker, prepared)
		if err != nil {
			b.Fatal(err)
		}
		if batched != 0 || msgs != 0 {
			b.Fatalf("sendPreparedEventualStateFrames disconnected result = %d/%d, want 0/0", batched, msgs)
		}
	}
}
