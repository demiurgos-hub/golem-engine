package golem

import "testing"

// binderSpawnEntity implements Entity, EntityIDSetter, ServerBinder, and Spawner
// so CreateEntity binding order relative to OnSpawn can be asserted.
type binderSpawnEntity struct {
	id           int64
	srv          *Server
	boundAtSpawn *Server
	spawned      bool
}

func (e *binderSpawnEntity) EntityID() int64              { return e.id }
func (e *binderSpawnEntity) SetEntityID(id int64)         { e.id = id }
func (e *binderSpawnEntity) TypeName() string             { return "binder" }
func (e *binderSpawnEntity) Position() (float32, float32) { return 0, 0 }
func (e *binderSpawnEntity) IsGlobal() bool               { return false }
func (e *binderSpawnEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *binderSpawnEntity) FullUpdate() ([]byte, error)  { return nil, nil }

func (e *binderSpawnEntity) BindServer(s *Server) { e.srv = s }

func (e *binderSpawnEntity) OnSpawn() {
	e.spawned = true
	e.boundAtSpawn = e.srv
}

func TestCreateEntityBindsServerBeforeOnSpawn(t *testing.T) {
	srv := NewServer(ServerConfig{})
	e := &binderSpawnEntity{}

	if err := srv.CreateEntity(e); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !e.spawned {
		t.Fatal("expected OnSpawn to run")
	}
	if e.boundAtSpawn != srv {
		t.Fatalf("OnSpawn saw Server() = %p, want %p", e.boundAtSpawn, srv)
	}
	if e.srv != srv {
		t.Fatalf("after CreateEntity, bound server = %p, want %p", e.srv, srv)
	}
	if e.id == 0 {
		t.Fatal("expected auto-assigned entity ID")
	}
}

func TestCreateEntityBindsServerBeforeOnSpawnOwned(t *testing.T) {
	srv := NewServer(ServerConfig{})
	e := &binderSpawnEntity{id: 42}

	if err := srv.CreateEntity(e, 7); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if !e.spawned {
		t.Fatal("expected OnSpawn to run")
	}
	if e.boundAtSpawn != srv {
		t.Fatalf("OnSpawn saw Server() = %p, want %p", e.boundAtSpawn, srv)
	}
	owner, ok := srv.Owner(42)
	if !ok || owner != 7 {
		t.Fatalf("Owner(42) = (%d, %v), want (7, true)", owner, ok)
	}
}

func TestCreateEntityDuplicateIDKeepsBinding(t *testing.T) {
	srv := NewServer(ServerConfig{})
	first := &binderSpawnEntity{id: 1}
	if err := srv.CreateEntity(first); err != nil {
		t.Fatalf("CreateEntity first: %v", err)
	}

	dup := &binderSpawnEntity{id: 1}
	if err := srv.CreateEntity(dup); err == nil {
		t.Fatal("expected duplicate ID error")
	}
	if dup.spawned {
		t.Fatal("OnSpawn must not run when Add fails")
	}
	if dup.srv != srv {
		t.Fatalf("BindServer should run before Add failure; got %p, want %p", dup.srv, srv)
	}
}
