package golem

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

func entitySpaceOf(t *testing.T, s *Server, id int64) SpaceID {
	t.Helper()
	space, ok := s.EntitySpace(id)
	if !ok {
		t.Fatalf("entity %d has no space", id)
	}
	return space
}

func TestCreateEntityInAndEntitySpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	plain := &snapshotTestEntity{typeName: "Monster"}
	if err := s.CreateEntity(plain); err != nil {
		t.Fatal(err)
	}
	var spawnSpace SpaceID
	spaced := &setupTestEntity{snapshotTestEntity: snapshotTestEntity{typeName: "Monster"}}
	spaced.spawn = func() { spawnSpace, _ = s.EntitySpace(spaced.id) }
	if err := s.CreateEntityIn(3, spaced, 11); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, s, plain.id) != 0 || entitySpaceOf(t, s, spaced.id) != 3 || spawnSpace != 3 {
		t.Fatalf("spaces: plain %d, spaced %d, in OnSpawn %d", entitySpaceOf(t, s, plain.id), entitySpaceOf(t, s, spaced.id), spawnSpace)
	}
	if owner, ok := s.Owner(spaced.id); !ok || owner != 11 {
		t.Fatal("CreateEntityIn lost the owner")
	}
	if err := s.CreateEntityIn(4, &snapshotTestEntity{typeName: "Monster"}, 1, 2); err == nil {
		t.Fatal("CreateEntityIn accepted two owners")
	}
	if _, ok := s.EntitySpace(999); ok {
		t.Fatal("unknown entity resolved a space")
	}
	s.DeleteEntity(spaced.id)
	if s.reg.SpacesInUse() {
		t.Fatal("deleted entity kept its space")
	}
}

func TestCreateEntityWithSetupInResolvesProvisionalSpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &snapshotTestEntity{typeName: "Monster"}
	var setupSpace SpaceID
	var moveErr error
	err := s.CreateEntityWithSetupIn(5, e, func(Entity) (func() error, error) {
		setupSpace, _ = s.EntitySpace(e.id)
		moveErr = s.SetEntitySpace(e.id, 6)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if setupSpace != 5 || entitySpaceOf(t, s, e.id) != 5 {
		t.Fatalf("setup space %d, published space %d", setupSpace, entitySpaceOf(t, s, e.id))
	}
	if moveErr == nil {
		t.Fatal("SetEntitySpace moved a provisional entity")
	}

	failed := &snapshotTestEntity{typeName: "Monster"}
	failure := errors.New("setup failed")
	if err := s.CreateEntityWithSetupIn(7, failed, func(Entity) (func() error, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatalf("setup error = %v", err)
	}
	if _, ok := s.EntitySpace(failed.id); ok {
		t.Fatal("failed setup left a space")
	}
	s.DeleteEntity(e.id)
	if s.reg.SpacesInUse() {
		t.Fatal("space entries leaked")
	}
}

func TestSpawnAvatarInSpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &avatarEntity{}
	if err := s.SpawnAvatar(&Session{ID: 4}, e, AvatarOptions{Space: 9}); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, s, e.id) != 9 {
		t.Fatal("avatar not created in AvatarOptions.Space")
	}
	other := &avatarEntity{}
	if err := s.SpawnAvatar(&Session{ID: 5}, other, AvatarOptions{}); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, s, other.id) != 0 {
		t.Fatal("default avatar not in space 0")
	}
}

func TestSetEntitySpaceGuards(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &snapshotTestEntity{typeName: "Monster"}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEntitySpace(999, 1); err == nil {
		t.Fatal("unknown entity moved")
	}
	if err := s.SetEntitySpace(e.id, 2); err != nil || entitySpaceOf(t, s, e.id) != 2 {
		t.Fatalf("move: %v", err)
	}
	if err := s.SetEntitySpace(e.id, 2); err != nil {
		t.Fatalf("same-space move: %v", err)
	}
	s.life = lifecycleStopped
	if err := s.SetEntitySpace(e.id, 3); !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("stopped server move = %v", err)
	}
}

func TestSetEntitySpaceRejectsPhysicsAndNavigation(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 3} {
		if err := s.CreateEntity(&navigationTestEntity{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	physicsTestAttach(t, s, w, 1, p.FreeDynamic, PhysicsAttachmentConfig{})
	if err := s.AttachNavigation(2, NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEntitySpace(1, 4); err == nil || !strings.Contains(err.Error(), "physics") {
		t.Fatalf("physics-attached move = %v", err)
	}
	if err := s.SetEntitySpace(2, 4); err == nil || !strings.Contains(err.Error(), "navigation") {
		t.Fatalf("navigation-attached move = %v", err)
	}
	if entitySpaceOf(t, s, 1) != 0 || entitySpaceOf(t, s, 2) != 0 {
		t.Fatal("rejected move changed a space")
	}
	if err := s.SetEntitySpace(3, 4); err != nil {
		t.Fatalf("plain entity move beside physics: %v", err)
	}
	s.physics.stepping = true
	err := s.SetEntitySpace(3, 5)
	s.physics.stepping = false
	if !errors.Is(err, p.ErrBusy) {
		t.Fatalf("move while stepping = %v", err)
	}
}

func TestReplicationFrameMoved(t *testing.T) {
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
			for id := int64(1); id <= 3; id++ {
				if err := srv.CreateEntity(&interestTickEntity{id: id, full: []byte{byte(id)}}); err != nil {
					t.Fatal(err)
				}
			}
			var moved [][]EntitySpaceMove
			defer srv.ObserveReplication(func(f ReplicationFrame) {
				moved = append(moved, append([]EntitySpaceMove(nil), f.Moved...))
			})()
			step := func() {
				t.Helper()
				srv.tick++
				if err := mode.tick(srv); err != nil {
					t.Fatal(err)
				}
			}
			step()
			for _, move := range []struct {
				id    int64
				space SpaceID
			}{{2, 4}, {1, 3}, {2, 5}, {3, 6}, {3, 0}} {
				if err := srv.SetEntitySpace(move.id, move.space); err != nil {
					t.Fatal(err)
				}
			}
			step()
			step()
			if err := srv.SetEntitySpace(1, 0); err != nil {
				t.Fatal(err)
			}
			step()
			want := [][]EntitySpaceMove{
				nil,
				{{ID: 2, From: 0, To: 5}, {ID: 1, From: 0, To: 3}},
				nil,
				{{ID: 1, From: 3, To: 0}},
			}
			if !reflect.DeepEqual(moved, want) {
				t.Fatalf("moved = %v, want %v", moved, want)
			}
		})
	}
}

func spacedSnapshotServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(ServerConfig{})
	if err := s.CreateEntity(&snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEntityIn(7, &snapshotTestEntity{id: 9, typeName: "Monster", state: []byte{2}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func restoreSnapshotTestEntity(rec snapshot.Record) (Entity, error) {
	return &snapshotTestEntity{id: rec.EntityID, typeName: rec.TypeName, state: rec.StateData}, nil
}

func snapshotFileVersion(t *testing.T, path string) byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data[4]
}

func TestSaveSnapshotKeepsVersion1WithoutSpaces(t *testing.T) {
	s := NewServer(ServerConfig{})
	if err := s.CreateEntity(&snapshotTestEntity{id: 4, typeName: "Monster", state: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "world.snap")
	if err := <-s.SaveSnapshot("fp", path); err != nil {
		t.Fatal(err)
	}
	if v := snapshotFileVersion(t, path); v != 1 {
		t.Fatalf("space-0 snapshot version = %d, want 1", v)
	}
}

func TestLoadSnapshotRestoresSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.snap")
	if err := <-spacedSnapshotServer(t).SaveSnapshot("fp", path); err != nil {
		t.Fatal(err)
	}
	if v := snapshotFileVersion(t, path); v != 2 {
		t.Fatalf("spaced snapshot version = %d, want 2", v)
	}
	r := NewServer(ServerConfig{})
	if err := r.LoadSnapshot(path, "fp", restoreSnapshotTestEntity); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, r, 4) != 0 || entitySpaceOf(t, r, 9) != 7 {
		t.Fatal("LoadSnapshot did not restore spaces")
	}
}

func TestLoadSnapshotStrictRestoresSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.snap")
	if err := <-spacedSnapshotServer(t).SaveSnapshot("fp", path); err != nil {
		t.Fatal(err)
	}
	r := NewServer(ServerConfig{})
	if err := r.LoadSnapshotStrict(path, "fp", restoreSnapshotTestEntity); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, r, 4) != 0 || entitySpaceOf(t, r, 9) != 7 {
		t.Fatal("LoadSnapshotStrict did not restore spaces")
	}
	flush, err := r.reg.FlushAll()
	if err != nil || len(flush.SpawnIDs) != 2 || flush.Moves != nil {
		t.Fatalf("strict restore flush: %+v, %v", flush, err)
	}
}

func TestContentSnapshotRestoresSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.json")
	hash := strings.Repeat("b", 64)
	if err := <-spacedSnapshotServer(t).SaveContentSnapshot("fp", hash, path); err != nil {
		t.Fatal(err)
	}
	r := NewServer(ServerConfig{})
	if err := r.LoadContentSnapshot(path, "fp", hash, restoreSnapshotTestEntity); err != nil {
		t.Fatal(err)
	}
	if entitySpaceOf(t, r, 9) != 7 {
		t.Fatal("content snapshot did not restore spaces")
	}
}

func TestPhysicsCheckpointOmitsSpaceZero(t *testing.T) {
	_, _, _, c := physicsCheckpointFixture(t)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"Space"`)) {
		t.Fatal("space-0 checkpoint entity recorded a space")
	}
}

func TestSpaceZeroRuntimeAliasesPhysicsAndNavigation(t *testing.T) {
	s := NewServer(ServerConfig{})
	if err := s.SetPhysicsWorld(&physicsTestWorld{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	if s.space0.physics == nil || s.space0.physics != s.physics || s.space0.navigation != s.navigation || s.navigation == nil {
		t.Fatal("Server.physics / Server.navigation must alias space 0's runtime")
	}
	if s.runtimeOf(42) != &s.space0 {
		t.Fatal("unknown entity must resolve to space 0's runtime")
	}
}
