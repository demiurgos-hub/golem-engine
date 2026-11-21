package golem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

// snapshotTestEntity is a minimal entity used by snapshot API tests.
type snapshotTestEntity struct {
	id       int64
	typeName string
	state    []byte
}

func (e *snapshotTestEntity) EntityID() int64              { return e.id }
func (e *snapshotTestEntity) SetEntityID(id int64)         { e.id = id }
func (e *snapshotTestEntity) TypeName() string             { return e.typeName }
func (e *snapshotTestEntity) Position() (float32, float32) { return 0, 0 }
func (e *snapshotTestEntity) IsGlobal() bool               { return false }
func (e *snapshotTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *snapshotTestEntity) FullUpdate() ([]byte, error)  { return e.state, nil }

// wrappedSnapshotTestEntity mimics a gameplay wrapper around a restored entity.
type wrappedSnapshotTestEntity struct {
	*snapshotTestEntity
}

// TestLoadSnapshotAcceptsWrappedRestoreEntitiesAndAdvancesIDCounter verifies
// that LoadSnapshot accepts wrapped entities from restore callbacks and seeds
// the server's next auto-assigned ID from the highest restored entity ID.
func TestLoadSnapshotAcceptsWrappedRestoreEntitiesAndAdvancesIDCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.snap")
	const fingerprint = "test-fingerprint"

	source := []registry.Entity{
		&snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{0x01}},
		&snapshotTestEntity{id: 9, typeName: "Player", state: []byte{0x02}},
	}
	if err := <-snapshot.Save(source, fingerprint, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	srv := NewServer(ServerConfig{})
	restoreCalls := 0
	err := srv.LoadSnapshot(path, fingerprint, func(rec snapshot.Record) (Entity, error) {
		restoreCalls++
		base := &snapshotTestEntity{id: rec.EntityID, typeName: rec.TypeName}
		if rec.TypeName == "Monster" {
			return &wrappedSnapshotTestEntity{snapshotTestEntity: base}, nil
		}
		return base, nil
	})
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	if restoreCalls != 2 {
		t.Fatalf("restoreCalls = %d, want 2", restoreCalls)
	}

	gotMonster, ok := srv.Get(4)
	if !ok {
		t.Fatal("Get(4) = not found, want restored Monster")
	}
	if _, ok := gotMonster.(*wrappedSnapshotTestEntity); !ok {
		t.Fatalf("Get(4) type = %T, want *wrappedSnapshotTestEntity", gotMonster)
	}

	gotPlayer, ok := srv.Get(9)
	if !ok {
		t.Fatal("Get(9) = not found, want restored Player")
	}
	if _, ok := gotPlayer.(*snapshotTestEntity); !ok {
		t.Fatalf("Get(9) type = %T, want *snapshotTestEntity", gotPlayer)
	}

	next := &snapshotTestEntity{typeName: "SpawnedLater"}
	if err := srv.CreateEntity(next); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if next.EntityID() != 10 {
		t.Fatalf("next EntityID = %d, want 10", next.EntityID())
	}
}

// blockingSnapshotEntity blocks its first FullUpdate until release closes;
// later calls return at once.
type blockingSnapshotEntity struct {
	snapshotTestEntity
	started atomic.Bool
	entered chan struct{}
	release chan struct{}
	srv     *Server
	postErr error
}

func (e *blockingSnapshotEntity) FullUpdate() ([]byte, error) {
	if e.started.CompareAndSwap(false, true) {
		close(e.entered)
		<-e.release
		// Entity code may use the server while Run waits for the capture.
		e.postErr = e.srv.Post(func(*Server) {})
	}
	return e.state, nil
}

// TestPreRunSnapshotCaptureHoldsFirstTick starts a snapshot capture before
// Run, blocks it inside FullUpdate, and starts Run: Run must accept work but
// not tick until the capture finishes, and then run normally.
func TestPreRunSnapshotCaptureHoldsFirstTick(t *testing.T) {
	saves := map[string]func(*Server, string) <-chan error{
		"SaveSnapshot": func(s *Server, dir string) <-chan error { return s.SaveSnapshot("fp", filepath.Join(dir, "a.snap")) },
		"SaveSnapshotOnTick": func(s *Server, dir string) <-chan error {
			return s.SaveSnapshotOnTick("fp", filepath.Join(dir, "a.snap"))
		},
		"SaveContentSnapshot": func(s *Server, dir string) <-chan error {
			return s.SaveContentSnapshot("fp", strings.Repeat("a", 64), filepath.Join(dir, "manifest.json"))
		},
	}
	for name, save := range saves {
		t.Run(name, func(t *testing.T) {
			s := NewServer(ServerConfig{TickRate: 200})
			e := &blockingSnapshotEntity{
				snapshotTestEntity: snapshotTestEntity{id: 1, typeName: "Monster", state: []byte{4}},
				entered:            make(chan struct{}), release: make(chan struct{}), srv: s,
			}
			if err := s.CreateEntity(e); err != nil {
				t.Fatal(err)
			}
			var ticks atomic.Int64
			s.OnTickStart(func(uint64) { ticks.Add(1) })
			dir := t.TempDir()
			saved := make(chan (<-chan error), 1)
			go func() { saved <- save(s, dir) }()
			<-e.entered

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- s.Run(ctx) }()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(e.release) }) }
			defer func() { release(); cancel(); <-runDone }()
			// Run has linearized lifecycleRunning once Post is accepted.
			deadline := time.Now().Add(5 * time.Second)
			for s.Post(func(*Server) {}) != nil {
				if time.Now().After(deadline) {
					t.Fatal("Run did not start")
				}
				time.Sleep(time.Millisecond)
			}
			// Many tick intervals pass without a tick while the capture runs.
			time.Sleep(60 * time.Millisecond)
			if n := ticks.Load(); n != 0 {
				t.Fatalf("%d ticks ran during the pre-Run capture", n)
			}
			release()
			if err := <-<-saved; err != nil {
				t.Fatal(err)
			}
			if e.postErr != nil {
				t.Fatalf("Post from FullUpdate: %v", e.postErr)
			}
			for ticks.Load() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("no tick after the capture finished")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

// TestSaveSnapshotOnTickSameTickSamePath queues two requests for one path in
// the same tick; both complete and the file holds the second (distinguished
// by its fingerprint).
func TestSaveSnapshotOnTickSameTickSamePath(t *testing.T) {
	s := NewServer(ServerConfig{TickRate: 200})
	if err := s.CreateEntity(&snapshotTestEntity{id: 4, typeName: "Monster", state: bytes.Repeat([]byte{1}, 1<<20)}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "world.snap")
	requests := make(chan [2]<-chan error, 1)
	var once sync.Once
	s.OnTick(func(float64, *Server) {
		once.Do(func() {
			requests <- [2]<-chan error{s.SaveSnapshotOnTick("first", path), s.SaveSnapshotOnTick("second", path)}
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()
	defer func() { cancel(); <-runDone }()
	pair := <-requests
	for i, errc := range pair {
		if err := <-errc; err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Decode(data, "second"); err != nil {
		t.Fatalf("file does not hold the second request: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the snapshot", len(entries))
	}
}
