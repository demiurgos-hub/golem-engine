package golem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/interest"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
	"github.com/demiurgos-hub/golem-engine/golem/visibility"
)

const maxPhysicsCheckpointBytes = 1 << 30

// PhysicsCheckpoint pairs accepted physical state, authoritative entity bytes,
// desired geometry and navigation intent under one host checkpoint identity.
// Runtime handles and callback implementations are never persistent identities.
type PhysicsCheckpoint struct {
	Version                         uint32
	CheckpointID, SchemaFingerprint string
	Tick                            uint64
	EntityCounter                   int64
	Physics                         p.Checkpoint
	DesiredPhysicsRevision          uint64
	DesiredPhysicsWorld             []byte
	Entities                        []PhysicsEntityCheckpoint
	Attachments                     []PhysicsAttachmentCheckpoint
	Navigation                      *NavigationCheckpoint
	NavigationAgents                []PhysicsNavigationCheckpoint
	Geometry                        []PhysicsGeometryCheckpoint
}

// PhysicsGeometryCheckpoint records one source lease's ownership after recovery.
type PhysicsGeometryCheckpoint struct {
	Key    string
	Source content.InstanceState
}

// PhysicsEntityCheckpoint contains authoritative serialized entity state.
type PhysicsEntityCheckpoint struct {
	Record snapshot.Record
	Owner  int64
}

// PhysicsAttachmentCheckpoint records stable body identity and explicit bindings.
type PhysicsAttachmentCheckpoint struct {
	EntityID int64
	BodyID   p.ContentID
	Config   PhysicsAttachmentConfig
}

// PhysicsNavigationCheckpoint identifies every navigation attachment, including
// idle bodies. Gameplay supplies profile-compatible behavior callbacks on restore.
type PhysicsNavigationCheckpoint struct {
	EntityID      int64
	Profile       surface.ProfileID
	StagedPhysics bool
}

// PhysicsCheckpointOptions supplies immutable desired-world source captures.
// Navigation fields are required when the server has surface navigation.
type PhysicsCheckpointOptions struct {
	ID, SchemaFingerprint, PhysicsFingerprint, NavigationFingerprint string
	DesiredPhysicsRevision, DesiredNavigationRevision                uint64
	DesiredPhysicsWorld, DesiredNavigationWorld                      []byte
}

// CapturePhysicsCheckpoint captures all entities and attached systems on the tick
// goroutine outside frame callbacks. Queued commands must first drain, because
// closures cannot be persisted. Direct body changes must already be synchronized.
func (s *Server) CapturePhysicsCheckpoint(options PhysicsCheckpointOptions) (PhysicsCheckpoint, error) {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return PhysicsCheckpoint{}, err
	}
	if len(ps.commands) != 0 || s.navigation != nil && s.navigation.frame != nil {
		return PhysicsCheckpoint{}, p.ErrBusy
	}
	backend, ok := ps.world.(p.CheckpointWorld)
	if !ok {
		return PhysicsCheckpoint{}, p.ErrUnsupported
	}
	physical, err := backend.CaptureCheckpoint(options.ID, options.PhysicsFingerprint)
	if err != nil {
		return PhysicsCheckpoint{}, err
	}
	c := PhysicsCheckpoint{Version: 1, CheckpointID: options.ID, SchemaFingerprint: options.SchemaFingerprint, Tick: s.tick, EntityCounter: atomic.LoadInt64(&s.idCounter), Physics: physical,
		DesiredPhysicsRevision: options.DesiredPhysicsRevision, DesiredPhysicsWorld: append([]byte(nil), options.DesiredPhysicsWorld...)}
	entities := s.reg.All()
	sort.Slice(entities, func(i, j int) bool { return entities[i].EntityID() < entities[j].EntityID() })
	for _, entity := range entities {
		data, e := entity.FullUpdate()
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		owner, _ := s.Owner(entity.EntityID())
		c.Entities = append(c.Entities, PhysicsEntityCheckpoint{Record: snapshot.Record{EntityID: entity.EntityID(), TypeName: entity.TypeName(), StateData: append([]byte(nil), data...)}, Owner: owner})
	}
	for _, id := range ps.ordered() {
		a := ps.agents[id]
		state, e := ps.world.Body(a.body)
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		x, y, z := a.position.Position3D()
		if state.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
			return PhysicsCheckpoint{}, fmt.Errorf("golem: entity %d differs from checkpoint anchor", id)
		}
		c.Attachments = append(c.Attachments, PhysicsAttachmentCheckpoint{EntityID: id, BodyID: state.ID, Config: a.config})
	}
	if n := s.navigation; n != nil {
		navigation, e := s.CaptureNavigationCheckpoint(options.ID, options.NavigationFingerprint, options.DesiredNavigationRevision, options.DesiredNavigationWorld)
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		c.Navigation = &navigation
		for _, id := range n.order {
			if a := n.agents[id]; a != nil {
				c.NavigationAgents = append(c.NavigationAgents, PhysicsNavigationCheckpoint{EntityID: id, Profile: a.config.Profile, StagedPhysics: a.config.StagedPhysics})
			}
		}
	}
	for key, instance := range ps.geometry {
		state, e := instance.CaptureState()
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		c.Geometry = append(c.Geometry, PhysicsGeometryCheckpoint{Key: key, Source: state})
	}
	sort.Slice(c.Geometry, func(i, j int) bool { return c.Geometry[i].Key < c.Geometry[j].Key })
	if err = c.Validate(); err != nil {
		return PhysicsCheckpoint{}, err
	}
	return c, nil
}

// Validate checks the complete checkpoint pairing before any live mutation.
func (c PhysicsCheckpoint) Validate() error {
	if c.Version != 1 || c.CheckpointID == "" || c.CheckpointID != c.Physics.ID || c.SchemaFingerprint == "" || len(c.SchemaFingerprint) > 4096 || c.DesiredPhysicsRevision == 0 || len(c.DesiredPhysicsWorld) == 0 || len(c.DesiredPhysicsWorld) > 48<<20 || len(c.Entities) > 100000 || len(c.Attachments) > 100000 || len(c.NavigationAgents) > 100000 {
		return p.ErrInvalid
	}
	if err := c.Physics.Validate(); err != nil {
		return err
	}
	entities := map[int64]bool{}
	geometryKeys := map[string]bool{}
	geometryBytes := 0
	for _, geometry := range c.Geometry {
		geometryBytes += len(geometry.Source.Source)
		if geometry.Key == "" || len(geometry.Key) > 4096 || geometryKeys[geometry.Key] || geometryBytes > 512<<20 {
			return p.ErrInvalid
		}
		geometryKeys[geometry.Key] = true
	}
	bodyIDs := map[p.ContentID]p.BodyDesc{}
	bindings := map[int64]bool{}
	boundBodies := map[p.ContentID]bool{}
	navAgents := map[int64]PhysicsNavigationCheckpoint{}
	for _, body := range c.Physics.Bodies {
		bodyIDs[body.Description.ID] = body.Description
	}
	bytes := 0
	for _, entity := range c.Entities {
		r := entity.Record
		if r.EntityID == 0 || r.TypeName == "" || len(r.TypeName) > 4096 || entities[r.EntityID] {
			return p.ErrInvalid
		}
		entities[r.EntityID] = true
		bytes += len(r.StateData)
		if bytes > 128<<20 {
			return p.ErrInvalid
		}
	}
	for _, a := range c.Attachments {
		body, ok := bodyIDs[a.BodyID]
		if !entities[a.EntityID] || bindings[a.EntityID] || boundBodies[a.BodyID] || !ok || body.Owner != a.EntityID || !p.Finite(a.Config.MaxDriveForce) || a.Config.MaxDriveForce < 0 || !p.Finite(a.Config.Placement.MaxPenetration) || a.Config.Placement.MaxPenetration < 0 {
			return p.ErrInvalid
		}
		bindings[a.EntityID], boundBodies[a.BodyID] = true, true
	}
	for _, a := range c.NavigationAgents {
		if !entities[a.EntityID] || a.Profile == "" || len(a.Profile) > 256 || navAgents[a.EntityID].EntityID != 0 || bindings[a.EntityID] && !a.StagedPhysics {
			return p.ErrInvalid
		}
		navAgents[a.EntityID] = a
	}
	if c.Navigation == nil {
		if len(c.NavigationAgents) != 0 {
			return p.ErrInvalid
		}
	} else {
		if c.Navigation.CheckpointID != c.CheckpointID {
			return p.ErrInvalid
		}
		if err := validateNavigationCheckpoint(*c.Navigation); err != nil {
			return err
		}
		for _, intent := range c.Navigation.Agents {
			if navAgents[intent.EntityID].Profile != intent.Profile {
				return p.ErrInvalid
			}
		}
	}
	return nil
}

// EncodePhysicsCheckpoint validates and serializes the paired capture.
func EncodePhysicsCheckpoint(c PhysicsCheckpoint) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if len(data) > maxPhysicsCheckpointBytes {
		return nil, p.ErrInvalid
	}
	return data, err
}

// DecodePhysicsCheckpoint rejects unknown fields, oversized input and trailing data.
func DecodePhysicsCheckpoint(data []byte) (PhysicsCheckpoint, error) {
	var c PhysicsCheckpoint
	if len(data) > maxPhysicsCheckpointBytes {
		return c, p.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if d.Decode(new(any)) != io.EOF {
		return PhysicsCheckpoint{}, p.ErrInvalid
	}
	if err := c.Validate(); err != nil {
		return PhysicsCheckpoint{}, err
	}
	return c, nil
}

// PhysicsRestoreOptions resolves application-specific entity types and navigation
// policies. Factories prepare private data only, without registering entities,
// changing world state or invoking spawn/remove hooks. PrepareNavigation must
// return an atomic NavigationBuildResult for the captured desired navigation world.
type PhysicsRestoreOptions struct {
	SchemaFingerprint, PhysicsFingerprint, NavigationFingerprint string
	Entity                                                       func(snapshot.Record) (Entity, error)
	NavigationConfig                                             func(PhysicsNavigationCheckpoint) (NavigationAgentConfig, error)
	PrepareNavigation                                            func(NavigationCheckpoint) (NavigationBuildResult, error)
}

// PhysicsRestored is an optional explicit recovery hook, called after complete
// state publication and readiness. Restore never calls ordinary OnSpawn/OnRemove.
type PhysicsRestored interface{ OnPhysicsRestored(*Server) }

// RestorePhysicsCheckpoint privately validates entities, bindings, physics and
// navigation before publication. Call at startup or on a quiescent tick with no
// connected clients, and stop accepting new connections for the whole operation.
// Live client revision rewind requires an explicit reconnect. Ephemeral interest,
// visibility and avatar/session membership are reset; rebuild them in recovery hooks.
// A preparation failure leaves accepted entities and physics untouched. A rare
// failure after navigation publication closes readiness and requires coherent retry.
func (s *Server) RestorePhysicsCheckpoint(c PhysicsCheckpoint, options PhysicsRestoreOptions) (err error) {
	ps := s.physics
	if ps == nil || ps.closed || ps.stepping || ps.dispatching || ps.restoring && ps.fault == nil {
		return p.ErrBusy
	}
	if s.navigation != nil && s.navigation.frame != nil {
		return p.ErrBusy
	}
	if len(s.listener.SessionIDs()) != 0 {
		return errors.New("golem: disconnect clients before physics restoration")
	}
	if err = c.Validate(); err != nil {
		return err
	}
	if options.Entity == nil || c.SchemaFingerprint != options.SchemaFingerprint || c.Physics.SourceFingerprint != options.PhysicsFingerprint {
		return errors.New("golem: physics checkpoint schema/source mismatch")
	}
	if (c.Navigation != nil) != (s.navigation != nil) {
		return errors.New("golem: physics checkpoint navigation configuration mismatch")
	}
	if c.Navigation != nil && (c.Navigation.SourceFingerprint != options.NavigationFingerprint || options.NavigationConfig == nil || options.PrepareNavigation == nil) {
		return errors.New("golem: navigation restore configuration mismatch")
	}
	backend, ok := ps.world.(p.CheckpointWorld)
	if !ok {
		return p.ErrUnsupported
	}
	priorPhysicsRestoring := ps.restoring
	ps.restoring = true
	priorNavRestoring := false
	if s.navigation != nil {
		priorNavRestoring = s.navigation.restoring
		s.navigation.restoring = true
	}
	published := false
	defer func() {
		if err != nil && published {
			ps.fault = err
			return
		}
		ps.restoring = false
		if err != nil {
			ps.restoring = priorPhysicsRestoring
		}
		if err != nil && s.navigation != nil {
			s.navigation.restoring = priorNavRestoring
		}
	}()
	prepared, err := backend.PrepareRestore(c.Physics)
	if err != nil {
		return err
	}
	defer prepared.Discard()
	geometry := map[string]*content.Instance{}
	geometryIDs := [4]map[p.ContentID]bool{{}, {}, {}, {}}
	for _, saved := range c.Geometry {
		instance, e := content.RestoreInstance(ps.world, saved.Source, prepared)
		if e != nil {
			return e
		}
		ids := instance.Prepared().ContentIDs()
		for kind, group := range [][]p.ContentID{ids.Bodies, ids.Shapes, ids.Colliders, ids.Joints} {
			for _, id := range group {
				if geometryIDs[kind][id] {
					return errors.New("golem: restored geometry leases have overlapping ownership")
				}
				geometryIDs[kind][id] = true
			}
		}
		geometry[saved.Key] = instance
	}
	entities := make(map[int64]Entity, len(c.Entities))
	entries := make([]registry.RestoreEntry, 0, len(c.Entities))
	var maxID int64
	for _, record := range c.Entities {
		entity, e := options.Entity(record.Record)
		if e != nil {
			return e
		}
		if entity == nil || entity.EntityID() != record.Record.EntityID || entity.TypeName() != record.Record.TypeName {
			return errors.New("golem: restored entity identity mismatch")
		}
		if binder, ok := entity.(ServerBinder); ok {
			binder.BindServer(s)
		}
		entities[entity.EntityID()] = entity
		entries = append(entries, registry.RestoreEntry{Entity: entity, Owner: record.Owner})
		maxID = max(maxID, entity.EntityID())
	}
	agents := map[int64]*physicsAttachment{}
	bodyOwners := map[p.BodyHandle]int64{}
	for _, binding := range c.Attachments {
		entity := entities[binding.EntityID]
		state, ok := prepared.Body(binding.BodyID)
		if !ok || !validPhysicsState(state) || state.Owner != binding.EntityID {
			return errors.New("golem: restored body identity mismatch")
		}
		position, readable := entity.(registry.Spatial3DEntity)
		writer, writable := entity.(registry.Position3DWriter)
		if !readable || !writable {
			return errors.New("golem: restored physics entity lacks 3D bindings")
		}
		if _, ok := entity.(ColliderProvider); ok {
			return errors.New("golem: restored physics has competing collider")
		}
		if _, ok := entity.(ColliderProvider3D); ok {
			return errors.New("golem: restored physics has competing 3D collider")
		}
		x, y, z := position.Position3D()
		if state.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
			return errors.New("golem: restored entity and gameplay anchor disagree")
		}
		a := &physicsAttachment{id: binding.EntityID, body: state.Handle, entity: entity, position: position, writer: writer, config: binding.Config, localAnchor: state.Pose.Inverse().Compose(state.Anchor)}
		if binding.Config.BindRotation {
			a.rotation, _ = entity.(PhysicsRotationWriter)
			if a.rotation == nil {
				return errors.New("golem: missing restored rotation binding")
			}
		}
		if binding.Config.BindVelocity {
			a.velocity, _ = entity.(PhysicsVelocityWriter)
			if a.velocity == nil {
				return errors.New("golem: missing restored velocity binding")
			}
		}
		if binding.Config.BindActivation {
			a.activation, _ = entity.(PhysicsActivationWriter)
			if a.activation == nil {
				return errors.New("golem: missing restored activation binding")
			}
		}
		agents[a.id], bodyOwners[a.body] = a, a.id
	}
	configs := map[int64]NavigationAgentConfig{}
	for _, saved := range c.NavigationAgents {
		cfg, e := options.NavigationConfig(saved)
		if e != nil {
			return e
		}
		if cfg.Profile != saved.Profile || cfg.StagedPhysics != saved.StagedPhysics {
			return errors.New("golem: restored navigation policy mismatch")
		}
		if e = validateNavigationAgent(&cfg); e != nil {
			return e
		}
		entity := entities[saved.EntityID]
		if _, ok := entity.(registry.Spatial3DEntity); !ok {
			return p.ErrInvalid
		}
		if _, ok := entity.(registry.Position3DWriter); !ok {
			return p.ErrInvalid
		}
		if a := agents[saved.EntityID]; a != nil {
			state, _ := prepared.Body(bodyIDFor(c.Attachments, saved.EntityID))
			if !physicsNavigationMode(state.Mode) {
				return p.ErrInvalid
			}
		}
		configs[saved.EntityID] = cfg
	}
	if c.Navigation != nil {
		for _, intent := range c.Navigation.Agents {
			if a := agents[intent.EntityID]; a != nil {
				state, _ := prepared.Body(bodyIDFor(c.Attachments, intent.EntityID))
				if state.Mode == p.FreeDynamic {
					return errors.New("golem: restored free body has drive intent")
				}
			}
		}
	}
	var navigation NavigationBuildResult
	if c.Navigation != nil {
		navigation, err = options.PrepareNavigation(*c.Navigation)
		if err != nil {
			return err
		}
		if navigation == nil {
			return p.ErrInvalid
		}
		committed := false
		defer func() {
			if !committed {
				navigation.Discard()
			}
		}()
		if err = navigation.Commit(); err != nil {
			return err
		}
		committed = true
		published = true
	}
	if err = prepared.Commit(); err != nil {
		return err
	}
	published = true
	ps.fault = nil
	ps.commands = nil
	old := s.reg.All()
	var callbacks []func()
	if n := s.navigation; n != nil {
		n.frameCallbacks = make([]func(), 0)
		if err = s.BeginNavigationRestore(); err != nil {
			return err
		}
		for len(n.order) > 0 {
			s.DetachNavigation(n.order[0])
		}
		callbacks = n.frameCallbacks
		n.frameCallbacks = nil
		n.framePrepared = false
	}
	for _, entity := range old {
		s.unregisterCollider(entity)
	}
	if err = s.reg.ReplaceWithoutHooks(entries); err != nil {
		return err
	}
	ps.agents, ps.bodies = agents, bodyOwners
	ps.geometry = geometry
	s.tick = max(s.tick, c.Tick, c.Physics.LastFrame)
	s.SetEntityIDCounter(max(maxID, c.EntityCounter, atomic.LoadInt64(&s.idCounter)))
	s.interestMu.Lock()
	if s.interest != nil {
		s.interest = interest.NewManager(s.config.CellSize)
	}
	s.visibility = visibility.NewManager()
	s.broadcastKnown = make(map[int64]map[int64]struct{})
	s.ownershipRefresh = nil
	s.interestMu.Unlock()
	s.avatarMu.Lock()
	s.sessionAvatar = map[int64]int64{}
	s.avatarSession = map[int64]int64{}
	s.avatarMu.Unlock()
	s.eventualTrackers = nil
	for _, entry := range entries {
		s.registerCollider(entry.Entity)
	}
	for _, binding := range c.Attachments {
		state, e := ps.world.Body(agents[binding.EntityID].body)
		if e != nil {
			return e
		}
		ps.write(agents[binding.EntityID], state, true)
	}
	for _, saved := range c.NavigationAgents {
		if err = s.AttachNavigation(saved.EntityID, configs[saved.EntityID]); err != nil {
			return err
		}
	}
	if c.Navigation != nil {
		if err = s.RestoreNavigationIntents(*c.Navigation, options.NavigationFingerprint); err != nil {
			return err
		}
		if err = s.CompleteNavigationRestore(nil); err != nil {
			return err
		}
	}
	ps.restoring = false
	for _, callback := range callbacks {
		callback()
	}
	for _, entry := range entries {
		if restored, ok := entry.Entity.(PhysicsRestored); ok {
			restored.OnPhysicsRestored(s)
		}
	}
	return nil
}

func bodyIDFor(bindings []PhysicsAttachmentCheckpoint, id int64) p.ContentID {
	for _, binding := range bindings {
		if binding.EntityID == id {
			return binding.BodyID
		}
	}
	return ""
}

type physicsCheckpointManifest struct {
	Version                    uint32
	CheckpointID, File, SHA256 string
}

// SavePhysicsCheckpoint writes immutable data first and atomically replaces the
// manifest last. It is synchronous filesystem work; capture on the tick, then pass
// the owned value to a worker. Completed data files are retained for recovery.
func SavePhysicsCheckpoint(directory string, c PhysicsCheckpoint) error {
	data, err := EncodePhysicsCheckpoint(c)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	name := hash + ".physics.json"
	path := filepath.Join(directory, name)
	if existing, e := os.ReadFile(path); e == nil {
		if !bytes.Equal(existing, data) {
			return errors.New("golem: checkpoint content-address collision")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else if err = writePhysicsCheckpointFile(path, data); err != nil {
		return err
	}
	manifest, _ := json.Marshal(physicsCheckpointManifest{Version: 1, CheckpointID: c.CheckpointID, File: name, SHA256: hash})
	return writePhysicsCheckpointFile(filepath.Join(directory, "physics-checkpoint.json"), manifest)
}

func writePhysicsCheckpointFile(path string, data []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".physics-checkpoint-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { file.Close(); os.Remove(name) }()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// LoadPhysicsCheckpoint verifies the manifest identity and SHA-256 before decoding.
func LoadPhysicsCheckpoint(directory string) (PhysicsCheckpoint, error) {
	manifestBytes, err := readPhysicsCheckpointFile(filepath.Join(directory, "physics-checkpoint.json"), 16384)
	if err != nil {
		return PhysicsCheckpoint{}, err
	}
	var manifest physicsCheckpointManifest
	d := json.NewDecoder(bytes.NewReader(manifestBytes))
	d.DisallowUnknownFields()
	if err = d.Decode(&manifest); err != nil {
		return PhysicsCheckpoint{}, err
	}
	if d.Decode(new(any)) != io.EOF || manifest.Version != 1 || len(manifest.SHA256) != 64 || manifest.File != manifest.SHA256+".physics.json" {
		return PhysicsCheckpoint{}, p.ErrInvalid
	}
	if _, err = hex.DecodeString(manifest.SHA256); err != nil {
		return PhysicsCheckpoint{}, p.ErrInvalid
	}
	data, err := readPhysicsCheckpointFile(filepath.Join(directory, manifest.File), maxPhysicsCheckpointBytes)
	if err != nil {
		return PhysicsCheckpoint{}, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != manifest.SHA256 {
		return PhysicsCheckpoint{}, errors.New("golem: checkpoint digest mismatch")
	}
	c, err := DecodePhysicsCheckpoint(data)
	if err == nil && c.CheckpointID != manifest.CheckpointID {
		return PhysicsCheckpoint{}, errors.New("golem: checkpoint manifest identity mismatch")
	}
	return c, err
}

func readPhysicsCheckpointFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, p.ErrInvalid
	}
	return data, err
}
