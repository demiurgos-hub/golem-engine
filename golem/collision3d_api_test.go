package golem

import (
	"context"
	"testing"
	"time"
)

type entity3D struct {
	id      int64
	x, y, z float32
}

func (e *entity3D) EntityID() int64                         { return e.id }
func (e *entity3D) TypeName() string                        { return "Entity3D" }
func (e *entity3D) Position() (float32, float32)            { return e.x, e.y }
func (e *entity3D) Position3D() (float32, float32, float32) { return e.x, e.y, e.z }
func (e *entity3D) SetPosition3D(x, y, z float32)           { e.x, e.y, e.z = x, y, z }
func (e *entity3D) IsGlobal() bool                          { return false }
func (e *entity3D) FlushUpdate() ([]byte, error)            { return nil, nil }
func (e *entity3D) FullUpdate() ([]byte, error)             { return []byte{1}, nil }
func (e *entity3D) LastFlushMask() uint64                   { return 0 }
func (e *entity3D) MarshalDeltaMask(uint64) ([]byte, error) { return nil, nil }

func TestServerRuns3DCollisionBackend(t *testing.T) {
	srv := NewServer(ServerConfig{TickRate: 1000})
	backend := NewCollisionSimple3DBackend()
	backend.Add(1, CollisionSphere{R: 1}, 1, 1, false)
	backend.Add(2, CollisionSphere{R: 1}, 1, 1, false)
	srv.SetCollision3DBackend(backend)

	if err := srv.CreateEntity(&entity3D{id: 1, x: 0, y: 0, z: 0}); err != nil {
		t.Fatal(err)
	}
	if err := srv.CreateEntity(&entity3D{id: 2, x: 1, y: 0, z: 0}); err != nil {
		t.Fatal(err)
	}

	gotContact := make(chan struct{}, 1)
	srv.OnContact3D(func(contacts []CollisionContact3D) {
		if len(contacts) > 0 {
			gotContact <- struct{}{}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	select {
	case <-gotContact:
		cancel()
	case <-ctx.Done():
		t.Fatal("timed out waiting for 3D contact")
	}
	<-done
}
