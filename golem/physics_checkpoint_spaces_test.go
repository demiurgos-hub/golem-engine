package golem

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/golem-engine/golem/registry"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

const physicsCheckpointV1Golden = "testdata/physics_checkpoint_v1.golden.json"

// TestPhysicsCheckpointSpaceZeroEncodingIsByteIdentical pins the version-1
// encoding of a space-0-only paired checkpoint, including an entity recorded in
// a space without services. The golden file was written before multi-space
// checkpoints existed; set GOLEM_UPDATE_GOLDEN=1 only for an intended format change.
func TestPhysicsCheckpointSpaceZeroEncodingIsByteIdentical(t *testing.T) {
	s, _, _, _ := physicsCheckpointFixture(t)
	if err := s.CreateEntityIn(4, &navigationTestEntity{id: 50, position: surface.Vec3{Y: 3}}); err != nil {
		t.Fatal(err)
	}
	c, err := s.CapturePhysicsCheckpoint(PhysicsCheckpointOptions{ID: "paired", SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav",
		DesiredPhysicsRevision: 1, DesiredNavigationRevision: 1, DesiredPhysicsWorld: []byte(`{"physics":"desired"}`), DesiredNavigationWorld: []byte(`{"nav":"desired"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 {
		t.Fatalf("space-0-only checkpoint version = %d", c.Version)
	}
	data, err := EncodePhysicsCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GOLEM_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(physicsCheckpointV1Golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(physicsCheckpointV1Golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(physicsCheckpointV1Golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, golden) {
		t.Fatalf("space-0-only checkpoint encoding changed:\n got %s\nwant %s", data, golden)
	}
	decoded, err := DecodePhysicsCheckpoint(golden)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodePhysicsCheckpoint(decoded)
	if err != nil || !bytes.Equal(again, golden) {
		t.Fatalf("golden checkpoint did not round trip: %v", err)
	}
}

// multiSpaceCheckpoint is a server with physics and surface navigation in
// spaces 0 and 3, an attached navigating character and a scripted body with an
// active trajectory in each, a static "door" body in each world (same content
// IDs in both worlds), a collider entity in space 3 and a plain entity in
// service-less space 5.
type multiSpaceCheckpoint struct {
	s        *Server
	w0, w3   *physicsCheckpointTestWorld
	b0, b3   *recordingBackend2D
	tokens   map[int64]PhysicsTrajectoryToken
	original map[int64]Entity
}

var multiSpaceNavigationConfig = NavigationAgentConfig{Profile: "human", Speed: 2, StagedPhysics: true, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}

func multiSpaceAttach(t *testing.T, s *Server, w *physicsCheckpointTestWorld, world uint64, id int64, mode p.MotionMode, body p.ContentID, cfg PhysicsAttachmentConfig) {
	t.Helper()
	e, _ := s.Get(id)
	x, y, z := e.(registry.Spatial3DEntity).Position3D()
	h := p.BodyHandle{World: world, Index: uint32(id), Generation: 1}
	state := p.BodyState{Handle: h, ID: body, Owner: id, Mode: mode, Pose: p.Identity(), Anchor: p.Identity(), Mass: 1, Enabled: true, Awake: true}
	state.Pose.Position, state.Anchor.Position = p.Vec3{X: x, Y: y, Z: z}, p.Vec3{X: x, Y: y, Z: z}
	w.bodies[h] = state
	if err := s.AttachPhysics(id, h, cfg); err != nil {
		t.Fatal(err)
	}
}

func multiSpaceDoor(w *physicsCheckpointTestWorld, world uint64) {
	h := p.BodyHandle{World: world, Index: 100, Generation: 1}
	pose := p.Identity()
	pose.Position.Z = 3
	w.bodies[h] = p.BodyState{Handle: h, ID: "door", Mode: p.Static, Pose: pose, Anchor: pose, Enabled: true}
}

// multiSpaceDoorLease is a source lease over the door body, as a live lease
// capture would have recorded it.
func multiSpaceDoorLease(t *testing.T) []PhysicsGeometryCheckpoint {
	t.Helper()
	source := physicsGeometrySource(t, 1).Source()
	source.Bodies[0].ID = "door"
	source.Colliders = nil
	prepared, err := content.Prepare(source, content.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	return []PhysicsGeometryCheckpoint{{Key: "door", Source: content.InstanceState{Fingerprint: prepared.Fingerprint(), Source: prepared.SourceBytes(), Settings: content.DefaultSettings()}}}
}

func newMultiSpaceCheckpoint(t *testing.T) *multiSpaceCheckpoint {
	t.Helper()
	s := NewServer(ServerConfig{})
	f := &multiSpaceCheckpoint{s: s, tokens: map[int64]PhysicsTrajectoryToken{}, original: map[int64]Entity{}}
	f.b0, f.b3, _, _ = twoSpaceRecordingLayers(s)
	f.w0 = &physicsCheckpointTestWorld{physicsTestWorld: &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{}}}
	f.w3 = &physicsCheckpointTestWorld{physicsTestWorld: &physicsTestWorld{bodies: map[p.BodyHandle]p.BodyState{}}, restoreWorld: 43}
	if err := s.SetPhysicsWorld(f.w0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(3).SetPhysicsWorld(f.w3); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(3).SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	type spec struct {
		space    SpaceID
		id       int64
		position surface.Vec3
	}
	for _, e := range []spec{{0, 1, surface.Vec3{X: 2}}, {0, 2, surface.Vec3{X: 1}}, {3, 11, surface.Vec3{X: 3}}, {3, 12, surface.Vec3{X: 1, Y: 1}}, {5, 60, surface.Vec3{Y: 7}}} {
		entity := &physicsCheckpointEntity{physicsBoundEntity: physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: e.id, position: e.position}}}
		if err := s.CreateEntityIn(e.space, entity, 7); err != nil {
			t.Fatal(err)
		}
		f.original[e.id] = entity
	}
	collider := &dualColliderEntity{id: 30}
	if err := s.CreateEntityIn(3, collider); err != nil {
		t.Fatal(err)
	}
	f.original[30] = collider
	for _, a := range []struct {
		w     *physicsCheckpointTestWorld
		world uint64
		ids   [2]int64
	}{{f.w0, 1, [2]int64{1, 2}}, {f.w3, 3, [2]int64{11, 12}}} {
		multiSpaceAttach(t, s, a.w, a.world, a.ids[0], p.DrivenCharacter, "character", PhysicsAttachmentConfig{BindActivation: true})
		multiSpaceAttach(t, s, a.w, a.world, a.ids[1], p.ScriptedAction, "mover", PhysicsAttachmentConfig{})
		multiSpaceDoor(a.w, a.world)
		if err := s.AttachNavigation(a.ids[0], multiSpaceNavigationConfig); err != nil {
			t.Fatal(err)
		}
		request := trajectoryRequest()
		request.SafetyProfile = "human"
		token, err := s.StartPhysicsTrajectory(a.ids[1], request)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[a.ids[1]] = token
	}
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNavigationDestination(11, surface.Vec3{X: -4}); err != nil {
		t.Fatal(err)
	}
	s.tick = 10
	s.SetEntityIDCounter(100)
	return f
}

func (f *multiSpaceCheckpoint) captureOptions() PhysicsCheckpointOptions {
	return PhysicsCheckpointOptions{ID: "multi", SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav",
		DesiredPhysicsRevision: 1, DesiredNavigationRevision: 1, DesiredPhysicsWorld: []byte(`{"space":0}`), DesiredNavigationWorld: []byte(`{"nav":0}`),
		Spaces: map[SpaceID]PhysicsSpaceCheckpointOptions{3: {PhysicsFingerprint: "physics-3", NavigationFingerprint: "nav-3",
			DesiredPhysicsRevision: 4, DesiredNavigationRevision: 5, DesiredPhysicsWorld: []byte(`{"space":3}`), DesiredNavigationWorld: []byte(`{"nav":3}`)}}}
}

// capture takes a version-2 checkpoint and adds the door lease to both sections.
func (f *multiSpaceCheckpoint) capture(t *testing.T) PhysicsCheckpoint {
	t.Helper()
	c, err := f.s.CapturePhysicsCheckpoint(f.captureOptions())
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Spaces {
		c.Spaces[i].Geometry = multiSpaceDoorLease(t)
	}
	if err = c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func multiSpaceRestoreOptions(t *testing.T, s *Server) PhysicsRestoreOptions {
	t.Helper()
	navigationConfig := func(PhysicsNavigationCheckpoint) (NavigationAgentConfig, error) {
		return multiSpaceNavigationConfig, nil
	}
	prepare := func(NavigationCheckpoint) (NavigationBuildResult, error) { return &physicsCheckpointNavigation{}, nil }
	return PhysicsRestoreOptions{SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav",
		Entity: func(r snapshot.Record) (Entity, error) {
			_ = s.eachRuntime(func(rt *spaceRuntime) error {
				if rt.physics != nil && !rt.physics.restoring || rt.navigation != nil && !rt.navigation.restoring {
					t.Errorf("restore factory ran with space %d readiness open", rt.id)
				}
				return nil
			})
			if r.TypeName == "dual" {
				return &dualColliderEntity{id: r.EntityID}, nil
			}
			e := &physicsCheckpointEntity{physicsBoundEntity: physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: r.EntityID}}}
			return e, json.Unmarshal(r.StateData, &e.position)
		},
		NavigationConfig: navigationConfig, PrepareNavigation: prepare,
		Spaces: map[SpaceID]PhysicsSpaceRestoreOptions{3: {PhysicsFingerprint: "physics-3", NavigationFingerprint: "nav-3", NavigationConfig: navigationConfig, PrepareNavigation: prepare}}}
}

// spaceReady reports physics and navigation readiness of one space.
func spaceReady(s *Server, space SpaceID) (physics, physicsRestoring, navigationRestoring bool, fault error) {
	physics, physicsRestoring, fault = s.Space(space).PhysicsReady()
	return physics, physicsRestoring, s.Space(space).NavigationReady().Restoring, fault
}

func TestPhysicsCheckpointCapturesEverySpaceAsVersion2(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	if c.Version != 2 || len(c.Spaces) != 2 || c.Spaces[0].Space != 0 || c.Spaces[1].Space != 3 {
		t.Fatalf("multi-space checkpoint shape: version %d, %d sections", c.Version, len(c.Spaces))
	}
	if !c.topLevelPhysicalZero() {
		t.Fatal("version 2 kept space 0 in the top-level fields")
	}
	spaces := map[int64]SpaceID{}
	for _, e := range c.Entities {
		spaces[e.Record.EntityID] = e.Record.Space
	}
	if len(spaces) != 6 || spaces[11] != 3 || spaces[30] != 3 || spaces[60] != 5 || spaces[1] != 0 {
		t.Fatalf("entity spaces = %v", spaces)
	}
	for i, want := range []struct {
		fingerprint      string
		revision         uint64
		character, mover int64
		destination      float64
	}{{"physics", 1, 1, 2, 10}, {"physics-3", 4, 11, 12, -4}} {
		section := c.Spaces[i]
		if section.Physics.ID != "multi" || section.DesiredPhysicsFingerprint != want.fingerprint || section.DesiredPhysicsRevision != want.revision || section.Navigation.CheckpointID != "multi" {
			t.Fatalf("section %d identity", section.Space)
		}
		if len(section.Attachments) != 2 || section.Attachments[0].EntityID != want.character || section.Attachments[1].EntityID != want.mover {
			t.Fatalf("section %d attachments: %+v", section.Space, section.Attachments)
		}
		if len(section.NavigationAgents) != 1 || section.NavigationAgents[0].EntityID != want.character || len(section.Navigation.Agents) != 1 || section.Navigation.Agents[0].Destination.X != want.destination {
			t.Fatalf("section %d navigation: %+v %+v", section.Space, section.NavigationAgents, section.Navigation.Agents)
		}
		if len(section.Trajectories) != 1 || section.Trajectories[0].EntityID != want.mover {
			t.Fatalf("section %d trajectories: %+v", section.Space, section.Trajectories)
		}
	}
	data, err := EncodePhysicsCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePhysicsCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodePhysicsCheckpoint(decoded)
	if err != nil || !bytes.Equal(again, data) {
		t.Fatalf("version 2 did not round trip: %v", err)
	}
	directory := t.TempDir()
	if err = SavePhysicsCheckpoint(directory, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPhysicsCheckpoint(directory)
	if err != nil || loaded.Version != 2 || len(loaded.Spaces) != 2 {
		t.Fatalf("manifest round trip: %v", err)
	}
}

func TestPhysicsCheckpointCaptureRequiresOptionsAndQuietSpaces(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	options := f.captureOptions()
	options.Spaces = nil
	if _, err := f.s.CapturePhysicsCheckpoint(options); err == nil || !strings.Contains(err.Error(), "space 3 has physics but no checkpoint options") {
		t.Fatalf("capture without space 3 options = %v", err)
	}
	options = f.captureOptions()
	options.Spaces[5] = options.Spaces[3]
	if _, err := f.s.CapturePhysicsCheckpoint(options); err == nil || !strings.Contains(err.Error(), "space 5") {
		t.Fatalf("capture with options for a service-less space = %v", err)
	}
	options = f.captureOptions()
	options.Spaces[0] = options.Spaces[3]
	if _, err := f.s.CapturePhysicsCheckpoint(options); err == nil {
		t.Fatal("capture accepted space 0 options in Spaces")
	}
	if err := f.s.Space(3).QueuePhysicsCommand(func(p.World) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CapturePhysicsCheckpoint(f.captureOptions()); !errors.Is(err, p.ErrBusy) {
		t.Fatalf("capture with queued space 3 commands = %v", err)
	}
	f.s.runtime(3).physics.commands = nil
	if err := f.s.Space(6).SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CapturePhysicsCheckpoint(f.captureOptions()); err == nil || !strings.Contains(err.Error(), "space 6 has surface navigation without physics") {
		t.Fatalf("capture with navigation-only space = %v", err)
	}
}

func TestPhysicsCheckpointRestoresEverySpace(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	s := f.s
	s.interestMu.Lock()
	s.sentSpace[99], s.sessionSpaceExplicit[99], s.spaceAckPending[99] = 3, 3, 1
	s.interestMu.Unlock()
	restoredHook := 0
	options := multiSpaceRestoreOptions(t, s)
	entity := options.Entity
	options.Entity = func(r snapshot.Record) (Entity, error) {
		e, err := entity(r)
		if pe, ok := e.(*physicsCheckpointEntity); ok {
			pe.restored = func(server *Server) {
				restoredHook++
				for _, space := range []SpaceID{0, 3} {
					if ready, _, navRestoring, _ := spaceReady(server, space); !ready || navRestoring {
						t.Errorf("recovery hook ran before space %d readiness", space)
					}
				}
			}
		}
		return e, err
	}
	if err := s.RestorePhysicsCheckpoint(c, options); err != nil {
		t.Fatal(err)
	}
	if restoredHook != 5 {
		t.Fatalf("OnPhysicsRestored ran %d times", restoredHook)
	}
	for id, old := range f.original {
		got, ok := s.Get(id)
		if !ok || got == old {
			t.Fatalf("entity %d was not replaced", id)
		}
		if pe, ok := old.(*physicsCheckpointEntity); ok && (pe.removed != 0 || got.(*physicsCheckpointEntity).spawned != 0) {
			t.Fatalf("entity %d used ordinary lifecycle hooks", id)
		}
	}
	for id, want := range map[int64]SpaceID{1: 0, 2: 0, 11: 3, 12: 3, 30: 3, 60: 5} {
		if space, _ := s.EntitySpace(id); space != want {
			t.Fatalf("entity %d restored in space %d, want %d", id, space, want)
		}
	}
	if owner, _ := s.Owner(11); owner != 7 {
		t.Fatal("space 3 ownership lost")
	}
	for id, world := range map[int64]uint64{1: 42, 2: 42, 11: 43, 12: 43} {
		state, err := s.PhysicsBody(id)
		if err != nil || state.Handle.World != world {
			t.Fatalf("entity %d body %+v (%v), want world %d", id, state.Handle, err, world)
		}
	}
	if s.space0.physics.agents[11] != nil || s.runtime(3).physics.agents[1] != nil || len(s.space0.physics.agents) != 2 || len(s.runtime(3).physics.agents) != 2 {
		t.Fatal("attachments restored in the wrong space")
	}
	for id, x := range map[int64]float64{1: 10, 11: -4} {
		intent, ok := s.NavigationAgentState(id)
		if !ok || !intent.HasDestination || intent.Destination.X != x {
			t.Fatalf("entity %d navigation intent %+v", id, intent)
		}
	}
	if s.space0.navigation.agents[11] != nil || s.runtime(3).navigation.agents[11] == nil {
		t.Fatal("navigation reattached in the wrong space")
	}
	for id, old := range f.tokens {
		state, err := s.PhysicsTrajectoryState(id)
		if err != nil || state.Status != PhysicsTrajectoryRunning || state.Token == old {
			t.Fatalf("entity %d trajectory %+v", id, state)
		}
		if err := s.CancelPhysicsTrajectory(old); !errors.Is(err, p.ErrStale) {
			t.Fatalf("entity %d pre-restore trajectory token stayed valid", id)
		}
	}
	for _, space := range []SpaceID{0, 3} {
		if lease, ok := s.Space(space).PhysicsGeometry("door"); !ok || lease == nil {
			t.Fatalf("space %d door lease missing", space)
		}
		if ready, restoring, navRestoring, err := spaceReady(s, space); !ready || restoring || navRestoring || err != nil {
			t.Fatalf("space %d readiness %v/%v/%v/%v", space, ready, restoring, navRestoring, err)
		}
	}
	if _, ok := f.b3.added[30]; !ok {
		t.Fatal("space 3 collider was not re-registered in space 3")
	}
	if _, ok := f.b0.added[30]; ok {
		t.Fatal("space 3 collider registered in space 0")
	}
	s.interestMu.Lock()
	reset := len(s.sentSpace) == 0 && len(s.sessionSpaceExplicit) == 0 && len(s.spaceAckPending) == 0
	s.interestMu.Unlock()
	if !reset {
		t.Fatal("session spaces survived restoration")
	}
	if s.nextID() != 101 {
		t.Fatal("entity counter moved backward")
	}
}

func TestPhysicsCheckpointRejectsMismatchedServicedSpaces(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	single, _, old, v1 := physicsCheckpointFixture(t)
	if err := single.RestorePhysicsCheckpoint(c, multiSpaceRestoreOptions(t, single)); err == nil || !strings.Contains(err.Error(), "serviced spaces") {
		t.Fatalf("two-space checkpoint restored into a space-0 server: %v", err)
	}
	if got, _ := single.Get(1); got != old {
		t.Fatal("rejected restore changed entities")
	}
	if err := f.s.RestorePhysicsCheckpoint(v1, multiSpaceRestoreOptions(t, f.s)); err == nil || !strings.Contains(err.Error(), "serviced spaces") {
		t.Fatalf("space-0 checkpoint restored into a two-space server: %v", err)
	}
	other := c
	other.Spaces = append([]PhysicsSpaceCheckpoint(nil), c.Spaces...)
	other.Spaces[1].Space = 4
	other.Entities = append([]PhysicsEntityCheckpoint(nil), c.Entities...)
	for i := range other.Entities {
		if other.Entities[i].Record.Space == 3 {
			other.Entities[i].Record.Space = 4
		}
	}
	if err := f.s.RestorePhysicsCheckpoint(other, multiSpaceRestoreOptions(t, f.s)); err == nil || !strings.Contains(err.Error(), "serviced spaces") {
		t.Fatalf("space 4 section restored into space 3: %v", err)
	}
	options := multiSpaceRestoreOptions(t, f.s)
	options.Spaces = nil
	if err := f.s.RestorePhysicsCheckpoint(c, options); err == nil || !strings.Contains(err.Error(), "no restore options for space 3") {
		t.Fatalf("restore without space 3 options: %v", err)
	}
	for id, old := range f.original {
		if got, _ := f.s.Get(id); got != old {
			t.Fatalf("rejected restore replaced entity %d", id)
		}
	}
	for _, space := range []SpaceID{0, 3} {
		if ready, _, navRestoring, _ := spaceReady(f.s, space); !ready || navRestoring {
			t.Fatalf("rejected restore changed space %d readiness", space)
		}
	}
}

func TestPhysicsCheckpointPartialCommitFaultsEverySpace(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	f.w3.commitErr = errors.New("space 3 commit failed")
	if err := f.s.RestorePhysicsCheckpoint(c, multiSpaceRestoreOptions(t, f.s)); !errors.Is(err, f.w3.commitErr) {
		t.Fatalf("partial commit = %v", err)
	}
	for _, space := range []SpaceID{0, 3} {
		if ready, restoring, navRestoring, err := spaceReady(f.s, space); ready || !restoring || !navRestoring || err == nil {
			t.Fatalf("space %d reopened after a partial commit: %v/%v/%v/%v", space, ready, restoring, navRestoring, err)
		}
	}
	for id, old := range f.original {
		if got, _ := f.s.Get(id); got != old {
			t.Fatalf("partial commit replaced entity %d", id)
		}
	}
	f.w3.commitErr = nil
	if err := f.s.RestorePhysicsCheckpoint(c, multiSpaceRestoreOptions(t, f.s)); err != nil {
		t.Fatal(err)
	}
	for _, space := range []SpaceID{0, 3} {
		if ready, restoring, navRestoring, err := spaceReady(f.s, space); !ready || restoring || navRestoring || err != nil {
			t.Fatalf("space %d retry readiness %v/%v/%v/%v", space, ready, restoring, navRestoring, err)
		}
	}
}

func TestPhysicsCheckpointFirstCommitFailureLeavesEverySpace(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	sentinel := errors.New("space 0 navigation commit failed")
	options := multiSpaceRestoreOptions(t, f.s)
	options.PrepareNavigation = func(NavigationCheckpoint) (NavigationBuildResult, error) {
		return &physicsCheckpointNavigation{commit: func() error { return sentinel }}, nil
	}
	space3 := &physicsCheckpointNavigation{}
	spaceOptions := options.Spaces[3]
	spaceOptions.PrepareNavigation = func(NavigationCheckpoint) (NavigationBuildResult, error) { return space3, nil }
	options.Spaces[3] = spaceOptions
	if err := f.s.RestorePhysicsCheckpoint(c, options); !errors.Is(err, sentinel) {
		t.Fatalf("first commit failure = %v", err)
	}
	if !space3.discarded || f.w0.discards != 1 || f.w3.discards != 1 {
		t.Fatalf("unpublished candidates leaked: nav %v, worlds %d/%d", space3.discarded, f.w0.discards, f.w3.discards)
	}
	for _, space := range []SpaceID{0, 3} {
		if ready, restoring, navRestoring, err := spaceReady(f.s, space); !ready || restoring || navRestoring || err != nil {
			t.Fatalf("space %d readiness after unpublished failure %v/%v/%v/%v", space, ready, restoring, navRestoring, err)
		}
	}
	if state, err := f.s.PhysicsBody(11); err != nil || state.Handle.World != 3 {
		t.Fatal("unpublished failure replaced space 3 physics")
	}
}

func TestPhysicsCheckpointPendingWorldPreparesEachSpace(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	s := f.s
	pending := func(rt *spaceRuntime, key string) {
		rt.navigation.buildStates["physics/"+key] = NavigationBuildState{Pending: true}
		rt.physics.geometryPending = map[string]PhysicsGeometryPending{key: {Identity: PhysicsGeometryIdentity{Key: key, PhysicsFingerprint: key + "-source", NavigationFingerprint: "nav-new", Revision: 2}, Profiles: []surface.ProfileID{"human"}, Bounds: NavigationTraversalBounds{Min: surface.Vec3{X: -1, Y: -1, Z: -1}, Max: surface.Vec3{X: 6, Y: 6, Z: 6}}, Activated: true}}
	}
	pending(&s.space0, "wall")
	pending(s.runtime(3), "gate")
	options := f.captureOptions()
	if _, err := s.CapturePhysicsCheckpoint(options); !errors.Is(err, p.ErrBusy) {
		t.Fatalf("pending capture without accepted captures = %v", err)
	}
	options.AcceptedPhysicsFingerprint, options.AcceptedNavigationFingerprint, options.AcceptedNavigationRevision, options.AcceptedNavigationWorld = "physics-accepted", "nav-accepted", 1, []byte(`{"nav":"accepted"}`)
	if _, err := s.CapturePhysicsCheckpoint(options); !errors.Is(err, p.ErrBusy) || !strings.Contains(err.Error(), "space 3") {
		t.Fatalf("pending capture without space 3 accepted captures = %v", err)
	}
	space3 := options.Spaces[3]
	space3.AcceptedPhysicsFingerprint, space3.AcceptedNavigationFingerprint, space3.AcceptedNavigationRevision, space3.AcceptedNavigationWorld = "physics-3-accepted", "nav-3-accepted", 2, []byte(`{"nav":"3-accepted"}`)
	options.Spaces[3] = space3
	c, err := s.CapturePhysicsCheckpoint(options)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Spaces[0].PendingWorld || !c.Spaces[1].PendingWorld || c.Spaces[1].Physics.SourceFingerprint != "physics-3-accepted" || c.Spaces[1].AcceptedNavigation.SourceFingerprint != "nav-3-accepted" || c.Spaces[1].PendingGeometry[0].Identity.Key != "gate" {
		t.Fatal("space 3 accepted and desired captures were conflated")
	}
	pair := func(w *physicsCheckpointTestWorld, checkpointID string, section PhysicsSpaceCheckpoint, revision uint64) (PreparedPhysicsRecovery, error) {
		physical, err := w.PrepareRestore(section.Physics)
		return PreparedPhysicsRecovery{CheckpointID: checkpointID, PhysicsFingerprint: section.DesiredPhysicsFingerprint, NavigationFingerprint: section.Navigation.SourceFingerprint,
			PhysicsRevision: revision, NavigationRevision: section.Navigation.DesiredRevision, Physics: physical, Navigation: &physicsCheckpointNavigation{}}, err
	}
	restore := multiSpaceRestoreOptions(t, s)
	var view PhysicsCheckpoint
	restore.PreparePendingWorld = func(saved PhysicsCheckpoint) (PreparedPhysicsRecovery, error) {
		view = saved
		section := saved.sections()[0]
		return pair(f.w0, saved.CheckpointID, section, section.DesiredPhysicsRevision)
	}
	if err = s.RestorePhysicsCheckpoint(c, restore); err == nil || !strings.Contains(err.Error(), "paired private") {
		t.Fatalf("pending space 3 restored without its pair hook: %v", err)
	}
	revision := uint64(99)
	var seen SpaceID = 99
	spaceRestore := restore.Spaces[3]
	spaceRestore.PreparePendingWorld = func(saved PhysicsCheckpoint, section PhysicsSpaceCheckpoint) (PreparedPhysicsRecovery, error) {
		if !s.runtime(3).physics.restoring || saved.Version != 2 {
			t.Error("space 3 pair prepared outside the barrier or without the whole checkpoint")
		}
		seen = section.Space
		return pair(f.w3, saved.CheckpointID, section, revision)
	}
	restore.Spaces[3] = spaceRestore
	if err = s.RestorePhysicsCheckpoint(c, restore); !errors.Is(err, p.ErrInvalid) {
		t.Fatalf("mismatched space 3 pair accepted: %v", err)
	}
	if seen != 3 || f.w0.discards != 1 || f.w3.discards != 1 {
		t.Fatalf("mismatched pair leaked candidates: space %d, discards %d/%d", seen, f.w0.discards, f.w3.discards)
	}
	for id, old := range f.original {
		if got, _ := s.Get(id); got != old {
			t.Fatalf("mismatched pair replaced entity %d", id)
		}
	}
	revision = 4
	if err = s.RestorePhysicsCheckpoint(c, restore); err != nil {
		t.Fatal(err)
	}
	if view.Version != 1 || len(view.Spaces) != 0 || !view.PendingWorld || view.Physics.ID != "multi" || view.DesiredPhysicsFingerprint != "physics" || len(view.Entities) != len(c.Entities) {
		t.Fatalf("space 0 legacy hook view: version %d, %d sections", view.Version, len(view.Spaces))
	}
	for _, space := range []SpaceID{0, 3} {
		if len(s.Space(space).PendingPhysicsGeometry()) != 0 || s.runtime(space).physicsWorldPending() {
			t.Fatalf("space %d kept pre-restore pending work", space)
		}
	}
	if intent, _ := s.NavigationAgentState(11); !intent.HasDestination || intent.Destination.X != -4 {
		t.Fatal("space 3 intent lost through pending recovery")
	}
}

func TestPhysicsCheckpointDecodeRejectsMalformedVersions(t *testing.T) {
	f := newMultiSpaceCheckpoint(t)
	c := f.capture(t)
	_, _, _, v1 := physicsCheckpointFixture(t)
	clone := func() PhysicsCheckpoint {
		var out PhysicsCheckpoint
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	cases := map[string]func() PhysicsCheckpoint{
		"top-level physics in v2":     func() PhysicsCheckpoint { x := clone(); x.Physics = x.Spaces[0].Physics; return x },
		"top-level attachments in v2": func() PhysicsCheckpoint { x := clone(); x.Attachments = x.Spaces[0].Attachments; return x },
		"unsorted sections":           func() PhysicsCheckpoint { x := clone(); x.Spaces[0], x.Spaces[1] = x.Spaces[1], x.Spaces[0]; return x },
		"duplicate sections":          func() PhysicsCheckpoint { x := clone(); x.Spaces[1].Space = 0; return x },
		"lone space-0 section":        func() PhysicsCheckpoint { x := clone(); x.Spaces = x.Spaces[:1]; return x },
		"no sections":                 func() PhysicsCheckpoint { x := clone(); x.Spaces = nil; return x },
		"section identity":            func() PhysicsCheckpoint { x := clone(); x.Spaces[1].Physics.ID = "other"; return x },
		"section navigation identity": func() PhysicsCheckpoint { x := clone(); x.Spaces[1].Navigation.CheckpointID = "other"; return x },
		"attachment in another space": func() PhysicsCheckpoint {
			x := clone()
			for i := range x.Entities {
				if x.Entities[i].Record.EntityID == 11 {
					x.Entities[i].Record.Space = 5
				}
			}
			return x
		},
		"navigation agent in another section": func() PhysicsCheckpoint {
			x := clone()
			x.Spaces[0].NavigationAgents = append(x.Spaces[0].NavigationAgents, x.Spaces[1].NavigationAgents...)
			return x
		},
		"duplicate body in one world": func() PhysicsCheckpoint {
			x := clone()
			x.Spaces[1].Physics.Bodies = append(x.Spaces[1].Physics.Bodies, x.Spaces[1].Physics.Bodies[0])
			return x
		},
		"version 3":        func() PhysicsCheckpoint { x := clone(); x.Version = 3; return x },
		"v1 with sections": func() PhysicsCheckpoint { x := v1; x.Spaces = c.Spaces; return x },
		"v1 attachment outside space 0": func() PhysicsCheckpoint {
			x := v1
			x.Entities = append([]PhysicsEntityCheckpoint(nil), v1.Entities...)
			x.Entities[0].Record.Space = 2
			return x
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			x := build()
			if err := x.Validate(); err == nil {
				t.Fatal("malformed checkpoint validated")
			}
			if _, err := EncodePhysicsCheckpoint(x); err == nil {
				t.Fatal("malformed checkpoint encoded")
			}
			data, err := json.Marshal(x)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = DecodePhysicsCheckpoint(data); err == nil {
				t.Fatal("malformed checkpoint decoded")
			}
		})
	}
	data, err := EncodePhysicsCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(data, []byte(`"Space":3,`), []byte(`"Space":3,"Extra":1,`), 1)
	if bytes.Equal(unknown, data) {
		t.Fatal("fixture lacks a space 3 section")
	}
	if _, err = DecodePhysicsCheckpoint(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown section field = %v", err)
	}
	retired := bytes.Replace(data, []byte(`"Space":3,"Physics":{"version":2`), []byte(`"Space":3,"Physics":{"version":1`), 1)
	if bytes.Equal(retired, data) {
		t.Fatal("fixture lacks a space 3 physics version")
	}
	if _, err = DecodePhysicsCheckpoint(retired); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("obsolete space 3 physics payload = %v", err)
	}
}
