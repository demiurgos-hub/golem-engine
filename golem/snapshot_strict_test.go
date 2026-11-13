package golem

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

func saveStrictTestSnapshot(t *testing.T, source ...registry.Entity) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "strict.snap")
	if err := <-snapshot.Save(source, "strict-fingerprint", path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSnapshotStrictPreservesStateWithoutSpawnAndReadsOnce(t *testing.T) {
	path := saveStrictTestSnapshot(t,
		&snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{13, 2}},
		&snapshotTestEntity{id: 9, typeName: "Monster", state: []byte{3, 7}})
	s := NewServer(ServerConfig{})
	s.SetEntityIDCounter(30)
	calls := 0
	err := s.LoadSnapshotStrict(path, "strict-fingerprint", func(rec snapshot.Record) (Entity, error) {
		calls++
		if s.Len() != 0 {
			t.Fatal("restore published during preflight")
		}
		if calls == 1 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		e := &setupTestEntity{snapshotTestEntity: snapshotTestEntity{id: rec.EntityID, typeName: rec.TypeName, state: append([]byte(nil), rec.StateData...)}}
		e.spawn = func() { e.state[0] = 100; t.Error("ordinary spawn reset restored state") }
		return e, nil
	})
	if err != nil || calls != 2 || s.Len() != 2 {
		t.Fatalf("strict load: %v, calls %d, entities %d", err, calls, s.Len())
	}
	for id, want := range map[int64][]byte{4: {13, 2}, 9: {3, 7}} {
		entity, ok := s.Get(id)
		if !ok {
			t.Fatal("restored entity missing")
		}
		e := entity.(*setupTestEntity)
		if e.srv != s || !bytes.Equal(e.state, want) {
			t.Fatalf("restored identity/state: %+v", e)
		}
	}
	if got := s.ReserveEntityID(); got != 31 {
		t.Fatalf("counter moved backward: %d", got)
	}
	flush, err := s.reg.FlushAll()
	if err != nil || len(flush.Spawns) != 2 || len(flush.Removals) != 0 {
		t.Fatalf("restored publication: %+v, %v", flush, err)
	}
}

func TestLoadSnapshotStrictRejectsRestoreFailuresWithoutPartialPublication(t *testing.T) {
	missing := errors.New("required monster definition missing")
	for _, scenario := range []string{"missing-definition", "nil", "typed-nil", "changed-id", "changed-type"} {
		t.Run(scenario, func(t *testing.T) {
			path := saveStrictTestSnapshot(t,
				&snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{1}},
				&snapshotTestEntity{id: 9, typeName: "Monster", state: []byte{2}})
			s := NewServer(ServerConfig{})
			existing := &snapshotTestEntity{id: 2, typeName: "Existing", state: []byte{7}}
			if err := s.CreateEntity(existing); err != nil {
				t.Fatal(err)
			}
			if _, err := s.reg.FlushAll(); err != nil {
				t.Fatal(err)
			}
			err := s.LoadSnapshotStrict(path, "strict-fingerprint", func(rec snapshot.Record) (Entity, error) {
				if rec.EntityID == 9 {
					switch scenario {
					case "missing-definition":
						return nil, missing
					case "nil":
						return nil, nil
					case "typed-nil":
						var nilEntity *snapshotTestEntity
						return nilEntity, nil
					case "changed-id":
						rec.EntityID++
					case "changed-type":
						rec.TypeName = "Other"
					}
				}
				return &snapshotTestEntity{id: rec.EntityID, typeName: rec.TypeName, state: rec.StateData}, nil
			})
			if err == nil || scenario == "missing-definition" && !errors.Is(err, missing) {
				t.Fatalf("failure not propagated: %v", err)
			}
			if s.Len() != 1 {
				t.Fatal("failed restore partially published")
			}
			if got, ok := s.Get(2); !ok || got != existing {
				t.Fatal("failed restore changed existing entity")
			}
			for _, id := range []int64{4, 9} {
				if _, ok := s.Get(id); ok {
					t.Fatal("failed restore retained staged entity")
				}
			}
			flush, flushErr := s.reg.FlushAll()
			if flushErr != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
				t.Fatalf("failed restore output: %+v, %v", flush, flushErr)
			}
		})
	}
}

func TestLoadSnapshotStrictRejectsInvalidRecordIdentitiesBeforeCallbacks(t *testing.T) {
	for _, scenario := range []string{"duplicate", "zero", "negative", "empty-type", "existing"} {
		t.Run(scenario, func(t *testing.T) {
			first := &snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{1}}
			second := &snapshotTestEntity{id: 9, typeName: "Monster", state: []byte{2}}
			s := NewServer(ServerConfig{})
			switch scenario {
			case "duplicate":
				second.id = first.id
			case "zero":
				second.id = 0
			case "negative":
				second.id = -1
			case "empty-type":
				second.typeName = ""
			case "existing":
				if err := s.CreateEntity(second); err != nil {
					t.Fatal(err)
				}
			}
			path := saveStrictTestSnapshot(t, first, second)
			err := s.LoadSnapshotStrict(path, "strict-fingerprint", func(snapshot.Record) (Entity, error) {
				t.Fatal("invalid records reached restore callback")
				return nil, nil
			})
			if err == nil {
				t.Fatal("invalid identities accepted")
			}
			if _, ok := s.Get(first.id); ok {
				t.Fatal("first entity was partially published")
			}
		})
	}
}

func TestLoadSnapshotStrictRequiresEntityOnlyStartup(t *testing.T) {
	s := NewServer(ServerConfig{})
	if err := s.SetPhysicsWorld(&physicsTestWorld{}); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadSnapshotStrict("unused", "", func(snapshot.Record) (Entity, error) { t.Fatal("physics restore callback called"); return nil, nil }); err == nil {
		t.Fatal("entity-only restore admitted configured physics")
	}
	s = NewServer(ServerConfig{})
	s.life = lifecycleRunning
	if err := s.LoadSnapshotStrict("unused", "", func(snapshot.Record) (Entity, error) { t.Fatal("running restore callback called"); return nil, nil }); err == nil {
		t.Fatal("strict restore admitted running server")
	}
}
