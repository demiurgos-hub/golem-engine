package hub

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/demiurgos-hub/golem-engine/golem/inspector/wire"
)

const writeTimeout = 10 * time.Second

// agentConn is one game-server agent socket.
type agentConn struct {
	conn        *websocket.Conn
	ip          string
	send        chan []byte
	target      *target // set on registration; guarded by Hub.mu
	pending     bool    // counted against pending limits; guarded by Hub.mu
	closeOnce   sync.Once
	done        chan struct{}
	closeCode   websocket.StatusCode // written before done is closed
	closeReason string
}

func (a *agentConn) enqueue(frame []byte) bool {
	select {
	case a.send <- frame:
		return true
	default:
		return false
	}
}

// closeWith asks the writer goroutine to flush queued frames (such as an
// Error explaining the close) and then close with code.
func (a *agentConn) closeWith(code websocket.StatusCode, reason string) {
	a.closeOnce.Do(func() {
		a.closeCode, a.closeReason = code, reason
		close(a.done)
	})
}

func (a *agentConn) writeLoop() {
	write := func(frame []byte, timeout time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return a.conn.Write(ctx, websocket.MessageBinary, frame)
	}
	for {
		select {
		case <-a.done:
			for flushing := true; flushing; {
				select {
				case frame := <-a.send:
					if write(frame, time.Second) != nil {
						flushing = false
					}
				default:
					flushing = false
				}
			}
			a.conn.Close(a.closeCode, a.closeReason)
			return
		case frame := <-a.send:
			if write(frame, writeTimeout) != nil {
				a.closeWith(websocket.StatusGoingAway, "write_failed")
			}
		}
	}
}

// clientConn is one browser socket.
type clientConn struct {
	conn             *websocket.Conn
	sid              string
	queue            chan [][]byte
	target           *target // guarded by Hub.mu
	seq              uint64  // guarded by Hub.mu
	awaitingSnapshot bool    // guarded by Hub.mu
	lastMessageAt    time.Time
	overflows        []time.Time
	closeOnce        sync.Once
	done             chan struct{}
	closeCode        websocket.StatusCode // written before done is closed
	closeReason      string
}

// closeWith asks the writer goroutine to close with code. Queued items are not
// flushed: a closing browser is usually a slow or unauthorized one.
func (c *clientConn) closeWith(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode, c.closeReason = code, reason
		close(c.done)
	})
}

// enqueue queues an item without blocking. When the queue is full the client
// is behind: its backlog is dropped and replaced by a resync plus a fresh
// snapshot; a client that overflows repeatedly is closed as a slow consumer.
// Caller holds Hub.mu.
func (c *clientConn) enqueue(h *Hub, item [][]byte) {
	select {
	case c.queue <- item:
		return
	default:
	}
	now := time.Now()
	keep := c.overflows[:0]
	for _, t := range c.overflows {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	c.overflows = append(keep, now)
	if len(c.overflows) > 3 {
		h.metrics.inc("closes_total", "role", "client", "reason", "slow_consumer")
		c.closeWith(closeSlowConsumer, "slow_consumer")
		return
	}
	for drained := false; !drained; {
		select {
		case <-c.queue:
		default:
			drained = true
		}
	}
	h.metrics.inc("resync_total", "reason", "slow_consumer")
	recovery := [][]byte{}
	if t := c.target; t != nil {
		recovery = append(recovery, wire.Encode(&wire.ResyncRequired{TargetID: t.id, Epoch: t.world.epoch, Reason: "slow_consumer"}))
		recovery = append(recovery, h.snapshotItemLocked(c, t)...)
	}
	select {
	case c.queue <- recovery:
	default:
	}
}

func (h *Hub) handleAgentSocket(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		writeError(w, http.StatusServiceUnavailable, "shutting_down")
		return
	}
	if !h.agentTokenOK(r) {
		h.metrics.inc("connections_total", "role", "agent", "outcome", "auth_failed")
		writeError(w, http.StatusUnauthorized, "auth_failed")
		return
	}
	ip := remoteIP(r)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "shutting_down")
		return
	}
	limited := h.pending >= h.cfg.MaxPendingAgents || h.pendingByIP[ip] >= h.cfg.MaxPendingAgentsPerIP
	if !limited {
		h.pending++
		h.pendingByIP[ip]++
	}
	h.mu.Unlock()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		if !limited {
			h.mu.Lock()
			h.pending--
			h.decIPLocked(ip)
			h.mu.Unlock()
		}
		return
	}
	if limited {
		h.metrics.inc("connections_total", "role", "agent", "outcome", "pending_limit")
		conn.Close(closeConnectionsLimit, "pending_auth_limit")
		return
	}
	conn.SetReadLimit(h.cfg.AgentMaxFrameBytes)
	a := &agentConn{conn: conn, ip: ip, send: make(chan []byte, 64), pending: true, done: make(chan struct{})}
	h.mu.Lock()
	h.agents[a] = struct{}{}
	h.mu.Unlock()
	h.metrics.inc("connections_total", "role", "agent", "outcome", "accepted")
	h.conns.Add(2)
	go func() { defer h.conns.Done(); a.writeLoop() }()
	defer h.conns.Done()
	h.serveAgent(a)
}

func (h *Hub) decIPLocked(ip string) {
	if h.pendingByIP[ip]--; h.pendingByIP[ip] <= 0 {
		delete(h.pendingByIP, ip)
	}
}

// unpendLocked releases a connection's pending-registration slot once.
func (h *Hub) unpendLocked(a *agentConn) {
	if a.pending {
		a.pending = false
		h.pending--
		h.decIPLocked(a.ip)
	}
}

func (h *Hub) serveAgent(a *agentConn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		h.mu.Lock()
		h.agentGoneLocked(a, time.Now())
		delete(h.agents, a)
		h.mu.Unlock()
		a.closeWith(websocket.StatusNormalClosure, "")
	}()

	regTimer := time.AfterFunc(h.cfg.AgentRegisterTimeout, func() {
		h.mu.Lock()
		registered := a.target != nil
		h.mu.Unlock()
		if !registered {
			h.metrics.inc("closes_total", "role", "agent", "reason", "registration_timeout")
			a.closeWith(closeRegisterTimeout, "registration_timeout")
		}
	})
	defer regTimer.Stop()

	for {
		typ, data, err := a.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusMessageTooBig || errors.Is(err, websocket.ErrMessageTooBig) {
				h.metrics.inc("closes_total", "role", "agent", "reason", "message_too_big")
			}
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		msg, err := wire.Decode(data)
		if err != nil {
			code := "invalid_message"
			if errors.Is(err, wire.ErrVersion) {
				code = "protocol_mismatch"
			}
			h.metrics.inc("rejections_total", "surface", "agent", "reason", code)
			a.enqueue(wire.Encode(&wire.Error{Code: code, Message: err.Error()}))
			h.mu.Lock()
			registered := a.target != nil
			h.mu.Unlock()
			if code == "protocol_mismatch" || !registered {
				a.closeWith(closeProtocol, code)
				return
			}
			continue
		}
		if !h.handleAgentMessage(a, msg) {
			return
		}
	}
}

// handleAgentMessage applies one agent message; false ends the session.
func (h *Hub) handleAgentMessage(a *agentConn, msg wire.Message) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.metrics.inc("messages_total", "role", "agent", "type", messageType(msg))
	if reg, ok := msg.(*wire.Register); ok {
		if code, reason := h.registerLocked(a, reg, now); code != 0 {
			h.metrics.inc("rejections_total", "surface", "agent", "reason", reason)
			a.enqueue(wire.Encode(&wire.Error{Code: reason}))
			a.closeWith(websocket.StatusCode(code), reason)
			return false
		}
		return true
	}
	t := a.target
	if t == nil || t.agent != a {
		// Unregistered, or replaced by a newer agent whose close is in flight.
		if a.pending {
			a.enqueue(wire.Encode(&wire.Error{Code: "not_registered"}))
			a.closeWith(closeProtocol, "not_registered")
		}
		return false
	}
	t.lastMessageAt = now
	switch m := msg.(type) {
	case *wire.Ping:
		a.enqueue(wire.Encode(&wire.Pong{ClientTime: m.ClientTime}))
	case *wire.ObserveStatus:
		h.observeStatusLocked(t, m, now)
	case *wire.SnapshotBegin:
		h.snapshotBeginLocked(t, m, now)
	case *wire.SnapshotChunk:
		if t.wantObserve {
			h.snapshotChunkLocked(t, m, now)
		}
	case *wire.SnapshotEnd:
		if t.wantObserve {
			h.snapshotEndLocked(t, m, now)
		}
	case *wire.Batch:
		if t.wantObserve {
			h.batchLocked(t, m, now)
		}
	case *wire.Error:
		t.lastError = m.Code
	}
	return true
}

func (h *Hub) handleClientSocket(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		writeError(w, http.StatusServiceUnavailable, "shutting_down")
		return
	}
	if !h.originAllowed(r) {
		h.metrics.inc("connections_total", "role", "client", "outcome", "origin")
		writeError(w, http.StatusForbidden, "origin_not_allowed")
		return
	}
	sid, ok := h.authenticated(r)
	if !ok {
		h.metrics.inc("connections_total", "role", "client", "outcome", "auth_required")
		writeError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	h.mu.Lock()
	full := len(h.clients) >= h.cfg.MaxClients || h.closed
	h.mu.Unlock()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // Origin checked above
	if err != nil {
		return
	}
	if full {
		h.metrics.inc("connections_total", "role", "client", "outcome", "connection_limit")
		conn.Close(closeConnectionsLimit, "connection_limit")
		return
	}
	conn.SetReadLimit(64 << 10)
	c := &clientConn{conn: conn, sid: sid, queue: make(chan [][]byte, h.cfg.ClientQueueItems), lastMessageAt: time.Now(), done: make(chan struct{})}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.metrics.inc("connections_total", "role", "client", "outcome", "accepted")
	h.conns.Add(1)
	defer h.conns.Done()
	h.serveClient(c)
}

func (h *Hub) serveClient(c *clientConn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		h.mu.Lock()
		h.releaseLocked(c)
		delete(h.clients, c)
		h.mu.Unlock()
		c.closeWith(websocket.StatusNormalClosure, "")
	}()
	queue := c.queue // captured once; enqueue may only touch the field under Hub.mu
	h.conns.Add(1)
	go func() {
		defer h.conns.Done()
		for {
			select {
			case <-c.done:
				c.conn.Close(c.closeCode, c.closeReason)
				return
			case item := <-queue:
				for _, frame := range item {
					wctx, wcancel := context.WithTimeout(context.Background(), writeTimeout)
					err := c.conn.Write(wctx, websocket.MessageBinary, frame)
					wcancel()
					if err != nil {
						if errors.Is(err, context.DeadlineExceeded) {
							h.metrics.inc("closes_total", "role", "client", "reason", "slow_consumer")
							c.closeWith(closeSlowConsumer, "slow_consumer")
						} else {
							c.closeWith(websocket.StatusGoingAway, "write_failed")
						}
						break
					}
				}
			}
		}
	}()

	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		h.mu.Lock()
		c.lastMessageAt = time.Now()
		h.mu.Unlock()
		if typ != websocket.MessageBinary {
			continue
		}
		msg, err := wire.Decode(data)
		if err != nil {
			code := "invalid_message"
			if errors.Is(err, wire.ErrVersion) {
				code = "protocol_mismatch"
			}
			h.mu.Lock()
			c.enqueue(h, [][]byte{wire.Encode(&wire.Error{Code: code, Message: err.Error()})})
			h.mu.Unlock()
			continue
		}
		h.handleClientMessage(c, msg)
	}
}

func (h *Hub) handleClientMessage(c *clientConn, msg wire.Message) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.metrics.inc("messages_total", "role", "client", "type", messageType(msg))
	reply := func(frames ...[]byte) { c.enqueue(h, frames) }
	switch m := msg.(type) {
	case *wire.Ping:
		reply(wire.Encode(&wire.Pong{ClientTime: m.ClientTime}))
	case *wire.Subscribe:
		if m.TargetID == "" {
			reply(wire.Encode(&wire.Error{Code: "invalid_message", Message: "subscribe needs a target id"}))
			return
		}
		t := h.targets[m.TargetID]
		if c.target != t {
			h.releaseLocked(c)
		}
		if t == nil {
			reply(wire.Encode(&wire.Error{Code: "target_not_found", TargetID: m.TargetID}))
			return
		}
		if c.target != t {
			h.acquireLocked(c, t, now)
		}
		item := append([][]byte{t.schemaFrame}, h.snapshotItemLocked(c, t)...)
		c.enqueue(h, item)
	case *wire.Resync:
		switch {
		case m.TargetID == "":
			reply(wire.Encode(&wire.Error{Code: "invalid_message", Message: "resync needs a target id"}))
		case c.target == nil:
			reply(wire.Encode(&wire.Error{Code: "not_subscribed", TargetID: m.TargetID}))
		case c.target.id != m.TargetID:
			reply(wire.Encode(&wire.Error{Code: "target_mismatch", TargetID: m.TargetID}))
		default:
			c.enqueue(h, h.snapshotItemLocked(c, c.target))
		}
	default:
		reply(wire.Encode(&wire.Error{Code: "invalid_message", Message: "unexpected " + messageType(msg)}))
	}
}

func messageType(msg wire.Message) string {
	switch msg.(type) {
	case *wire.Register:
		return "register"
	case *wire.ObserveStatus:
		return "observe_status"
	case *wire.SnapshotBegin:
		return "snapshot_begin"
	case *wire.SnapshotChunk:
		return "snapshot_chunk"
	case *wire.SnapshotEnd:
		return "snapshot_end"
	case *wire.Batch:
		return "batch"
	case *wire.Ping:
		return "ping"
	case *wire.Subscribe:
		return "subscribe"
	case *wire.Resync:
		return "resync"
	case *wire.Error:
		return "error"
	}
	return "other"
}
