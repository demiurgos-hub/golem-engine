package golem

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golem-engine/golem/collision3d"
	"golem-engine/golem/interest"
	golemnet "golem-engine/golem/net"
	"golem-engine/golem/registry"
	"golem-engine/golem/world"

	"golem.collision"
	"golem.nav"
)

// msgKind identifies the type of a queued session event.
type msgKind uint8

const (
	msgConnect           msgKind = 0
	msgMessage           msgKind = 1
	msgDatagram          msgKind = 2
	msgReliableUnordered msgKind = 3
	msgReliableOrdered   msgKind = 4
	msgEventualFeedback  msgKind = 5
	msgDisconnect        msgKind = 6
)

// pendingMsg is a session event queued from a connection goroutine to be
// dispatched on the tick goroutine.
type pendingMsg struct {
	kind             msgKind
	sess             *Session
	data             []byte
	eventualFeedback []golemnet.EventualStateDelivery
}

// interestTickScratch holds tick-local lookup maps reused by runInterestTick.
// The cached payload fragments are immutable inputs; only the maps are reset
// between ticks. Per-session batch buffers are still owned by the send path
// and must not be reused after SendBatch enqueues them.
type interestTickScratch struct {
	spawnData        map[int64][]byte
	deltaData        map[int64][]byte
	removalData      map[int64][]byte
	wrappedSpawns    map[int64][]byte
	wrappedDeltas    map[int64][]byte
	wrappedRemovals  map[int64][]byte
	wrappedFull      map[int64][]byte
	eventualChanges  map[int64]eventualStateChange
	eventualFrames   map[int64]eventualPreparedFrame
	eventualDirty    []eventualStateChange
	eventualPrepared []eventualPreparedFrame
}

// reset clears the reused per-tick lookup maps while preserving capacity.
func (s *interestTickScratch) reset() {
	s.eventualDirty = s.eventualDirty[:0]
	if s.spawnData == nil {
		s.spawnData = make(map[int64][]byte)
		s.deltaData = make(map[int64][]byte)
		s.removalData = make(map[int64][]byte)
		s.wrappedSpawns = make(map[int64][]byte)
		s.wrappedDeltas = make(map[int64][]byte)
		s.wrappedRemovals = make(map[int64][]byte)
		s.wrappedFull = make(map[int64][]byte)
		s.eventualChanges = make(map[int64]eventualStateChange)
		s.eventualFrames = make(map[int64]eventualPreparedFrame)
		return
	}

	clear(s.spawnData)
	clear(s.deltaData)
	clear(s.removalData)
	clear(s.wrappedSpawns)
	clear(s.wrappedDeltas)
	clear(s.wrappedRemovals)
	clear(s.wrappedFull)
	clear(s.eventualChanges)
	clear(s.eventualFrames)
}

const msgQueueCap = 4096

// StateUpdateLane selects the transport lane used for incremental entity
// updates during integrated networking.
type StateUpdateLane string

const (
	// StateUpdateLaneStream keeps incremental entity updates on the reliable
	// stream path.
	StateUpdateLaneStream StateUpdateLane = "stream"
	// StateUpdateLaneDatagram sends incremental entity updates over
	// state-aware WebTransport datagrams that rebase lost fields to current values.
	StateUpdateLaneDatagram StateUpdateLane = "datagram"
)

// ServerConfig holds configuration for the game server.
type ServerConfig struct {
	TickRate          int                // ticks per second (default: 20)
	Addr              string             // listen address (e.g. ":8080" or ":4433"); enables integrated networking when set
	Path              string             // transport endpoint path (default: "/ws" or "/wt")
	Transport         golemnet.Transport // integrated transport kind (default: golem.TransportWebTransport)
	TLSCertFile       string             // PEM certificate file used by WebTransport / HTTP3
	TLSKeyFile        string             // PEM private key file used by WebTransport / HTTP3
	DevSelfSignedCert bool               // generate a short-lived self-signed certificate for WebTransport when no files are configured
	// WebTransportAllowedOrigins lists exact browser origins allowed to connect
	// to WebTransport, e.g. "https://game.example.com:8080".
	WebTransportAllowedOrigins []string
	// WebTransportAllowSameHostOrigin allows HTTPS origins whose hostname
	// matches the WebTransport request host, regardless of port.
	WebTransportAllowSameHostOrigin bool
	StaticDir                       string          // directory of static files served over HTTP (optional)
	MapDir                          string          // directory of map files served at /maps/ over HTTP (optional)
	CellSize                        float64         // spatial hash cell size; >0 enables interest management
	StateUpdateLane                 StateUpdateLane // incremental entity update transport lane (default: datagram)
	// LogReplicationStats emits a log line about once per second with batched
	// state-update counts, wire message counts, and registry delta flush size
	// for the most recently completed tick (reliable stream vs datagrams).
	// The environment variable GOLEM_LOG_REPLICATION_STATS=1 (or "true", case
	// insensitive) also turns this on for quick debugging without recompiling.
	LogReplicationStats bool
}

// TickFunc is the signature for the user's per-tick game logic callback.
// dt is the fixed time step in seconds; s is the server (entities, world, networking).
type TickFunc func(dt float64, s *Server)

// UpdateFunc is called after each tick with all serialized entity updates
// (spawns, deltas, and removals combined) before auto-broadcast.
type UpdateFunc func(updates [][]byte)

// ContactFunc is called after each collision step with all contacts detected
// in that tick. It is only invoked when at least one contact was found.
type ContactFunc func(contacts []collision.Contact)

// Contact3DFunc is called after each 3D collision step with contacts detected
// in that tick. It is only invoked when at least one contact was found.
type Contact3DFunc func(contacts []collision3d.Contact)

// RemovalSerializer converts a removed entity ID and revision into a serialized
// EntityUpdate message. Provided by generated code (e.g. synced.MarshalEntityRemoved).
type RemovalSerializer func(entityID int64, revision uint64) ([]byte, error)

// Server runs the core game loop and, when Addr is configured, an integrated
// transport server for client connections.
type Server struct {
	idCounter            int64 // atomically incremented; 0 is reserved as "unassigned"
	reg                  *registry.Registry
	World                *world.Store
	config               ServerConfig
	listener             *golemnet.Listener
	interest             *interest.Manager
	collision            collision.Backend
	collision3D          collision3d.Backend
	navBackend           nav.Backend
	contactEventsEnabled bool
	triggerPairs         map[[2]int64]struct{}
	solidPairs           map[[2]int64]struct{}
	tick                 uint64
	onTickStart          []func(tick uint64)
	onTickEnd            []func(tick uint64, wall time.Duration)
	onTick               TickFunc
	onUpdates            UpdateFunc
	onContact            ContactFunc
	onContact3D          Contact3DFunc
	removalSerializer    RemovalSerializer
	onConnect            func(*Session)
	onMessage            func(*Session, []byte)
	onDatagram           func(*Session, []byte)
	onReliableUnordered  func(*Session, []byte)
	onReliableOrdered    func(*Session, []byte)
	onDisconnect         func(*Session)
	msgQueue             chan pendingMsg
	interestScratch      interestTickScratch
	eventualScratch      eventualStateSendScratch
	eventualTrackers     map[int64]*eventualStateTracker
	nextEventualToken    uint64

	replStatsMu         sync.Mutex
	replLastTick        uint64
	replDeltasFlushed   int
	replStreamBatched   int
	replStreamMsgs      int
	replDatagramBatched int
	replDatagramMsgs    int
}

// NewServer creates a game server with the given configuration.
// The internal Listener is always created so Handler() works regardless of
// whether Addr is set. When Addr is set, Run also starts the built-in
// transport server; otherwise only the tick loop runs and the game
// is expected to mount Handler() on its own router.
// When CellSize > 0, interest management is enabled and entity snapshots
// on connect are deferred to the interest system.
// Session lifecycle hooks (OnConnect, OnMessage, OnDisconnect) are queued
// from connection goroutines and dispatched on the tick goroutine.
func NewServer(cfg ServerConfig) *Server {
	if cfg.TickRate <= 0 {
		cfg.TickRate = 20
	}
	if envLogReplicationStats() {
		cfg.LogReplicationStats = true
	}
	cfg.Transport = normalizeServerTransport(cfg.Transport)
	cfg.StateUpdateLane = normalizeStateUpdateLane(cfg.StateUpdateLane)
	validateStateUpdateLane(cfg)
	s := &Server{
		reg:      registry.NewRegistry(),
		World:    world.NewStore(),
		config:   cfg,
		msgQueue: make(chan pendingMsg, msgQueueCap),
	}
	if cfg.CellSize > 0 {
		s.interest = interest.NewManager(cfg.CellSize)
	}
	s.listener = golemnet.NewListener(s.reg, golemnet.Config{
		Addr:                            cfg.Addr,
		Path:                            cfg.Path,
		Transport:                       cfg.Transport,
		TLSCertFile:                     cfg.TLSCertFile,
		TLSKeyFile:                      cfg.TLSKeyFile,
		DevSelfSignedCert:               cfg.DevSelfSignedCert,
		WebTransportAllowedOrigins:      cfg.WebTransportAllowedOrigins,
		WebTransportAllowSameHostOrigin: cfg.WebTransportAllowSameHostOrigin,
		StaticDir:                       cfg.StaticDir,
		MapDir:                          cfg.MapDir,
	})
	s.listener.SetMessageWrapper(WrapEntityUpdate)
	if s.interest != nil {
		s.listener.SetInterestEnabled(true)
	}
	s.listener.SetWorldSnapshotFunc(func() ([][]byte, error) {
		updates, err := s.World.MarshalAll()
		if err != nil {
			return nil, err
		}
		wrapped := make([][]byte, len(updates))
		for i, u := range updates {
			wrapped[i] = WrapWorldUpdate(u)
		}
		return wrapped, nil
	})
	// Wire listener session-event hooks to enqueue methods so that user
	// callbacks always fire on the tick goroutine rather than the connection
	// goroutine. OnConnect/OnMessage/OnDisconnect on Server only store the
	// user callback; they never touch the listener.
	s.listener.OnConnect(s.enqueueConnect)
	s.listener.OnMessage(s.enqueueMessage)
	s.listener.OnDatagram(s.enqueueDatagram)
	s.listener.OnReliableUnordered(s.enqueueReliableUnordered)
	s.listener.OnReliableOrdered(s.enqueueReliableOrdered)
	s.listener.OnEventualStateFeedback(s.enqueueEventualStateFeedback)
	s.listener.OnDisconnect(s.enqueueDisconnect)
	return s
}

func normalizeStateUpdateLane(lane StateUpdateLane) StateUpdateLane {
	if lane == "" {
		return StateUpdateLaneDatagram
	}
	return lane
}

// normalizeServerTransport applies the Server-level transport default.
func normalizeServerTransport(transport golemnet.Transport) golemnet.Transport {
	if transport == "" {
		return golemnet.TransportWebTransport
	}
	return transport
}

func validateStateUpdateLane(cfg ServerConfig) {
	switch cfg.StateUpdateLane {
	case StateUpdateLaneStream:
		return
	case StateUpdateLaneDatagram:
		if cfg.Transport != golemnet.TransportWebTransport {
			panic(fmt.Sprintf("golem: StateUpdateLane %s requires TransportWebTransport", cfg.StateUpdateLane))
		}
	default:
		panic(fmt.Sprintf("golem: unknown StateUpdateLane %q", cfg.StateUpdateLane))
	}
}

// nextID atomically increments the entity ID counter and returns the new value.
// IDs start at 1; 0 is reserved as the sentinel for "not yet assigned".
func (s *Server) nextID() int64 {
	return atomic.AddInt64(&s.idCounter, 1)
}

// ReserveEntityID increments the counter and returns the reserved ID.
// Pass the returned value as the optional trailing argument to the generated
// NewSynced* constructor when you need to know the ID before the entity is
// registered (e.g. to wire up relationships between entities up-front).
func (s *Server) ReserveEntityID() int64 {
	return s.nextID()
}

// SetEntityIDCounter seeds the ID counter to n. The next auto-assigned or
// reserved ID will be n+1. Call this during server initialisation when
// loading persisted state so that new entities never collide with existing ones.
func (s *Server) SetEntityIDCounter(n int64) {
	atomic.StoreInt64(&s.idCounter, n)
}

// CreateEntity registers e for simulation and replication. If the entity was
// constructed without an ID (EntityID() == 0), the next counter value is
// assigned automatically via EntityIDSetter. With no extra owner arguments the
// entity is unowned (e.g. world NPC); with one argument that value is the
// owning session ID for command authority. More than one owner argument is invalid.
func (s *Server) CreateEntity(e Entity, owner ...int64) error {
	if len(owner) > 1 {
		return fmt.Errorf("golem: CreateEntity expects 0 or 1 owner session ID, got %d", len(owner))
	}

	if e.EntityID() == 0 {
		setter, ok := e.(registry.EntityIDSetter)
		if !ok {
			return fmt.Errorf("golem: entity has no ID and does not implement EntityIDSetter")
		}
		setter.SetEntityID(s.nextID())
	}

	if len(owner) == 1 {
		return s.reg.AddOwned(e, owner[0])
	}
	return s.reg.Add(e)
}

// DeleteEntity unregisters an entity by ID and queues a removal for clients.
func (s *Server) DeleteEntity(id int64) { s.reg.DeleteEntity(id) }

// Get returns the entity with the given ID, or (nil, false) if not found.
func (s *Server) Get(id int64) (Entity, bool) { return s.reg.Get(id) }

// Owner returns the session ID that owns the entity, if any.
func (s *Server) Owner(entityID int64) (sessionID int64, owned bool) {
	return s.reg.Owner(entityID)
}

// SetOwner updates the owning session of an existing entity.
func (s *Server) SetOwner(entityID, sessionID int64) bool { return s.reg.SetOwner(entityID, sessionID) }

// All returns a snapshot of every registered entity.
func (s *Server) All() []Entity { return s.reg.All() }

// Len returns the number of registered entities.
func (s *Server) Len() int { return s.reg.Len() }

// SnapshotAll returns a full-state serialized update for every live entity.
func (s *Server) SnapshotAll() ([][]byte, error) { return s.reg.SnapshotAll() }

// OnTick registers the game logic callback invoked once per tick.
func (s *Server) OnTick(fn TickFunc) {
	s.onTick = fn
}

// OnTickStart appends a callback called at the very start of each tick,
// before entity updates and game logic. tick is the 1-based tick counter.
// Runs on the tick goroutine, so runtime/trace regions and pprof.Do labels
// nest cleanly with work done in OnTick and OnTickEnd.
func (s *Server) OnTickStart(fn func(tick uint64)) {
	s.onTickStart = append(s.onTickStart, fn)
}

// OnTickEnd appends a callback called at the end of each tick, after all
// entity updates and the broadcast flush. wall is the total tick wall time.
// Use it to record per-tick latency histograms or close trace regions opened
// in OnTickStart.
func (s *Server) OnTickEnd(fn func(tick uint64, wall time.Duration)) {
	s.onTickEnd = append(s.onTickEnd, fn)
}

// Tick returns the current tick counter. The counter is 1-based and is
// incremented at the start of each tick before OnTickStart fires.
// Safe to read from OnTick, OnTickStart, and OnTickEnd.
func (s *Server) Tick() uint64 { return s.tick }

// OnUpdates registers a callback that receives all serialized entity updates
// (spawns, deltas, and removals) after each tick. When integrated networking
// is active the server broadcasts automatically; use OnUpdates for extra
// logic like logging or filtering.
func (s *Server) OnUpdates(fn UpdateFunc) {
	s.onUpdates = fn
}

// SetRemovalSerializer registers the function used to serialize EntityRemoved
// messages. Pass the generated synced.MarshalEntityRemoved.
func (s *Server) SetRemovalSerializer(fn RemovalSerializer) {
	s.removalSerializer = fn
}

// SetCollisionBackend attaches a collision backend to the server. When set,
// each tick syncs entity positions into the backend, steps it, reads physics
// corrections back via PositionWriter, and fires OnContact handlers.
// Shape registration (Add/Remove) remains in game code.
func (s *Server) SetCollisionBackend(b collision.Backend) {
	s.collision = b
}

// SetCollision3DBackend attaches a 3D collision backend to the server. When set,
// each tick syncs 3D entity positions into the backend, steps it, reads
// corrections back via Position3DWriter, and fires OnContact3D handlers.
func (s *Server) SetCollision3DBackend(b collision3d.Backend) {
	s.collision3D = b
}

// OnContact registers a callback invoked after each collision step when at
// least one contact was detected. Contacts include both solid overlaps and
// trigger overlaps (Depth == 0 for triggers).
func (s *Server) OnContact(fn ContactFunc) {
	s.onContact = fn
}

// OnContact3D registers a callback invoked after each 3D collision step when at
// least one contact was detected.
func (s *Server) OnContact3D(fn Contact3DFunc) {
	s.onContact3D = fn
}

// Handler returns the configured transport endpoint handler for external
// mounting on a custom router. When using WebTransport, pair it with
// WebTransportServer on a caller-owned HTTP/3 server.
func (s *Server) Handler() http.HandlerFunc {
	return s.listener.Handler()
}

// WebTransportServer returns the configured WebTransport server for callers
// that mount Server.Handler on a caller-owned HTTP/3 server. Callers still
// own TLS configuration and server startup.
func (s *Server) WebTransportServer(h3 *http3.Server) *webtransport.Server {
	return s.listener.WebTransportServer(h3)
}

// MapFileHandler returns an http.Handler that serves map files from dir.
// Mount it on a custom router when using an external HTTP server:
//
//	mux.Handle("/maps/", http.StripPrefix("/maps/", server.MapFileHandler("maps/")))
//
// When using the integrated server, set ServerConfig.MapDir instead.
func (s *Server) MapFileHandler(dir string) http.Handler {
	return http.FileServer(http.Dir(dir))
}

// OnUpgrade registers a hook called before transport session acceptance. The
// hook receives the HTTP request for auth inspection; returning a non-nil
// error rejects the request with HTTP 401. The returned value is stored in
// Session.Data before OnConnect fires.
func (s *Server) OnUpgrade(fn func(*http.Request) (any, error)) {
	s.listener.OnUpgrade(fn)
}

// SetWebTransportCheckOrigin overrides the WebTransport origin policy. Passing
// nil restores the policy built from ServerConfig.
func (s *Server) SetWebTransportCheckOrigin(fn func(*http.Request) bool) {
	s.listener.SetWebTransportCheckOrigin(fn)
}

// OnConnect registers a hook called when a client connects and receives the
// world-state snapshot. The hook fires on the tick goroutine, so it is safe
// to call CreateEntity and other Server methods directly.
func (s *Server) OnConnect(fn func(*Session)) {
	s.onConnect = fn
}

// OnMessage registers a hook called when a client sends a binary message.
// The hook fires on the tick goroutine, so it is safe to call CreateEntity,
// DeleteEntity, and other Server methods directly.
func (s *Server) OnMessage(fn func(*Session, []byte)) {
	s.onMessage = fn
}

// OnDatagram registers a hook called when a client sends an unreliable datagram.
// The hook fires on the tick goroutine, so it is safe to call Server methods directly.
func (s *Server) OnDatagram(fn func(*Session, []byte)) {
	s.onDatagram = fn
}

// OnReliableUnordered registers a hook called when a client sends a reliable unordered datagram.
// The hook fires on the tick goroutine, so it is safe to call Server methods directly.
func (s *Server) OnReliableUnordered(fn func(*Session, []byte)) {
	s.onReliableUnordered = fn
}

// OnReliableOrdered registers a hook called when a client sends a reliable ordered datagram.
// The hook fires on the tick goroutine, so it is safe to call Server methods directly.
func (s *Server) OnReliableOrdered(fn func(*Session, []byte)) {
	s.onReliableOrdered = fn
}

// OnDisconnect registers a hook called when a client disconnects.
// The hook fires on the tick goroutine, so it is safe to call DeleteEntity
// and other Server methods directly.
func (s *Server) OnDisconnect(fn func(*Session)) {
	s.onDisconnect = fn
}

// Send delivers a single binary message to a specific session without
// altering bytes. When integrated networking uses the ServerMessage envelope,
// entity payloads must already be wrapped (e.g. WrapEntityUpdate); world
// payloads must use WrapWorldUpdate.
func (s *Server) Send(sessionID int64, data []byte) error {
	return s.listener.Send(sessionID, data)
}

// Broadcast sends a set of binary messages to every connected session.
// No-op when no clients are connected.
func (s *Server) Broadcast(data [][]byte) error {
	return s.listener.Broadcast(data)
}

// BroadcastEvent sends a pre-wrapped server event frame to every connected
// session without applying the entity messageWrapper.
func (s *Server) BroadcastEvent(data []byte) error {
	return s.listener.BroadcastRaw(data)
}

// SendUnreliable sends one lossy datagram to a specific session when supported.
func (s *Server) SendUnreliable(sessionID int64, data []byte) error {
	return s.listener.SendUnreliable(sessionID, data)
}

// BroadcastUnreliable sends one lossy datagram to every connected session.
func (s *Server) BroadcastUnreliable(data []byte) error {
	return s.listener.BroadcastUnreliable(data)
}

// SendReliableUnordered sends one reliable unordered datagram to a specific session when supported.
func (s *Server) SendReliableUnordered(sessionID int64, data []byte) error {
	return s.listener.SendReliableUnordered(sessionID, data)
}

// BroadcastReliableUnordered sends one reliable unordered datagram to every connected session.
func (s *Server) BroadcastReliableUnordered(data []byte) error {
	return s.listener.BroadcastReliableUnordered(data)
}

// SendReliableOrdered sends one reliable ordered datagram to a specific session when supported.
func (s *Server) SendReliableOrdered(sessionID int64, data []byte) error {
	return s.listener.SendReliableOrdered(sessionID, data)
}

// BroadcastReliableOrdered sends one reliable ordered datagram to every connected session.
func (s *Server) BroadcastReliableOrdered(data []byte) error {
	return s.listener.BroadcastReliableOrdered(data)
}

// WebTransportCertificateHashes returns the WebTransport certificate digests
// known by the integrated listener.
func (s *Server) WebTransportCertificateHashes() []golemnet.CertificateHash {
	return s.listener.CertificateHashes()
}

// WaitReady blocks until the integrated listener has prepared its transport
// endpoint. When WebTransport is active, the TLS certificate has been resolved
// after this returns nil.
func (s *Server) WaitReady(ctx context.Context) error {
	if s.config.Addr == "" {
		return nil
	}
	return s.listener.WaitReady(ctx)
}

// SessionsKnowing returns the IDs of all sessions that currently have
// entityID in their known FOI set. When interest management is not enabled,
// returns all connected session IDs (every entity is visible to every session).
// Must be called from the game tick goroutine when interest management is active.
func (s *Server) SessionsKnowing(entityID int64) []int64 {
	allSessions := s.listener.SessionIDs()
	if s.interest == nil {
		return allSessions
	}
	var result []int64
	for _, sid := range allSessions {
		known := s.interest.Known(sid)
		if _, ok := known[entityID]; ok {
			result = append(result, sid)
		}
	}
	return result
}

// AssignFOI associates a session with a circular field of interest centred
// on the given entity. Panics if interest management is not enabled (CellSize <= 0).
func (s *Server) AssignFOI(sessionID, entityID int64, radius, margin float64) {
	if s.interest == nil {
		panic("golem: AssignFOI called but interest management is not enabled (CellSize <= 0)")
	}
	s.interest.AssignFOI(sessionID, entityID, radius, margin)
}

// RemoveFOI removes a session's field of interest and clears its known set.
// Panics if interest management is not enabled.
func (s *Server) RemoveFOI(sessionID int64) {
	if s.interest == nil {
		panic("golem: RemoveFOI called but interest management is not enabled (CellSize <= 0)")
	}
	s.interest.RemoveFOI(sessionID)
}

// PushWorldData broadcasts the current value of a single world data type to
// all connected sessions. Returns nil if the name is not in the store or the
// broadcast succeeds. Returns a non-nil error if serialization fails.
func (s *Server) PushWorldData(name string) error {
	d := s.World.Get(name)
	if d == nil {
		return nil
	}
	data, err := d.MarshalUpdate()
	if err != nil {
		return err
	}
	return s.listener.BroadcastRaw(WrapWorldUpdate(data))
}

// enqueue pushes m onto the message queue. If the queue is full the event is
// dropped and a warning is logged to avoid blocking the connection goroutine.
func (s *Server) enqueue(m pendingMsg) {
	select {
	case s.msgQueue <- m:
	default:
		sessID := int64(0)
		if m.sess != nil {
			sessID = m.sess.ID
		}
		log.Printf("golem: msgQueue full, dropping %d event for session %d", m.kind, sessID)
	}
}

// enqueueConnect is installed on the Listener as the OnConnect hook. It runs
// on the connection goroutine and pushes a connect event for tick-time dispatch.
func (s *Server) enqueueConnect(sess *Session) {
	s.enqueue(pendingMsg{kind: msgConnect, sess: sess})
}

// enqueueMessage is installed on the Listener as the OnMessage hook. It runs
// on the connection goroutine, copies the payload, and pushes a message event
// for tick-time dispatch.
func (s *Server) enqueueMessage(sess *Session, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.enqueue(pendingMsg{kind: msgMessage, sess: sess, data: cp})
}

// enqueueDatagram is installed on the Listener as the OnDatagram hook. It runs
// on the connection goroutine, copies the payload, and pushes a datagram event
// for tick-time dispatch.
func (s *Server) enqueueDatagram(sess *Session, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.enqueue(pendingMsg{kind: msgDatagram, sess: sess, data: cp})
}

// enqueueReliableUnordered is installed on the Listener as the reliable unordered hook.
// It runs on the connection goroutine, copies the payload, and pushes a datagram event
// for tick-time dispatch.
func (s *Server) enqueueReliableUnordered(sess *Session, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.enqueue(pendingMsg{kind: msgReliableUnordered, sess: sess, data: cp})
}

// enqueueReliableOrdered is installed on the Listener as the reliable ordered hook.
// It runs on the connection goroutine, copies the payload, and pushes a datagram event
// for tick-time dispatch.
func (s *Server) enqueueReliableOrdered(sess *Session, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.enqueue(pendingMsg{kind: msgReliableOrdered, sess: sess, data: cp})
}

// enqueueEventualStateFeedback is installed on the Listener as the eventual state feedback hook.
// It runs on the connection goroutine and pushes ACK/loss feedback for tick-time dispatch.
func (s *Server) enqueueEventualStateFeedback(sess *Session, feedback []golemnet.EventualStateDelivery) {
	s.enqueue(pendingMsg{kind: msgEventualFeedback, sess: sess, eventualFeedback: feedback})
}

// enqueueDisconnect is installed on the Listener as the OnDisconnect hook. It
// runs on the connection goroutine and pushes a disconnect event for tick-time
// dispatch.
func (s *Server) enqueueDisconnect(sess *Session) {
	s.enqueue(pendingMsg{kind: msgDisconnect, sess: sess})
}

// drainMessages processes all queued session events in FIFO order on the tick
// goroutine. Called once per tick before entity ticks so that handlers such as
// CreateEntity and DeleteEntity are safe without additional synchronization.
func (s *Server) drainMessages() {
	for {
		select {
		case m := <-s.msgQueue:
			switch m.kind {
			case msgConnect:
				if s.onConnect != nil {
					s.onConnect(m.sess)
				}
			case msgMessage:
				if s.onMessage != nil {
					s.onMessage(m.sess, m.data)
				}
			case msgDatagram:
				if s.onDatagram != nil {
					s.onDatagram(m.sess, m.data)
				}
			case msgReliableUnordered:
				if s.onReliableUnordered != nil {
					s.onReliableUnordered(m.sess, m.data)
				}
			case msgReliableOrdered:
				if s.onReliableOrdered != nil {
					s.onReliableOrdered(m.sess, m.data)
				}
			case msgEventualFeedback:
				s.applyEventualStateFeedback(m.sess.ID, m.eventualFeedback)
			case msgDisconnect:
				if s.eventualTrackers != nil {
					delete(s.eventualTrackers, m.sess.ID)
				}
				if s.onDisconnect != nil {
					s.onDisconnect(m.sess)
				}
			}
		default:
			return
		}
	}
}

// Run starts the game loop at the configured tick rate. When Addr is set it
// also starts the built-in transport server. Entity updates are
// auto-broadcast to all connected clients after each tick regardless of
// whether the built-in server or an external router is used.
// Each tick runs in order: OnTickStart, drain session-event queue (OnConnect /
// OnMessage / OnDisconnect), entity ticks, OnTick game logic, collision step,
// flush and broadcast, OnTickEnd.
// Blocks until ctx is cancelled. Returns ctx.Err() on clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	if s.config.Addr != "" {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		listenerErr := make(chan error, 1)
		go func() {
			err := s.listener.ListenAndServe(ctx)
			listenerErr <- err
			if err != nil {
				cancel()
			}
		}()

		loopErr := s.runLoop(ctx)
		cancel()
		lErr := <-listenerErr

		if lErr != nil && loopErr == context.Canceled {
			return fmt.Errorf("listener: %w", lErr)
		}
		return loopErr
	}
	return s.runLoop(ctx)
}

// envLogReplicationStats reports whether the process environment requests
// replication batching / wire-size logs (1 Hz) without setting ServerConfig.
func envLogReplicationStats() bool {
	v := strings.TrimSpace(os.Getenv("GOLEM_LOG_REPLICATION_STATS"))
	if v == "" {
		return false
	}
	v = strings.ToLower(v)
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (s *Server) runLoop(ctx context.Context) error {
	interval := time.Second / time.Duration(s.config.TickRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	dt := interval.Seconds()
	if s.config.LogReplicationStats {
		log.Print("golem: LogReplicationStats enabled (1 Hz; GOLEM_LOG_REPLICATION_STATS=1 or ServerConfig.LogReplicationStats)")
		go s.replicationStatsLoop(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.tick++
			start := time.Now()
			for _, fn := range s.onTickStart {
				fn(s.tick)
			}

			s.drainMessages()

			s.reg.TickAll(dt)

			if s.onTick != nil {
				s.onTick(dt, s)
			}

			if s.collision != nil {
				for _, e := range s.reg.All() {
					x, y := e.Position()
					s.collision.Update(e.EntityID(), float64(x), float64(y))
				}
				contacts := s.collision.Step(dt)
				s.collision.ReadBack(func(id int64, x, y float64) {
					e, ok := s.reg.Get(id)
					if !ok {
						return
					}
					if pw, ok := e.(registry.PositionWriter); ok {
						pw.SetPosition(float32(x), float32(y))
					}
				})
				if s.onContact != nil && len(contacts) > 0 {
					s.onContact(contacts)
				}
				if s.contactEventsEnabled {
					s.dispatchContactEvents(contacts)
				}
			}

			if s.collision3D != nil {
				for _, e := range s.reg.All() {
					sp, ok := e.(registry.Spatial3DEntity)
					if !ok {
						continue
					}
					x, y, z := sp.Position3D()
					s.collision3D.Update(e.EntityID(), float64(x), float64(y), float64(z))
				}
				contacts := s.collision3D.Step(dt)
				s.collision3D.ReadBack(func(id int64, x, y, z float64) {
					e, ok := s.reg.Get(id)
					if !ok {
						return
					}
					if pw, ok := e.(registry.Position3DWriter); ok {
						pw.SetPosition3D(float32(x), float32(y), float32(z))
					}
				})
				if s.onContact3D != nil && len(contacts) > 0 {
					s.onContact3D(contacts)
				}
			}

			if s.interest != nil {
				if err := s.runInterestTick(); err != nil {
					return err
				}
			} else {
				if err := s.runBroadcastTick(); err != nil {
					return err
				}
			}

			wall := time.Since(start)
			for _, fn := range s.onTickEnd {
				fn(s.tick, wall)
			}
		}
	}
}

// runBroadcastTick is the original blind-broadcast path used when interest
// management is not enabled.
func (s *Server) runBroadcastTick() error {
	result, err := s.reg.FlushAll()
	if err != nil {
		return err
	}
	deltasFlushed := len(result.Deltas)

	var (
		updates       [][]byte
		streamUpdates [][]byte
	)
	updates = append(updates, result.Spawns...)
	updates = append(updates, result.Deltas...)
	streamUpdates = append(streamUpdates, result.Spawns...)

	for i, id := range result.Removals {
		if s.removalSerializer == nil {
			return fmt.Errorf("entity %d removed but no RemovalSerializer configured", id)
		}
		data, err := s.removalSerializer(id, result.RemovalRevisions[i])
		if err != nil {
			return fmt.Errorf("serializing removal for entity %d: %w", id, err)
		}
		updates = append(updates, data)
		streamUpdates = append(streamUpdates, data)
	}
	if s.usesDatagramStateUpdates() {
		s.clearEventualEntities(result.Removals)
	}

	if len(updates) == 0 {
		s.storeReplicationSnapshot(0, 0, 0, 0, deltasFlushed)
		return nil
	}
	if s.onUpdates != nil {
		s.onUpdates(updates)
	}
	sessionIDs := s.listener.SessionIDs()
	nClients := len(sessionIDs)
	if !s.usesDatagramStateUpdates() {
		if err := s.listener.BroadcastBatch(updates); err != nil {
			return err
		}
		s.storeBroadcastStreamOnly(updates, deltasFlushed, nClients)
		return nil
	}

	if len(streamUpdates) > 0 {
		if err := s.listener.BroadcastBatch(streamUpdates); err != nil {
			return err
		}
	}
	var datagramBatched, datagramMsgs int
	eventualCache := newEventualStateTickCache()
	eventualChanges := make([]eventualStateChange, 0, len(result.DeltaIDs))
	eventualPrepared := make([]eventualPreparedFrame, 0, len(result.DeltaIDs))
	for _, id := range result.DeltaIDs {
		ch := s.eventualChangeForDelta(id)
		eventualChanges = append(eventualChanges, ch)
		prepared, err := s.eventualPreparedFrameForChange(ch)
		if err != nil {
			return err
		}
		eventualPrepared = append(eventualPrepared, prepared)
	}
	for _, sessionID := range sessionIDs {
		tracker := s.eventualTracker(sessionID)
		var (
			batched int
			msgs    int
			err     error
		)
		if !tracker.hasDirty() {
			batched, msgs, err = s.sendPreparedEventualStateFrames(sessionID, tracker, eventualPrepared)
		} else {
			for _, ch := range eventualChanges {
				tracker.markDirtyChange(ch)
			}
			batched, msgs, err = s.sendEventualState(sessionID, tracker, eventualCache)
		}
		if err != nil {
			return err
		}
		datagramBatched += batched
		datagramMsgs += msgs
	}
	s.storeReplicationSnapshot(len(streamUpdates)*nClients, 0, datagramBatched, datagramMsgs, deltasFlushed)
	return nil
}

// runInterestTick performs per-session interest-filtered sends.
func (s *Server) runInterestTick() error {
	s.interest.UpdateGrid(s.reg)
	diffs := s.interest.ComputeDiffs()

	result, err := s.reg.FlushAll()
	if err != nil {
		return err
	}

	scratch := &s.interestScratch
	scratch.reset()

	spawnData := scratch.spawnData
	wrappedSpawns := scratch.wrappedSpawns
	for i, id := range result.SpawnIDs {
		data := result.Spawns[i]
		spawnData[id] = data
		wrappedSpawns[id] = s.listener.Wrap(data)
	}

	deltaData := scratch.deltaData
	wrappedDeltas := scratch.wrappedDeltas
	eventualChanges := scratch.eventualChanges
	eventualFrames := scratch.eventualFrames
	for i, id := range result.DeltaIDs {
		data := result.Deltas[i]
		deltaData[id] = data
		if s.usesDatagramStateUpdates() {
			ch := s.eventualChangeForDelta(id)
			eventualChanges[id] = ch
			prepared, err := s.eventualPreparedFrameForChange(ch)
			if err != nil {
				return err
			}
			eventualFrames[id] = prepared
		}
	}

	removalData := scratch.removalData
	wrappedRemovals := scratch.wrappedRemovals
	for i, id := range result.Removals {
		if s.removalSerializer == nil {
			return fmt.Errorf("entity %d removed but no RemovalSerializer configured", id)
		}
		data, err := s.removalSerializer(id, result.RemovalRevisions[i])
		if err != nil {
			return fmt.Errorf("serializing removal for entity %d: %w", id, err)
		}
		removalData[id] = data
		wrappedRemovals[id] = s.listener.Wrap(data)
	}
	if s.usesDatagramStateUpdates() {
		s.clearEventualEntities(result.Removals)
	}

	wrappedFull := scratch.wrappedFull
	wrapDelta := func(id int64) ([]byte, bool) {
		if data, ok := wrappedDeltas[id]; ok {
			return data, true
		}
		data, ok := deltaData[id]
		if !ok {
			return nil, false
		}
		wrapped := s.listener.Wrap(data)
		wrappedDeltas[id] = wrapped
		return wrapped, true
	}

	sessionIDs := s.listener.SessionIDs()
	var eventualCache *eventualStateTickCache
	if s.usesDatagramStateUpdates() {
		eventualCache = newEventualStateTickCache()
	}

	var streamBatchedSum, streamMsgsSum, datagramBatchedSum, datagramMsgsSum int

	for _, sessionID := range sessionIDs {
		diff, hasDiff := diffs[sessionID]
		if !hasDiff {
			continue
		}

		var (
			streamFrames         [][]byte
			eventualDirty        = scratch.eventualDirty[:0]
			eventualDirectFrames = scratch.eventualPrepared[:0]
		)

		for _, id := range diff.Entered {
			if s.usesDatagramStateUpdates() {
				s.clearEventualEntity(sessionID, id)
			}
			data, ok := wrappedSpawns[id]
			if !ok {
				data, ok = wrappedFull[id]
				if !ok {
					e, found := s.reg.Get(id)
					if !found {
						continue
					}
					var err error
					data, err = e.FullUpdate()
					if err != nil {
						return fmt.Errorf("full update for entity %d entering FOI: %w", id, err)
					}
					data = s.listener.Wrap(data)
					wrappedFull[id] = data
				}
			}
			streamFrames = append(streamFrames, data)
		}

		for _, id := range diff.Stayed {
			if _, ok := deltaData[id]; ok {
				if s.usesDatagramStateUpdates() {
					if ch, ok := eventualChanges[id]; ok {
						eventualDirty = append(eventualDirty, ch)
						if prepared, ok := eventualFrames[id]; ok {
							eventualDirectFrames = append(eventualDirectFrames, prepared)
						}
					}
				} else {
					if data, ok := wrapDelta(id); ok {
						streamFrames = append(streamFrames, data)
					}
				}
			}
			if data, ok := wrappedSpawns[id]; ok {
				streamFrames = append(streamFrames, data)
			}
		}
		if len(eventualDirty) > 0 {
			tracker := s.eventualTracker(sessionID)
			if !tracker.hasDirty() {
				// Prepared frames are the current tick's hot path. Requeued tracker
				// state still uses the generic cache path below.
			} else {
				for _, ch := range eventualDirty {
					tracker.markDirtyChange(ch)
				}
				eventualDirectFrames = eventualDirectFrames[:0]
			}
		}

		for _, id := range diff.Exited {
			if s.usesDatagramStateUpdates() {
				s.clearEventualEntity(sessionID, id)
			}
			if data, ok := wrappedRemovals[id]; ok {
				streamFrames = append(streamFrames, data)
			} else {
				if s.removalSerializer == nil {
					return fmt.Errorf("entity %d exited FOI but no RemovalSerializer configured", id)
				}
				revision := uint64(1)
				if e, found := s.reg.Get(id); found {
					if r, ok := e.(registry.StateRevisioner); ok {
						revision = r.StateRevision() + 1
					}
				}
				data, err := s.removalSerializer(id, revision)
				if err != nil {
					return fmt.Errorf("serializing FOI exit for entity %d: %w", id, err)
				}
				removalData[id] = data
				data = s.listener.Wrap(data)
				wrappedRemovals[id] = data
				streamFrames = append(streamFrames, data)
			}
		}

		if len(streamFrames) > 0 {
			if err := s.listener.SendBatch(sessionID, streamFrames); err != nil {
				if isDisconnectedSessionSend(err) {
					continue
				}
				return err
			}
			if s.config.LogReplicationStats {
				if c, err := golemnet.ReliableStreamWriteChunkCount(s.config.Transport, streamFrames); err == nil {
					streamMsgsSum += c
				}
			}
			streamBatchedSum += len(streamFrames)
		}
		if s.usesDatagramStateUpdates() {
			var (
				batched int
				msgs    int
				err     error
			)
			if len(eventualDirectFrames) > 0 {
				batched, msgs, err = s.sendPreparedEventualStateFrames(sessionID, s.eventualTracker(sessionID), eventualDirectFrames)
			} else {
				batched, msgs, err = s.sendEventualState(sessionID, s.eventualTracker(sessionID), eventualCache)
			}
			if err != nil {
				return err
			}
			datagramBatchedSum += batched
			datagramMsgsSum += msgs
		}
		scratch.eventualDirty = eventualDirty[:0]
		scratch.eventualPrepared = eventualDirectFrames[:0]
	}

	s.storeReplicationSnapshot(streamBatchedSum, streamMsgsSum, datagramBatchedSum, datagramMsgsSum, len(result.Deltas))
	return nil
}

func (s *Server) usesDatagramStateUpdates() bool {
	return s.config.StateUpdateLane == StateUpdateLaneDatagram
}

// isDisconnectedSessionSend reports whether an automatic replication send lost
// its target session after taking a connected-session snapshot.
func isDisconnectedSessionSend(err error) bool {
	return errors.Is(err, golemnet.ErrSessionNotFound)
}

// storeReplicationSnapshot records counts for the most recently completed
// flush/broadcast (tick goroutine only). wire message totals include every
// session (broadcast multiplies by client count; interest sums per-send chunks).
func (s *Server) storeReplicationSnapshot(streamBatched, streamWireMsgs, datagramBatched, datagramWireMsgs, deltasFlushed int) {
	if !s.config.LogReplicationStats {
		return
	}
	s.replStatsMu.Lock()
	defer s.replStatsMu.Unlock()
	s.replLastTick = s.tick
	s.replDeltasFlushed = deltasFlushed
	s.replStreamBatched = streamBatched
	s.replStreamMsgs = streamWireMsgs
	s.replDatagramBatched = datagramBatched
	s.replDatagramMsgs = datagramWireMsgs
}

func (s *Server) storeBroadcastStreamOnly(updates [][]byte, deltasFlushed, nClients int) {
	if !s.config.LogReplicationStats {
		return
	}
	wrapped := make([][]byte, len(updates))
	for i, d := range updates {
		wrapped[i] = s.listener.Wrap(d)
	}
	chunkN, err := golemnet.ReliableStreamWriteChunkCount(s.config.Transport, wrapped)
	if err != nil {
		chunkN = 0
	}
	s.storeReplicationSnapshot(len(updates), chunkN*nClients, 0, 0, deltasFlushed)
}

func (s *Server) logReplicationStatsLine() {
	s.replStatsMu.Lock()
	lastTick := s.replLastTick
	df := s.replDeltasFlushed
	sb, sm, db, dm := s.replStreamBatched, s.replStreamMsgs, s.replDatagramBatched, s.replDatagramMsgs
	s.replStatsMu.Unlock()
	log.Printf("golem: replication last_tick=%d flushed_entity_deltas=%d stream{batched_frames=%d wire_msgs=%d} datagram{batched_frames=%d wire_payloads=%d}",
		lastTick, df, sb, sm, db, dm)
	log.Printf("golem: net outbound backlog %s", s.listener.OutboundBacklogForLog(time.Now()))
}

func (s *Server) replicationStatsLoop(ctx context.Context) {
	// Ticker's first fire is one period later; emit once immediately so
	// logging is visible without waiting and before the first game tick
	// (values may be zero until a tick runs).
	s.logReplicationStatsLine()
	period := time.NewTicker(time.Second)
	defer period.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-period.C:
			s.logReplicationStatsLine()
		}
	}
}
