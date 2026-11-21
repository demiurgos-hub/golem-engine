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
	"reflect"
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

// maxPhysicsCheckpointSpaces bounds the space sections of one checkpoint.
const maxPhysicsCheckpointSpaces = 4096

// PhysicsCheckpoint pairs accepted physical state, authoritative entity bytes,
// desired geometry and navigation intent under one host checkpoint identity.
// Runtime handles and callback implementations are never persistent identities.
//
// Version 1 describes a server where only space 0 has physics and surface
// navigation: space 0's sections live in the top-level physical fields, and
// entity records may still carry other (service-less) spaces in Record.Space.
// Its encoding is unchanged from single-space releases. Version 2 describes a
// server where any other space has physics or surface navigation: the
// top-level physical fields (Physics through Trajectories, except Entities)
// stay zero and Spaces holds one section per space with services, in strictly
// ascending space order. Capture picks the version; both decode.
type PhysicsCheckpoint struct {
	Version                         uint32
	CheckpointID, SchemaFingerprint string
	Tick                            uint64
	EntityCounter                   int64
	Physics                         p.Checkpoint
	DesiredPhysicsRevision          uint64
	DesiredPhysicsFingerprint       string
	DesiredPhysicsWorld             []byte
	PendingWorld                    bool
	PendingGeometry                 []PhysicsGeometryPending
	AcceptedNavigation              *NavigationCheckpoint
	Entities                        []PhysicsEntityCheckpoint
	Attachments                     []PhysicsAttachmentCheckpoint
	Navigation                      *NavigationCheckpoint
	NavigationAgents                []PhysicsNavigationCheckpoint
	Geometry                        []PhysicsGeometryCheckpoint
	Trajectories                    []PhysicsTrajectoryCheckpoint
	// Spaces holds the per-space sections of a version-2 checkpoint; it is
	// empty (and omitted from JSON) in version 1.
	Spaces []PhysicsSpaceCheckpoint `json:",omitempty"`
}

// PhysicsSpaceCheckpoint is one space's physical world, desired captures,
// bindings, navigation and source leases inside a version-2 PhysicsCheckpoint.
// The fields mean what the top-level fields of the same name mean in version 1.
// Physics.ID and the navigation captures' CheckpointID equal the enclosing
// CheckpointID; body content IDs are unique within the section's world only.
// Attachments, navigation agents and trajectories name entities recorded in
// this space.
type PhysicsSpaceCheckpoint struct {
	Space                     SpaceID
	Physics                   p.Checkpoint
	DesiredPhysicsRevision    uint64
	DesiredPhysicsFingerprint string
	DesiredPhysicsWorld       []byte
	PendingWorld              bool
	PendingGeometry           []PhysicsGeometryPending
	AcceptedNavigation        *NavigationCheckpoint
	Attachments               []PhysicsAttachmentCheckpoint
	Navigation                *NavigationCheckpoint
	NavigationAgents          []PhysicsNavigationCheckpoint
	Geometry                  []PhysicsGeometryCheckpoint
	Trajectories              []PhysicsTrajectoryCheckpoint
}

// PhysicsGeometryCheckpoint records one source lease's ownership after recovery.
type PhysicsGeometryCheckpoint struct {
	Key      string
	Identity PhysicsGeometryIdentity
	Source   content.InstanceState
}

// PhysicsEntityCheckpoint contains authoritative serialized entity state.
// Record.Space keeps the entity's space and is omitted from JSON in space 0.
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
// The top-level capture fields describe space 0; Spaces supplies the same
// captures for every other space with physics (key 0 is rejected); the space-0
// fields are ignored while space 0 has neither physics nor navigation. Navigation
// fields are required when the space has surface navigation. With pending
// changes, supply the separately accepted navigation capture and physics
// fingerprint as well; without them capture returns ErrBusy.
type PhysicsCheckpointOptions struct {
	ID, SchemaFingerprint, PhysicsFingerprint, NavigationFingerprint string
	DesiredPhysicsRevision, DesiredNavigationRevision                uint64
	DesiredPhysicsWorld, DesiredNavigationWorld                      []byte
	// Pending changes require a separately captured accepted navigation world and
	// physical fingerprint, so recovery never mistakes desired bytes for live state.
	AcceptedPhysicsFingerprint, AcceptedNavigationFingerprint string
	AcceptedNavigationRevision                                uint64
	AcceptedNavigationWorld                                   []byte
	// Spaces holds the captures of the spaces other than 0, by space.
	Spaces map[SpaceID]PhysicsSpaceCheckpointOptions
}

// PhysicsSpaceCheckpointOptions supplies one non-zero space's desired (and,
// with pending geometry, accepted) world captures; the fields mean what the
// PhysicsCheckpointOptions fields of the same name mean for space 0.
type PhysicsSpaceCheckpointOptions struct {
	PhysicsFingerprint, NavigationFingerprint                 string
	DesiredPhysicsRevision, DesiredNavigationRevision         uint64
	DesiredPhysicsWorld, DesiredNavigationWorld               []byte
	AcceptedPhysicsFingerprint, AcceptedNavigationFingerprint string
	AcceptedNavigationRevision                                uint64
	AcceptedNavigationWorld                                   []byte
}

// space returns the capture options of one space.
func (o PhysicsCheckpointOptions) space(id SpaceID) (PhysicsSpaceCheckpointOptions, bool) {
	if id != 0 {
		options, ok := o.Spaces[id]
		return options, ok
	}
	return PhysicsSpaceCheckpointOptions{PhysicsFingerprint: o.PhysicsFingerprint, NavigationFingerprint: o.NavigationFingerprint,
		DesiredPhysicsRevision: o.DesiredPhysicsRevision, DesiredNavigationRevision: o.DesiredNavigationRevision,
		DesiredPhysicsWorld: o.DesiredPhysicsWorld, DesiredNavigationWorld: o.DesiredNavigationWorld,
		AcceptedPhysicsFingerprint: o.AcceptedPhysicsFingerprint, AcceptedNavigationFingerprint: o.AcceptedNavigationFingerprint,
		AcceptedNavigationRevision: o.AcceptedNavigationRevision, AcceptedNavigationWorld: o.AcceptedNavigationWorld}, true
}

// servicedRuntimes returns the runtimes with physics or surface navigation in
// ascending space order. A paired checkpoint holds one section per such space.
func (s *Server) servicedRuntimes() []*spaceRuntime {
	var out []*spaceRuntime
	_ = s.eachRuntime(func(rt *spaceRuntime) error {
		if rt.physics != nil || rt.navigation != nil {
			out = append(out, rt)
		}
		return nil
	})
	return out
}

// checkpointRuntimes returns the serviced runtimes, rejecting a server without
// physics and any space with surface navigation but no physics world.
func (s *Server) checkpointRuntimes() ([]*spaceRuntime, error) {
	runtimes := s.servicedRuntimes()
	if len(runtimes) == 0 {
		return nil, errors.New("golem: physics is not configured")
	}
	for _, rt := range runtimes {
		if rt.physics == nil {
			return nil, fmt.Errorf("golem: space %d has surface navigation without physics; paired checkpoints require a physics world in every serviced space", rt.id)
		}
	}
	return runtimes, nil
}

// CapturePhysicsCheckpoint captures all entities and attached systems on the tick
// goroutine outside frame callbacks. Queued commands must first drain, because
// closures cannot be persisted. Direct body changes must already be synchronized.
// Desired captures must describe the accepted world unless separate accepted
// captures are supplied for a pending change; worker closures are never captured.
//
// Every space with physics or surface navigation is captured, and each needs a
// physics world and options (top-level fields for space 0, Spaces for the
// others). While only space 0 has services the result is version 1, byte for
// byte as before multi-space checkpoints; otherwise it is version 2. Every space
// is checked for queued commands, open frames and restoration before anything
// is captured. Entities of service-less spaces are captured with their space.
func (s *Server) CapturePhysicsCheckpoint(options PhysicsCheckpointOptions) (PhysicsCheckpoint, error) {
	runtimes, err := s.checkpointRuntimes()
	if err != nil {
		return PhysicsCheckpoint{}, err
	}
	if _, ok := options.Spaces[0]; ok {
		return PhysicsCheckpoint{}, errors.New("golem: space 0 checkpoint options belong in the top-level fields")
	}
	serviced := make(map[SpaceID]bool, len(runtimes))
	for _, rt := range runtimes {
		serviced[rt.id] = true
	}
	for space := range options.Spaces {
		if !serviced[space] {
			return PhysicsCheckpoint{}, fmt.Errorf("golem: checkpoint options for space %d, which has no physics", space)
		}
	}
	pending := make([]bool, len(runtimes))
	spaceOptions := make([]PhysicsSpaceCheckpointOptions, len(runtimes))
	for i, rt := range runtimes {
		o, ok := options.space(rt.id)
		if !ok {
			return PhysicsCheckpoint{}, fmt.Errorf("golem: space %d has physics but no checkpoint options", rt.id)
		}
		if pending[i], err = rt.checkpointCapturable(o); err != nil {
			return PhysicsCheckpoint{}, err
		}
		spaceOptions[i] = o
	}
	c := PhysicsCheckpoint{Version: 1, CheckpointID: options.ID, SchemaFingerprint: options.SchemaFingerprint, Tick: s.tick, EntityCounter: atomic.LoadInt64(&s.idCounter)}
	entities := s.reg.All()
	sort.Slice(entities, func(i, j int) bool { return entities[i].EntityID() < entities[j].EntityID() })
	for _, entity := range entities {
		data, e := entity.FullUpdate()
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		owner, _ := s.Owner(entity.EntityID())
		space, _ := s.reg.Space(entity.EntityID())
		c.Entities = append(c.Entities, PhysicsEntityCheckpoint{Record: snapshot.Record{EntityID: entity.EntityID(), TypeName: entity.TypeName(), StateData: append([]byte(nil), data...), Space: space}, Owner: owner})
	}
	sections := make([]PhysicsSpaceCheckpoint, 0, len(runtimes))
	for i, rt := range runtimes {
		section, e := rt.capturePhysicsSpace(options.ID, spaceOptions[i], pending[i])
		if e != nil {
			return PhysicsCheckpoint{}, e
		}
		sections = append(sections, section)
	}
	if len(sections) == 1 && sections[0].Space == 0 {
		c.setSpaceZero(sections[0])
	} else {
		c.Version, c.Spaces = 2, sections
	}
	if err = c.Validate(); err != nil {
		return PhysicsCheckpoint{}, err
	}
	return c, nil
}

// checkpointCapturable rejects a space that cannot be captured now and reports
// whether its world has pending geometry. It changes nothing.
func (rt *spaceRuntime) checkpointCapturable(o PhysicsSpaceCheckpointOptions) (bool, error) {
	ps := rt.physics
	if err := ps.mutable(); err != nil {
		return false, err
	}
	n := rt.navigation
	if len(ps.commands) != 0 || n != nil && n.frame != nil {
		return false, p.ErrBusy
	}
	pending := rt.physicsWorldPending()
	if n != nil && (n.restoring || n.restoreErr != nil) {
		return false, p.ErrBusy
	}
	if pending && (o.AcceptedPhysicsFingerprint == "" || o.AcceptedNavigationFingerprint == "" || o.AcceptedNavigationRevision == 0 || len(o.AcceptedNavigationWorld) == 0) {
		return false, fmt.Errorf("%w: pending geometry in space %d requires separate accepted-world captures", p.ErrBusy, rt.id)
	}
	if !pending && o.AcceptedPhysicsFingerprint != "" && o.AcceptedPhysicsFingerprint != o.PhysicsFingerprint {
		return false, p.ErrInvalid
	}
	if _, ok := ps.world.(p.CheckpointWorld); !ok {
		return false, p.ErrUnsupported
	}
	return pending, nil
}

// capturePhysicsSpace captures one space's world, bindings, trajectories,
// navigation and source leases after checkpointCapturable accepted it.
func (rt *spaceRuntime) capturePhysicsSpace(id string, o PhysicsSpaceCheckpointOptions, pending bool) (PhysicsSpaceCheckpoint, error) {
	ps := rt.physics
	physicalFingerprint := o.PhysicsFingerprint
	if o.AcceptedPhysicsFingerprint != "" {
		physicalFingerprint = o.AcceptedPhysicsFingerprint
	}
	physical, err := ps.world.(p.CheckpointWorld).CaptureCheckpoint(id, physicalFingerprint)
	if err != nil {
		return PhysicsSpaceCheckpoint{}, err
	}
	c := PhysicsSpaceCheckpoint{Space: rt.id, Physics: physical, DesiredPhysicsRevision: o.DesiredPhysicsRevision, DesiredPhysicsFingerprint: o.PhysicsFingerprint,
		DesiredPhysicsWorld: append([]byte(nil), o.DesiredPhysicsWorld...), PendingWorld: pending, PendingGeometry: ps.pendingGeometry()}
	for _, entityID := range ps.ordered() {
		a := ps.agents[entityID]
		state, e := ps.world.Body(a.body)
		if e != nil {
			return PhysicsSpaceCheckpoint{}, e
		}
		x, y, z := a.position.Position3D()
		if state.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
			return PhysicsSpaceCheckpoint{}, fmt.Errorf("golem: entity %d differs from checkpoint anchor", entityID)
		}
		c.Attachments = append(c.Attachments, PhysicsAttachmentCheckpoint{EntityID: entityID, BodyID: state.ID, Config: a.config})
	}
	c.Trajectories = ps.captureTrajectories()
	if n := rt.navigation; n != nil {
		navigation, e := captureNavigationCheckpoint(rt, id, o.NavigationFingerprint, o.DesiredNavigationRevision, o.DesiredNavigationWorld)
		if e != nil {
			return PhysicsSpaceCheckpoint{}, e
		}
		c.Navigation = &navigation
		if pending {
			accepted, e := captureNavigationCheckpoint(rt, id, o.AcceptedNavigationFingerprint, o.AcceptedNavigationRevision, o.AcceptedNavigationWorld)
			if e != nil {
				return PhysicsSpaceCheckpoint{}, e
			}
			c.AcceptedNavigation = &accepted
		}
		for _, entityID := range n.order {
			if a := n.agents[entityID]; a != nil {
				c.NavigationAgents = append(c.NavigationAgents, PhysicsNavigationCheckpoint{EntityID: entityID, Profile: a.config.Profile, StagedPhysics: a.config.StagedPhysics})
			}
		}
	}
	for key, instance := range ps.geometry {
		state, e := instance.CaptureState()
		if e != nil {
			return PhysicsSpaceCheckpoint{}, e
		}
		c.Geometry = append(c.Geometry, PhysicsGeometryCheckpoint{Key: key, Identity: ps.geometryIdentities[key], Source: state})
	}
	sort.Slice(c.Geometry, func(i, j int) bool { return c.Geometry[i].Key < c.Geometry[j].Key })
	return c, nil
}

// setSpaceZero stores space 0's section in the version-1 top-level fields.
func (c *PhysicsCheckpoint) setSpaceZero(s PhysicsSpaceCheckpoint) {
	c.Physics, c.DesiredPhysicsRevision, c.DesiredPhysicsFingerprint, c.DesiredPhysicsWorld = s.Physics, s.DesiredPhysicsRevision, s.DesiredPhysicsFingerprint, s.DesiredPhysicsWorld
	c.PendingWorld, c.PendingGeometry, c.AcceptedNavigation = s.PendingWorld, s.PendingGeometry, s.AcceptedNavigation
	c.Attachments, c.Navigation, c.NavigationAgents, c.Geometry, c.Trajectories = s.Attachments, s.Navigation, s.NavigationAgents, s.Geometry, s.Trajectories
}

// sections returns the per-space sections: version 1's top-level fields as
// space 0's only section, or version 2's Spaces.
func (c PhysicsCheckpoint) sections() []PhysicsSpaceCheckpoint {
	if c.Version == 2 {
		return c.Spaces
	}
	return []PhysicsSpaceCheckpoint{{Space: 0, Physics: c.Physics, DesiredPhysicsRevision: c.DesiredPhysicsRevision, DesiredPhysicsFingerprint: c.DesiredPhysicsFingerprint,
		DesiredPhysicsWorld: c.DesiredPhysicsWorld, PendingWorld: c.PendingWorld, PendingGeometry: c.PendingGeometry, AcceptedNavigation: c.AcceptedNavigation,
		Attachments: c.Attachments, Navigation: c.Navigation, NavigationAgents: c.NavigationAgents, Geometry: c.Geometry, Trajectories: c.Trajectories}}
}

// spaceZeroView returns the version-1-shaped checkpoint that space 0's legacy
// PreparePendingWorld hook receives: c itself in version 1, or in version 2 a
// copy holding space 0's section in the top-level fields (with every entity).
func (c PhysicsCheckpoint) spaceZeroView(section PhysicsSpaceCheckpoint) PhysicsCheckpoint {
	if c.Version != 2 {
		return c
	}
	view := PhysicsCheckpoint{Version: 1, CheckpointID: c.CheckpointID, SchemaFingerprint: c.SchemaFingerprint, Tick: c.Tick, EntityCounter: c.EntityCounter, Entities: c.Entities}
	view.setSpaceZero(section)
	return view
}

// topLevelPhysicalZero reports whether every per-space top-level field is
// empty, as version 2 requires.
func (c PhysicsCheckpoint) topLevelPhysicalZero() bool {
	return reflect.ValueOf(c.Physics).IsZero() && c.DesiredPhysicsRevision == 0 && c.DesiredPhysicsFingerprint == "" && c.DesiredPhysicsWorld == nil &&
		!c.PendingWorld && c.PendingGeometry == nil && c.AcceptedNavigation == nil && c.Attachments == nil && c.Navigation == nil &&
		c.NavigationAgents == nil && c.Geometry == nil && c.Trajectories == nil
}

// Validate checks the complete checkpoint pairing before any live mutation.
//
// Both versions require a checkpoint identity, schema fingerprint and unique
// entity records. Version 1 requires empty Spaces and that every attachment and
// navigation agent names an entity recorded in space 0. Version 2 requires
// empty top-level physical fields and 1 to 4096 sections in strictly ascending
// space order, other than a lone space-0 section (that state encodes as
// version 1). Each section is validated like a version-1 checkpoint: its
// Physics.ID and navigation CheckpointIDs equal CheckpointID, its body content
// IDs are unique within its world, and its attachments, navigation agents and
// trajectories name entities recorded in its space. Entities of spaces without
// a section are allowed and carry no services. Object limits apply across all
// sections.
func (c PhysicsCheckpoint) Validate() error {
	if c.CheckpointID == "" || c.SchemaFingerprint == "" || len(c.SchemaFingerprint) > 4096 || len(c.Entities) > 100000 {
		return p.ErrInvalid
	}
	switch c.Version {
	case 1:
		if len(c.Spaces) != 0 {
			return p.ErrInvalid
		}
	case 2:
		if !c.topLevelPhysicalZero() || len(c.Spaces) == 0 || len(c.Spaces) > maxPhysicsCheckpointSpaces || len(c.Spaces) == 1 && c.Spaces[0].Space == 0 {
			return p.ErrInvalid
		}
	default:
		return p.ErrInvalid
	}
	entities := make(map[int64]SpaceID, len(c.Entities))
	bytes := 0
	for _, entity := range c.Entities {
		r := entity.Record
		if _, duplicate := entities[r.EntityID]; r.EntityID == 0 || r.TypeName == "" || len(r.TypeName) > 4096 || duplicate {
			return p.ErrInvalid
		}
		entities[r.EntityID] = r.Space
		bytes += len(r.StateData)
		if bytes > 128<<20 {
			return p.ErrInvalid
		}
	}
	var attachments, agents, geometry, pending, geometryBytes int
	for i, section := range c.sections() {
		if i > 0 && section.Space <= c.Spaces[i-1].Space {
			return p.ErrInvalid
		}
		attachments += len(section.Attachments)
		agents += len(section.NavigationAgents)
		geometry += len(section.Geometry)
		pending += len(section.PendingGeometry)
		for _, g := range section.Geometry {
			geometryBytes += len(g.Source.Source)
		}
		if attachments > 100000 || agents > 100000 || geometry > 100000 || pending > 100000 || geometryBytes > 512<<20 {
			return p.ErrInvalid
		}
		if err := section.validate(c.CheckpointID, entities); err != nil {
			return err
		}
	}
	return nil
}

// validate checks one space section against the checkpoint identity and the
// recorded entity spaces.
func (c PhysicsSpaceCheckpoint) validate(checkpointID string, entities map[int64]SpaceID) error {
	if checkpointID != c.Physics.ID || c.DesiredPhysicsRevision == 0 || len(c.DesiredPhysicsWorld) == 0 || len(c.DesiredPhysicsWorld) > 48<<20 || len(c.Attachments) > 100000 || len(c.NavigationAgents) > 100000 {
		return p.ErrInvalid
	}
	if err := c.Physics.Validate(); err != nil {
		return err
	}
	if c.DesiredPhysicsFingerprint == "" || len(c.DesiredPhysicsFingerprint) > 4096 || len(c.Geometry) > 100000 || len(c.PendingGeometry) > 100000 {
		return p.ErrInvalid
	}
	if c.PendingWorld {
		if c.Navigation == nil || c.AcceptedNavigation == nil || c.AcceptedNavigation.CheckpointID != checkpointID {
			return p.ErrInvalid
		}
		if err := validateNavigationCheckpoint(*c.AcceptedNavigation); err != nil {
			return err
		}
	} else if c.AcceptedNavigation != nil || len(c.PendingGeometry) != 0 {
		return p.ErrInvalid
	}
	if !c.PendingWorld && c.Physics.SourceFingerprint != c.DesiredPhysicsFingerprint {
		return p.ErrInvalid
	}
	if err := validatePendingPhysicsGeometry(c.PendingGeometry); err != nil {
		return err
	}
	inSpace := func(id int64) bool {
		space, ok := entities[id]
		return ok && space == c.Space
	}
	geometryKeys := map[string]bool{}
	geometryBytes := 0
	for _, geometry := range c.Geometry {
		geometryBytes += len(geometry.Source.Source)
		if geometry.Key == "" || len(geometry.Key) > 4096 || geometryKeys[geometry.Key] || geometryBytes > 512<<20 {
			return p.ErrInvalid
		}
		if geometry.Identity.Key != "" && (geometry.Identity.Key != geometry.Key || geometry.Identity.PhysicsFingerprint != geometry.Source.Fingerprint) {
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
	for _, a := range c.Attachments {
		body, ok := bodyIDs[a.BodyID]
		if !inSpace(a.EntityID) || bindings[a.EntityID] || boundBodies[a.BodyID] || !ok || body.Owner != a.EntityID || !p.Finite(a.Config.MaxDriveForce) || a.Config.MaxDriveForce < 0 || !p.Finite(a.Config.Placement.MaxPenetration) || a.Config.Placement.MaxPenetration < 0 {
			return p.ErrInvalid
		}
		bindings[a.EntityID], boundBodies[a.BodyID] = true, true
	}
	if err := validateTrajectoryCheckpoints(c.Trajectories, c.Attachments, bodyIDs, c.Navigation != nil); err != nil {
		return err
	}
	if len(c.Trajectories) > 0 && !c.Physics.Config.Continuous {
		return p.ErrUnsupported
	}
	for _, a := range c.NavigationAgents {
		// A bound body requires staged navigation, and a staged agent requires a
		// body: the space's physics step faults on a staged agent without one.
		if !inSpace(a.EntityID) || a.Profile == "" || len(a.Profile) > 256 || navAgents[a.EntityID].EntityID != 0 || bindings[a.EntityID] != a.StagedPhysics {
			return p.ErrInvalid
		}
		navAgents[a.EntityID] = a
	}
	if c.AcceptedNavigation != nil {
		for _, intent := range c.AcceptedNavigation.Agents {
			if navAgents[intent.EntityID].Profile != intent.Profile {
				return p.ErrInvalid
			}
		}
	}
	if c.Navigation == nil {
		if len(c.NavigationAgents) != 0 {
			return p.ErrInvalid
		}
	} else {
		if c.Navigation.CheckpointID != checkpointID {
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

// DecodePhysicsCheckpoint accepts versions 1 and 2 and rejects unknown fields,
// oversized input, trailing data and obsolete embedded physics versions (of
// every space section) requiring explicit state migration.
func DecodePhysicsCheckpoint(data []byte) (PhysicsCheckpoint, error) {
	var c PhysicsCheckpoint
	if len(data) > maxPhysicsCheckpointBytes {
		return c, p.ErrInvalid
	}
	type physicsHeader struct {
		Version uint32 `json:"version"`
	}
	var header struct {
		Version uint32
		Physics physicsHeader
		Spaces  []struct{ Physics physicsHeader }
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return c, err
	}
	if header.Version == 2 {
		for _, section := range header.Spaces {
			if section.Physics.Version != p.CheckpointVersion {
				return c, (p.Checkpoint{Version: section.Physics.Version}).Validate()
			}
		}
	} else if header.Physics.Version != p.CheckpointVersion {
		return c, (p.Checkpoint{Version: header.Physics.Version}).Validate()
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
// PendingWorld checkpoints instead require PreparePendingWorld to reconstruct
// matching desired physics and navigation privately from both captured states.
//
// The fingerprints and navigation/pending hooks describe space 0; Spaces holds
// the same for every other space in the checkpoint (key 0 is rejected). For a
// version-2 checkpoint, space 0's PreparePendingWorld receives a version-1-shaped
// view holding space 0's section in the top-level fields and every entity.
type PhysicsRestoreOptions struct {
	SchemaFingerprint, PhysicsFingerprint, NavigationFingerprint string
	Entity                                                       func(snapshot.Record) (Entity, error)
	NavigationConfig                                             func(PhysicsNavigationCheckpoint) (NavigationAgentConfig, error)
	PrepareNavigation                                            func(NavigationCheckpoint) (NavigationBuildResult, error)
	PreparePendingWorld                                          func(PhysicsCheckpoint) (PreparedPhysicsRecovery, error)
	// Spaces holds the restore options of the spaces other than 0, by space.
	Spaces map[SpaceID]PhysicsSpaceRestoreOptions
}

// PhysicsSpaceRestoreOptions supplies one non-zero space's expected source
// fingerprints and navigation/pending-world hooks; the fields mean what the
// PhysicsRestoreOptions fields of the same name mean for space 0.
// PreparePendingWorld receives the whole checkpoint and the space's section and
// returns that space's private pair.
type PhysicsSpaceRestoreOptions struct {
	PhysicsFingerprint, NavigationFingerprint string
	NavigationConfig                          func(PhysicsNavigationCheckpoint) (NavigationAgentConfig, error)
	PrepareNavigation                         func(NavigationCheckpoint) (NavigationBuildResult, error)
	PreparePendingWorld                       func(PhysicsCheckpoint, PhysicsSpaceCheckpoint) (PreparedPhysicsRecovery, error)
}

// space returns the restore options of one space.
func (o PhysicsRestoreOptions) space(id SpaceID) (PhysicsSpaceRestoreOptions, bool) {
	if id != 0 {
		options, ok := o.Spaces[id]
		return options, ok
	}
	out := PhysicsSpaceRestoreOptions{PhysicsFingerprint: o.PhysicsFingerprint, NavigationFingerprint: o.NavigationFingerprint,
		NavigationConfig: o.NavigationConfig, PrepareNavigation: o.PrepareNavigation}
	if prepare := o.PreparePendingWorld; prepare != nil {
		out.PreparePendingWorld = func(c PhysicsCheckpoint, section PhysicsSpaceCheckpoint) (PreparedPhysicsRecovery, error) {
			return prepare(c.spaceZeroView(section))
		}
	}
	return out, true
}

// PhysicsRestored is an optional explicit recovery hook, called after complete
// state publication and readiness. Restore never calls ordinary OnSpawn/OnRemove.
type PhysicsRestored interface{ OnPhysicsRestored(*Server) }

// physicsSpaceRestore is one space's private restoration candidate.
type physicsSpaceRestore struct {
	rt                     *spaceRuntime
	section                PhysicsSpaceCheckpoint
	options                PhysicsSpaceRestoreOptions
	priorPhysicsRestoring  bool
	priorNavRestoring      bool
	prepared               p.PreparedRestore
	navigation             NavigationBuildResult
	navigationCommitted    bool
	geometry               map[string]*content.Instance
	geometryIdentities     map[string]PhysicsGeometryIdentity
	agents                 map[int64]*physicsAttachment
	bodyOwners             map[p.BodyHandle]int64
	trajectories           map[int64]*physicsTrajectory
	trajectoryGeneration   uint64
	navigationConfigs      map[int64]NavigationAgentConfig
	navigationFrameReplays []func()
}

// RestorePhysicsCheckpoint privately validates entities, bindings, physics and
// navigation before publication. Call at startup or on a quiescent tick with no
// connected clients, and stop accepting new connections for the whole operation.
// Live client revision rewind requires an explicit reconnect. Ephemeral interest,
// visibility, avatar/session membership and session spaces (announced and
// SetSessionSpace) are reset; rebuild them in recovery hooks.
//
// The checkpoint must hold a section for exactly the spaces that have physics
// or surface navigation on this server, each with a physics world and the same
// navigation presence. Every space is prepared privately first; then every
// space's navigation and physics commit in ascending space order. A
// preparation failure (or a failure of the very first commit) leaves accepted
// entities and physics of every space untouched. A failure after any commit
// faults every space and keeps physics and navigation readiness closed in all
// of them until a coherent retry succeeds. Entities are restored in their
// recorded spaces, with schema colliders on those spaces' layers; every
// collider removal and registration is checked (layer defined, helper bound)
// before the first commit, so a collider that cannot register fails the
// restore with nothing changed. A server without physics, or with a serviced
// space without physics, gets the same configuration error as
// CapturePhysicsCheckpoint; ErrBusy reports only a busy or closed service.
func (s *Server) RestorePhysicsCheckpoint(c PhysicsCheckpoint, options PhysicsRestoreOptions) (err error) {
	runtimes, err := s.checkpointRuntimes()
	if err != nil {
		return err
	}
	for _, rt := range runtimes {
		ps := rt.physics
		if ps.closed || ps.stepping || ps.dispatching || ps.restoring && ps.fault == nil {
			return p.ErrBusy
		}
		if rt.navigation != nil && rt.navigation.frame != nil {
			return p.ErrBusy
		}
	}
	if len(s.listener.SessionIDs()) != 0 {
		return errors.New("golem: disconnect clients before physics restoration")
	}
	if err = c.Validate(); err != nil {
		return err
	}
	if options.Entity == nil || c.SchemaFingerprint != options.SchemaFingerprint {
		return errors.New("golem: physics checkpoint schema/source mismatch")
	}
	if _, ok := options.Spaces[0]; ok {
		return errors.New("golem: space 0 restore options belong in the top-level fields")
	}
	sections := c.sections()
	if len(sections) != len(runtimes) {
		return fmt.Errorf("golem: physics checkpoint has %d serviced spaces, server has %d", len(sections), len(runtimes))
	}
	restores := make([]*physicsSpaceRestore, len(runtimes))
	for i, rt := range runtimes {
		section := sections[i]
		if section.Space != rt.id {
			return fmt.Errorf("golem: physics checkpoint serviced spaces differ from the server's (space %d vs %d)", section.Space, rt.id)
		}
		o, ok := options.space(rt.id)
		if !ok {
			return fmt.Errorf("golem: no restore options for space %d", rt.id)
		}
		if section.DesiredPhysicsFingerprint != o.PhysicsFingerprint {
			return errors.New("golem: physics checkpoint schema/source mismatch")
		}
		if (section.Navigation != nil) != (rt.navigation != nil) {
			return errors.New("golem: physics checkpoint navigation configuration mismatch")
		}
		if section.Navigation != nil && (section.Navigation.SourceFingerprint != o.NavigationFingerprint || o.NavigationConfig == nil || !section.PendingWorld && o.PrepareNavigation == nil) {
			return errors.New("golem: navigation restore configuration mismatch")
		}
		if section.PendingWorld && o.PreparePendingWorld == nil {
			return errors.New("golem: pending geometry requires paired private physics/navigation reconstruction")
		}
		if _, ok := rt.physics.world.(p.CheckpointWorld); !ok {
			return p.ErrUnsupported
		}
		restores[i] = &physicsSpaceRestore{rt: rt, section: section, options: o}
	}
	for _, r := range restores {
		r.priorPhysicsRestoring = r.rt.physics.restoring
		r.rt.physics.restoring = true
		if n := r.rt.navigation; n != nil {
			r.priorNavRestoring = n.restoring
			n.restoring = true
		}
	}
	// published: some space committed. completed: the restore is coherent and
	// readiness is open; only replays and recovery hooks remain.
	published, completed := false, false
	// Registered first so it runs last, after the Discard defer below. A panic
	// before completion (a Layers or entity setter, navigation reattachment)
	// counts as a failure: once anything is published, readiness stays closed
	// and every space faults. The panic is then re-raised. A panic in a replay
	// or OnPhysicsRestored hook after completion leaves the coherent restore
	// open.
	defer func() {
		if rec := recover(); rec != nil {
			if err == nil && !completed {
				err = fmt.Errorf("golem: physics restore panicked: %v", rec)
			}
			defer panic(rec)
		}
		for _, r := range restores {
			ps, n := r.rt.physics, r.rt.navigation
			if published && (err != nil || !completed) {
				// Some space already committed: no space may reopen readiness.
				ps.fault, ps.restoring = err, true
				if n != nil {
					n.restoring = true
				}
				continue
			}
			ps.restoring = false
			if err != nil {
				ps.restoring = r.priorPhysicsRestoring
				if n != nil {
					n.restoring = r.priorNavRestoring
				}
			}
		}
	}()
	defer func() {
		for _, r := range restores {
			if r.prepared != nil {
				r.prepared.Discard()
			}
			if r.navigation != nil && !r.navigationCommitted {
				r.navigation.Discard()
			}
		}
	}()
	entities := make(map[int64]Entity, len(c.Entities))
	entries := make([]registry.RestoreEntry, 0, len(c.Entities))
	var maxID int64
	for _, r := range restores {
		if err = r.preparePhysics(c); err != nil {
			return err
		}
	}
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
		entries = append(entries, registry.RestoreEntry{Entity: entity, Owner: record.Owner, Space: record.Record.Space})
		maxID = max(maxID, entity.EntityID())
	}
	for _, r := range restores {
		if err = r.prepareBindings(entities); err != nil {
			return err
		}
	}
	// Schema colliders are re-registered only after the registry and every
	// world have been replaced, where a Layers panic would leave everything
	// half-published. Check every removal and registration first.
	for _, entity := range s.reg.All() {
		if err = s.runtimeOf(entity.EntityID()).checkColliderRemove(entity); err != nil {
			return fmt.Errorf("golem: physics restore: %w", err)
		}
	}
	colliders := make([]colliderAdd, len(entries))
	for i, entry := range entries {
		if colliders[i], err = s.planColliderAdd(s.runtime(entry.Space), entry.Entity); err != nil {
			return fmt.Errorf("golem: physics restore: %w", err)
		}
	}
	for _, r := range restores {
		if r.section.Navigation != nil && r.navigation == nil {
			if r.navigation, err = r.options.PrepareNavigation(*r.section.Navigation); err != nil {
				return err
			}
			if r.navigation == nil {
				return p.ErrInvalid
			}
		}
	}
	tick := max(s.tick, c.Tick)
	for _, r := range restores {
		if r.navigation != nil {
			if err = r.navigation.Commit(); err != nil {
				return err
			}
			r.navigationCommitted = true
			published = true
		}
		if err = r.prepared.Commit(); err != nil {
			return err
		}
		published = true
		tick = max(tick, r.section.Physics.LastFrame)
	}
	for _, r := range restores {
		r.rt.physics.fault = nil
		r.rt.physics.commands, r.rt.physics.pendingDeletes, r.rt.physics.retired = nil, nil, nil
	}
	old := s.reg.All()
	for _, r := range restores {
		n := r.rt.navigation
		if n == nil {
			continue
		}
		// Worker results were prepared against the replaced world. Invalidate every
		// generation before readiness reopens, including completed queued results.
		for key := range n.buildStates {
			n.cancelBuild(key)
		}
		n.frameCallbacks = make([]func(), 0)
		if err = n.beginRestore(); err != nil {
			return err
		}
		for len(n.order) > 0 {
			n.detach(n.order[0])
		}
		r.navigationFrameReplays = n.frameCallbacks
		n.frameCallbacks = nil
		n.framePrepared = false
	}
	for _, entity := range old {
		s.unregisterCollider(entity)
	}
	if err = s.reg.ReplaceWithoutHooks(entries); err != nil {
		return err
	}
	for _, r := range restores {
		ps := r.rt.physics
		ps.agents, ps.bodies = r.agents, r.bodyOwners
		for id, a := range ps.agents {
			a.incarnation, _ = s.reg.Incarnation(id)
		}
		ps.trajectories, ps.trajectoryGeneration = r.trajectories, r.trajectoryGeneration
		ps.geometry = r.geometry
		ps.geometryIdentities, ps.geometryPending = r.geometryIdentities, nil
	}
	s.tick = tick
	s.SetEntityIDCounter(max(maxID, c.EntityCounter, atomic.LoadInt64(&s.idCounter)))
	s.interestMu.Lock()
	if s.interest != nil {
		s.interest = interest.NewManager(s.config.CellSize)
	}
	s.visibility = visibility.NewManager()
	s.broadcastKnown = make(map[int64]map[int64]struct{})
	s.ownershipRefresh = nil
	s.sentSpace = make(map[int64]SpaceID)
	s.sessionSpaceExplicit = make(map[int64]SpaceID)
	s.spaceTransitionSeq = make(map[int64]uint64)
	s.spaceAckPending = make(map[int64]uint64)
	s.spaceAnnouncing = make(map[int64]SpaceID)
	s.foiOrphanKnown = make(map[int64]map[int64]uint64)
	s.interestMu.Unlock()
	s.avatarMu.Lock()
	s.sessionAvatar = map[int64]int64{}
	s.avatarSession = map[int64]int64{}
	s.avatarMu.Unlock()
	s.eventualTrackers = nil
	for i, entry := range entries {
		s.runtime(entry.Space).applyColliderAdd(entry.Entity.EntityID(), colliders[i])
	}
	for _, r := range restores {
		ps := r.rt.physics
		for _, binding := range r.section.Attachments {
			state, e := ps.world.Body(r.agents[binding.EntityID].body)
			if e != nil {
				return e
			}
			ps.write(r.agents[binding.EntityID], state, true)
		}
	}
	for _, r := range restores {
		for _, saved := range r.section.NavigationAgents {
			if err = s.attachNavigation(r.rt, saved.EntityID, r.navigationConfigs[saved.EntityID]); err != nil {
				return err
			}
		}
	}
	for _, r := range restores {
		if r.section.Navigation != nil {
			if err = s.restoreNavigationIntents(r.rt, *r.section.Navigation, r.options.NavigationFingerprint); err != nil {
				return err
			}
		}
	}
	for _, r := range restores {
		if r.section.Navigation != nil {
			if err = r.rt.navigation.completeRestore(nil); err != nil {
				return err
			}
		}
	}
	for _, r := range restores {
		r.rt.physics.restoring = false
	}
	completed = true
	for _, r := range restores {
		for _, callback := range r.navigationFrameReplays {
			callback()
		}
	}
	for _, entry := range entries {
		if restored, ok := entry.Entity.(PhysicsRestored); ok {
			restored.OnPhysicsRestored(s)
		}
	}
	return nil
}

// preparePhysics privately prepares the space's physical world (or its pending
// pair) and source leases.
func (r *physicsSpaceRestore) preparePhysics(c PhysicsCheckpoint) error {
	ps := r.rt.physics
	savedGeometry := r.section.Geometry
	if r.section.PendingWorld {
		pair, e := r.options.PreparePendingWorld(c, r.section)
		if e != nil {
			pair.Discard()
			return e
		}
		if e = pair.validate(c.CheckpointID, r.section); e != nil {
			pair.Discard()
			return e
		}
		r.prepared, r.navigation, savedGeometry = pair.Physics, pair.Navigation, pair.Geometry
	} else {
		prepared, e := ps.world.(p.CheckpointWorld).PrepareRestore(r.section.Physics)
		if e != nil {
			return e
		}
		r.prepared = prepared
	}
	r.geometry = map[string]*content.Instance{}
	r.geometryIdentities = map[string]PhysicsGeometryIdentity{}
	geometryIDs := [4]map[p.ContentID]bool{{}, {}, {}, {}}
	for _, saved := range savedGeometry {
		instance, e := content.RestoreInstance(ps.world, saved.Source, r.prepared)
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
		r.geometry[saved.Key] = instance
		if saved.Identity.Key != "" {
			r.geometryIdentities[saved.Key] = saved.Identity
		}
	}
	return nil
}

// prepareBindings privately binds the space's restored entities to prepared
// bodies, trajectories and navigation configurations.
func (r *physicsSpaceRestore) prepareBindings(entities map[int64]Entity) (err error) {
	c, prepared := r.section, r.prepared
	r.agents = map[int64]*physicsAttachment{}
	r.bodyOwners = map[p.BodyHandle]int64{}
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
		r.agents[a.id], r.bodyOwners[a.body] = a, a.id
	}
	r.trajectories, r.trajectoryGeneration, err = r.rt.physics.restoreTrajectoryCandidates(c.Trajectories, r.agents, c.Attachments, prepared)
	if err != nil {
		return err
	}
	r.navigationConfigs = map[int64]NavigationAgentConfig{}
	for _, saved := range c.NavigationAgents {
		cfg, e := r.options.NavigationConfig(saved)
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
		if a := r.agents[saved.EntityID]; a != nil {
			state, _ := prepared.Body(bodyIDFor(c.Attachments, saved.EntityID))
			if !physicsNavigationMode(state.Mode) {
				return p.ErrInvalid
			}
		}
		r.navigationConfigs[saved.EntityID] = cfg
	}
	if c.Navigation != nil {
		for _, intent := range c.Navigation.Agents {
			if a := r.agents[intent.EntityID]; a != nil {
				state, _ := prepared.Body(bodyIDFor(c.Attachments, intent.EntityID))
				if state.Mode == p.FreeDynamic {
					return errors.New("golem: restored free body has drive intent")
				}
			}
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
