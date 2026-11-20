package golem

import (
	"reflect"
	"sync"
	"testing"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

func newObserverTestServer(t *testing.T, cellSize float64) *Server {
	t.Helper()
	srv := NewServer(ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream, CellSize: cellSize})
	srv.SetRemovalSerializer(func(entityID int64, _ uint64) ([]byte, error) { return []byte{byte(entityID)}, nil })
	return srv
}

func TestObserveReplicationDeliversFlushedEntities(t *testing.T) {
	for _, mode := range []struct {
		name     string
		cellSize float64
		tick     func(*Server) error
	}{
		{"broadcast", 0, (*Server).runBroadcastTick},
		{"interest", 4, (*Server).runInterestTick},
	} {
		t.Run(mode.name, func(t *testing.T) {
			srv := newObserverTestServer(t, mode.cellSize)
			a := &interestTickEntity{id: 1, full: []byte("a"), revision: 2}
			b := &interestTickEntity{id: 2, full: []byte("b")}
			for _, e := range []*interestTickEntity{a, b} {
				if err := srv.CreateEntity(e); err != nil {
					t.Fatal(err)
				}
			}
			var frames []ReplicationFrame
			var masks []uint64
			remove := srv.ObserveReplication(func(f ReplicationFrame) {
				frames = append(frames, ReplicationFrame{
					Tick:    f.Tick,
					Spawned: append([]int64(nil), f.Spawned...),
					Changed: append([]int64(nil), f.Changed...),
					Removed: append([]RemovedEntity(nil), f.Removed...),
				})
				for _, id := range f.Changed {
					e, _ := srv.Get(id)
					masks = append(masks, e.(*interestTickEntity).LastFlushMask())
				}
			})
			second := 0
			removeSecond := srv.ObserveReplication(func(ReplicationFrame) { second++ })

			srv.tick = 1
			if err := mode.tick(srv); err != nil {
				t.Fatal(err)
			}
			a.flush = []byte("delta")
			srv.tick = 2
			if err := mode.tick(srv); err != nil {
				t.Fatal(err)
			}
			a.flush = nil
			srv.DeleteEntity(b.id)
			removeSecond()
			srv.tick = 3
			if err := mode.tick(srv); err != nil {
				t.Fatal(err)
			}
			remove()
			remove() // idempotent
			srv.tick = 4
			if err := mode.tick(srv); err != nil {
				t.Fatal(err)
			}

			want := []ReplicationFrame{
				{Tick: 1, Spawned: []int64{1, 2}, Changed: []int64{}, Removed: []RemovedEntity{}},
				{Tick: 2, Spawned: []int64{}, Changed: []int64{1}, Removed: []RemovedEntity{}},
				{Tick: 3, Spawned: []int64{}, Changed: []int64{}, Removed: []RemovedEntity{{ID: 2, Revision: 1}}},
			}
			normalize := func(fs []ReplicationFrame) {
				for i := range fs {
					if fs[i].Spawned == nil {
						fs[i].Spawned = []int64{}
					}
					if fs[i].Changed == nil {
						fs[i].Changed = []int64{}
					}
					if fs[i].Removed == nil {
						fs[i].Removed = []RemovedEntity{}
					}
				}
			}
			normalize(frames)
			if len(frames) == 2 || len(frames) == 3 {
				// Spawn order follows registry iteration, not insertion.
				if len(frames[0].Spawned) == 2 && frames[0].Spawned[0] == 2 {
					frames[0].Spawned[0], frames[0].Spawned[1] = 1, 2
				}
			}
			if !reflect.DeepEqual(frames, want) {
				t.Fatalf("frames = %+v\nwant %+v", frames, want)
			}
			if !reflect.DeepEqual(masks, []uint64{1}) {
				t.Fatalf("LastFlushMask inside observer = %v, want [1]", masks)
			}
			if second != 2 {
				t.Fatalf("second observer calls = %d, want 2", second)
			}
		})
	}
}

func TestObserveReplicationConcurrentRegistration(t *testing.T) {
	srv := newObserverTestServer(t, 0)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remove := srv.ObserveReplication(func(ReplicationFrame) {})
			remove()
		}()
	}
	for i := 0; i < 50; i++ {
		if err := srv.runBroadcastTick(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if p := srv.replicationObservers.list.Load(); p != nil && len(*p) != 0 {
		t.Fatalf("observers left registered: %d", len(*p))
	}
	if srv.ObserveReplication(nil) == nil {
		t.Fatal("nil observer must return a no-op remover")
	}
}
