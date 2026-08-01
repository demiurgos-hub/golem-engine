package golem

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
	"github.com/demiurgos-hub/golem-engine/golem/interest"
	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/visibility"
	"github.com/demiurgos-hub/golem-engine/golem/world"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	"github.com/demiurgos-hub/golem-engine/golem/nav"
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
	spawnData          map[int64][]byte
	deltaData          map[int64][]byte
	removalData        map[int64][]byte
	wrappedSpawns      map[int64][]byte
	wrappedDeltas      map[int64][]byte
	wrappedRemovals    map[int64][]byte
	wrappedFull        map[int64][]byte // authoritative full frames
	wrappedPublicFull  map[int64][]byte // public/redacted full frames
	wrappedPublicDelta map[int64][]byte // public/redacted stream deltas (nil value = skip)
	eventualChanges    map[int64]eventualStateChange
	eventualFrames     map[int64]eventualPreparedFrame
	eventualDirty      []eventualStateChange
	eventualPrepared   []eventualPreparedFrame
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
		s.wrappedPublicFull = make(map[int64][]byte)
		s.wrappedPublicDelta = make(map[int64][]byte)
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
	clear(s.wrappedPublicFull)
	clear(s.wrappedPublicDelta)
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
	// WorldSnapshotExclude lists world data names omitted from the connect
	// snapshot. Entries remain in Server.World; callers own on-demand delivery
	// (typically via SendWorldData / SendStoredWorldData). NewServer copies and
	// normalizes the slice (drops empties/duplicates) so later caller mutation
	// cannot race with snapshot reads. Large embedded maps can exceed the
	// 32000-byte reliable frame cap — prefer map_url for oversized payloads.
	WorldSnapshotExclude []string
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
	visibility           *visibility.Manager
	broadcastKnown       map[int64]map[int64]struct{} // sessionID → known entity IDs (broadcast mode)
	ownershipRefresh     map[int64]ownershipRefresh   // entityID → coalesced ownership confidentiality transition
	collision            collision.Backend
	collision3D          collision3d.Backend
	layers               *collision.Layers
	layers3D             *collision3d.Layers
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

	// Avatar indexes (session ↔ entity). Protected by avatarMu; never hold
	// avatarMu while calling registry hooks or interest AssignFOI/RemoveFOI.
	avatarMu      sync.Mutex
	sessionAvatar map[int64]int64 // sessionID → entityID (or avatarSpawnPending)
	avatarSession map[int64]int64 // entityID → sessionID

	// interestMu guards interest.Manager, visibility.Manager, and broadcastKnown
	// through Server APIs (AssignFOI, RemoveFOI, visibility group ops,
	// SessionsKnowing, UpdateGrid+ComputeDiffs, broadcast known-set updates).
	// Lock order with avatarMu: interestMu before avatarMu when both are needed.
	// Never hold interestMu across network sends, FullUpdate, or registry hooks.
	// Visibility policy is point-in-time: mutations after a replication/snapshot
	// decision apply on the next pass via known-set enter/exit transitions.
	interestMu sync.Mutex

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
	cfg.WorldSnapshotExclude = normalizeWorldSnapshotExclude(cfg.WorldSnapshotExclude)
	validateStateUpdateLane(cfg)
	worldSnapshotExclude := worldSnapshotExcludeSet(cfg.WorldSnapshotExclude)
	s := &Server{
		reg:            registry.NewRegistry(),
		World:          world.NewStore(),
		config:         cfg,
		msgQueue:       make(chan pendingMsg, msgQueueCap),
		sessionAvatar:  make(map[int64]int64),
		avatarSession:  make(map[int64]int64),
		visibility:     visibility.NewManager(),
		broadcastKnown: make(map[int64]map[int64]struct{}),
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
	} else {
		// Broadcast mode: recipient-aware connect snapshots exclude grouped
		// entities from non-members using a point-in-time Allows selection.
		// Known state is seeded only after every entity frame succeeds.
		s.listener.SetEntitySnapshotFunc(s.entitySnapshotForSession)
		s.listener.SetEntitySnapshotCompleteFunc(s.commitBroadcastSnapshotKnown)
	}
	s.listener.SetWorldSnapshotFunc(func() ([][]byte, error) {
		updates, err := s.World.MarshalAllExcept(worldSnapshotExclude)
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

// ServerBinder is implemented by entities that store a back-reference to the
// owning Server. CreateEntity calls BindServer after ID assignment and before
// registry Add/AddOwned so OnSpawn can use the bound server.
type ServerBinder interface {
	BindServer(*Server)
}

// CreateEntity registers e for simulation and replication. If the entity was
// constructed without an ID (EntityID() == 0), the next counter value is
// assigned automatically via EntityIDSetter. If e implements ServerBinder,
// BindServer is invoked after validation/ID assignment and before registry
// insertion so OnSpawn can call Server().
//
// When e implements ColliderProvider or ColliderProvider3D, its shape is
// registered on the configured named-layer helper after successful registry
// insertion and before OnSpawn. Registration is skipped without panicking when
// no layer helper is set — call SetCollisionLayers / SetCollisionLayers3D
// (or generated EnableCollision / EnableCollision3D) before spawning or
// snapshot-loading collidable entities. Failed duplicate insertion does not
// register a collider.
//
// With no extra owner arguments the entity is unowned (e.g. world NPC); with
// one argument that value is the owning session ID for command authority.
// More than one owner argument is invalid.
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

	if binder, ok := e.(ServerBinder); ok {
		binder.BindServer(s)
	}

	var err error
	if len(owner) == 1 {
		err = s.reg.AddOwnedWithoutSpawn(e, owner[0])
	} else {
		err = s.reg.AddWithoutSpawn(e)
	}
	if err != nil {
		return err
	}

	// After successful insertion, before OnSpawn, so spawn hooks observe the shape.
	s.registerCollider(e)
	registry.NotifySpawn(e)
	return nil
}

// DeleteEntity unregisters an entity by ID and queues a removal for clients.
// If the entity is a session avatar, both avatar indexes are cleared in O(1)
// before registry deletion. When the entity implements ColliderProvider or
// ColliderProvider3D and a matching layer helper is configured, its shape is
// removed after avatar index cleanup and before registry deletion (and thus
// before OnRemove). Safe to call repeatedly; does not hold the avatar mutex
// while invoking registry hooks (OnRemove) or interest operations.
func (s *Server) DeleteEntity(id int64) {
	s.clearAvatarByEntity(id)
	if e, ok := s.reg.Get(id); ok {
		s.unregisterCollider(e)
	}
	s.reg.DeleteEntity(id)
}

// Get returns the entity with the given ID, or (nil, false) if not found.
func (s *Server) Get(id int64) (Entity, bool) { return s.reg.Get(id) }

// Owner returns the session ID that owns the entity, if any.
func (s *Server) Owner(entityID int64) (sessionID int64, owned bool) {
	return s.reg.Owner(entityID)
}

// SetOwner updates the owning session of an existing entity for command
// authority immediately (e.g. reconnect with a new session ID). sessionID 0
// clears ownership. Ownership transfer does not transfer avatar identity:
// session↔avatar indexes from SpawnAvatar are unchanged. Re-bind avatars
// explicitly with SpawnAvatar after disconnect cleanup (or after DeleteEntity)
// rather than via SetOwner.
//
// Concurrent SetOwner calls are serialized under interestMu (lock order:
// interestMu before the registry mutex). Owner lookup, registry mutation, and
// ownership-refresh coalescing are atomic relative to other Server.SetOwner
// calls so a stale pre-change owner cannot be recorded. Locks are not held
// across network sends or registry hooks.
//
// For entities with visibility: owner vars, SetOwner also queues a replication
// confidentiality refresh processed on the next replication pass: the original
// old owner that still knows the entity receives public full state (clearing
// retained private fields), and the final new owner that knows it receives
// authoritative full state. Same-tick A→…→A coalesces to a no-op. Visibility:
// owner is replication redaction only — command authorization remains the
// separate Owner check used by generated routers.
func (s *Server) SetOwner(entityID, sessionID int64) bool {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()

	e, exists := s.reg.Get(entityID)
	if !exists {
		return false
	}
	oldOwner, owned := s.reg.Owner(entityID)
	if !owned {
		oldOwner = 0
	}
	if oldOwner == sessionID {
		return true
	}
	if !s.reg.SetOwner(entityID, sessionID) {
		return false
	}
	if entityIsOwnerScoped(e) {
		s.queueOwnershipRefreshLocked(entityID, oldOwner, sessionID)
	}
	return true
}

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
// The hook fires on the tick goroutine before automatic avatar cleanup, so
// Avatar / AvatarOf still resolve during the callback. After the hook returns,
// the server RemoveFOI (when interest is enabled), deletes any avatar still
// bound to the session (including a replacement spawned inside the callback),
// and clears avatar indexes — replacements are reaped so they cannot leak past
// disconnect. It is safe to call DeleteEntity and other Server methods from the
// hook. Disconnect events from the listener always carry a non-nil *Session.
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
// entityID in their actual replication known set. In interest mode this is the
// FOI known set; in broadcast mode it is the broadcast known set seeded by
// successful connect snapshots and updated by replication decisions. It never
// consults raw visibility-group membership — a session that just joined a
// group is not returned until the next replication pass queues full state.
// Safe to call concurrently with AssignFOI / RemoveFOI / visibility APIs /
// SpawnAvatar; guarded by interestMu.
func (s *Server) SessionsKnowing(entityID int64) []int64 {
	allSessions := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	var result []int64
	for _, sid := range allSessions {
		var known map[int64]struct{}
		if s.interest != nil {
			known = s.interest.Known(sid)
		} else {
			known = s.broadcastKnown[sid]
		}
		if _, ok := known[entityID]; ok {
			result = append(result, sid)
		}
	}
	return result
}

// JoinVisibilityGroup adds sessionID to the named visibility group.
// Grouped entities assigned to that group replicate only to members.
// Concurrency-safe under interestMu and never waits on network I/O.
// Policy is point-in-time: a join after a replication/snapshot decision takes
// effect on the next pass (via known-set enter/stay), and does not revoke
// frames already selected or queued.
func (s *Server) JoinVisibilityGroup(sessionID int64, group string) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.visibility.JoinGroup(sessionID, group)
}

// LeaveVisibilityGroup removes sessionID from the named visibility group.
// Concurrency-safe under interestMu and never waits on network I/O.
// A leave after a replication/snapshot decision takes effect on the next pass
// (known recipients get EntityRemoved when no longer allowed).
func (s *Server) LeaveVisibilityGroup(sessionID int64, group string) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.visibility.LeaveGroup(sessionID, group)
}

// SetEntityVisibilityGroup assigns entityID to group. An empty group clears the
// assignment and makes the entity public (replicated to all otherwise-eligible
// sessions). Concurrency-safe under interestMu and never waits on network I/O.
// Public→grouped (and other) mutations after a blind/filtered decision or
// connect-snapshot ID selection apply on the next replication pass via
// known-set removal/full transitions; already selected/queued frames are not
// retroactively revoked.
func (s *Server) SetEntityVisibilityGroup(entityID int64, group string) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.visibility.SetEntityGroup(entityID, group)
}

// AssignFOI associates a session with a circular field of interest centred
// on the given entity. Panics if interest management is not enabled (CellSize <= 0).
// Concurrency-safe with other Server FOI APIs via interestMu.
func (s *Server) AssignFOI(sessionID, entityID int64, radius, margin float64) {
	if s.interest == nil {
		panic("golem: AssignFOI called but interest management is not enabled (CellSize <= 0)")
	}
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.interest.AssignFOI(sessionID, entityID, radius, margin)
}

// RemoveFOI removes a session's field of interest and clears its known set.
// Panics if interest management is not enabled.
// Concurrency-safe with other Server FOI APIs via interestMu.
func (s *Server) RemoveFOI(sessionID int64) {
	if s.interest == nil {
		panic("golem: RemoveFOI called but interest management is not enabled (CellSize <= 0)")
	}
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.interest.RemoveFOI(sessionID)
}

// hasFOI reports whether sessionID currently has an assigned FOI.
// Returns false when interest management is disabled.
func (s *Server) hasFOI(sessionID int64) bool {
	if s.interest == nil {
		return false
	}
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	return s.interest.HasFOI(sessionID)
}

// PushWorldData broadcasts the current value of a single world data type to
// all connected sessions. Returns nil if the name is not in the store or the
// broadcast succeeds. Returns a non-nil error if serialization fails.
// Reliable frames are capped at 32000 bytes; oversized embedded maps should
// use map_url instead of tile_data.
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

// SendWorldData serializes data directly, wraps it with WrapWorldUpdate, and
// sends it on the reliable stream to one session. It does not read or mutate
// Server.World, so two sessions can receive different generated values that
// share the same WorldName. Nil and typed-nil data return an error. Marshal
// failures, oversize reliable frames (32000-byte cap), and disconnected
// sessions propagate. Prefer map_url when payloads may exceed the frame cap.
// Names listed in WorldSnapshotExclude are omitted from connect snapshots;
// callers own delivering those values (for example via this method).
func (s *Server) SendWorldData(sessionID int64, data WorldData) error {
	if isNilWorldData(data) {
		return errors.New("golem: SendWorldData requires non-nil world data")
	}
	payload, err := data.MarshalUpdate()
	if err != nil {
		return err
	}
	return s.Send(sessionID, WrapWorldUpdate(payload))
}

// SendStoredWorldData sends the currently stored world value for name to one
// session. Returns nil when the name is missing (same no-op as PushWorldData).
// Marshal failures, oversize reliable frames, and disconnected sessions
// propagate. Prefer SendWorldData when delivering a per-session value that
// must not replace Server.World.
func (s *Server) SendStoredWorldData(sessionID int64, name string) error {
	d := s.World.Get(name)
	if d == nil {
		return nil
	}
	return s.SendWorldData(sessionID, d)
}

// normalizeWorldSnapshotExclude copies names, dropping empties and duplicates
// so NewServer owns an immutable-after-construction exclusion list.
func normalizeWorldSnapshotExclude(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func worldSnapshotExcludeSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// isNilWorldData reports whether data is a nil interface or a typed nil
// (pointer/map/slice/chan/func/interface). Non-nillable concrete values are
// never treated as nil.
func isNilWorldData(data WorldData) bool {
	if data == nil {
		return true
	}
	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
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
// dispatch. The listener always passes a non-nil *Session (see
// Listener.serveAcceptedSession); drainMessages relies on that invariant.
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
				// Listener disconnect events always carry a non-nil *Session.
				if s.eventualTrackers != nil {
					delete(s.eventualTrackers, m.sess.ID)
				}
				if s.onDisconnect != nil {
					s.onDisconnect(m.sess)
				}
				// Avatar remains readable during onDisconnect; clean up after.
				// Reaps any avatar still bound to the session, including a
				// replacement spawned inside the user callback.
				s.cleanupAvatarOnDisconnect(m.sess.ID)
				s.cleanupVisibilityOnDisconnect(m.sess.ID)
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

// runBroadcastTick replicates entities when interest management is not enabled.
// The blind-vs-filtered decision is linearized under interestMu. When no
// visibility groups are assigned and every session's known set already matches
// the live public world, it uses the blind BroadcastBatch fast path while still
// maintaining per-session known sets. Otherwise it computes per-session
// known-set diffs (enter/stay/exit). A visibility mutation after this decision
// takes effect on the next pass; interestMu is never held across BroadcastBatch.
func (s *Server) runBroadcastTick() error {
	result, err := s.reg.FlushAll()
	if err != nil {
		return err
	}
	deltasFlushed := len(result.Deltas)
	live := s.reg.All()
	sessionIDs := s.listener.SessionIDs()

	s.interestMu.Lock()
	filter := s.shouldFilterBroadcastLocked(sessionIDs, live)
	s.interestMu.Unlock()

	if filter {
		return s.runBroadcastTickFiltered(result, deltasFlushed)
	}
	return s.runBroadcastTickBlind(result, deltasFlushed)
}

// shouldFilterBroadcastLocked reports whether broadcast replication must run
// the per-session known-set path. Caller must hold interestMu.
func (s *Server) shouldFilterBroadcastLocked(sessionIDs []int64, live []Entity) bool {
	if s.visibility.HasGroupedEntities() {
		return true
	}
	if s.hasPendingOwnershipRefreshLocked() {
		return true
	}
	if hasOwnerScopedEntities(live) {
		return true
	}
	liveCount := len(live)
	for _, sid := range sessionIDs {
		known := s.broadcastKnown[sid]
		if known == nil {
			if liveCount > 0 {
				return true
			}
			continue
		}
		if len(known) != liveCount {
			return true
		}
		for _, e := range live {
			if _, ok := known[e.EntityID()]; !ok {
				return true
			}
		}
	}
	return false
}

// runBroadcastTickBlind is the fast path used when no entity has a visibility
// group. It preserves blind BroadcastBatch semantics and incrementally updates
// broadcast known sets so later policy activation is transition-safe.
func (s *Server) runBroadcastTickBlind(result registry.FlushResult, deltasFlushed int) error {
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
		s.cleanupVisibilityForRemovals(result.Removals)
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
		s.applyBlindBroadcastKnown(sessionIDs, result.SpawnIDs, result.Removals)
		s.cleanupVisibilityForRemovals(result.Removals)
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
	s.applyBlindBroadcastKnown(sessionIDs, result.SpawnIDs, result.Removals)
	s.cleanupVisibilityForRemovals(result.Removals)
	s.storeReplicationSnapshot(len(streamUpdates)*nClients, 0, datagramBatched, datagramMsgs, deltasFlushed)
	return nil
}

// runBroadcastTickFiltered performs per-session known-set replication when
// visibility groups, owner-scoped payloads, or ownership refreshes require it.
func (s *Server) runBroadcastTickFiltered(result registry.FlushResult, deltasFlushed int) error {
	scratch := &s.interestScratch
	scratch.reset()

	spawnData := scratch.spawnData
	wrappedSpawns := scratch.wrappedSpawns
	wrappedFull := scratch.wrappedFull
	for i, id := range result.SpawnIDs {
		data := result.Spawns[i]
		spawnData[id] = data
		wrapped := s.listener.Wrap(data)
		wrappedSpawns[id] = wrapped
		wrappedFull[id] = wrapped
	}

	deltaData := scratch.deltaData
	eventualChanges := scratch.eventualChanges
	eventualFrames := scratch.eventualFrames
	ownerScopedDeltas := false
	for i, id := range result.DeltaIDs {
		data := result.Deltas[i]
		deltaData[id] = data
		if e, ok := s.reg.Get(id); ok && entityIsOwnerScoped(e) {
			ownerScopedDeltas = true
		}
		if s.usesDatagramStateUpdates() {
			eventualChanges[id] = s.eventualChangeForDelta(id)
		}
	}
	if s.usesDatagramStateUpdates() && !ownerScopedDeltas {
		for id, ch := range eventualChanges {
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

	var updates [][]byte
	updates = append(updates, result.Spawns...)
	updates = append(updates, result.Deltas...)
	for _, id := range result.Removals {
		updates = append(updates, removalData[id])
	}
	// Capture session IDs before onUpdates so a disconnect during the callback
	// still observes SendBatch ErrSessionNotFound and can clear known state.
	sessionIDs := s.listener.SessionIDs()
	if s.onUpdates != nil && len(updates) > 0 {
		s.onUpdates(updates)
	}

	live := s.reg.All()
	var eventualCache *eventualStateTickCache
	if s.usesDatagramStateUpdates() {
		eventualCache = newEventualStateTickCache()
	}

	s.interestMu.Lock()
	ownershipPending := s.snapshotOwnershipRefreshLocked()
	s.interestMu.Unlock()

	var streamBatchedSum, streamMsgsSum, datagramBatchedSum, datagramMsgsSum int
	wrappedPublicFull := scratch.wrappedPublicFull
	wrappedPublicDelta := scratch.wrappedPublicDelta
	wrappedDeltas := scratch.wrappedDeltas

	for _, sessionID := range sessionIDs {
		entered, stayed, exited := s.computeBroadcastDiff(sessionID, live)

		var (
			streamFrames         [][]byte
			eventualDirty        = scratch.eventualDirty[:0]
			eventualDirectFrames = scratch.eventualPrepared[:0]
		)

		for _, id := range entered {
			if s.usesDatagramStateUpdates() {
				s.clearEventualEntity(sessionID, id)
			}
			data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
			if err != nil {
				return fmt.Errorf("full update for entity %d entering visibility: %w", id, err)
			}
			if !ok {
				continue
			}
			streamFrames = append(streamFrames, data)
		}

		for _, id := range stayed {
			if authDelta, ok := deltaData[id]; ok {
				if s.usesDatagramStateUpdates() {
					if ch, ok := eventualChanges[id]; ok {
						sch, send := s.eventualChangeForSession(sessionID, ch)
						if send {
							eventualDirty = append(eventualDirty, sch)
							if !sch.public && !ownerScopedDeltas {
								if prepared, ok := eventualFrames[id]; ok {
									eventualDirectFrames = append(eventualDirectFrames, prepared)
								}
							}
						}
					}
				} else {
					data, send, err := s.streamDeltaForSession(sessionID, id, authDelta, wrappedDeltas, wrappedPublicDelta)
					if err != nil {
						return err
					}
					if send {
						streamFrames = append(streamFrames, data)
					}
				}
			}
			if _, ok := spawnData[id]; ok {
				data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
				if err != nil {
					return err
				}
				if ok {
					streamFrames = append(streamFrames, data)
				}
			}
		}
		for _, id := range exited {
			if s.usesDatagramStateUpdates() {
				s.clearEventualEntity(sessionID, id)
			}
			if data, ok := wrappedRemovals[id]; ok {
				streamFrames = append(streamFrames, data)
			} else {
				if s.removalSerializer == nil {
					return fmt.Errorf("entity %d exited visibility but no RemovalSerializer configured", id)
				}
				revision := uint64(1)
				if e, found := s.reg.Get(id); found {
					if r, ok := e.(registry.StateRevisioner); ok {
						revision = r.StateRevision() + 1
					}
				}
				data, err := s.removalSerializer(id, revision)
				if err != nil {
					return fmt.Errorf("serializing visibility exit for entity %d: %w", id, err)
				}
				removalData[id] = data
				data = s.listener.Wrap(data)
				wrappedRemovals[id] = data
				streamFrames = append(streamFrames, data)
			}
		}

		var err error
		streamFrames, err = s.appendOwnershipRefreshFrames(sessionID, ownershipPending, stayed, wrappedFull, wrappedPublicFull, streamFrames)
		if err != nil {
			return err
		}

		if len(streamFrames) > 0 {
			if err := s.listener.SendBatch(sessionID, streamFrames); err != nil {
				if isDisconnectedSessionSend(err) {
					s.clearBroadcastKnownSession(sessionID)
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
			batched, msgs, err := s.sendSessionEventualState(sessionID, eventualDirty, eventualDirectFrames, eventualCache)
			if err != nil {
				if isDisconnectedSessionSend(err) {
					s.clearBroadcastKnownSession(sessionID)
					continue
				}
				return err
			}
			datagramBatchedSum += batched
			datagramMsgsSum += msgs
		}
		scratch.eventualDirty = eventualDirty[:0]
		scratch.eventualPrepared = eventualDirectFrames[:0]
	}

	s.cleanupVisibilityForRemovals(result.Removals)
	s.storeReplicationSnapshot(streamBatchedSum, streamMsgsSum, datagramBatchedSum, datagramMsgsSum, deltasFlushed)
	return nil
}

// sendSessionEventualState sends this tick's per-session eventual changes,
// merging into any requeued tracker dirty state when present.
func (s *Server) sendSessionEventualState(
	sessionID int64,
	eventualDirty []eventualStateChange,
	eventualDirectFrames []eventualPreparedFrame,
	eventualCache *eventualStateTickCache,
) (int, int, error) {
	tracker := s.eventualTracker(sessionID)
	if len(eventualDirty) > 0 && tracker.hasDirty() {
		for _, ch := range eventualDirty {
			tracker.markDirtyChange(ch)
		}
		return s.sendEventualState(sessionID, tracker, eventualCache)
	}
	if len(eventualDirectFrames) > 0 {
		return s.sendPreparedEventualStateFrames(sessionID, tracker, eventualDirectFrames)
	}
	if len(eventualDirty) > 0 {
		return s.sendEventualStateChanges(sessionID, tracker, eventualCache, eventualDirty)
	}
	return s.sendEventualState(sessionID, tracker, eventualCache)
}

// clearBroadcastKnownSession drops broadcast known state for a session that
// disconnected after enter/exit decisions were applied but before sends completed.
func (s *Server) clearBroadcastKnownSession(sessionID int64) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	delete(s.broadcastKnown, sessionID)
}

// computeBroadcastDiff updates broadcast known state for sessionID and returns
// enter/stay/exit entity IDs. Known transitions are accepted here (queued sends
// count as known), matching interest-mode semantics.
func (s *Server) computeBroadcastDiff(sessionID int64, live []Entity) (entered, stayed, exited []int64) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()

	known := s.broadcastKnown[sessionID]
	if known == nil {
		known = make(map[int64]struct{})
		s.broadcastKnown[sessionID] = known
	}

	desired := make(map[int64]struct{}, len(live))
	for _, e := range live {
		id := e.EntityID()
		if s.visibility.Allows(sessionID, id) {
			desired[id] = struct{}{}
		}
	}

	for id := range desired {
		if _, ok := known[id]; ok {
			stayed = append(stayed, id)
		} else {
			entered = append(entered, id)
			known[id] = struct{}{}
		}
	}
	for id := range known {
		if _, ok := desired[id]; !ok {
			exited = append(exited, id)
			delete(known, id)
		}
	}
	return entered, stayed, exited
}

// applyBlindBroadcastKnown incrementally maintains broadcast known sets on the
// blind fast path: add successful spawn IDs, remove despawned IDs.
func (s *Server) applyBlindBroadcastKnown(sessionIDs, spawnIDs, removalIDs []int64) {
	if len(sessionIDs) == 0 {
		return
	}
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	for _, sid := range sessionIDs {
		known := s.broadcastKnown[sid]
		if known == nil {
			known = make(map[int64]struct{})
			s.broadcastKnown[sid] = known
		}
		for _, id := range spawnIDs {
			known[id] = struct{}{}
		}
		for _, id := range removalIDs {
			delete(known, id)
		}
	}
}

// cleanupVisibilityForRemovals drops group assignments after recipients have
// been given removal decisions for the tick.
func (s *Server) cleanupVisibilityForRemovals(removalIDs []int64) {
	if len(removalIDs) == 0 {
		return
	}
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	for _, id := range removalIDs {
		s.visibility.RemoveEntity(id)
	}
}

// cleanupVisibilityOnDisconnect clears session group membership and broadcast
// known state. Interest FOI cleanup remains in cleanupAvatarOnDisconnect.
func (s *Server) cleanupVisibilityOnDisconnect(sessionID int64) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	s.visibility.RemoveSession(sessionID)
	delete(s.broadcastKnown, sessionID)
}

// entitySnapshotForSession returns connect-time entity frames visible to
// sessionID under a point-in-time visibility policy. Allowed entity IDs are
// selected under interestMu, then the mutex is released before FullUpdate and
// Listener writes. The current owner receives authoritative full state; every
// other recipient receives PublicFullUpdate when the entity is owner-scoped.
// A later public→grouped mutation does not revoke frames already selected;
// successful snapshot IDs become known and can be removed on the next
// replication pass. Provider/write failures never commit known state.
func (s *Server) entitySnapshotForSession(sessionID int64) ([]golemnet.EntitySnapshot, error) {
	all := s.reg.All()
	allowed := make([]Entity, 0, len(all))
	s.interestMu.Lock()
	for _, e := range all {
		if s.visibility.Allows(sessionID, e.EntityID()) {
			allowed = append(allowed, e)
		}
	}
	s.interestMu.Unlock()

	out := make([]golemnet.EntitySnapshot, 0, len(allowed))
	for _, e := range allowed {
		id := e.EntityID()
		data, err := s.fullUpdateForSession(e, sessionID)
		if err != nil {
			return nil, fmt.Errorf("snapshotting entity %d (%s): %w", id, e.TypeName(), err)
		}
		out = append(out, golemnet.EntitySnapshot{EntityID: id, Data: data})
	}
	return out, nil
}

// commitBroadcastSnapshotKnown seeds the broadcast known set after every
// connect snapshot entity frame has been written successfully.
func (s *Server) commitBroadcastSnapshotKnown(sessionID int64, entityIDs []int64) {
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	known := make(map[int64]struct{}, len(entityIDs))
	for _, id := range entityIDs {
		known[id] = struct{}{}
	}
	s.broadcastKnown[sessionID] = known
}

// copyInterestDiffs deep-copies ComputeDiffs output so callers can release
// interestMu before consuming the result (manager Diff storage is reused).
func copyInterestDiffs(in map[int64]*interest.Diff) map[int64]*interest.Diff {
	out := make(map[int64]*interest.Diff, len(in))
	for sid, d := range in {
		if d == nil {
			out[sid] = nil
			continue
		}
		out[sid] = &interest.Diff{
			Entered: append([]int64(nil), d.Entered...),
			Stayed:  append([]int64(nil), d.Stayed...),
			Exited:  append([]int64(nil), d.Exited...),
		}
	}
	return out
}

// runInterestTick performs per-session interest-filtered sends.
func (s *Server) runInterestTick() error {
	// Snapshot interest diffs under interestMu, then release before registry
	// flush and network sends. Diff storage is reused by the manager, so copy.
	// Visibility Allows is evaluated under the same lock (no policy mirroring).
	s.interestMu.Lock()
	s.interest.UpdateGrid(s.reg)
	diffs := copyInterestDiffs(s.interest.ComputeDiffsFiltered(s.visibility.Allows))
	ownershipPending := s.snapshotOwnershipRefreshLocked()
	s.interestMu.Unlock()

	result, err := s.reg.FlushAll()
	if err != nil {
		return err
	}

	scratch := &s.interestScratch
	scratch.reset()

	spawnData := scratch.spawnData
	wrappedSpawns := scratch.wrappedSpawns
	wrappedFull := scratch.wrappedFull
	for i, id := range result.SpawnIDs {
		data := result.Spawns[i]
		spawnData[id] = data
		wrapped := s.listener.Wrap(data)
		wrappedSpawns[id] = wrapped
		wrappedFull[id] = wrapped
	}

	deltaData := scratch.deltaData
	eventualChanges := scratch.eventualChanges
	eventualFrames := scratch.eventualFrames
	ownerScopedDeltas := false
	for i, id := range result.DeltaIDs {
		data := result.Deltas[i]
		deltaData[id] = data
		if e, ok := s.reg.Get(id); ok && entityIsOwnerScoped(e) {
			ownerScopedDeltas = true
		}
		if s.usesDatagramStateUpdates() {
			eventualChanges[id] = s.eventualChangeForDelta(id)
		}
	}
	if s.usesDatagramStateUpdates() && !ownerScopedDeltas {
		for id, ch := range eventualChanges {
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

	wrappedPublicFull := scratch.wrappedPublicFull
	wrappedPublicDelta := scratch.wrappedPublicDelta
	wrappedDeltas := scratch.wrappedDeltas

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
			data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
			if err != nil {
				return fmt.Errorf("full update for entity %d entering FOI: %w", id, err)
			}
			if !ok {
				continue
			}
			streamFrames = append(streamFrames, data)
		}

		for _, id := range diff.Stayed {
			if authDelta, ok := deltaData[id]; ok {
				if s.usesDatagramStateUpdates() {
					if ch, ok := eventualChanges[id]; ok {
						sch, send := s.eventualChangeForSession(sessionID, ch)
						if send {
							eventualDirty = append(eventualDirty, sch)
							if !sch.public && !ownerScopedDeltas {
								if prepared, ok := eventualFrames[id]; ok {
									eventualDirectFrames = append(eventualDirectFrames, prepared)
								}
							}
						}
					}
				} else {
					data, send, err := s.streamDeltaForSession(sessionID, id, authDelta, wrappedDeltas, wrappedPublicDelta)
					if err != nil {
						return err
					}
					if send {
						streamFrames = append(streamFrames, data)
					}
				}
			}
			if _, ok := spawnData[id]; ok {
				data, ok, err := s.wrappedFullForSession(sessionID, id, spawnData, wrappedFull, wrappedPublicFull)
				if err != nil {
					return err
				}
				if ok {
					streamFrames = append(streamFrames, data)
				}
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

		streamFrames, err = s.appendOwnershipRefreshFrames(sessionID, ownershipPending, diff.Stayed, wrappedFull, wrappedPublicFull, streamFrames)
		if err != nil {
			return err
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
			batched, msgs, err := s.sendSessionEventualState(sessionID, eventualDirty, eventualDirectFrames, eventualCache)
			if err != nil {
				return err
			}
			datagramBatchedSum += batched
			datagramMsgsSum += msgs
		}
		scratch.eventualDirty = eventualDirty[:0]
		scratch.eventualPrepared = eventualDirectFrames[:0]
	}

	s.cleanupVisibilityForRemovals(result.Removals)
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
