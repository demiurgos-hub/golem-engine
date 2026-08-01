package net

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

type snapshotEntity struct {
	id   int64
	data []byte
}

func (e *snapshotEntity) EntityID() int64              { return e.id }
func (e *snapshotEntity) TypeName() string             { return "snap" }
func (e *snapshotEntity) Position() (float32, float32) { return 0, 0 }
func (e *snapshotEntity) IsGlobal() bool               { return false }
func (e *snapshotEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *snapshotEntity) FullUpdate() ([]byte, error)  { return e.data, nil }

func TestEntitySnapshotProviderPreservesIDsAndCompletesAfterSuccess(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Add(&snapshotEntity{id: 7, data: []byte("seven")}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	listener := NewListener(reg, Config{Transport: TransportWebSocket})

	var (
		providerCalls atomic.Int64
		completeIDs   []int64
		completeOK    atomic.Bool
	)
	listener.SetEntitySnapshotFunc(func(sessionID int64) ([]EntitySnapshot, error) {
		providerCalls.Add(1)
		if sessionID == 0 {
			t.Errorf("expected non-zero session ID")
		}
		return []EntitySnapshot{{EntityID: 7, Data: []byte("seven")}}, nil
	})
	listener.SetEntitySnapshotCompleteFunc(func(sessionID int64, entityIDs []int64) {
		completeIDs = append([]int64(nil), entityIDs...)
		completeOK.Store(true)
	})

	httpSrv := httptest.NewServer(listener.Handler())
	defer httpSrv.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpSrv.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if completeOK.Load() && len(listener.SessionIDs()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls.Load())
	}
	if !completeOK.Load() {
		t.Fatal("expected snapshot complete callback")
	}
	if len(completeIDs) != 1 || completeIDs[0] != 7 {
		t.Fatalf("complete IDs = %v, want [7]", completeIDs)
	}
	_, msg, err := client.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(msg, []byte("seven")) {
		t.Fatalf("snapshot payload = %q, want seven", msg)
	}
}

func TestEntitySnapshotNilProviderKeepsSnapshotAll(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Add(&snapshotEntity{id: 3, data: []byte("default")}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	listener := NewListener(reg, Config{Transport: TransportWebSocket})

	httpSrv := httptest.NewServer(listener.Handler())
	defer httpSrv.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpSrv.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(listener.SessionIDs()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, msg, err := client.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(msg, []byte("default")) {
		t.Fatalf("default snapshot = %q, want default", msg)
	}
}

func TestEntitySnapshotProviderErrorSkipsComplete(t *testing.T) {
	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{Transport: TransportWebSocket})
	var completeCalls atomic.Int64
	listener.SetEntitySnapshotFunc(func(sessionID int64) ([]EntitySnapshot, error) {
		return nil, errors.New("provider boom")
	})
	listener.SetEntitySnapshotCompleteFunc(func(sessionID int64, entityIDs []int64) {
		completeCalls.Add(1)
	})

	httpSrv := httptest.NewServer(listener.Handler())
	defer httpSrv.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpSrv.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	time.Sleep(200 * time.Millisecond)
	if completeCalls.Load() != 0 {
		t.Fatalf("complete calls = %d, want 0 after provider error", completeCalls.Load())
	}
	if n := len(listener.SessionIDs()); n != 0 {
		t.Fatalf("session count = %d, want 0 after failed snapshot", n)
	}
}

func TestEntitySnapshotFailureSkipsComplete(t *testing.T) {
	reg := registry.NewRegistry()
	listener := NewListener(reg, Config{Transport: TransportWebSocket})
	var completeCalls atomic.Int64
	listener.SetEntitySnapshotFunc(func(sessionID int64) ([]EntitySnapshot, error) {
		return []EntitySnapshot{{EntityID: 1, Data: bytes.Repeat([]byte("x"), 40000)}}, nil
	})
	listener.SetEntitySnapshotCompleteFunc(func(sessionID int64, entityIDs []int64) {
		completeCalls.Add(1)
	})

	httpSrv := httptest.NewServer(listener.Handler())
	defer httpSrv.Close()
	client, _, err := websocket.Dial(context.Background(), "ws"+httpSrv.URL[4:], nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.CloseNow()

	time.Sleep(200 * time.Millisecond)
	if completeCalls.Load() != 0 {
		t.Fatalf("complete calls = %d, want 0 after failed snapshot", completeCalls.Load())
	}
	if n := len(listener.SessionIDs()); n != 0 {
		t.Fatalf("session count = %d, want 0 after failed snapshot", n)
	}
}
