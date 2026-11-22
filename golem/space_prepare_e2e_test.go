package golem

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"
)

// e2eEvents records each server event the real client applies, with the
// space the client was in at that moment.
type e2eEvents struct {
	mu     sync.Mutex
	client *golemclient.GameClient
	got    []string
}

func (e *e2eEvents) ApplyRaw(event []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, fmt.Sprintf("%s@%d", event, e.client.Space()))
}

func (e *e2eEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.got...)
}

// TestPrepareSpaceChangeWithRealGoClient: reliable-lane events sent right
// before a prepared move reach the real client while it is still in the old
// space, an event sent between the preparation and the move arrives too, and
// the client then follows the move.
func TestPrepareSpaceChangeWithRealGoClient(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	s := NewServer(ServerConfig{Addr: addr, DevSelfSignedCert: true, TickRate: 50, CellSize: 8})
	s.SetRemovalSerializer(func(id int64, rev uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d:%d", id, rev)), nil
	})
	avatar := &e2eEntity{id: 1, rev: 1}
	sessions := make(chan int64, 1)
	s.OnConnect(func(sess *Session) {
		if err := s.SpawnAvatar(sess, avatar, AvatarOptions{FOIRadius: 20, FOIMargin: 2}); err != nil {
			t.Error(err)
		}
		sessions <- sess.ID
	})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()
	defer func() { cancel(); <-runDone }()
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if err := s.WaitReady(readyCtx); err != nil {
		t.Fatal(err)
	}

	events := &e2eEvents{}
	mgr := &e2eManager{vals: map[int64]int{}, last: map[int64]uint64{}, via: map[string]int{}}
	client := golemclient.NewGameClient(golemclient.GameClientOptions{
		DecodeEntity:  func(b []byte) (any, error) { return append([]byte(nil), b...), nil },
		EntityManager: mgr,
		EventManager:  events,
	})
	events.client = client
	if err := client.Connect(readyCtx, golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport,
		URL: "https://" + addr + "/wt", TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}); err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	var sid int64
	select {
	case sid = <-sessions:
	case <-readyCtx.Done():
		t.Fatal("session was not admitted")
	}

	readyOutcome := make(chan bool, 1)
	if err := s.Post(func(s *Server) {
		for i := 0; i < 20; i++ {
			lane := EventReliableOrdered
			if i%2 == 1 {
				lane = EventReliableUnordered
			}
			if err := s.SendServerEvent(sid, lane, []byte(fmt.Sprintf("before-%02d", i))); err != nil {
				t.Error(err)
			}
		}
		s.PrepareSpaceChange(sid, func(s *Server, ok bool) {
			if ok {
				if err := s.MoveToSpace(SpaceTransfer{To: 3, Entities: []int64{avatar.id}}); err != nil {
					t.Error(err)
				}
			}
			readyOutcome <- ok
		})
		if err := s.SendServerEvent(sid, EventReliableOrdered, []byte("prepared")); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-readyOutcome:
		if !ok {
			t.Fatal("preparation failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preparation never completed")
	}
	for deadline := time.Now().Add(5 * time.Second); client.Space() != 3; {
		if time.Now().After(deadline) {
			t.Fatal("client never reached space 3")
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := events.snapshot()
	seen := map[string]bool{}
	for _, e := range got {
		seen[e] = true
	}
	for i := 0; i < 20; i++ {
		if want := fmt.Sprintf("before-%02d@0", i); !seen[want] {
			t.Errorf("client did not apply %s in the old space; events = %q", want, got)
		}
	}
	if !seen["prepared@0"] {
		t.Errorf("event sent between the preparation and the move missing from the old space; events = %q", got)
	}
}
