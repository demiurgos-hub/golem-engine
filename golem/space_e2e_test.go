package golem

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"
)

// This file joins a real Server.Run to the real Go client runtime over
// WebTransport with datagram state updates, which no other test does: the
// server tests read raw frames and the client tests feed hand-built ones.

// e2eEntity is a minimal revisioned entity with one replicated value.
type e2eEntity struct {
	mu    sync.Mutex
	id    int64
	x     float64
	rev   uint64
	val   int
	dirty bool
	mask  uint64
}

func (e *e2eEntity) EntityID() int64              { return e.id }
func (e *e2eEntity) TypeName() string             { return "e2e" }
func (e *e2eEntity) Position() (float32, float32) { return float32(e.x), 0 }
func (e *e2eEntity) IsGlobal() bool               { return false }
func (e *e2eEntity) StateRevision() uint64        { e.mu.Lock(); defer e.mu.Unlock(); return e.rev }
func (e *e2eEntity) bump()                        { e.mu.Lock(); e.val++; e.rev++; e.dirty = true; e.mu.Unlock() }
func (e *e2eEntity) FlushUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dirty {
		e.mask = 0
		return nil, nil
	}
	e.dirty, e.mask = false, 1
	return []byte(fmt.Sprintf("delta:%d:%d:%d", e.id, e.rev, e.val)), nil
}
func (e *e2eEntity) FullUpdate() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []byte(fmt.Sprintf("full:%d:%d:%d", e.id, e.rev, e.val)), nil
}
func (e *e2eEntity) LastFlushMask() uint64 { e.mu.Lock(); defer e.mu.Unlock(); return e.mask }
func (e *e2eEntity) MarshalDeltaMask(uint64) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []byte(fmt.Sprintf("delta:%d:%d:%d", e.id, e.rev, e.val)), nil
}
func (e *e2eEntity) MarshalCompactDeltaMask(mask uint64) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := binary.AppendUvarint(nil, uint64(e.id))
	out = binary.AppendUvarint(out, e.rev)
	out = binary.AppendUvarint(out, mask)
	return binary.AppendUvarint(out, uint64(e.val)), nil
}

// e2eManager applies updates with the revision fence of the generated
// managers and logs what a game would see: spawns, full updates and removals.
type e2eManager struct {
	mu   sync.Mutex
	vals map[int64]int
	last map[int64]uint64
	log  []string
	via  map[string]int
}

func (m *e2eManager) note(s string) { m.log = append(m.log, s) }
func (m *e2eManager) ApplyUpdate(u any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := strings.Split(string(u.([]byte)), ":")
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	rev, _ := strconv.ParseUint(parts[2], 10, 64)
	if last, ok := m.last[id]; ok && rev < last {
		m.note(fmt.Sprintf("stale-%s:%d", parts[0], id))
		return
	}
	switch parts[0] {
	case "full":
		val, _ := strconv.Atoi(parts[3])
		if _, known := m.vals[id]; !known {
			m.note(fmt.Sprintf("spawn:%d", id))
		} else {
			m.note(fmt.Sprintf("full-update:%d", id))
		}
		m.vals[id], m.last[id] = val, rev
	case "delta":
		if _, known := m.vals[id]; !known {
			m.note(fmt.Sprintf("delta-unknown:%d", id))
			return
		}
		val, _ := strconv.Atoi(parts[3])
		m.vals[id], m.last[id] = val, rev
		m.via["stream-delta"]++
	case "removed":
		m.last[id] = rev
		if _, known := m.vals[id]; known {
			delete(m.vals, id)
			m.note(fmt.Sprintf("remove:%d", id))
		}
	}
}
func (m *e2eManager) ApplyCompactUpdate(frame []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, n := binary.Uvarint(frame)
	rev, k := binary.Uvarint(frame[n:])
	_, j := binary.Uvarint(frame[n+k:])
	val, _ := binary.Uvarint(frame[n+k+j:])
	if last, ok := m.last[int64(id)]; ok && rev < last {
		m.via["stale-datagram"]++
		return
	}
	if _, known := m.vals[int64(id)]; !known {
		m.via[fmt.Sprintf("datagram-unknown:%d", id)]++
		return
	}
	m.vals[int64(id)], m.last[int64(id)] = int(val), rev
	m.via["datagram"]++
}
func (m *e2eManager) Get(id int64) any { m.mu.Lock(); defer m.mu.Unlock(); return m.vals[id] }
func (m *e2eManager) snapshot() (map[int64]int, []string, map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vals := map[int64]int{}
	for k, v := range m.vals {
		vals[k] = v
	}
	via := map[string]int{}
	for k, v := range m.via {
		via[k] = v
	}
	return vals, append([]string(nil), m.log...), via
}

// TestSpaceSwitchWithRealGoClient moves an avatar and the crate it carries from
// space 0 to space 3 and back while every entity changes each tick. The real
// client must follow both switches, never see the carried entities removed,
// acknowledge each transition so the server leaves the stream-only gate, and
// then receive datagram deltas again.
func TestSpaceSwitchWithRealGoClient(t *testing.T) {
	spaceModes(t, spaceSwitchWithRealGoClient)
}

func spaceSwitchWithRealGoClient(t *testing.T, interestMode bool) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	cfg := ServerConfig{Addr: addr, DevSelfSignedCert: true, TickRate: 50}
	if interestMode {
		cfg.CellSize = 8
	}
	s := NewServer(cfg)
	s.SetRemovalSerializer(func(id int64, rev uint64) ([]byte, error) {
		return []byte(fmt.Sprintf("removed:%d:%d", id, rev)), nil
	})
	avatar, crate := &e2eEntity{id: 1, rev: 1}, &e2eEntity{id: 2, x: 1, rev: 1}
	npc, bat := &e2eEntity{id: 3, x: 2, rev: 1}, &e2eEntity{id: 11, x: 2, rev: 1}
	if err := s.CreateEntity(crate); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEntity(npc); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEntityIn(3, bat); err != nil {
		t.Fatal(err)
	}
	s.OnConnect(func(sess *Session) {
		opts := AvatarOptions{}
		if interestMode {
			opts.FOIRadius, opts.FOIMargin = 20, 2
		}
		if err := s.SpawnAvatar(sess, avatar, opts); err != nil {
			t.Error(err)
		}
	})
	s.OnTick(func(float64, *Server) {
		for _, e := range []*e2eEntity{avatar, crate, npc, bat} {
			e.bump()
		}
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

	mgr := &e2eManager{vals: map[int64]int{}, last: map[int64]uint64{}, via: map[string]int{}}
	client := golemclient.NewGameClient(golemclient.GameClientOptions{
		DecodeEntity:  func(b []byte) (any, error) { return append([]byte(nil), b...), nil },
		EntityManager: mgr,
	})
	client.OnSpaceChanged(func(old, cur golemclient.SpaceID) {
		mgr.mu.Lock()
		mgr.note(fmt.Sprintf("space:%d->%d", old, cur))
		mgr.mu.Unlock()
	})
	if err := client.Connect(readyCtx, golemclient.ConnectOptions{Transport: golemclient.TransportWebTransport,
		URL: "https://" + addr + "/wt", TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}); err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				vals, log, via := mgr.snapshot()
				t.Fatalf("timeout waiting for %s: vals=%v via=%v log=%q", what, vals, via, log)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	datagrams := func() int { _, _, via := mgr.snapshot(); return via["datagram"] }
	crateVal := func() int { vals, _, _ := mgr.snapshot(); return vals[2] }
	gated := func() bool {
		s.interestMu.Lock()
		defer s.interestMu.Unlock()
		return len(s.spaceAckPending) != 0
	}
	move := func(to SpaceID) {
		t.Helper()
		done := make(chan error, 1)
		if err := s.Post(func(*Server) { done <- s.MoveToSpace(SpaceTransfer{To: to, Entities: []int64{1, 2}}) }); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	waitFor("datagram deltas in space 0", func() bool { return datagrams() > 10 })
	move(3)
	waitFor("client in space 3", func() bool { return client.Space() == 3 })
	waitFor("gate released by SpaceAck from the real client", func() bool { return !gated() })
	base, v := datagrams(), crateVal()
	waitFor("datagram deltas resume in space 3", func() bool { return datagrams() > base+10 && crateVal() > v+10 })
	move(0)
	waitFor("client back in space 0", func() bool { return client.Space() == 0 })
	waitFor("gate released again", func() bool { return !gated() })
	base, v = datagrams(), crateVal()
	waitFor("datagram deltas resume in space 0", func() bool { return datagrams() > base+10 && crateVal() > v+10 })

	vals, log, via := mgr.snapshot()
	for _, entry := range log {
		switch entry {
		case "remove:1", "remove:2":
			t.Errorf("carried entity was despawned: %s", entry)
		case "spawn:1", "spawn:2":
			// Each carried entity spawns exactly once, at connect.
			if strings.Count(strings.Join(log, " ")+" ", entry+" ") != 1 {
				t.Errorf("carried entity spawned again: %q", log)
			}
		}
	}
	// Back in space 0: the avatar, the crate and the overworld NPC, not the bat.
	if _, bat := vals[11]; len(vals) != 3 || bat {
		t.Errorf("client entities after returning = %v", vals)
	}
	if slices.Index(log, "space:0->3") < 0 || slices.Index(log, "space:3->0") < slices.Index(log, "space:0->3") {
		t.Errorf("client did not report both switches in order: %q", log)
	}
	if via["datagram-unknown:1"]+via["datagram-unknown:2"] != 0 {
		t.Errorf("a datagram for a carried entity arrived while the client did not know it: %v", via)
	}
	if t.Failed() {
		t.Logf("client entities=%v via=%v log=%q", vals, via, log)
	}
}
