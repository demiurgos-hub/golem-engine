package hub

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
	"github.com/demiurgos-hub/golem-engine/golem/pb"
	"golang.org/x/crypto/bcrypt"
)

const testToken = "agent-token-for-tests"

func testSchema() *wire.Schema {
	return &wire.Schema{Dimensions: 2, RemovedTag: 3, Entities: []wire.Entity{{
		Name: "Actor", StateTag: 1, DeltaTag: 2,
		Fields: []wire.Field{
			{Name: "entity_id", Tag: 1, Bit: -1, Type: "int64", Role: wire.RoleIdentity},
			{Name: "pos_x", Tag: 2, Bit: 0, Type: "float", Role: wire.RolePosition},
			{Name: "pos_y", Tag: 3, Bit: 1, Type: "float", Role: wire.RolePosition},
			{Name: "revision", Tag: 1000, Bit: -1, Type: "uint64", Role: wire.RoleRevision},
			{Name: "hp", Tag: 4, Bit: 2, Sync: "tick", Type: "int32", Role: wire.RoleVariable},
		},
	}}}
}

func actorState(id int64, x float32, hp int32) []byte {
	w := &pb.Writer{}
	w.Tag(1, 0).Int64(id)
	w.Tag(2, 5).Float32(x)
	w.Tag(4, 0).Int32(hp)
	return w.Finish()
}

func hpDelta(id int64, hp int32) []byte {
	w := &pb.Writer{}
	w.Tag(1, 0).Int64(id)
	w.Tag(4, 0).Int32(hp)
	return w.Finish()
}

type testHub struct {
	t      *testing.T
	hub    *Hub
	server *httptest.Server
}

func newTestHub(t *testing.T, mutate func(*Config)) *testHub {
	t.Helper()
	cfg := Config{
		AgentToken:            testToken,
		ObserveGrace:          30 * time.Millisecond,
		JanitorInterval:       20 * time.Millisecond,
		AgentHeartbeatTimeout: 5 * time.Second,
		Logger:                log.New(io.Discard, "", 0),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Close(ctx)
		srv.Close()
	})
	return &testHub{t: t, hub: h, server: srv}
}

func (th *testHub) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(th.server.URL, "http") + path
}

type peer struct {
	t    *testing.T
	conn *websocket.Conn
	msgs chan wire.Message
	errc chan error
}

func newPeer(t *testing.T, conn *websocket.Conn) *peer {
	p := &peer{t: t, conn: conn, msgs: make(chan wire.Message, 1024), errc: make(chan error, 1)}
	conn.SetReadLimit(16 << 20)
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				p.errc <- err
				return
			}
			msg, err := wire.Decode(data)
			if err != nil {
				p.errc <- err
				return
			}
			if _, ok := msg.(*wire.Pong); ok {
				continue
			}
			p.msgs <- msg
		}
	}()
	t.Cleanup(func() { conn.CloseNow() })
	return p
}

func (p *peer) send(msg wire.Message) {
	p.t.Helper()
	if err := p.conn.Write(context.Background(), websocket.MessageBinary, wire.Encode(msg)); err != nil {
		p.t.Fatal(err)
	}
}

// expect skips messages until one of type T arrives.
func expect[T wire.Message](t *testing.T, p *peer) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-p.msgs:
			if m, ok := msg.(T); ok {
				return m
			}
		case err := <-p.errc:
			var zero T
			t.Fatalf("connection ended waiting for %T: %v", zero, err)
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

// expectNone fails if a message of type T arrives within d.
func expectNone[T wire.Message](t *testing.T, p *peer, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case msg := <-p.msgs:
			if _, ok := msg.(T); ok {
				t.Fatalf("unexpected %T", msg)
			}
		case <-deadline:
			return
		}
	}
}

func (p *peer) closeCode() websocket.StatusCode {
	p.t.Helper()
	select {
	case err := <-p.errc:
		return websocket.CloseStatus(err)
	case <-time.After(5 * time.Second):
		p.t.Fatal("connection stayed open")
		return 0
	}
}

func (th *testHub) dialAgent(token string) (*peer, *http.Response, error) {
	th.t.Helper()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.Dial(context.Background(), th.wsURL("/ws/agent"), &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, resp, err
	}
	return newPeer(th.t, conn), resp, nil
}

func (th *testHub) agent(targetID string, labels map[string]string) *peer {
	th.t.Helper()
	p, _, err := th.dialAgent(testToken)
	if err != nil {
		th.t.Fatal(err)
	}
	p.send(&wire.Register{TargetID: targetID, Labels: labels, Schema: testSchema()})
	th.waitTarget(targetID, func(ti TargetInfo) bool { return ti.Connected })
	return p
}

func (th *testHub) browser(client *http.Client) *peer {
	th.t.Helper()
	hdr := http.Header{}
	hdr.Set("Origin", th.server.URL)
	conn, _, err := websocket.Dial(context.Background(), th.wsURL("/ws/client"), &websocket.DialOptions{HTTPHeader: hdr, HTTPClient: client})
	if err != nil {
		th.t.Fatal(err)
	}
	return newPeer(th.t, conn)
}

func (th *testHub) targets() map[string]TargetInfo {
	out := map[string]TargetInfo{}
	for _, ti := range th.hub.Targets() {
		out[ti.TargetID] = ti
	}
	return out
}

func (th *testHub) waitTarget(id string, cond func(TargetInfo) bool) TargetInfo {
	th.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ti, ok := th.targets()[id]; ok && cond(ti) {
			return ti
		}
		time.Sleep(5 * time.Millisecond)
	}
	th.t.Fatalf("target %q never reached the expected state: %+v", id, th.targets()[id])
	return TargetInfo{}
}

// streamSnapshot answers an ObserveStart with a snapshot of the given entities.
func streamSnapshot(t *testing.T, agent *peer, epoch uint64, states ...wire.EntityState) uint64 {
	t.Helper()
	expect[*wire.ObserveStart](t, agent)
	agent.send(&wire.ObserveStatus{State: wire.ObserveStarting, Epoch: epoch})
	agent.send(&wire.SnapshotBegin{Epoch: epoch, Seq: 1, EntityCount: uint64(len(states))})
	agent.send(&wire.SnapshotChunk{Epoch: epoch, Seq: 2, Entities: states})
	agent.send(&wire.SnapshotEnd{Epoch: epoch, Seq: 3})
	agent.send(&wire.ObserveStatus{State: wire.ObserveReady, Epoch: epoch, EntityCount: uint64(len(states))})
	return 3
}

func TestColdObservationLifecycle(t *testing.T) {
	th := newTestHub(t, nil)
	agent := th.agent("cave-1", map[string]string{"map": "caves"})
	expectNone[*wire.ObserveStart](t, agent, 50*time.Millisecond)
	if ti := th.targets()["cave-1"]; ti.ObserveState != "idle" || ti.Labels["map"] != "caves" || len(ti.EntityTypes) != 1 {
		t.Fatalf("target before viewers = %+v", ti)
	}

	b1 := th.browser(nil)
	b1.send(&wire.Subscribe{TargetID: "cave-1"})
	schema := expect[*wire.TargetSchema](t, b1)
	if schema.TargetID != "cave-1" || schema.Schema.Entities[0].Name != "Actor" {
		t.Fatalf("schema = %+v", schema)
	}
	seq := streamSnapshot(t, agent, 1, wire.EntityState{ID: 7, TypeTag: 1, State: actorState(7, 1.5, 10)})
	begin := expect[*wire.SnapshotBegin](t, b1)
	chunk := expect[*wire.SnapshotChunk](t, b1)
	end := expect[*wire.SnapshotEnd](t, b1)
	if begin.TargetID != "cave-1" || begin.EntityCount != 1 || chunk.Seq != begin.Seq+1 || end.Seq != chunk.Seq+1 || len(chunk.Entities) != 1 {
		t.Fatalf("browser snapshot begin=%+v chunk=%+v end=%+v", begin, chunk, end)
	}
	if st := expect[*wire.WorldStatus](t, b1); !st.Ready || !st.AgentConnected || st.EntityCount != 1 {
		t.Fatalf("status = %+v", st)
	}

	agent.send(&wire.Batch{Epoch: 1, Seq: seq + 1, Tick: 5, Deltas: []wire.EntityDelta{{ID: 7, TypeTag: 1, Mask: 4, Delta: hpDelta(7, 3)}}})
	batch := expect[*wire.Batch](t, b1)
	if batch.Seq != end.Seq+1 || batch.TargetID != "cave-1" || len(batch.Deltas) != 1 {
		t.Fatalf("forwarded batch = %+v", batch)
	}

	// A late joiner gets the merged state from the hub store, without a new
	// observe.start.
	b2 := th.browser(nil)
	b2.send(&wire.Subscribe{TargetID: "cave-1"})
	late := expect[*wire.SnapshotChunk](t, b2)
	f, err := wire.ParseState(late.Entities[0].TypeTag, late.Entities[0].State)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.EncodeState()), string([]byte{4 << 3, 3})) {
		t.Fatal("late joiner did not get the merged hp")
	}
	expectNone[*wire.ObserveStart](t, agent, 50*time.Millisecond)

	// Leaving and returning within the grace period keeps observation on.
	b1.conn.Close(websocket.StatusNormalClosure, "")
	b2.conn.Close(websocket.StatusNormalClosure, "")
	b3 := th.browser(nil)
	b3.send(&wire.Subscribe{TargetID: "cave-1"})
	expect[*wire.SnapshotEnd](t, b3)
	expectNone[*wire.ObserveStop](t, agent, 60*time.Millisecond)
	b3.conn.Close(websocket.StatusNormalClosure, "")
	expect[*wire.ObserveStop](t, agent)
	ti := th.waitTarget("cave-1", func(ti TargetInfo) bool { return ti.ObserveState == "idle" })
	if ti.EntityCount != 0 || ti.ViewerCount != 0 || ti.LastSnapshotEntityCount == nil || *ti.LastSnapshotEntityCount != 1 {
		t.Fatalf("after stop = %+v", ti)
	}
}

func TestSeqGapResyncsBrowsersAndRestartsAgent(t *testing.T) {
	th := newTestHub(t, nil)
	agent := th.agent("cave-1", nil)
	b := th.browser(nil)
	b.send(&wire.Subscribe{TargetID: "cave-1"})
	seq := streamSnapshot(t, agent, 1, wire.EntityState{ID: 1, TypeTag: 1, State: actorState(1, 0, 1)})
	expect[*wire.SnapshotEnd](t, b)
	agent.send(&wire.Batch{Epoch: 1, Seq: seq + 2})
	if rr := expect[*wire.ResyncRequired](t, b); rr.Reason != "seq_gap" {
		t.Fatalf("resync reason = %q", rr.Reason)
	}
	// The restart request is rate limited to one per second and then retried.
	streamSnapshot(t, agent, 2, wire.EntityState{ID: 1, TypeTag: 1, State: actorState(1, 0, 1)})
	if begin := expect[*wire.SnapshotBegin](t, b); begin.Epoch != 2 {
		t.Fatalf("resnapshot epoch = %d", begin.Epoch)
	}
}

func TestTargetIsolationAndUnknownTargets(t *testing.T) {
	th := newTestHub(t, nil)
	a := th.agent("a", nil)
	bAgent := th.agent("b", nil)
	viewer := th.browser(nil)
	viewer.send(&wire.Subscribe{TargetID: "a"})
	streamSnapshot(t, a, 1)
	expect[*wire.SnapshotEnd](t, viewer)

	other := th.browser(nil)
	other.send(&wire.Subscribe{TargetID: "b"})
	seqB := streamSnapshot(t, bAgent, 1, wire.EntityState{ID: 5, TypeTag: 1, State: actorState(5, 0, 0)})
	expect[*wire.SnapshotEnd](t, other)
	bAgent.send(&wire.Batch{Epoch: 1, Seq: seqB + 1, Removals: []wire.Removal{{ID: 5}}})
	expect[*wire.Batch](t, other)
	expectNone[*wire.Batch](t, viewer, 50*time.Millisecond)

	viewer.send(&wire.Resync{TargetID: "b"})
	if e := expect[*wire.Error](t, viewer); e.Code != "target_mismatch" {
		t.Fatalf("resync other target error = %q", e.Code)
	}
	viewer.send(&wire.Subscribe{TargetID: "missing"})
	if e := expect[*wire.Error](t, viewer); e.Code != "target_not_found" {
		t.Fatalf("unknown target error = %q", e.Code)
	}
	expect[*wire.ObserveStop](t, a) // switching away released the old demand
	viewer.send(&wire.Subscribe{})
	if e := expect[*wire.Error](t, viewer); e.Code != "invalid_message" {
		t.Fatalf("empty subscribe error = %q", e.Code)
	}
}

func TestAgentRegistrationRules(t *testing.T) {
	th := newTestHub(t, func(c *Config) { c.AgentRegisterTimeout = 100 * time.Millisecond })
	if _, resp, err := th.dialAgent("wrong"); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: err=%v resp=%v", err, resp)
	}

	p, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	p.send(&wire.Register{Schema: testSchema()})
	if code := p.closeCode(); code != closeProtocol {
		t.Fatalf("missing target id close = %d", code)
	}

	p, _, _ = th.dialAgent(testToken)
	frame := wire.Encode(&wire.Register{TargetID: "x", Schema: testSchema()})
	frame[1] = 9 // version 9
	if err := p.conn.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
		t.Fatal(err)
	}
	if e := expect[*wire.Error](t, p); e.Code != "protocol_mismatch" {
		t.Fatalf("version error = %q", e.Code)
	}
	if code := p.closeCode(); code != closeProtocol {
		t.Fatalf("version close = %d", code)
	}

	p, _, _ = th.dialAgent(testToken)
	if code := p.closeCode(); code != closeRegisterTimeout {
		t.Fatalf("registration timeout close = %d", code)
	}
	if len(th.targets()) != 0 {
		t.Fatalf("rejected agents registered targets: %v", th.targets())
	}
}

func TestDuplicateTargetReplacesOldAgent(t *testing.T) {
	th := newTestHub(t, nil)
	old := th.agent("cave-1", map[string]string{"v": "1"})
	viewer := th.browser(nil)
	viewer.send(&wire.Subscribe{TargetID: "cave-1"})
	streamSnapshot(t, old, 1)
	expect[*wire.SnapshotEnd](t, viewer)

	replacement, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	replacement.send(&wire.Register{TargetID: "cave-1", Labels: map[string]string{"v": "2"}, Schema: testSchema()})
	if code := old.closeCode(); code != closeReplaced {
		t.Fatalf("old agent close = %d", code)
	}
	if rr := expect[*wire.ResyncRequired](t, viewer); rr.Reason != "agent_replaced" {
		t.Fatalf("resync reason = %q", rr.Reason)
	}
	expect[*wire.ObserveStart](t, replacement) // viewers still present
	if ti := th.targets()["cave-1"]; ti.Labels["v"] != "2" || !ti.Connected {
		t.Fatalf("replaced target = %+v", ti)
	}
	if len(th.targets()) != 1 {
		t.Fatal("replacement created a second target")
	}
}

func TestStaleAgentAndClientCleanup(t *testing.T) {
	th := newTestHub(t, func(c *Config) {
		c.AgentHeartbeatTimeout = 150 * time.Millisecond
		c.ClientHeartbeatTimeout = 400 * time.Millisecond
	})
	agent := th.agent("cave-1", nil)
	viewer := th.browser(nil)
	viewer.send(&wire.Subscribe{TargetID: "cave-1"})
	expect[*wire.ObserveStart](t, agent)
	if code := agent.closeCode(); code != closeStaleAgent {
		t.Fatalf("stale agent close = %d", code)
	}
	if rr := expect[*wire.ResyncRequired](t, viewer); rr.Reason != "stale_agent" {
		t.Fatalf("stale resync reason = %q", rr.Reason)
	}
	if code := viewer.closeCode(); code != closeStaleClient {
		t.Fatalf("stale client close = %d", code)
	}
	if len(th.targets()) != 0 {
		t.Fatal("stale target not purged")
	}
}

func TestOfflineTargetIsKeptUntilHeartbeatTimeout(t *testing.T) {
	th := newTestHub(t, func(c *Config) { c.AgentHeartbeatTimeout = 200 * time.Millisecond })
	agent := th.agent("cave-1", nil)
	agent.conn.Close(websocket.StatusNormalClosure, "")
	th.waitTarget("cave-1", func(ti TargetInfo) bool { return !ti.Connected && ti.DisconnectedAt != nil })
	deadline := time.Now().Add(3 * time.Second)
	for len(th.targets()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("offline target never purged")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPendingAgentLimit(t *testing.T) {
	th := newTestHub(t, func(c *Config) { c.MaxPendingAgents = 1; c.MaxPendingAgentsPerIP = 1 })
	first, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	if code := second.closeCode(); code != closeConnectionsLimit {
		t.Fatalf("second pending close = %d", code)
	}
	first.send(&wire.Register{TargetID: "ok", Schema: testSchema()})
	th.waitTarget("ok", func(ti TargetInfo) bool { return ti.Connected })
	th.agent("ok-2", nil) // registration freed the pending slot
}

func TestSlowClientIsResyncedThenClosed(t *testing.T) {
	th := newTestHub(t, func(c *Config) { c.ClientQueueItems = 1 })
	agent := th.agent("cave-1", nil)
	h := th.hub
	viewer := th.browser(nil)
	viewer.send(&wire.Subscribe{TargetID: "cave-1"})
	streamSnapshot(t, agent, 1)
	expect[*wire.SnapshotEnd](t, viewer)
	// Simulate a stalled writer by enqueueing directly while holding mu.
	h.mu.Lock()
	var c *clientConn
	for cc := range h.clients {
		c = cc
	}
	stalled := make(chan [][]byte, 1)
	c.queue, stalled = stalled, c.queue
	for i := 0; i < 6; i++ {
		c.enqueue(h, [][]byte{wire.Encode(&wire.Pong{})})
	}
	h.mu.Unlock()
	_ = stalled
	if code := viewer.closeCode(); code != closeSlowConsumer {
		t.Fatalf("slow consumer close = %d", code)
	}
}

func TestHealthAndMetrics(t *testing.T) {
	metricsToken := strings.Repeat("m", 40)
	th := newTestHub(t, func(c *Config) { c.MetricsToken = metricsToken })
	for path, want := range map[string]int{"/live": 200, "/ready": 200, "/health": 200, "/metrics": 401} {
		resp, err := http.Get(th.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	th.agent("cave-1", nil)
	req, _ := http.NewRequest(http.MethodGet, th.server.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+metricsToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`golem_inspector_targets{state="connected"} 1`, `golem_inspector_messages_total{role="agent",type="register"} 1`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}

	plain := newTestHub(t, nil)
	resp, err = http.Get(plain.server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("metrics disabled = %d", resp.StatusCode)
	}
}

func TestShutdownClosesSocketsWithServiceRestart(t *testing.T) {
	th := newTestHub(t, nil)
	agent := th.agent("cave-1", nil)
	viewer := th.browser(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := th.hub.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if code := agent.closeCode(); code != closeServiceRestart {
		t.Fatalf("agent close = %d", code)
	}
	if code := viewer.closeCode(); code != closeServiceRestart {
		t.Fatalf("viewer close = %d", code)
	}
	resp, err := http.Get(th.server.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready after close = %d", resp.StatusCode)
	}
	if err := th.hub.Close(ctx); err != nil {
		t.Fatal("second Close:", err)
	}
}

func authClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func postJSON(t *testing.T, client *http.Client, url, origin, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAuthFlow(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	th := newTestHub(t, func(c *Config) { c.PasswordHash = string(hash) })
	base := th.server.URL
	client := authClient(t)

	resp, _ := client.Get(base + "/api/targets")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("targets without session = %d", resp.StatusCode)
	}
	hdr := http.Header{}
	hdr.Set("Origin", base)
	if _, resp, err := websocket.Dial(context.Background(), th.wsURL("/ws/client"), &websocket.DialOptions{HTTPHeader: hdr}); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated socket: err=%v", err)
	}
	if r := postJSON(t, client, base+"/auth/login", "https://evil.example", `{"password":"hunter2"}`); r.StatusCode != http.StatusForbidden {
		t.Fatalf("bad origin login = %d", r.StatusCode)
	}
	if r := postJSON(t, client, base+"/auth/login", base, `{"password":"nope"}`); r.StatusCode != http.StatusUnauthorized || len(r.Cookies()) != 0 {
		t.Fatalf("bad password login = %d cookies=%v", r.StatusCode, r.Cookies())
	}
	r := postJSON(t, client, base+"/auth/login", base, `{"password":"hunter2"}`)
	if r.StatusCode != http.StatusOK || len(r.Cookies()) != 1 {
		t.Fatalf("login = %d", r.StatusCode)
	}
	ck := r.Cookies()[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Secure || ck.MaxAge != 36000 {
		t.Fatalf("cookie flags = %+v", ck)
	}
	resp, _ = client.Get(base + "/auth/session")
	var session map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	if !session["authenticated"] || session["authDisabled"] {
		t.Fatalf("session = %v", session)
	}

	viewer := th.browser(client)
	if r := postJSON(t, client, base+"/auth/logout", base, ``); r.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", r.StatusCode)
	}
	if code := viewer.closeCode(); code != closeAuth {
		t.Fatalf("socket after logout close = %d", code)
	}
	// Replaying the old cookie fails: the session is revoked server-side.
	replay := authClient(t)
	u, _ := url.Parse(base)
	replay.Jar.SetCookies(u, []*http.Cookie{{Name: ck.Name, Value: ck.Value}})
	resp, _ = replay.Get(base + "/api/targets")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed cookie = %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	th := newTestHub(t, func(c *Config) { c.PasswordHash = string(hash) })
	client := authClient(t)
	for i := 0; i < loginMaxFailures; i++ {
		if r := postJSON(t, client, th.server.URL+"/auth/login", th.server.URL, `{"password":"x"}`); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, r.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, th.server.URL+"/auth/login", strings.NewReader(`{"password":"pw"}`))
	req.Header.Set("Origin", th.server.URL)
	req.Header.Set("X-Forwarded-For", "10.9.8.7") // ignored
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after failures = %d", resp.StatusCode)
	}
	resp, _ = http.Get(th.server.URL + "/health")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("health affected by rate limit")
	}
}

func TestAuthDisabledAndOriginCheck(t *testing.T) {
	th := newTestHub(t, nil)
	resp, _ := http.Get(th.server.URL + "/auth/session")
	var session map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	if !session["authenticated"] || !session["authDisabled"] {
		t.Fatalf("disabled session = %v", session)
	}
	hdr := http.Header{}
	hdr.Set("Origin", "https://evil.example")
	if _, resp, err := websocket.Dial(context.Background(), th.wsURL("/ws/client"), &websocket.DialOptions{HTTPHeader: hdr}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin socket: err=%v", err)
	}
}

func TestStaticHosting(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html":       {Data: []byte("<html>inspector</html>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	th := newTestHub(t, func(c *Config) { c.WebFS = webFS })
	get := func(path, accept string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodGet, th.server.URL+path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(b)
	}
	if resp, body := get("/", "text/html"); resp.StatusCode != 200 || !strings.Contains(body, "inspector") || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("index = %d %q %q", resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := get("/assets/app-1.js", ""); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset = %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, body := get("/targets/cave-1", "text/html,application/xhtml+xml"); resp.StatusCode != 200 || !strings.Contains(body, "inspector") {
		t.Fatalf("spa fallback = %d", resp.StatusCode)
	}
	for _, path := range []string{"/api/unknown", "/assets/missing.js", "/ws/other"} {
		if resp, body := get(path, "text/html"); resp.StatusCode != 404 || !strings.Contains(body, "not_found") {
			t.Errorf("%s = %d %q", path, resp.StatusCode, body)
		}
	}
	if resp, _ := get("/targets/cave-1", "application/json"); resp.StatusCode != 404 {
		t.Fatalf("non-HTML fallback = %d", resp.StatusCode)
	}
}

func TestConfigValidation(t *testing.T) {
	hash12, _ := bcrypt.GenerateFromPassword([]byte("pw"), MinBcryptCost)
	good := Config{
		Production: true, TLS: true, CookieSecure: true, PasswordHash: string(hash12),
		AgentToken: strings.Repeat("a", 40), PublicOrigins: []string{"https://inspector.example.org"},
		WebFS: fstest.MapFS{"index.html": {}},
	}
	if err := good.withDefaults().Validate(); err != nil {
		t.Fatalf("good production config: %v", err)
	}
	cases := map[string]func(*Config){
		"no tls":            func(c *Config) { c.TLS = false },
		"no password":       func(c *Config) { c.PasswordHash = "" },
		"weak token":        func(c *Config) { c.AgentToken = "short" },
		"placeholder token": func(c *Config) { c.AgentToken = "change-me-" + strings.Repeat("x", 40) },
		"http origin":       func(c *Config) { c.PublicOrigins = []string{"http://inspector.example.org"} },
		"insecure cookie":   func(c *Config) { c.CookieSecure = false },
		"no ui":             func(c *Config) { c.WebFS = nil },
		"same metrics":      func(c *Config) { c.MetricsToken = c.AgentToken },
	}
	for name, mutate := range cases {
		c := good
		mutate(&c)
		if err := c.withDefaults().Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	low, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	c := good
	c.PasswordHash = string(low)
	if c.withDefaults().Validate() == nil {
		t.Error("low bcrypt cost accepted in production")
	}
	if (Config{PasswordHash: "plain"}).withDefaults().Validate() == nil {
		t.Error("non-bcrypt hash accepted")
	}
	if (Config{}).withDefaults().AgentToken != DevAgentToken {
		t.Error("dev token default missing")
	}
}

func TestAgentLimitAllowsReplacement(t *testing.T) {
	th := newTestHub(t, func(c *Config) { c.MaxAgents = 1 })
	old := th.agent("only", nil)
	extra, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	extra.send(&wire.Register{TargetID: "second", Schema: testSchema()})
	if code := extra.closeCode(); code != closeConnectionsLimit {
		t.Fatalf("over-limit close = %d", code)
	}
	replacement, _, err := th.dialAgent(testToken)
	if err != nil {
		t.Fatal(err)
	}
	replacement.send(&wire.Register{TargetID: "only", Schema: testSchema()})
	if code := old.closeCode(); code != closeReplaced {
		t.Fatalf("replaced close = %d", code)
	}
}
