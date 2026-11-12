package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

type workload struct {
	Characters, DynamicBodies, Projectiles, StaticColliders, Triangles, Queries int
	TilesX, TilesZ, Cells                                                       int
}

func counts(mode string) (workload, error) {
	switch mode {
	case "full":
		return workload{1000, 1000, 500, 100000, 5000000, 10000, 25, 10, 100}, nil
	case "rehearsal":
		return workload{100, 100, 50, 5000, 200000, 1000, 5, 2, 100}, nil
	case "correctness":
		return workload{16, 16, 8, 200, 5000, 100, 1, 1, 50}, nil
	default:
		return workload{}, fmt.Errorf("unknown mode %q", mode)
	}
}

type actor struct {
	ID               int64
	position, before p.Vec3
	body             p.BodyHandle
	ends             [2]surface.Vec3
	target           int
	admitted         bool
	travel           float32
	requestAt        time.Time
}

// EntityID identifies the qualification actor.
func (a *actor) EntityID() int64 { return a.ID }

// TypeName supplies the registry type.
func (a *actor) TypeName() string { return "PhysicsQualificationActor" }

// Position supplies the legacy spatial projection.
func (a *actor) Position() (float32, float32) { return a.position.X, a.position.Z }

// Position3D returns the accepted float32 gameplay anchor.
func (a *actor) Position3D() (float32, float32, float32) {
	return a.position.X, a.position.Y, a.position.Z
}

// SetPosition3D accepts committed server readback without gameplay side effects.
func (a *actor) SetPosition3D(x, y, z float32) { a.position = p.Vec3{X: x, Y: y, Z: z} }

// IsGlobal leaves this workload actor spatially scoped.
func (a *actor) IsGlobal() bool { return false }

// FlushUpdate emits no network payload in this server workload.
func (a *actor) FlushUpdate() ([]byte, error) { return nil, nil }

// FullUpdate emits no network payload in this server workload.
func (a *actor) FullUpdate() ([]byte, error) { return nil, nil }

type scene struct {
	world                            *box3d.World
	server                           *golem.Server
	actors                           []*actor
	dynamics                         []p.BodyHandle
	projectiles                      []projectile
	counts                           workload
	boxCenters                       []p.Vec3
	queryTasks                       []p.QueryTask
	queryOutcomes                    []p.QueryOutcome
	queryHits                        []p.Hit
	queryWorkers                     int
	admissionLatency, renewalLatency histogram
}

func makeScene(o options, c workload, observeNav func(golem.NavigationTickMetrics)) (*scene, error) {
	s := &scene{counts: c, queryWorkers: max(1, o.QueryWorkers)}
	config := p.DefaultConfig()
	config.Workers = uint32(max(1, o.PhysicsWorkers))
	w, err := box3d.New(config)
	if err != nil {
		return nil, err
	}
	s.world = w
	ok := false
	defer func() {
		if !ok {
			_ = w.Close()
		}
	}()
	s.server = golem.NewServer(golem.ServerConfig{TickRate: 20})
	if err = s.server.SetPhysicsWorld(observedWorld{World: w, out: o.Out}); err != nil {
		return nil, err
	}
	half := float32(o.Extent / 2)
	geometryHash := sha256.New()
	geometryEncoder := json.NewEncoder(geometryHash)
	createdTriangles := 0
	// Every tile owns a different cooked vertex/index array. Instance multiplication
	// cannot stand in for the required five million resident static triangles.
	for tz := 0; tz < c.TilesZ; tz++ {
		for tx := 0; tx < c.TilesX; tx++ {
			d := floorTile(tx, tz, c, half)
			createdTriangles += len(d.Indices) / 3
			if e := geometryEncoder.Encode(d); e != nil {
				return nil, e
			}
			shape, e := w.CreateShape(d)
			if e != nil {
				return nil, e
			}
			if _, e = s.addBody(fmt.Sprintf("floor/%d/%d", tx, tz), p.Static, p.Vec3{}, shape, 0); e != nil {
				return nil, e
			}
		}
	}
	box, e := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .4, Y: .5, Z: .4}})
	if e != nil {
		return nil, e
	}
	boxCount := c.StaticColliders - c.TilesX*c.TilesZ
	cols := int(math.Ceil(math.Sqrt(float64(boxCount))))
	for i := 0; i < boxCount; i++ {
		// The dense static field occupies a distinct part of the same finite world.
		// Query targets visit it explicitly; character lanes remain traversable.
		v := p.Vec3{X: half * (.65 + .3*float32(i%cols)/float32(cols)), Y: .5, Z: half * (-.95 + 1.9*float32(i/cols)/float32(cols))}
		if e := geometryEncoder.Encode(v); e != nil {
			return nil, e
		}
		if _, e = s.addBody(fmt.Sprintf("static/%d", i), p.Static, v, box, 0); e != nil {
			return nil, e
		}
		s.boxCenters = append(s.boxCenters, v)
	}
	profiles := []geom.Profile{{ID: "human", Radius: .4, Height: 1.8, MaxSlope: 45, MaxClimb: .4}, {ID: "large", Radius: .8, Height: 2.4, MaxSlope: 40, MaxClimb: .3}}
	worlds := make([]*dynamic.World, 0, 2)
	// Navigation uses the analytically identical plane. It is baked by the real
	// library, with independent clearance profiles and crossing tile seams.
	ground := geom.Asset{ID: "floor", Vertices: []geom.Vec3{{X: -float64(half), Z: -float64(half)}, {X: float64(half), Z: -float64(half)}, {X: -float64(half), Z: float64(half)}, {X: float64(half), Z: float64(half)}}, Triangles: []geom.Triangle{{Indices: [3]uint32{0, 2, 1}, Area: 1}, {Indices: [3]uint32{1, 2, 3}, Area: 1}}}
	navScene := geom.Scene{Assets: []geom.Asset{ground}, Instances: []geom.Instance{{ID: "floor", AssetID: "floor", Revision: 1, Transform: geom.Identity()}}, Volumes: []geom.Volume{{ID: "static-field", Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: float64(half) * .64, Y: -1, Z: -float64(half)}, Max: geom.Vec3{X: float64(half), Y: 8, Z: float64(half)}}}}}
	for _, profile := range profiles {
		cfg := bake.DefaultConfig("physics-stress-plane", geom.Bounds{Min: geom.Vec3{X: -float64(half), Y: -2, Z: -float64(half)}, Max: geom.Vec3{X: float64(half), Y: 8, Z: float64(half)}})
		cfg.Profile = profile
		cfg.Settings.CellSize = 1
		cfg.Settings.CellHeight = .1
		cfg.Settings.TileSize = 128
		cfg.Settings.BuildLayers = true
		nw, e := dynamic.New(context.Background(), navScene, cfg)
		if e != nil {
			return nil, e
		}
		worlds = append(worlds, nw)
		bundle, e := nw.Snapshot().Bundle()
		if e != nil {
			return nil, e
		}
		data, e := artifact.Encode(bundle)
		if e != nil {
			return nil, e
		}
		if e = writeBytes(filepath.Join(o.Out, "nav-"+profile.ID+".bin"), data); e != nil {
			return nil, e
		}
		if e = writeJSON(filepath.Join(o.Out, "nav-"+profile.ID+"-settings.json"), cfg); e != nil {
			return nil, e
		}
	}
	backend, e := adapter.NewDynamic(worlds...)
	if e != nil {
		return nil, e
	}
	if e = s.server.SetSurfaceNavigation(backend, golem.NavigationConfig{PathWorkers: o.Workers, BuildWorkers: 1, PathQueueCapacity: c.Characters * 2, CompletionCapacity: c.Characters * 2, PathResultsPerTick: c.Characters, PathDrainBudget: timeBudget, MovementAgentsPerTick: c.Characters, MovementBudget: timeBudget, RequiredProfiles: []surface.ProfileID{"human", "large"}, ObserveTick: observeNav, ObservePathAdmission: func(m golem.NavigationPathAdmissionMetrics) {
		if m.EntityID > 0 && m.EntityID <= int64(len(s.actors)) {
			a := s.actors[m.EntityID-1]
			if a.admitted {
				s.renewalLatency.add(m.AdmittedAt.Sub(a.requestAt))
			} else {
				s.admissionLatency.add(m.AdmittedAt.Sub(a.requestAt))
			}
			a.admitted = true
		}
	}}); e != nil {
		return nil, e
	}
	columns := int(math.Ceil(math.Sqrt(float64(c.Characters))))
	for i := 0; i < c.Characters; i++ {
		profile := profiles[i%2]
		shape, e := w.CreateShape(p.ShapeDesc{Kind: p.Capsule, Radius: float32(profile.Radius), PointA: p.Vec3{Y: float32(profile.Radius)}, PointB: p.Vec3{Y: float32(profile.Height - profile.Radius)}})
		if e != nil {
			return nil, e
		}
		spacing := float32(2.8)
		x := -half*.75 + float32(i%columns)*spacing
		z := float32(i/columns-columns/2) * spacing
		query, e := worlds[i%2].Snapshot().NewQuery(navigation.Limits{})
		if e != nil {
			return nil, e
		}
		projected := query.Project(context.Background(), geom.Vec3{X: float64(x), Z: float64(z)}, navigation.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}, navigation.Filter{})
		if projected.Code != navigation.Complete {
			return nil, fmt.Errorf("character %d spawn projection: %v", i, projected.Code)
		}
		point := projected.Surface.Position()
		pos := p.Vec3{X: float32(point.X), Y: float32(point.Y), Z: float32(point.Z)}
		body, e := s.addBody(fmt.Sprintf("character/%d", i), p.DrivenCharacter, pos, shape, int64(i+1))
		if e != nil {
			return nil, e
		}
		a := &actor{ID: int64(i + 1), position: pos, body: body, target: 1, ends: [2]surface.Vec3{{X: float64(pos.X), Y: float64(pos.Y), Z: float64(pos.Z)}, {X: float64(pos.X + half*.8), Y: float64(pos.Y), Z: float64(pos.Z)}}}
		s.actors = append(s.actors, a)
		if e = s.server.CreateEntity(a); e != nil {
			return nil, e
		}
		if e = s.server.AttachPhysics(a.ID, body, golem.PhysicsAttachmentConfig{MaxDriveForce: 1000}); e != nil {
			return nil, e
		}
		if e = s.server.AttachNavigation(a.ID, golem.NavigationAgentConfig{Profile: surface.ProfileID(profile.ID), Speed: 3, Acceleration: 12, StoppingDistance: .2, SupportTolerance: .2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: .4, Down: .4}, StagedPhysics: true}); e != nil {
			return nil, e
		}
		a.requestAt = golem.NavigationNow()
		if e = s.server.SetNavigationDestination(a.ID, a.ends[1]); e != nil {
			return nil, e
		}
	}
	for i := 0; i < c.DynamicBodies; i++ {
		pos := p.Vec3{X: -half*.5 + float32(i%columns)*2.8, Y: 2 + float32(i%3), Z: half*.5 + float32(i/columns)*1.1}
		body, e := s.addBody(fmt.Sprintf("dynamic/%d", i), p.FreeDynamic, pos, box, 0)
		if e != nil {
			return nil, e
		}
		if e = w.SetVelocity(body, p.Vec3{}, p.Vec3{X: .3, Y: .2, Z: .1}); e != nil {
			return nil, e
		}
		s.dynamics = append(s.dynamics, body)
	}
	for i := 0; i+1 < len(s.dynamics); i += 10 {
		_, e = w.CreateJoint(p.JointDesc{ID: p.ContentID(fmt.Sprintf("distance/%d", i)), Kind: p.Distance, A: s.dynamics[i], B: s.dynamics[i+1], FrameA: p.Identity(), FrameB: p.Identity(), Length: 3, Spring: true, Hertz: 2, Damping: .7})
		if e != nil {
			return nil, e
		}
	}
	s.projectiles = make([]projectile, c.Projectiles)
	for i := range s.projectiles {
		s.resetProjectile(i, 0, half)
	}
	if e = w.Synchronize(); e != nil {
		return nil, e
	}
	actual := w.Metrics()
	if createdTriangles != c.Triangles || actual.Colliders != c.StaticColliders+c.Characters+c.DynamicBodies {
		return nil, fmt.Errorf("constructed workload count mismatch: %d triangles, %+v", createdTriangles, actual)
	}
	if e = writeJSON(filepath.Join(o.Out, "scene.json"), map[string]any{"actualStaticTriangles": createdTriangles, "actualStaticColliders": boxCount + c.TilesX*c.TilesZ, "uniqueMeshAssets": c.TilesX * c.TilesZ, "staticGeometrySHA256": hex.EncodeToString(geometryHash.Sum(nil)), "worldMetrics": actual, "projectileStates": len(s.projectiles), "navigationCellSize": 1, "navigationCellHeight": .1, "navigationTileSize": 128, "driveForceNewtons": 1000, "characterMassKG": 80, "dynamicMassKG": 20, "distanceJoints": actual.Joints, "physicalFeetY": 0, "anchorPolicy": "exact projected nav support height is explicit body-local offset above feet", "solver": config}); e != nil {
		return nil, e
	}
	ok = true
	return s, nil
}

func floorTile(tx, tz int, c workload, half float32) p.ShapeDesc {
	n := c.Cells
	d := p.ShapeDesc{Kind: p.TriangleMesh, Vertices: make([]p.Vec3, (n+1)*(n+1)), Indices: make([]uint32, 0, n*n*6)}
	for z := 0; z <= n; z++ {
		for x := 0; x <= n; x++ {
			d.Vertices[z*(n+1)+x] = p.Vec3{X: -half + 2*half*(float32(tx)+float32(x)/float32(n))/float32(c.TilesX), Z: -half + 2*half*(float32(tz)+float32(z)/float32(n))/float32(c.TilesZ)}
		}
	}
	for z := 0; z < n; z++ {
		for x := 0; x < n; x++ {
			a := uint32(z*(n+1) + x)
			d.Indices = append(d.Indices, a, a+uint32(n+1), a+1, a+1, a+uint32(n+1), a+uint32(n+2))
		}
	}
	return d
}

func (s *scene) addBody(id string, mode p.MotionMode, pos p.Vec3, shape p.ShapeHandle, owner int64) (p.BodyHandle, error) {
	d := p.DefaultBody(mode)
	d.ID = p.ContentID(id)
	d.Pose.Position = pos
	d.Owner = owner
	if mode != p.Static {
		d.AllowSleep = false
		d.Mass = 20
		d.Continuous = true
	}
	if mode == p.DrivenCharacter {
		d.Mass = 80
		// The body feet touch the authored plane. Navigation's raster support
		// height is an explicit gameplay-anchor offset, not thicker geometry.
		d.Pose.Position.Y = 0
		d.Anchor.Position.Y = pos.Y
		d.Locks = p.MotionLocks{AngularX: true, AngularY: true, AngularZ: true}
	}
	body, e := s.world.CreateBody(d)
	if e != nil {
		return body, e
	}
	cd := p.DefaultCollider(body, shape)
	cd.ID = p.ContentID(id + "/collider")
	cd.Material.Friction = .5
	_, e = s.world.CreateCollider(cd)
	return body, e
}
