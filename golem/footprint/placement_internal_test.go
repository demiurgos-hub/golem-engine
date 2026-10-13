package footprint

import (
	"sync/atomic"
	"testing"
	"time"
)

type blockingRemoval struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (b *blockingRemoval) Remove(int64) {
	b.calls.Add(1)
	close(b.started)
	<-b.release
}

func TestRemovalLoserDoesNotWaitForBackend(t *testing.T) {
	backend := &blockingRemoval{started: make(chan struct{}), release: make(chan struct{})}
	h := &placementHandle{backend: backend, ids: []int64{-1}}
	done := make(chan struct{})
	go func() { h.Remove(); close(done) }()
	defer func() { close(backend.release); <-done }()
	<-backend.started
	loser := make(chan struct{})
	go func() { h.Remove(); close(loser) }()
	select {
	case <-loser:
	case <-time.After(time.Second):
		t.Fatal("losing Remove blocked on backend")
	}
	if backend.calls.Load() != 1 {
		t.Fatal("duplicate backend removal")
	}
}
