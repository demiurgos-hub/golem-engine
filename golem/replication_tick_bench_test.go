package golem

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

// benchmarkSpaces selects how a replication benchmark uses spaces.
type benchmarkSpaces uint8

const (
	benchNoSpaces benchmarkSpaces = iota
	// benchSpacesActive keeps every session in space 0 but places one entity
	// in space 1, so space bookkeeping is active.
	benchSpacesActive
	// benchTwoSpaces splits entities and sessions between spaces 0 and 1.
	benchTwoSpaces
)

// benchmarkReplicationTick measures one full replication pass with connected
// WebSocket sessions and every entity changing each tick. It covers the
// single-space paths (blind broadcast, filtered broadcast and interest) so the
// cost of space bookkeeping on games that never use spaces stays visible, and
// the same passes with spaces active or split in two.
func benchmarkReplicationTick(b *testing.B, cellSize float64, grouped bool, spaces benchmarkSpaces) {
	const (
		sessionCount = 8
		entityCount  = 200
	)
	log.SetOutput(io.Discard)
	s := NewServer(ServerConfig{Transport: golemnet.TransportWebSocket, StateUpdateLane: StateUpdateLaneStream, CellSize: cellSize})
	s.SetRemovalSerializer(func(id int64, _ uint64) ([]byte, error) { return []byte("removed"), nil })
	httpServer := httptest.NewServer(s.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	// Registered first, so it runs after the connections close: restore
	// logging only once every session has gone.
	b.Cleanup(func() {
		cancel()
		httpServer.Close()
		for deadline := time.Now().Add(2 * time.Second); len(s.listener.SessionIDs()) > 0 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		log.SetOutput(os.Stderr)
	})
	for i := 0; i < sessionCount; i++ {
		conn, _, err := websocket.Dial(ctx, "ws"+httpServer.URL[4:], nil)
		if err != nil {
			b.Fatal(err)
		}
		conn.SetReadLimit(1 << 20)
		b.Cleanup(func() { conn.CloseNow() })
		go func() {
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()
	}
	sessions := waitForSessionIDs(b, s, sessionCount)
	entities := make([]*interestTickEntity, entityCount)
	for i := range entities {
		e := &interestTickEntity{id: int64(i + 1), x: float64(i % 20), y: float64(i / 20), full: []byte(fmt.Sprintf("full-%d", i)), flush: []byte("delta")}
		entities[i] = e
		space := SpaceID(0)
		switch {
		case spaces == benchSpacesActive && i == entityCount-1:
			space = 1
		case spaces == benchTwoSpaces && i%2 == 1:
			space = 1
		}
		if err := s.CreateEntityIn(space, e); err != nil {
			b.Fatal(err)
		}
	}
	if grouped {
		s.SetEntityVisibilityGroup(entities[0].id, "g")
		for _, sid := range sessions {
			s.JoinVisibilityGroup(sid, "g")
		}
	}
	run := s.runBroadcastTick
	if cellSize > 0 {
		run = s.runInterestTick
		// With two spaces, odd sessions anchor to odd entities in space 1.
		for i, sid := range sessions {
			s.AssignFOI(sid, entities[i].id, 12, 2)
		}
	} else if spaces == benchTwoSpaces {
		for i, sid := range sessions {
			if i%2 == 1 {
				s.SetSessionSpace(sid, 1)
			}
		}
	}
	for i := 0; i < 3; i++ {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicationTickSingleSpace(b *testing.B) {
	b.Run("broadcast_blind", func(b *testing.B) { benchmarkReplicationTick(b, 0, false, benchNoSpaces) })
	b.Run("broadcast_filtered", func(b *testing.B) { benchmarkReplicationTick(b, 0, true, benchNoSpaces) })
	b.Run("interest", func(b *testing.B) { benchmarkReplicationTick(b, 8, false, benchNoSpaces) })
	b.Run("broadcast_spaces_active", func(b *testing.B) { benchmarkReplicationTick(b, 0, false, benchSpacesActive) })
	b.Run("broadcast_two_spaces", func(b *testing.B) { benchmarkReplicationTick(b, 0, false, benchTwoSpaces) })
	b.Run("interest_two_spaces", func(b *testing.B) { benchmarkReplicationTick(b, 8, false, benchTwoSpaces) })
}
