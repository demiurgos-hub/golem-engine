// Package hub implements the Golem Inspector hub: it accepts outbound
// connections from in-process agents (golem/inspector), keeps a world store
// per observed target, starts and stops observation as browsers come and go,
// and serves the inspector UI behind a shared-password login.
//
// The package does not import the golem runtime; cmd/golem-inspector wraps it
// as a binary and other programs can embed Handler in their own server.
package hub

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
)

// Close codes and reasons shared with agents and browsers.
const (
	closeAuth             = 4001
	closeReplaced         = 4002
	closeProtocol         = 4003
	closeRegisterTimeout  = 4004
	closeStaleAgent       = 4005
	closeSlowConsumer     = 4008
	closeStaleClient      = 4009
	closeServiceRestart   = 1012
	closeConnectionsLimit = 1013
)

// Maximum entities per browser snapshot chunk.
const snapshotChunkEntities = 128

// Hub is a Golem Inspector hub. Create it with New, serve Handler, and call
// Close on shutdown. All state is guarded by mu; network writes happen on
// per-connection goroutines so mu is never held across I/O.
type Hub struct {
	cfg      Config
	sessions *sessionStore
	limiter  *loginLimiter
	metrics  *metrics
	handler  http.Handler

	mu          sync.Mutex
	targets     map[string]*target
	agents      map[*agentConn]struct{}
	clients     map[*clientConn]struct{}
	pending     int
	pendingByIP map[string]int
	closed      bool

	shuttingDown atomic.Bool
	stop         chan struct{}
	janitorDone  chan struct{}
	conns        sync.WaitGroup
}

type target struct {
	id            string
	labels        map[string]string
	schema        *wire.Schema
	schemaFrame   []byte
	engineVersion string

	agent          *agentConn
	firstSeen      time.Time
	registeredAt   time.Time
	connectedAt    time.Time
	disconnectedAt time.Time
	lastMessageAt  time.Time
	lastError      string

	viewers      map[*clientConn]struct{}
	wantObserve  bool
	observeState wire.ObserveState
	lastStartAt  time.Time
	stopTimer    *time.Timer
	restartTimer *time.Timer

	world world
}

// world is the hub's mirror of one target's entities for the current epoch.
type world struct {
	epoch    uint64
	seq      uint64 // last applied agent seq
	tick     uint64
	ready    bool
	entities map[int64]*entity
	pending  *pendingSnapshot

	snapshotStartedAt       time.Time
	lastSnapshotCompletedAt time.Time
	lastSnapshotDuration    time.Duration
	lastSnapshotEntityCount int
	lastSnapshotError       string
}

type pendingSnapshot struct {
	epoch     uint64
	seq       uint64
	tick      uint64
	count     uint64
	entities  map[int64]*entity
	startedAt time.Time
}

// entity is one stored entity: its merged state fields and its space.
type entity struct {
	*wire.EntityFields
	space uint32
}

// New validates cfg and returns a running Hub.
func New(cfg Config) (*Hub, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	h := &Hub{
		cfg:         cfg,
		sessions:    newSessionStore(),
		limiter:     newLoginLimiter(),
		metrics:     newMetrics(),
		targets:     map[string]*target{},
		agents:      map[*agentConn]struct{}{},
		clients:     map[*clientConn]struct{}{},
		pendingByIP: map[string]int{},
		stop:        make(chan struct{}),
		janitorDone: make(chan struct{}),
	}
	h.handler = h.routes()
	go h.janitor()
	return h, nil
}

// Handler returns the hub's HTTP handler: agent and browser WebSockets, the
// REST/auth API, health probes, metrics and the embedded UI.
func (h *Hub) Handler() http.Handler { return h.handler }

// Close marks the hub not ready, closes every socket with 1012
// service_restart, stops background work and waits up to ctx for connection
// goroutines. It is safe to call more than once.
func (h *Hub) Close(ctx context.Context) error {
	h.shuttingDown.Store(true)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	close(h.stop)
	for a := range h.agents {
		a.closeWith(closeServiceRestart, "service_restart")
	}
	for c := range h.clients {
		c.closeWith(closeServiceRestart, "service_restart")
	}
	for _, t := range h.targets {
		if t.stopTimer != nil {
			t.stopTimer.Stop()
		}
		if t.restartTimer != nil {
			t.restartTimer.Stop()
		}
	}
	h.mu.Unlock()
	<-h.janitorDone
	done := make(chan struct{})
	go func() { h.conns.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) janitor() {
	defer close(h.janitorDone)
	t := time.NewTicker(h.cfg.JanitorInterval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.sweep(time.Now())
		}
	}
}

// sweep purges silent agents and long-offline targets, closes idle browsers
// and expires sessions.
func (h *Hub) sweep(now time.Time) {
	h.sessions.sweep(now)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.targets {
		switch {
		case t.agent != nil && now.Sub(t.lastMessageAt) > h.cfg.AgentHeartbeatTimeout:
			t.agent.closeWith(closeStaleAgent, "stale_agent")
			h.purgeLocked(t, "stale_agent")
		case t.agent == nil && now.Sub(t.disconnectedAt) > h.cfg.AgentHeartbeatTimeout:
			h.purgeLocked(t, "agent_offline")
		}
	}
	for c := range h.clients {
		if now.Sub(c.lastMessageAt) > h.cfg.ClientHeartbeatTimeout {
			h.metrics.inc("closes_total", "role", "client", "reason", "stale_client")
			c.closeWith(closeStaleClient, "stale_client")
		} else if h.cfg.AuthEnabled() && !h.sessions.valid(c.sid, now) {
			c.closeWith(closeAuth, "auth_expired")
		}
	}
}

// purgeLocked removes a target and tells its viewers.
func (h *Hub) purgeLocked(t *target, reason string) {
	h.metrics.inc("stale_cleanups_total")
	if t.stopTimer != nil {
		t.stopTimer.Stop()
	}
	if t.restartTimer != nil {
		t.restartTimer.Stop()
	}
	for c := range t.viewers {
		h.metrics.inc("resync_total", "reason", reason)
		c.enqueue(h, [][]byte{
			wire.Encode(&wire.ResyncRequired{TargetID: t.id, Epoch: t.world.epoch, Reason: reason}),
			wire.Encode(&wire.Error{Code: "target_not_found", Message: "target went offline", TargetID: t.id}),
		})
		c.target = nil
	}
	if t.agent != nil {
		t.agent.target = nil
	}
	delete(h.targets, t.id)
}

// ---- agent events (called with h.mu held) ----

func (h *Hub) registerLocked(a *agentConn, reg *wire.Register, now time.Time) (code int, reason string) {
	if a.target != nil {
		if a.target.id != reg.TargetID {
			return closeProtocol, "target_mismatch"
		}
		return 0, "" // re-registration on the same socket is a no-op
	}
	if reg.TargetID == "" || len(reg.TargetID) > 128 {
		return closeProtocol, "invalid_target_id"
	}
	if reg.Schema == nil || reg.Schema.Validate() != nil {
		return closeProtocol, "invalid_schema"
	}
	t := h.targets[reg.TargetID]
	// A replacement takes over its target's slot instead of adding one.
	if (t == nil || t.agent == nil) && h.registeredAgentsLocked() >= h.cfg.MaxAgents {
		return closeConnectionsLimit, "connection_limit"
	}
	resync := ""
	if t == nil {
		t = &target{id: reg.TargetID, firstSeen: now, viewers: map[*clientConn]struct{}{}}
		h.targets[t.id] = t
	} else if t.agent != nil {
		old := t.agent
		old.target = nil
		h.metrics.inc("closes_total", "role", "agent", "reason", "replaced")
		old.closeWith(closeReplaced, "replaced")
		resync = "agent_replaced"
	} else if t.world.ready || t.world.epoch > 0 {
		resync = "agent_reconnected"
	}
	if t.stopTimer != nil {
		t.stopTimer.Stop()
		t.stopTimer = nil
	}
	h.unpendLocked(a)
	a.target = t
	t.agent = a
	t.labels = reg.Labels
	t.schema = reg.Schema
	t.schemaFrame = wire.Encode(&wire.TargetSchema{TargetID: t.id, Schema: reg.Schema})
	t.engineVersion = reg.EngineVersion
	t.registeredAt, t.connectedAt, t.lastMessageAt = now, now, now
	t.lastError = ""
	t.world = world{}
	t.wantObserve = false
	t.observeState = wire.ObserveIdle
	for c := range t.viewers {
		item := [][]byte{t.schemaFrame}
		if resync != "" {
			h.metrics.inc("resync_total", "reason", resync)
			item = append(item, wire.Encode(&wire.ResyncRequired{TargetID: t.id, Reason: resync}))
		}
		c.enqueue(h, append(item, h.statusFrame(t)))
	}
	if len(t.viewers) > 0 {
		h.startLocked(t, now)
	}
	return 0, ""
}

func (h *Hub) registeredAgentsLocked() int {
	n := 0
	for _, t := range h.targets {
		if t.agent != nil {
			n++
		}
	}
	return n
}

func (h *Hub) agentGoneLocked(a *agentConn, now time.Time) {
	h.unpendLocked(a)
	t := a.target
	if t == nil || t.agent != a {
		return
	}
	t.agent = nil
	t.disconnectedAt = now
	t.wantObserve = false
	t.observeState = wire.ObserveIdle
	t.world.pending = nil
	for c := range t.viewers {
		c.enqueue(h, [][]byte{h.statusFrame(t)})
	}
}

func (h *Hub) observeStatusLocked(t *target, st *wire.ObserveStatus, now time.Time) {
	switch st.State {
	case wire.ObserveIdle:
		h.resetWorldLocked(t, "")
		t.observeState = wire.ObserveIdle
		if t.wantObserve && len(t.viewers) > 0 {
			h.startLocked(t, now) // the agent lost its stream; ask again
		} else {
			t.wantObserve = false
		}
	case wire.ObserveStarting:
		if t.wantObserve {
			t.observeState = wire.ObserveStarting
		}
	case wire.ObserveReady:
		if t.wantObserve && t.world.ready {
			t.observeState = wire.ObserveReady
		}
	}
	for c := range t.viewers {
		c.enqueue(h, [][]byte{h.statusFrame(t)})
	}
}

func (h *Hub) snapshotBeginLocked(t *target, m *wire.SnapshotBegin, now time.Time) {
	if !t.wantObserve {
		return // late frames after a stop
	}
	t.world.pending = &pendingSnapshot{epoch: m.Epoch, seq: m.Seq, tick: m.Tick, count: m.EntityCount, entities: map[int64]*entity{}, startedAt: now}
	t.world.snapshotStartedAt = now
}

func (h *Hub) snapshotChunkLocked(t *target, m *wire.SnapshotChunk, now time.Time) {
	p := t.world.pending
	switch {
	case p == nil:
		h.snapshotFailedLocked(t, "snapshot_chunk_without_begin", now)
		return
	case m.Epoch != p.epoch:
		h.snapshotFailedLocked(t, "snapshot_epoch_mismatch", now)
		return
	case m.Seq != p.seq+1:
		h.snapshotFailedLocked(t, "seq_gap", now)
		return
	}
	p.seq = m.Seq
	for _, e := range m.Entities {
		f, err := wire.ParseState(e.TypeTag, e.State)
		if err != nil || t.schema.EntityByTag(e.TypeTag) == nil {
			h.snapshotFailedLocked(t, "invalid_entity_state", now)
			return
		}
		p.entities[e.ID] = &entity{EntityFields: f, space: e.Space}
	}
}

func (h *Hub) snapshotEndLocked(t *target, m *wire.SnapshotEnd, now time.Time) {
	p := t.world.pending
	switch {
	case p == nil:
		h.snapshotFailedLocked(t, "snapshot_end_without_begin", now)
		return
	case m.Epoch != p.epoch:
		h.snapshotFailedLocked(t, "snapshot_epoch_mismatch", now)
		return
	case m.Seq != p.seq+1:
		h.snapshotFailedLocked(t, "seq_gap", now)
		return
	case uint64(len(p.entities)) != p.count:
		h.snapshotFailedLocked(t, "snapshot_entity_count_mismatch", now)
		return
	}
	w := &t.world
	w.epoch, w.seq, w.tick = p.epoch, m.Seq, p.tick
	w.entities = p.entities
	w.ready = true
	w.pending = nil
	w.lastSnapshotCompletedAt = now
	w.lastSnapshotDuration = now.Sub(p.startedAt)
	w.lastSnapshotEntityCount = len(p.entities)
	w.lastSnapshotError = ""
	t.observeState = wire.ObserveReady
	h.metrics.inc("snapshots_total", "result", "ok")
	h.metrics.add("snapshot_duration_seconds_sum", w.lastSnapshotDuration.Seconds())
	h.metrics.inc("snapshot_duration_seconds_count")
	for c := range t.viewers {
		c.enqueue(h, h.snapshotItemLocked(c, t))
	}
}

func (h *Hub) snapshotFailedLocked(t *target, reason string, now time.Time) {
	h.metrics.inc("snapshots_total", "result", "failed")
	t.world.lastSnapshotError = reason
	t.lastError = reason
	h.resetWorldLocked(t, reason)
	h.requestSnapshotLocked(t, now)
}

func (h *Hub) batchLocked(t *target, b *wire.Batch, now time.Time) {
	w := &t.world
	if !w.ready || b.Epoch != w.epoch {
		if w.ready && b.Epoch > w.epoch {
			h.resetWorldLocked(t, "epoch_mismatch")
			h.requestSnapshotLocked(t, now)
		}
		return // stale frames from an earlier epoch, or before the snapshot
	}
	if b.Seq != w.seq+1 {
		t.lastError = "seq_gap"
		h.resetWorldLocked(t, "seq_gap")
		h.requestSnapshotLocked(t, now)
		return
	}
	w.seq, w.tick = b.Seq, b.Tick
	out := &wire.Batch{TargetID: t.id, Epoch: b.Epoch, Tick: b.Tick}
	for _, s := range b.Spawns {
		f, err := wire.ParseState(s.TypeTag, s.State)
		if err != nil || t.schema.EntityByTag(s.TypeTag) == nil {
			continue
		}
		w.entities[s.ID] = &entity{EntityFields: f, space: s.Space}
		out.Spawns = append(out.Spawns, s)
	}
	for _, d := range b.Deltas {
		f := w.entities[d.ID]
		ent := t.schema.EntityByTag(d.TypeTag)
		if f == nil || ent == nil || f.TypeTag != ent.StateTag {
			continue
		}
		if err := f.ApplyDelta(ent, d.Mask, d.Delta); err != nil {
			continue
		}
		out.Deltas = append(out.Deltas, d)
	}
	for _, m := range b.Moves {
		if e := w.entities[m.ID]; e != nil {
			e.space = m.Space
			out.Moves = append(out.Moves, m)
		}
	}
	for _, r := range b.Removals {
		if _, ok := w.entities[r.ID]; ok {
			delete(w.entities, r.ID)
			out.Removals = append(out.Removals, r)
		}
	}
	if len(t.viewers) == 0 {
		return
	}
	body := wire.EncodeBatchBody(out)
	for c := range t.viewers {
		if c.awaitingSnapshot {
			continue
		}
		c.seq++
		c.enqueue(h, [][]byte{wire.EncodeBatchFrame(body, c.seq)})
	}
}

// resetWorldLocked drops the target's entities and, when reason is set, tells
// viewers to discard theirs.
func (h *Hub) resetWorldLocked(t *target, reason string) {
	keep := t.world
	t.world = world{
		epoch:                   keep.epoch,
		lastSnapshotCompletedAt: keep.lastSnapshotCompletedAt,
		lastSnapshotDuration:    keep.lastSnapshotDuration,
		lastSnapshotEntityCount: keep.lastSnapshotEntityCount,
		lastSnapshotError:       keep.lastSnapshotError,
	}
	if reason == "" {
		return
	}
	for c := range t.viewers {
		h.metrics.inc("resync_total", "reason", reason)
		c.enqueue(h, [][]byte{wire.Encode(&wire.ResyncRequired{TargetID: t.id, Epoch: keep.epoch, Reason: reason}), h.statusFrame(t)})
	}
}

// requestSnapshotLocked asks the agent for a fresh snapshot, at most once a
// second so a misbehaving agent cannot make the hub spin; an early request is
// deferred rather than dropped.
func (h *Hub) requestSnapshotLocked(t *target, now time.Time) {
	if !t.wantObserve {
		return
	}
	wait := time.Second - now.Sub(t.lastStartAt)
	if wait <= 0 {
		h.startLocked(t, now)
		return
	}
	if t.restartTimer != nil {
		return
	}
	t.restartTimer = time.AfterFunc(wait, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		t.restartTimer = nil
		if !h.closed && h.targets[t.id] == t && t.wantObserve && !t.world.ready && t.world.pending == nil {
			h.startLocked(t, time.Now())
		}
	})
}

// ---- observation (viewer refcount) ----

func (h *Hub) startLocked(t *target, now time.Time) {
	if t.agent == nil {
		return
	}
	if t.agent.enqueue(wire.Encode(&wire.ObserveStart{})) {
		h.metrics.inc("observe_commands_total", "action", "start")
	}
	t.wantObserve = true
	t.lastStartAt = now
	if t.observeState == wire.ObserveIdle {
		t.observeState = wire.ObserveStarting
	}
}

func (h *Hub) stopLocked(t *target) {
	if t.agent != nil && t.wantObserve {
		if t.agent.enqueue(wire.Encode(&wire.ObserveStop{})) {
			h.metrics.inc("observe_commands_total", "action", "stop")
		}
	}
	t.wantObserve = false
	t.observeState = wire.ObserveIdle
	h.resetWorldLocked(t, "")
}

func (h *Hub) acquireLocked(c *clientConn, t *target, now time.Time) {
	t.viewers[c] = struct{}{}
	c.target = t
	if t.stopTimer != nil {
		t.stopTimer.Stop()
		t.stopTimer = nil
	}
	if !t.wantObserve {
		h.startLocked(t, now)
	}
}

func (h *Hub) releaseLocked(c *clientConn) {
	t := c.target
	if t == nil {
		return
	}
	c.target = nil
	c.awaitingSnapshot = false
	delete(t.viewers, c)
	if len(t.viewers) > 0 || h.targets[t.id] != t {
		return
	}
	if t.stopTimer != nil {
		t.stopTimer.Stop()
	}
	t.stopTimer = time.AfterFunc(h.cfg.ObserveGrace, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(t.viewers) == 0 && h.targets[t.id] == t && !h.closed {
			t.stopTimer = nil
			h.stopLocked(t)
		}
	})
}

// ---- browser frames ----

func (h *Hub) statusFrame(t *target) []byte {
	return wire.Encode(&wire.WorldStatus{
		TargetID:       t.id,
		Epoch:          t.world.epoch,
		EntityCount:    uint64(len(t.world.entities)),
		Ready:          t.world.ready,
		AgentConnected: t.agent != nil,
		ObserveState:   t.observeState,
		Tick:           t.world.tick,
	})
}

// snapshotItemLocked builds a per-client snapshot of t's committed world. The
// client's seq continues across snapshots; the browser resets its baseline on
// SnapshotBegin.
func (h *Hub) snapshotItemLocked(c *clientConn, t *target) [][]byte {
	c.awaitingSnapshot = false
	w := &t.world
	if !w.ready {
		c.awaitingSnapshot = true
		return [][]byte{h.statusFrame(t)}
	}
	ids := make([]int64, 0, len(w.entities))
	for id := range w.entities {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	c.seq++
	frames := [][]byte{wire.Encode(&wire.SnapshotBegin{TargetID: t.id, Epoch: w.epoch, Seq: c.seq, Tick: w.tick, EntityCount: uint64(len(ids))})}
	for start := 0; start < len(ids) || start == 0; start += snapshotChunkEntities {
		end := min(start+snapshotChunkEntities, len(ids))
		chunk := &wire.SnapshotChunk{TargetID: t.id, Epoch: w.epoch}
		for _, id := range ids[start:end] {
			e := w.entities[id]
			chunk.Entities = append(chunk.Entities, wire.EntityState{ID: id, TypeTag: e.TypeTag, State: e.EncodeState(), Space: e.space})
		}
		c.seq++
		chunk.Seq = c.seq
		frames = append(frames, wire.Encode(chunk))
		if end == len(ids) {
			break
		}
	}
	c.seq++
	frames = append(frames, wire.Encode(&wire.SnapshotEnd{TargetID: t.id, Epoch: w.epoch, Seq: c.seq}), h.statusFrame(t))
	return frames
}

func (h *Hub) closeClientsForSession(sid, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.sid == sid {
			h.metrics.inc("closes_total", "role", "client", "reason", reason)
			c.closeWith(closeAuth, reason)
		}
	}
}

// TargetInfo is the JSON shape of one entry in GET /api/targets.
type TargetInfo struct {
	TargetID                string            `json:"targetId"`
	Labels                  map[string]string `json:"labels"`
	EngineVersion           string            `json:"engineVersion,omitempty"`
	SchemaFingerprint       string            `json:"schemaFingerprint,omitempty"`
	EntityTypes             []string          `json:"entityTypes"`
	Connected               bool              `json:"connected"`
	ObserveState            string            `json:"observeState"`
	Ready                   bool              `json:"ready"`
	EntityCount             int               `json:"entityCount"`
	ViewerCount             int               `json:"viewerCount"`
	FirstRegisteredAt       *time.Time        `json:"firstRegisteredAt"`
	RegisteredAt            *time.Time        `json:"registeredAt"`
	ConnectedAt             *time.Time        `json:"connectedAt"`
	DisconnectedAt          *time.Time        `json:"disconnectedAt"`
	LastMessageAt           *time.Time        `json:"lastMessageAt"`
	SnapshotStartedAt       *time.Time        `json:"snapshotStartedAt"`
	LastSnapshotCompletedAt *time.Time        `json:"lastSnapshotCompletedAt"`
	LastSnapshotDurationMs  *int64            `json:"lastSnapshotDurationMs"`
	LastSnapshotEntityCount *int              `json:"lastSnapshotEntityCount"`
	LastSnapshotError       string            `json:"lastSnapshotError,omitempty"`
	LastError               string            `json:"lastError,omitempty"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// Targets returns a sorted summary of every known target.
func (h *Hub) Targets() []TargetInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]TargetInfo, 0, len(h.targets))
	for _, t := range h.targets {
		info := TargetInfo{
			TargetID:          t.id,
			Labels:            t.labels,
			EngineVersion:     t.engineVersion,
			Connected:         t.agent != nil,
			ObserveState:      t.observeState.String(),
			Ready:             t.world.ready,
			EntityCount:       len(t.world.entities),
			ViewerCount:       len(t.viewers),
			FirstRegisteredAt: timePtr(t.firstSeen),
			RegisteredAt:      timePtr(t.registeredAt),
			ConnectedAt:       timePtr(t.connectedAt),
			DisconnectedAt:    timePtr(t.disconnectedAt),
			LastMessageAt:     timePtr(t.lastMessageAt),
			SnapshotStartedAt: timePtr(t.world.snapshotStartedAt),
			LastSnapshotError: t.world.lastSnapshotError,
			LastError:         t.lastError,
			EntityTypes:       []string{},
		}
		if info.Labels == nil {
			info.Labels = map[string]string{}
		}
		if t.schema != nil {
			info.SchemaFingerprint = t.schema.Fingerprint
			for _, e := range t.schema.Entities {
				info.EntityTypes = append(info.EntityTypes, e.Name)
			}
		}
		if !t.world.lastSnapshotCompletedAt.IsZero() {
			info.LastSnapshotCompletedAt = timePtr(t.world.lastSnapshotCompletedAt)
			ms := t.world.lastSnapshotDuration.Milliseconds()
			n := t.world.lastSnapshotEntityCount
			info.LastSnapshotDurationMs, info.LastSnapshotEntityCount = &ms, &n
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TargetID < out[j].TargetID })
	return out
}
