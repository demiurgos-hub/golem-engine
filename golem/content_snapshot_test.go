package golem

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

func TestContentSnapshotPinsAndFreezesState(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &snapshotTestEntity{id: 5, typeName: "Monster", state: []byte{37, 2}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "save.json")
	hash := strings.Repeat("a", 64)
	done := s.SaveContentSnapshot("schema", hash, path)
	e.state[0] = 99 // Capture must already be complete; the worker owns bytes only.
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r := NewServer(ServerConfig{})
	calls := 0
	err := r.LoadContentSnapshot(path, "schema", hash, func(rec snapshot.Record) (Entity, error) {
		calls++
		if calls == 1 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		return &snapshotTestEntity{id: rec.EntityID, typeName: rec.TypeName, state: rec.StateData}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(5)
	if !bytes.Equal(got.(*snapshotTestEntity).state, []byte{37, 2}) {
		t.Fatal("restore applied changed/default values")
	}
}

func TestContentSnapshotRejectsMismatchBeforeCallbacks(t *testing.T) {
	for _, kind := range []string{"content", "schema", "data", "path", "definition"} {
		t.Run(kind, func(t *testing.T) {
			s := NewServer(ServerConfig{})
			if err := s.CreateEntity(&snapshotTestEntity{id: 1, typeName: "Monster", state: []byte{4}}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "save.json")
			hash := strings.Repeat("a", 64)
			if err := <-s.SaveContentSnapshot("schema", hash, path); err != nil {
				t.Fatal(err)
			}
			fingerprint := "schema"
			var manifest contentSnapshotManifest
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "content":
				hash = strings.Repeat("b", 64)
			case "schema":
				fingerprint = "other"
			case "data":
				if err = os.WriteFile(filepath.Join(filepath.Dir(path), manifest.Snapshot), []byte("corrupt"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "path":
				manifest.Snapshot = "../outside"
				data, _ = json.Marshal(manifest)
				if err = os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := NewServer(ServerConfig{})
			calls := 0
			err = r.LoadContentSnapshot(path, fingerprint, hash, func(rec snapshot.Record) (Entity, error) {
				calls++
				if kind != "definition" {
					t.Error("invalid manifest reached callback")
				}
				return nil, errors.New("unknown definition")
			})
			if err == nil || r.Len() != 0 {
				t.Fatal("invalid restore published", err)
			}
			if kind == "definition" && calls != 1 {
				t.Fatal("definition not validated")
			}
		})
	}
}

func TestContentSnapshotPreservesPreviousGeneration(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &snapshotTestEntity{id: 1, typeName: "Monster", state: []byte{4}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "save.json")
	hash := strings.Repeat("a", 64)
	if err := <-s.SaveContentSnapshot("schema", hash, path); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-s.SaveContentSnapshot("schema", "bad", path); err == nil {
		t.Fatal("invalid pin accepted")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(old, current) {
		t.Fatal("failure changed previous manifest")
	}
	e.state[0] = 8
	if err := <-s.SaveContentSnapshot("schema", hash, path); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "content-*.snapshot"))
	if err != nil || len(files) != 2 {
		t.Fatal("previous generation discarded", files, err)
	}
	if err = s.SetPhysicsWorld(&physicsTestWorld{}); err != nil {
		t.Fatal(err)
	}
	if err = <-s.SaveContentSnapshot("schema", hash, path); err == nil {
		t.Fatal("physics world accepted entity-only save")
	}
}
