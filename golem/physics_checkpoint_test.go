package golem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

func TestPhysicsCheckpointRejectsRetiredSolverSchema(t *testing.T) {
	legacy := []byte(`{"Physics":{"Config":{"CCDMaxDelta":0.004166667},"version":1},"Version":1}`)
	if _, err := DecodePhysicsCheckpoint(legacy); !errors.Is(err, p.ErrUnsupported) || !strings.Contains(err.Error(), "retired global motion-refinement") {
		t.Fatalf("obsolete solver settings were not explicitly rejected: %v", err)
	}
}

type physicsCheckpointTestWorld struct {
	*physicsTestWorld
	prepareErr, commitErr, trajectoryErr error
	discards                             int
}

func (w *physicsCheckpointTestWorld) CaptureCheckpoint(id, fingerprint string) (p.Checkpoint, error) {
	c := p.Checkpoint{Version: p.CheckpointVersion, ID: id, SourceFingerprint: fingerprint, Revision: 1, LastFrame: w.pending.ID, Config: p.DefaultConfig()}
	for _, state := range w.bodies {
		d := p.DefaultBody(state.Mode)
		d.ID, d.Owner, d.Pose = state.ID, state.Owner, state.Pose
		d.Anchor = state.Pose.Inverse().Compose(state.Anchor)
		d.LinearVelocity, d.AngularVelocity = state.LinearVelocity, state.AngularVelocity
		d.Enabled, d.Awake = state.Enabled, state.Awake
		c.Bodies = append(c.Bodies, p.CheckpointBody{Description: d})
	}
	return c, c.Validate()
}
func (w *physicsCheckpointTestWorld) PrepareRestore(c p.Checkpoint) (p.PreparedRestore, error) {
	if w.prepareErr != nil {
		return nil, w.prepareErr
	}
	r := &physicsCheckpointTestRestore{world: w, states: map[p.BodyHandle]p.BodyState{}, ids: map[p.ContentID]p.BodyHandle{}}
	for i, body := range c.Bodies {
		d := body.Description
		h := p.BodyHandle{World: 42, Index: uint32(i + 1), Generation: 1}
		r.states[h] = p.BodyState{Handle: h, ID: d.ID, Owner: d.Owner, Mode: d.Mode, Pose: d.Pose, Anchor: d.Pose.Compose(d.Anchor), LinearVelocity: d.LinearVelocity, AngularVelocity: d.AngularVelocity, Enabled: d.Enabled, Awake: d.Awake}
		r.ids[d.ID] = h
	}
	return r, nil
}

type physicsCheckpointTestRestore struct {
	world  *physicsCheckpointTestWorld
	states map[p.BodyHandle]p.BodyState
	ids    map[p.ContentID]p.BodyHandle
	closed bool
}

func (*physicsCheckpointTestRestore) SupportsPoseMotion() bool { return true }
func (r *physicsCheckpointTestRestore) ValidatePoseMotion(p.BodyHandle, []p.PoseMotionSegment) error {
	return r.world.trajectoryErr
}

func (r *physicsCheckpointTestRestore) Body(id p.ContentID) (p.BodyState, bool) {
	h, ok := r.ids[id]
	return r.states[h], ok && !r.closed
}
func (r *physicsCheckpointTestRestore) FindBody(id p.ContentID) (p.BodyState, bool) {
	return r.Body(id)
}
func (*physicsCheckpointTestRestore) FindShape(p.ContentID) (p.ShapeHandle, bool) {
	return p.ShapeHandle{}, false
}
func (*physicsCheckpointTestRestore) FindCollider(p.ContentID) (p.ColliderHandle, bool) {
	return p.ColliderHandle{}, false
}
func (*physicsCheckpointTestRestore) FindJoint(p.ContentID) (p.JointHandle, bool) {
	return p.JointHandle{}, false
}
func (r *physicsCheckpointTestRestore) Commit() error {
	if r.world.commitErr != nil {
		return r.world.commitErr
	}
	if r.closed {
		return p.ErrStale
	}
	r.world.bodies = r.states
	r.closed = true
	return nil
}
func (r *physicsCheckpointTestRestore) Discard() {
	if !r.closed {
		r.world.discards++
		r.closed = true
	}
}

type physicsCheckpointEntity struct {
	physicsBoundEntity
	spawned, removed int
	restored         func(*Server)
}

func (e *physicsCheckpointEntity) FullUpdate() ([]byte, error) { return json.Marshal(e.position) }
func (e *physicsCheckpointEntity) OnSpawn()                    { e.spawned++ }
func (e *physicsCheckpointEntity) OnRemove()                   { e.removed++ }
func (e *physicsCheckpointEntity) OnPhysicsRestored(s *Server) {
	if e.restored != nil {
		e.restored(s)
	}
}

type physicsCheckpointNavigation struct {
	commit    func() error
	discarded bool
}

func (b *physicsCheckpointNavigation) Commit() error {
	if b.commit != nil {
		return b.commit()
	}
	return nil
}
func (b *physicsCheckpointNavigation) Discard() { b.discarded = true }

func physicsCheckpointFixture(t *testing.T) (*Server, *physicsCheckpointTestWorld, *physicsCheckpointEntity, PhysicsCheckpoint) {
	t.Helper()
	s := NewServer(ServerConfig{})
	w := &physicsCheckpointTestWorld{physicsTestWorld: &physicsTestWorld{}}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	e := &physicsCheckpointEntity{physicsBoundEntity: physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: 1, position: surface.Vec3{X: 2}}}}
	if err := s.CreateEntity(e, 7); err != nil {
		t.Fatal(err)
	}
	h := physicsTestAttach(t, s, w.physicsTestWorld, 1, p.DrivenCharacter, PhysicsAttachmentConfig{BindActivation: true})
	state := w.bodies[h]
	state.ID = "character"
	w.bodies[h] = state
	cfg := NavigationAgentConfig{Profile: "human", Speed: 2, StagedPhysics: true, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}
	if err := s.AttachNavigation(1, cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
		t.Fatal(err)
	}
	s.tick = 10
	s.SetEntityIDCounter(100)
	c, err := s.CapturePhysicsCheckpoint(PhysicsCheckpointOptions{ID: "paired", SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav",
		DesiredPhysicsRevision: 1, DesiredNavigationRevision: 1, DesiredPhysicsWorld: []byte(`{"physics":"desired"}`), DesiredNavigationWorld: []byte(`{"nav":"desired"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return s, w, e, c
}

func physicsCheckpointOptions(t *testing.T, s *Server, restored func(*Server)) PhysicsRestoreOptions {
	t.Helper()
	cfg := s.navigation.agents[1].config
	return PhysicsRestoreOptions{SchemaFingerprint: "schema", PhysicsFingerprint: "physics", NavigationFingerprint: "nav",
		Entity: func(r snapshot.Record) (Entity, error) {
			if !s.physics.restoring || !s.navigation.restoring {
				t.Error("restore factory ran with readiness open")
			}
			e := &physicsCheckpointEntity{physicsBoundEntity: physicsBoundEntity{navigationTestEntity: navigationTestEntity{id: r.EntityID}}, restored: restored}
			if err := json.Unmarshal(r.StateData, &e.position); err != nil {
				return nil, err
			}
			return e, nil
		}, NavigationConfig: func(PhysicsNavigationCheckpoint) (NavigationAgentConfig, error) { return cfg, nil },
		PrepareNavigation: func(NavigationCheckpoint) (NavigationBuildResult, error) { return &physicsCheckpointNavigation{}, nil }}
}

func TestPhysicsCheckpointRestoresPairedIdentityAndSuppressesOrdinaryHooks(t *testing.T) {
	s, w, old, c := physicsCheckpointFixture(t)
	oldHandle := s.physics.agents[1].body
	called := false
	options := physicsCheckpointOptions(t, s, func(server *Server) {
		called = true
		if server.physics.restoring || server.navigation.restoring {
			t.Error("recovery callback preceded readiness")
		}
		state, err := server.PhysicsBody(1)
		intent, _ := server.NavigationAgentState(1)
		if err != nil || state.Handle == oldHandle || state.ID != "character" || !intent.HasDestination || intent.Destination.X != 10 {
			t.Error("recovery callback observed unpaired state")
		}
	})
	build := &physicsCheckpointNavigation{}
	options.PrepareNavigation = func(NavigationCheckpoint) (NavigationBuildResult, error) { return build, nil }
	if err := s.RestorePhysicsCheckpoint(c, options); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.Get(1)
	e := restored.(*physicsCheckpointEntity)
	if !called || e.spawned != 0 || old.removed != 0 || e.position.X != 2 || !e.active || build.discarded {
		t.Fatal("restore used ordinary lifecycle hooks or discarded published navigation")
	}
	if owner, ok := s.Owner(1); !ok || owner != 7 {
		t.Fatal("authoritative command ownership was lost")
	}
	if _, err := w.Body(oldHandle); !errors.Is(err, p.ErrStale) {
		t.Fatal("old body still resolves")
	}
	if s.nextID() != 101 {
		t.Fatal("checkpoint lost previously allocated entity IDs")
	}
}

func TestPhysicsCheckpointPreparationFailureLeavesAcceptedState(t *testing.T) {
	for _, failure := range []string{"entity", "world", "navigation", "body-commit"} {
		t.Run(failure, func(t *testing.T) {
			s, w, old, c := physicsCheckpointFixture(t)
			oldHandle := s.physics.agents[1].body
			options := physicsCheckpointOptions(t, s, nil)
			sentinel := errors.New("prepare failed")
			switch failure {
			case "entity":
				options.Entity = func(snapshot.Record) (Entity, error) { return nil, sentinel }
			case "world":
				w.prepareErr = sentinel
			case "navigation":
				options.PrepareNavigation = func(NavigationCheckpoint) (NavigationBuildResult, error) {
					return &physicsCheckpointNavigation{commit: func() error { return sentinel }}, nil
				}
			case "body-commit":
				w.commitErr = sentinel
			}
			if err := s.RestorePhysicsCheckpoint(c, options); !errors.Is(err, sentinel) {
				t.Fatalf("error=%v", err)
			}
			if got, _ := s.Get(1); got != old {
				t.Fatal("failed restoration replaced entities")
			}
			if _, err := w.Body(oldHandle); err != nil {
				t.Fatal("failed restoration replaced physics")
			}
			if failure == "body-commit" {
				if !s.physics.restoring || s.physics.fault == nil || !s.navigation.restoring {
					t.Fatal("late failure reopened mixed world readiness")
				}
			} else if s.physics.restoring || s.navigation.restoring {
				t.Fatal("private prepare failure changed readiness")
			}
		})
	}
}

func TestPhysicsCheckpointManifestPublishesLastAndRejectsTampering(t *testing.T) {
	_, _, _, c := physicsCheckpointFixture(t)
	directory := t.TempDir()
	if err := SavePhysicsCheckpoint(directory, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPhysicsCheckpoint(directory)
	if err != nil || got.CheckpointID != c.CheckpointID {
		t.Fatalf("checkpoint=%+v err=%v", got, err)
	}
	invalid := c
	invalid.Navigation = nil
	if err := SavePhysicsCheckpoint(directory, invalid); err == nil {
		t.Fatal("incomplete pair was published")
	}
	if got, err := LoadPhysicsCheckpoint(directory); err != nil || got.CheckpointID != c.CheckpointID {
		t.Fatal("failed save replaced previous manifest")
	}
	data, err := os.ReadFile(filepath.Join(directory, "physics-checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest physicsCheckpointManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, manifest.File), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadPhysicsCheckpoint(directory); err == nil {
		t.Fatal("tampered checkpoint passed digest verification")
	}
}

func TestPhysicsCheckpointRetriesWholePairAfterLateCommitFailure(t *testing.T) {
	s, w, old, c := physicsCheckpointFixture(t)
	options := physicsCheckpointOptions(t, s, nil)
	w.commitErr = errors.New("late commit failed")
	if err := s.RestorePhysicsCheckpoint(c, options); err == nil {
		t.Fatal("late failure was hidden")
	}
	if ready, restoring, err := s.PhysicsReady(); ready || !restoring || err == nil {
		t.Fatal("late failure reopened readiness")
	}
	w.commitErr = nil
	if err := s.RestorePhysicsCheckpoint(c, options); err != nil {
		t.Fatal(err)
	}
	if ready, restoring, err := s.PhysicsReady(); !ready || restoring || err != nil {
		t.Fatalf("coherent retry did not reopen readiness: %v/%v/%v", ready, restoring, err)
	}
	if restored, _ := s.Get(1); restored == old || old.removed != 0 {
		t.Fatal("retry failed to replace entities without ordinary hooks")
	}
}

func TestPhysicsCheckpointRejectsOverlappingRestoredSourceOwnership(t *testing.T) {
	s, _, old, c := physicsCheckpointFixture(t)
	source := physicsGeometrySource(t, 1).Source()
	source.Bodies[0].ID = "character"
	source.Colliders = nil
	prepared, err := content.Prepare(source, content.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	state := content.InstanceState{Fingerprint: prepared.Fingerprint(), Source: prepared.SourceBytes(), Settings: content.DefaultSettings()}
	c.Geometry = []PhysicsGeometryCheckpoint{{Key: "first", Source: state}, {Key: "second", Source: state}}
	if err = s.RestorePhysicsCheckpoint(c, physicsCheckpointOptions(t, s, nil)); err == nil {
		t.Fatal("two leases acquired the same restored body")
	}
	if got, _ := s.Get(1); got != old {
		t.Fatal("ownership rejection changed live entities")
	}
}

func TestPhysicsCheckpointPendingCaptureRequiresAcceptedStateAndRestoresDesiredPair(t *testing.T) {
	s, w, old, _ := physicsCheckpointFixture(t)
	s.navigation.buildStates["physics/wall"] = NavigationBuildState{Pending: true}
	s.physics.geometryPending = map[string]PhysicsGeometryPending{"wall": {Identity: PhysicsGeometryIdentity{Key: "wall", PhysicsFingerprint: "wall-source", NavigationFingerprint: "nav-new", Revision: 2}, Profiles: []surface.ProfileID{"human"}, Bounds: NavigationTraversalBounds{Min: surface.Vec3{X: -1, Y: -1, Z: -1}, Max: surface.Vec3{X: 6, Y: 6, Z: 6}}, Activated: true}}
	options := PhysicsCheckpointOptions{ID: "pending", SchemaFingerprint: "schema", PhysicsFingerprint: "physics-new", NavigationFingerprint: "nav-new", DesiredPhysicsRevision: 2, DesiredNavigationRevision: 2, DesiredPhysicsWorld: []byte(`{"wall":"new"}`), DesiredNavigationWorld: []byte(`{"nav":"new"}`)}
	if _, err := s.CapturePhysicsCheckpoint(options); !errors.Is(err, p.ErrBusy) {
		t.Fatalf("unpaired pending capture admitted: %v", err)
	}
	options.AcceptedPhysicsFingerprint, options.AcceptedNavigationFingerprint = "physics-accepted", "nav-accepted"
	options.AcceptedNavigationRevision, options.AcceptedNavigationWorld = 1, []byte(`{"nav":"accepted"}`)
	c, err := s.CapturePhysicsCheckpoint(options)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePhysicsCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	c, err = DecodePhysicsCheckpoint(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !c.PendingWorld || c.Physics.SourceFingerprint != "physics-accepted" || c.DesiredPhysicsFingerprint != "physics-new" || c.AcceptedNavigation.SourceFingerprint != "nav-accepted" || len(c.PendingGeometry) != 1 || !c.PendingGeometry[0].Activated {
		t.Fatal("accepted/desired captures or pending change identity were conflated")
	}
	restore := physicsCheckpointOptions(t, s, nil)
	restore.PhysicsFingerprint, restore.NavigationFingerprint = "physics-new", "nav-new"
	if err = s.RestorePhysicsCheckpoint(c, restore); err == nil || !strings.Contains(err.Error(), "paired private") {
		t.Fatal("pending restore admitted an ordinary newer-nav/old-physics pair")
	}
	var published bool
	restore.PreparePendingWorld = func(saved PhysicsCheckpoint) (PreparedPhysicsRecovery, error) {
		if !s.physics.restoring || !s.navigation.restoring || saved.AcceptedNavigation.DesiredRevision != 1 {
			t.Error("private pending reconstruction lost accepted state/barrier")
		}
		desired := saved.Physics
		desired.SourceFingerprint = "physics-new"
		body := p.DefaultBody(p.Static)
		body.ID = "new-wall"
		body.Pose.Position.X = 5
		desired.Bodies = append(append([]p.CheckpointBody(nil), desired.Bodies...), p.CheckpointBody{Description: body})
		physical, e := w.PrepareRestore(desired)
		if e != nil {
			return PreparedPhysicsRecovery{}, e
		}
		nav := &physicsCheckpointNavigation{commit: func() error {
			if entity, _ := s.Get(1); entity != old {
				t.Error("entities changed before pair publication")
			}
			for _, state := range w.bodies {
				if state.ID == "new-wall" {
					t.Error("private desired physics leaked before pair commit")
				}
			}
			published = true
			return nil
		}}
		return PreparedPhysicsRecovery{CheckpointID: saved.CheckpointID, PhysicsFingerprint: saved.DesiredPhysicsFingerprint, NavigationFingerprint: saved.Navigation.SourceFingerprint, PhysicsRevision: saved.DesiredPhysicsRevision, NavigationRevision: saved.Navigation.DesiredRevision, Physics: physical, Navigation: nav}, nil
	}
	if err = s.RestorePhysicsCheckpoint(c, restore); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, state := range w.bodies {
		found = found || state.ID == "new-wall"
	}
	state, _ := s.PhysicsBody(1)
	intent, _ := s.NavigationAgentState(1)
	if !published || !found || state.ID != "character" || state.Anchor.Position.X != 2 || !intent.HasDestination || intent.Destination.X != 10 || len(s.PendingPhysicsGeometry()) != 0 {
		t.Fatal("desired pair did not preserve stable dynamic state and intent")
	}
	if s.navigation.buildStates["physics/wall"].Pending || s.physicsWorldPending() {
		t.Fatal("obsolete pre-restore world build remained publishable")
	}
}

func TestPhysicsPendingRecoveryRejectsMismatchedPairBeforePublication(t *testing.T) {
	s, w, old, c := physicsCheckpointFixture(t)
	c.PendingWorld = true
	accepted := *c.Navigation
	c.AcceptedNavigation = &accepted
	options := physicsCheckpointOptions(t, s, nil)
	build := &physicsCheckpointNavigation{commit: func() error { t.Error("mismatched pair published navigation"); return nil }}
	options.PreparePendingWorld = func(saved PhysicsCheckpoint) (PreparedPhysicsRecovery, error) {
		physical, err := w.PrepareRestore(saved.Physics)
		return PreparedPhysicsRecovery{CheckpointID: saved.CheckpointID, PhysicsFingerprint: "wrong-source", NavigationFingerprint: saved.Navigation.SourceFingerprint, PhysicsRevision: saved.DesiredPhysicsRevision, NavigationRevision: saved.Navigation.DesiredRevision, Physics: physical, Navigation: build}, err
	}
	if err := s.RestorePhysicsCheckpoint(c, options); !errors.Is(err, p.ErrInvalid) {
		t.Fatalf("mismatched pair accepted: %v", err)
	}
	if entity, _ := s.Get(1); entity != old || !build.discarded || w.discards != 1 || s.physics.restoring || s.navigation.restoring {
		t.Fatal("mismatched private pair changed accepted state or leaked candidates")
	}
}
