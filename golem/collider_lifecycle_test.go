package golem

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

type avatarColliderLifecycleEntity struct {
	dualColliderEntity
	onSpawn, onRemove func()
}

func (e *avatarColliderLifecycleEntity) OnSpawn() {
	if e.onSpawn != nil {
		e.onSpawn()
	}
}
func (e *avatarColliderLifecycleEntity) OnRemove() {
	if e.onRemove != nil {
		e.onRemove()
	}
}

func TestAvatarTeardownRemovesProviderColliders(t *testing.T) {
	for _, providers := range []string{"2d", "3d", "both"} {
		for _, scenario := range []string{"rollback-no-interest", "rollback-reservation", "disconnect", "disconnect-delete", "disconnect-replace"} {
			t.Run(providers+"/"+scenario, func(t *testing.T) {
				cfg := ServerConfig{CellSize: 8}
				if scenario == "rollback-no-interest" {
					cfg.CellSize = 0
				}
				s := NewServer(cfg)
				b2, b3 := newRecordingBackend2D(), newRecordingBackend3D()
				if providers != "3d" {
					s.SetCollisionLayers(NewCollisionLayers().Bind(b2).Define("Player"))
				}
				if providers != "2d" {
					s.SetCollisionLayers3D(NewCollisionLayers3D().Bind(b3).Define("Player"))
				}
				sess := &Session{ID: 7}
				removed := 0
				newEntity := func() *avatarColliderLifecycleEntity {
					e := &avatarColliderLifecycleEntity{}
					e.onRemove = func() {
						removed++
						if _, ok := b2.added[e.id]; ok {
							t.Error("2D collider remains during OnRemove")
						}
						if _, ok := b3.added[e.id]; ok {
							t.Error("3D collider remains during OnRemove")
						}
						if _, ok := s.Get(e.id); ok {
							t.Error("entity remains during OnRemove")
						}
						if _, ok := s.Avatar(sess.ID); ok {
							t.Error("avatar remains during OnRemove")
						}
						s.SessionsKnowing(e.id) // Registry and avatar hooks must not hold interestMu.
					}
					return e
				}
				e := newEntity()
				if scenario == "rollback-reservation" {
					e.onSpawn = func() { s.releaseAvatarReservation(sess.ID) }
				}
				err := s.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 5})
				rollback := scenario == "rollback-no-interest" || scenario == "rollback-reservation"
				if (err != nil) != rollback {
					t.Fatalf("SpawnAvatar: %v", err)
				}
				wantRemovals := 1
				if !rollback {
					s.OnDisconnect(func(session *Session) {
						if avatar, ok := s.Avatar(session.ID); !ok || avatar != e || removed != 0 {
							t.Error("avatar removed before disconnect callback")
						}
						if scenario != "disconnect" {
							s.DeleteEntity(e.id)
							s.DeleteEntity(e.id)
						}
						if scenario == "disconnect-replace" {
							wantRemovals++
							if err := s.SpawnAvatar(session, newEntity(), AvatarOptions{FOIRadius: 5}); err != nil {
								t.Fatal(err)
							}
						}
					})
					s.enqueueDisconnect(sess)
					s.drainMessages()
				}
				s.DeleteEntity(e.id)
				if removed != wantRemovals || len(b2.added)+len(b3.added) != 0 || s.Len() != 0 || s.hasFOI(sess.ID) {
					t.Fatalf("incomplete teardown: callbacks=%d registry=%d", removed, s.Len())
				}
				if len(s.sessionAvatar)+len(s.avatarSession) != 0 {
					t.Fatal("indexes/reservation remain")
				}
				if providers != "3d" && len(b2.removed) != wantRemovals {
					t.Fatal("2D removal count")
				}
				if providers != "2d" && len(b3.removed) != wantRemovals {
					t.Fatal("3D removal count")
				}
			})
		}
	}
}

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

// twoSpaceRecordingLayers configures recording 2D/3D layer helpers on space 0
// and space 3; space 3's helpers define only the layers listed.
func twoSpaceRecordingLayers(srv *Server, layers3 ...string) (b0, b3 *recordingBackend2D, c0, c3 *recordingBackend3D) {
	b0, b3 = newRecordingBackend2D(), newRecordingBackend2D()
	c0, c3 = newRecordingBackend3D(), newRecordingBackend3D()
	if len(layers3) == 0 {
		layers3 = []string{"Player"}
	}
	srv.SetCollisionLayers(NewCollisionLayers().Bind(b0).Define("Player"))
	srv.SetCollisionLayers3D(NewCollisionLayers3D().Bind(c0).Define("Player"))
	srv.Space(3).SetCollisionLayers(NewCollisionLayers().Bind(b3).Define(layers3...))
	srv.Space(3).SetCollisionLayers3D(NewCollisionLayers3D().Bind(c3).Define(layers3...))
	return b0, b3, c0, c3
}

func TestCreateEntityInRegistersColliderInItsSpace(t *testing.T) {
	srv := NewServer(ServerConfig{})
	b0, b3, c0, c3 := twoSpaceRecordingLayers(srv)
	e := &dualColliderEntity{id: 5}
	if err := srv.CreateEntityIn(3, e); err != nil {
		t.Fatal(err)
	}
	if len(b0.added) != 0 || len(c0.added) != 0 {
		t.Fatal("space 3 entity registered on space 0's layers")
	}
	if _, ok := b3.added[5]; !ok {
		t.Fatal("2D collider missing from space 3")
	}
	if _, ok := c3.added[5]; !ok {
		t.Fatal("3D collider missing from space 3")
	}
	srv.DeleteEntity(5)
	if len(b3.added) != 0 || len(c3.added) != 0 || len(b0.removed) != 0 || len(c0.removed) != 0 {
		t.Fatalf("DeleteEntity removed from the wrong space: space0 %v/%v, space3 left %v/%v", b0.removed, c0.removed, b3.added, c3.added)
	}

	// A space without layer helpers skips registration, like CreateEntity
	// without helpers.
	if err := srv.CreateEntityIn(8, &dualColliderEntity{id: 6}); err != nil {
		t.Fatal(err)
	}
	if len(b0.added)+len(b3.added)+len(c0.added)+len(c3.added) != 0 {
		t.Fatal("entity in a space without helpers was registered somewhere")
	}
	if srv.runtime(8) != nil {
		t.Fatal("CreateEntityIn created a runtime")
	}
}

func TestEntitySetupRollbackRemovesColliderFromItsSpace(t *testing.T) {
	srv := NewServer(ServerConfig{})
	b0, b3, c0, c3 := twoSpaceRecordingLayers(srv)
	e := &dualColliderEntity{id: 4}
	err := srv.CreateEntityWithSetupIn(3, e, func(Entity) (func() error, error) {
		if _, ok := b3.added[4]; !ok {
			t.Error("setup did not observe the space 3 collider")
		}
		return nil, errors.New("setup failed")
	})
	if err == nil {
		t.Fatal("setup error was not returned")
	}
	if len(b3.added) != 0 || len(c3.added) != 0 || len(b3.removed) != 1 || len(c3.removed) != 1 {
		t.Fatalf("rollback left space 3 colliders: %v %v", b3.added, c3.added)
	}
	if len(b0.removed) != 0 || len(c0.removed) != 0 {
		t.Fatal("rollback touched space 0's layers")
	}
}

func TestMoveToSpaceMovesLegacyColliders(t *testing.T) {
	srv := NewServer(ServerConfig{})
	b0, b3, c0, c3 := twoSpaceRecordingLayers(srv)
	e := &dualColliderEntity{id: 1}
	plain := &stubEntity{id: 2}
	if err := srv.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateEntity(plain); err != nil {
		t.Fatal(err)
	}
	if err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := b0.added[1]; ok {
		t.Fatal("2D collider stayed in space 0")
	}
	if _, ok := c0.added[1]; ok {
		t.Fatal("3D collider stayed in space 0")
	}
	if b3.added[1] != (CollisionCircle{R: 1}) || c3.added[1] != (CollisionSphere{R: 2}) {
		t.Fatalf("colliders not registered in space 3: %v %v", b3.added, c3.added)
	}

	// Into a space without helpers: removed, not registered anywhere.
	if err := srv.SetEntitySpace(1, 9); err != nil {
		t.Fatal(err)
	}
	if len(b3.added)+len(c3.added)+len(b0.added)+len(c0.added) != 0 {
		t.Fatal("collider survived a move into a space without helpers")
	}
	// Back into space 0: registered again.
	if err := srv.SetEntitySpace(1, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := b0.added[1]; !ok {
		t.Fatal("moving back did not register in space 0")
	}
	if srv.runtime(9) != nil {
		t.Fatal("MoveToSpace created a runtime")
	}
}

func TestMoveToSpaceRejectsUndefinedLayerWithoutChanges(t *testing.T) {
	srv := NewServer(ServerConfig{})
	b0, b3, _, c3 := twoSpaceRecordingLayers(srv, "Wall")
	ok := &stubEntity{id: 1}
	e := &colliderSpawnEntity{id: 2, backend: b0}
	for _, x := range []Entity{ok, e} {
		if err := srv.CreateEntity(x); err != nil {
			t.Fatal(err)
		}
	}
	err := srv.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{1, 2}})
	if err == nil || !strings.Contains(err.Error(), `"Player" is not defined in space 3`) {
		t.Fatalf("err = %v, want undefined layer rejection", err)
	}
	for _, id := range []int64{1, 2} {
		if space, _ := srv.EntitySpace(id); space != 0 {
			t.Fatalf("entity %d moved to %d despite the rejection", id, space)
		}
	}
	if _, ok := b0.added[2]; !ok || len(b0.removed) != 0 || len(b3.added) != 0 || len(c3.added) != 0 {
		t.Fatal("rejected move changed colliders")
	}
}
