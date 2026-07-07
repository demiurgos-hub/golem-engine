package golem

import (
	"path/filepath"
	"testing"

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
