package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/demiurgos-hub/golem-engine/examples/unity-integration/generated"
	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/content"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

const fixtureRecipe = "unity-integration-v1:floor-40x12:blocker-runtime:kinematic-capsules"

type entityState struct {
	ID           int64   `json:"id"`
	DefinitionID string  `json:"definition_id"`
	Health       int32   `json:"health"`
	X            float32 `json:"x"`
	Y            float32 `json:"y"`
	Z            float32 `json:"z"`
}

type fixtureState struct {
	Mode                 string        `json:"mode"`
	Phase                string        `json:"phase"`
	Entities             []entityState `json:"entities"`
	InitialEntities      []entityState `json:"initial_entities"`
	CheckpointEntities   []entityState `json:"checkpoint_entities"`
	MotionPassed         bool          `json:"motion_passed"`
	QueryPassed          bool          `json:"query_passed"`
	PoseEqualityPassed   bool          `json:"pose_equality_passed"`
	CheckpointReady      bool          `json:"checkpoint_ready"`
	CheckpointPath       string        `json:"checkpoint_path"`
	RestoredCheckpointID string        `json:"restored_checkpoint_id"`
	ContentHash          string        `json:"content_hash"`
	Tick                 uint64        `json:"tick"`
	Error                string        `json:"error"`
}

type monster struct {
	*generated.SyncedMonster
	definition       generated.ContentMonster
	spawns, restores int
}

// OnSpawn records ordinary creation after the complete instance setup.
func (m *monster) OnSpawn() { m.spawns++ }

// OnPhysicsRestored records the explicit paired-recovery lifecycle.
func (m *monster) OnPhysicsRestored(*golem.Server) { m.restores++ }

type replaceableNavigation struct{ *adapter.Backend }
type navigationReplacement struct{ commit func() }

// Commit publishes the privately reconstructed navigation world.
func (r navigationReplacement) Commit() error { r.commit(); return nil }

// Discard releases no additional host resources for this in-memory fixture.
func (r navigationReplacement) Discard() {}

type fixture struct {
	options           options
	server            *golem.Server
	world             *box3d.World
	definitions       *generated.Definitions
	navWorld          *dynamic.World
	navBackend        *replaceableNavigation
	actors            map[int64]*monster
	startGate         chan struct{}
	startOnce         sync.Once
	controlMu         sync.Mutex
	mu                sync.Mutex
	state             fixtureState
	baseline          map[int64]entityState
	blockedFrames     int
	checkpointStarted bool
	saves             sync.WaitGroup
}

func newFixture(o options) (_ *fixture, err error) {
	file, err := os.Open(o.content)
	if err != nil {
		return nil, err
	}
	defs, loadErr := generated.LoadDefinitions(file, content.Options{})
	closeErr := file.Close()
	if loadErr != nil {
		return nil, loadErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = defs.Catalog().ValidateFactories(map[string]bool{"monster": true}); err != nil {
		return nil, err
	}
	for _, id := range []string{"demo/Monster/scout", "demo/Monster/guard"} {
		def, ok := defs.LookupMonster(id)
		if !ok || def.Factory() != "monster" || def.Stats().Speed() <= 0 || def.Movement().Clearance() != .5 {
			return nil, fmt.Errorf("incompatible fixture definition %s", id)
		}
	}
	w, err := box3d.New(p.DefaultConfig())
	if err != nil {
		return nil, err
	}
	f := &fixture{options: o, world: w, definitions: defs, actors: map[int64]*monster{}, startGate: make(chan struct{}), baseline: map[int64]entityState{}}
	f.server, _ = generated.NewServer(golem.ServerConfig{TickRate: 20, Transport: golem.TransportWebSocket, StateUpdateLane: golem.StateUpdateLaneStream})
	defer func() {
		if err != nil {
			f.dispose()
		}
	}()
	if err = f.server.SetPhysicsWorld(w); err != nil {
		return nil, err
	}
	scene, config := navigationSource()
	f.navWorld, err = dynamic.New(context.Background(), scene, config)
	if err != nil {
		return nil, err
	}
	backend, err := adapter.NewDynamic(f.navWorld)
	if err != nil {
		return nil, err
	}
	f.navBackend = &replaceableNavigation{backend}
	if err = f.server.SetSurfaceNavigation(f.navBackend, golem.NavigationConfig{RequiredProfiles: []surface.ProfileID{"human"}, PathWorkers: 1, BuildWorkers: 1}); err != nil {
		return nil, err
	}
	f.state = fixtureState{Mode: "fresh", Phase: "ready", ContentHash: defs.Hash(), Entities: []entityState{}, InitialEntities: []entityState{}, CheckpointEntities: []entityState{}}
	if o.resume == "" {
		if err = f.makeFresh(); err != nil {
			return nil, err
		}
	} else {
		f.state.Mode, f.state.Phase = "resume", "resumed"
		if err = f.restore(o.resume); err != nil {
			return nil, err
		}
	}
	f.state.Entities = f.entityStates()
	f.state.InitialEntities = append([]entityState(nil), f.state.Entities...)
	for _, e := range f.state.Entities {
		f.baseline[e.ID] = e
	}
	if err = f.verifyPoseAndQuery(); err != nil {
		return nil, err
	}
	if o.evidence != "" {
		if err = os.MkdirAll(o.evidence, 0700); err != nil {
			return nil, err
		}
		if err = writeJSONExclusive(filepath.Join(o.evidence, "initial-state.json"), f.snapshot()); err != nil {
			return nil, err
		}
	}
	f.server.OnTick(func(_ float64, _ *golem.Server) {
		state, ok := f.server.NavigationAgentState(3)
		if ok && state.Mode == golem.NavigationArrived {
			x, _, z := f.actors[3].Position3D()
			target := float64(12)
			if x > 0 {
				target = -12
			}
			if e := f.server.SetNavigationDestination(3, surface.Vec3{X: target, Y: state.Destination.Y, Z: float64(z)}); e != nil {
				f.fail(e)
			}
		}
	})
	f.server.OnTickEnd(func(_ uint64, _ time.Duration) {
		if e := f.verifyPoseAndQuery(); e != nil {
			f.fail(e)
		}
		states := f.entityStates()
		for _, e := range states {
			if e.ID == 1 && e.X > -.95 && e.X < -.899 {
				f.blockedFrames++
			}
		}
		guard := f.actors[3]
		gx, _, _ := guard.Position3D()
		moved := abs(gx-f.baseline[3].X) > .5
		f.mu.Lock()
		f.state.Entities, f.state.Tick = states, f.server.Tick()
		f.state.MotionPassed = f.state.MotionPassed || (moved && (f.state.Mode == "resume" || f.blockedFrames >= 3))
		f.mu.Unlock()
	})
	return f, nil
}

func navigationSource() (geom.Scene, bake.Config) {
	ground := geom.Asset{ID: "floor", Vertices: []geom.Vec3{{X: -20, Z: -6}, {X: 20, Z: -6}, {X: -20, Z: 6}, {X: 20, Z: 6}}, Triangles: []geom.Triangle{{Indices: [3]uint32{0, 2, 1}, Area: 1}, {Indices: [3]uint32{1, 2, 3}, Area: 1}}}
	scene := geom.Scene{Assets: []geom.Asset{ground}, Instances: []geom.Instance{{ID: "floor", AssetID: "floor", Revision: 1, Transform: geom.Identity()}}}
	config := bake.DefaultConfig("unity-integration", geom.Bounds{Min: geom.Vec3{X: -20, Y: -1, Z: -6}, Max: geom.Vec3{X: 20, Y: 4, Z: 6}})
	config.Profile = geom.Profile{ID: "human", Radius: .5, Height: 1.8, MaxSlope: 45, MaxClimb: .3}
	config.Settings.CellSize, config.Settings.CellHeight, config.Settings.TileSize = .25, .1, 64
	return scene, config
}

func (f *fixture) makeFresh() error {
	if _, err := f.addBody("floor", p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 20, Y: .5, Z: 6}}, 0, 1); err != nil {
		return err
	}
	// A runtime physical obstacle deliberately remains absent from the static nav floor.
	if _, err := f.addBody("blocker", p.Kinematic, p.Vec3{Y: 1}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: 1, Z: 1}}, 0, 2); err != nil {
		return err
	}
	for _, entry := range []struct {
		id         int64
		definition string
		x, z       float64
	}{{1, "scout", -3, 0}, {2, "scout", -3, 3}, {3, "guard", -12, -3}} {
		query, err := f.navWorld.Snapshot().NewQuery(navigation.Limits{})
		if err != nil {
			return err
		}
		projected := query.Project(context.Background(), geom.Vec3{X: entry.x, Z: entry.z}, navigation.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}, navigation.Filter{})
		if projected.Code != navigation.Complete {
			return fmt.Errorf("spawn %d navigation projection: %v", entry.id, projected.Code)
		}
		point := projected.Surface.Position()
		position := p.Vec3{X: float32(point.X), Y: float32(point.Y), Z: float32(point.Z)}
		def, _ := f.definitions.LookupMonster("demo/Monster/" + entry.definition)
		m := &monster{SyncedMonster: generated.NewSyncedMonster(position.X, position.Y, position.Z, def.ID(), def.Health(), entry.id), definition: def}
		shape := p.ShapeDesc{Kind: p.Capsule, Radius: .4, PointA: p.Vec3{Y: .4}, PointB: p.Vec3{Y: 1.4}}
		body, err := f.addBody(fmt.Sprintf("actor/%d", entry.id), p.Kinematic, position, shape, entry.id, 4)
		if err != nil {
			return err
		}
		if err = f.world.Synchronize(); err != nil {
			return err
		}
		if err = f.server.CreateEntityWithSetup(m, func(golem.Entity) (func() error, error) {
			if e := f.server.AttachPhysics(entry.id, body, golem.PhysicsAttachmentConfig{}); e != nil {
				return nil, e
			}
			return nil, f.server.AttachNavigation(entry.id, agentConfig(def))
		}); err != nil {
			return fmt.Errorf("spawn %d at %+v: %w", entry.id, position, err)
		}
		if m.spawns != 1 {
			return errors.New("fresh entity did not spawn once")
		}
		f.actors[entry.id] = m
	}
	return nil
}

func agentConfig(def generated.ContentMonster) golem.NavigationAgentConfig {
	return golem.NavigationAgentConfig{Profile: "human", Speed: float64(def.Stats().Speed()), Acceleration: 12, StoppingDistance: .1, SupportTolerance: .2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: .4, Down: .4}, StagedPhysics: true}
}

func (f *fixture) addBody(id string, mode p.MotionMode, position p.Vec3, shape p.ShapeDesc, owner int64, category uint64) (p.BodyHandle, error) {
	shape.ID = p.ContentID(id + "/shape")
	h, err := f.world.CreateShape(shape)
	if err != nil {
		return p.BodyHandle{}, err
	}
	desc := p.DefaultBody(mode)
	desc.ID, desc.Owner, desc.Pose.Position = p.ContentID(id), owner, position
	if owner != 0 {
		// Keep an explicit two-millimetre authored clearance above the floor;
		// the gameplay anchor remains exactly on the baked navigation surface.
		desc.Pose.Position.Y = .002
		desc.Anchor.Position.Y = position.Y - .002
		desc.Controller = p.DefaultCharacterController(mode)
		desc.Controller.Gravity = false
		desc.Controller.StepHeight = 0
		desc.Continuous = true
	}
	body, err := f.world.CreateBody(desc)
	if err != nil {
		return body, err
	}
	c := p.DefaultCollider(body, h)
	c.ID, c.Owner, c.Filter.Category = p.ContentID(id+"/collider"), owner, category
	_, err = f.world.CreateCollider(c)
	return body, err
}

func (f *fixture) verifyPoseAndQuery() error {
	for id, m := range f.actors {
		body, err := f.server.PhysicsBody(id)
		if err != nil {
			return err
		}
		x, y, z := m.Position3D()
		if body.Anchor.Position != (p.Vec3{X: x, Y: y, Z: z}) {
			return fmt.Errorf("entity %d differs from accepted physics anchor", id)
		}
		full := m.FullState()
		if full.PosX != x || full.PosY != y || full.PosZ != z {
			return fmt.Errorf("entity %d replication pose mismatch", id)
		}
		if id == 1 && x > -.899 {
			return fmt.Errorf("actor crossed physical blocker: x=%g", x)
		}
	}
	if err := f.world.Synchronize(); err != nil {
		return err
	}
	hits := make([]p.Hit, 4)
	result, err := f.world.Query(p.Query{Kind: p.Ray, Mode: p.All, Pose: p.Pose{Position: p.Vec3{X: -2, Y: 1}, Rotation: p.Identity().Rotation}, Translation: p.Vec3{X: 4}, Filter: p.QueryFilter{Mask: 2, RequireSolid: true}}, hits)
	if err != nil {
		return err
	}
	if result.Incomplete || result.Total != 1 || result.Count != 1 || hits[0].Content != "blocker/collider" {
		return fmt.Errorf("runtime blocker query mismatch: %+v %+v", result, hits[:result.Count])
	}
	f.mu.Lock()
	f.state.QueryPassed, f.state.PoseEqualityPassed = true, true
	f.mu.Unlock()
	return nil
}

func (f *fixture) entityStates() []entityState {
	result := make([]entityState, 0, len(f.actors))
	for id, m := range f.actors {
		x, y, z := m.Position3D()
		result = append(result, entityState{id, m.DefinitionId(), m.Health(), x, y, z})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (f *fixture) snapshot() fixtureState {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy := f.state
	copy.Entities = append([]entityState{}, copy.Entities...)
	copy.InitialEntities = append([]entityState{}, copy.InitialEntities...)
	copy.CheckpointEntities = append([]entityState{}, copy.CheckpointEntities...)
	return copy
}

func (f *fixture) fail(err error) {
	f.mu.Lock()
	if f.state.Error == "" {
		f.state.Error = err.Error()
	}
	f.mu.Unlock()
}

func (f *fixture) start() error {
	f.controlMu.Lock()
	defer f.controlMu.Unlock()
	state := f.snapshot()
	if state.Phase != "ready" && state.Phase != "resumed" {
		return errors.New("fixture already started")
	}
	if state.Mode == "fresh" {
		for _, target := range []struct {
			id int64
			x  float64
		}{{1, 3}, {3, 12}} {
			_, y, z := f.actors[target.id].Position3D()
			if err := f.server.SetNavigationDestination(target.id, surface.Vec3{X: target.x, Y: float64(y), Z: float64(z)}); err != nil {
				return err
			}
		}
	}
	f.mu.Lock()
	f.state.Phase = "moving"
	f.mu.Unlock()
	f.startOnce.Do(func() { close(f.startGate) })
	return nil
}

func (f *fixture) run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		f.dispose()
		return ctx.Err()
	case <-f.startGate:
	}
	err := f.server.Run(ctx)
	f.saves.Wait()
	if state := f.snapshot(); state.Error != "" {
		return errors.New(state.Error)
	}
	return err
}

func (f *fixture) dispose() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = f.server.Run(ctx)
}
func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
