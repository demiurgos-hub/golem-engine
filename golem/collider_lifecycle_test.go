package golem

import (
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

type recordingBackend2D struct {
	added   map[int64]collision.Shape
	removed []int64
}

func newRecordingBackend2D() *recordingBackend2D {
	return &recordingBackend2D{added: make(map[int64]collision.Shape)}
}
func (b *recordingBackend2D) Add(id int64, shape collision.Shape, _, _ uint32, _ bool) {
	b.added[id] = shape
}
func (b *recordingBackend2D) Set(id int64, shape collision.Shape, _, _ uint32, _ bool) {
	b.added[id] = shape
}
func (b *recordingBackend2D) Remove(id int64) {
	delete(b.added, id)
	b.removed = append(b.removed, id)
}
func (b *recordingBackend2D) Update(int64, float64, float64)         {}
func (b *recordingBackend2D) Step(float64) []collision.Contact       { return nil }
func (b *recordingBackend2D) ReadBack(func(int64, float64, float64)) {}

type recordingBackend3D struct {
	added   map[int64]collision3d.Shape
	removed []int64
}

func newRecordingBackend3D() *recordingBackend3D {
	return &recordingBackend3D{added: make(map[int64]collision3d.Shape)}
}
func (b *recordingBackend3D) Add(id int64, shape collision3d.Shape, _, _ uint32, _ bool) {
	b.added[id] = shape
}
func (b *recordingBackend3D) Set(id int64, shape collision3d.Shape, _, _ uint32, _ bool) {
	b.added[id] = shape
}
func (b *recordingBackend3D) Remove(id int64) {
	delete(b.added, id)
	b.removed = append(b.removed, id)
}
func (b *recordingBackend3D) Update(int64, float64, float64, float64)         {}
func (b *recordingBackend3D) Step(float64) []collision3d.Contact              { return nil }
func (b *recordingBackend3D) ReadBack(func(int64, float64, float64, float64)) {}

type colliderSpawnEntity struct {
	id            int64
	sawRegistered bool
	removedAtOnRm bool
	backend       *recordingBackend2D
}

func (e *colliderSpawnEntity) EntityID() int64              { return e.id }
func (e *colliderSpawnEntity) SetEntityID(id int64)         { e.id = id }
func (e *colliderSpawnEntity) TypeName() string             { return "collider2d" }
func (e *colliderSpawnEntity) Position() (float32, float32) { return 0, 0 }
func (e *colliderSpawnEntity) IsGlobal() bool               { return false }
func (e *colliderSpawnEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *colliderSpawnEntity) FullUpdate() ([]byte, error)  { return nil, nil }
func (e *colliderSpawnEntity) Collider() (CollisionShape, string, bool) {
	return CollisionCircle{R: 1}, "Player", false
}
func (e *colliderSpawnEntity) OnSpawn() {
	_, e.sawRegistered = e.backend.added[e.id]
}
func (e *colliderSpawnEntity) OnRemove() {
	_, still := e.backend.added[e.id]
	e.removedAtOnRm = !still
}

type collider3DSpawnEntity struct {
	id            int64
	sawRegistered bool
	removedAtOnRm bool
	backend       *recordingBackend3D
}

func (e *collider3DSpawnEntity) EntityID() int64              { return e.id }
func (e *collider3DSpawnEntity) SetEntityID(id int64)         { e.id = id }
func (e *collider3DSpawnEntity) TypeName() string             { return "collider3d" }
func (e *collider3DSpawnEntity) Position() (float32, float32) { return 0, 0 }
func (e *collider3DSpawnEntity) Position3D() (float32, float32, float32) {
	return 0, 0, 0
}
func (e *collider3DSpawnEntity) IsGlobal() bool               { return false }
func (e *collider3DSpawnEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *collider3DSpawnEntity) FullUpdate() ([]byte, error)  { return nil, nil }
func (e *collider3DSpawnEntity) Collider3D() (CollisionShape3D, string, bool) {
	return CollisionSphere{R: 1}, "Player", true
}
func (e *collider3DSpawnEntity) OnSpawn() {
	_, e.sawRegistered = e.backend.added[e.id]
}
func (e *collider3DSpawnEntity) OnRemove() {
	_, still := e.backend.added[e.id]
	e.removedAtOnRm = !still
}

func TestCreateEntityRegistersColliderBeforeOnSpawn(t *testing.T) {
	srv := NewServer(ServerConfig{})
	backend := newRecordingBackend2D()
	layers := NewCollisionLayers().Bind(backend).Define("Player")
	srv.SetCollisionBackend(backend)
	srv.SetCollisionLayers(layers)

	e := &colliderSpawnEntity{backend: backend}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !e.sawRegistered {
		t.Fatal("OnSpawn must observe an already-registered collider")
	}
	if _, ok := backend.added[e.id]; !ok {
		t.Fatal("expected shape registered after CreateEntity")
	}
}

func TestDeleteEntityRemovesColliderBeforeOnRemove(t *testing.T) {
	srv := NewServer(ServerConfig{})
	backend := newRecordingBackend2D()
	layers := NewCollisionLayers().Bind(backend).Define("Player")
	srv.SetCollisionLayers(layers)

	e := &colliderSpawnEntity{id: 9, backend: backend}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	srv.DeleteEntity(9)
	if !e.removedAtOnRm {
		t.Fatal("OnRemove must observe collider already removed")
	}
	if _, ok := backend.added[9]; ok {
		t.Fatal("shape should be gone after DeleteEntity")
	}
}

func TestCreateEntityDuplicateDoesNotRegisterCollider(t *testing.T) {
	srv := NewServer(ServerConfig{})
	backend := newRecordingBackend2D()
	layers := NewCollisionLayers().Bind(backend).Define("Player")
	srv.SetCollisionLayers(layers)

	first := &colliderSpawnEntity{id: 1, backend: backend}
	if err := srv.CreateEntity(first); err != nil {
		t.Fatalf("CreateEntity first: %v", err)
	}
	dup := &colliderSpawnEntity{id: 1, backend: backend}
	if err := srv.CreateEntity(dup); err == nil {
		t.Fatal("expected duplicate error")
	}
	if dup.sawRegistered {
		t.Fatal("OnSpawn must not run on failed CreateEntity")
	}
	if len(backend.removed) != 0 {
		t.Fatalf("failed CreateEntity must not touch colliders; removed=%v", backend.removed)
	}
	if _, ok := backend.added[1]; !ok {
		t.Fatal("original entity collider must remain")
	}
}

func TestCreateEntitySkipsRegistrationWithoutLayers(t *testing.T) {
	srv := NewServer(ServerConfig{})
	backend := newRecordingBackend2D()
	e := &colliderSpawnEntity{backend: backend}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if len(backend.added) != 0 {
		t.Fatal("missing layers helper must not register shapes")
	}
	if e.sawRegistered {
		t.Fatal("OnSpawn should not see a registration without layers")
	}
}

func TestCreateEntityRegistersCollider3DBeforeOnSpawn(t *testing.T) {
	srv := NewServer(ServerConfig{})
	backend := newRecordingBackend3D()
	layers := NewCollisionLayers3D().Bind(backend).Define("Player")
	srv.SetCollision3DBackend(backend)
	srv.SetCollisionLayers3D(layers)

	e := &collider3DSpawnEntity{backend: backend}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !e.sawRegistered {
		t.Fatal("OnSpawn must observe an already-registered 3D collider")
	}

	srv.DeleteEntity(e.id)
	if !e.removedAtOnRm {
		t.Fatal("OnRemove must observe 3D collider already removed")
	}
}

type dualColliderEntity struct {
	id       int64
	backend2 *recordingBackend2D
	backend3 *recordingBackend3D
}

func (e *dualColliderEntity) EntityID() int64              { return e.id }
func (e *dualColliderEntity) SetEntityID(id int64)         { e.id = id }
func (e *dualColliderEntity) TypeName() string             { return "dual" }
func (e *dualColliderEntity) Position() (float32, float32) { return 0, 0 }
func (e *dualColliderEntity) IsGlobal() bool               { return false }
func (e *dualColliderEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *dualColliderEntity) FullUpdate() ([]byte, error)  { return []byte{0x01}, nil }
func (e *dualColliderEntity) Collider() (CollisionShape, string, bool) {
	return CollisionCircle{R: 1}, "Player", false
}
func (e *dualColliderEntity) Collider3D() (CollisionShape3D, string, bool) {
	return CollisionSphere{R: 2}, "Player", false
}

func TestCreateDeleteRegistersBothProvidersIndependently(t *testing.T) {
	srv := NewServer(ServerConfig{})
	b2 := newRecordingBackend2D()
	b3 := newRecordingBackend3D()
	srv.SetCollisionLayers(NewCollisionLayers().Bind(b2).Define("Player"))
	srv.SetCollisionLayers3D(NewCollisionLayers3D().Bind(b3).Define("Player"))

	e := &dualColliderEntity{backend2: b2, backend3: b3}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, ok := b2.added[e.id]; !ok {
		t.Fatal("expected 2D registration")
	}
	if _, ok := b3.added[e.id]; !ok {
		t.Fatal("expected 3D registration")
	}

	id := e.id
	srv.DeleteEntity(id)
	if _, ok := b2.added[id]; ok {
		t.Fatal("expected 2D removal")
	}
	if _, ok := b3.added[id]; ok {
		t.Fatal("expected 3D removal")
	}
	if len(b2.removed) == 0 || len(b3.removed) == 0 {
		t.Fatalf("expected both backends Remove; 2D=%v 3D=%v", b2.removed, b3.removed)
	}
}

func TestMustCollisionBackendPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil 2D backend")
		}
	}()
	MustCollisionBackend(nil)
}

func TestMustCollisionBackend3DPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil 3D backend")
		}
	}()
	MustCollisionBackend3D(nil)
}

func TestMustCollisionBackendAllowsNonNil(t *testing.T) {
	MustCollisionBackend(newRecordingBackend2D())
	MustCollisionBackend3D(newRecordingBackend3D())
}

func TestLoadSnapshotRegistersCollidersViaCreateEntity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.snap")
	const fingerprint = "collider-restore-fingerprint"

	source := []registry.Entity{
		&dualColliderEntity{id: 7},
	}
	if err := <-snapshot.Save(source, fingerprint, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	srv := NewServer(ServerConfig{})
	b2 := newRecordingBackend2D()
	srv.SetCollisionLayers(NewCollisionLayers().Bind(b2).Define("Player"))

	err := srv.LoadSnapshot(path, fingerprint, func(rec snapshot.Record) (Entity, error) {
		return &colliderSpawnEntity{id: rec.EntityID, backend: b2}, nil
	})
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if _, ok := b2.added[7]; !ok {
		t.Fatal("LoadSnapshot → CreateEntity must register restored collider")
	}
}
