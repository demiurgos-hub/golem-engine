package objecttemplate

import (
	"context"
	"fmt"
	"sort"
	"sync"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

type bodyPlan struct {
	source   *content.Body
	desc     p.BodyDesc
	rotation mat3
}

type colliderPlan struct {
	source *content.Collider
	body   int
	// ownBody gives a static triangle mesh with a collider-local pose its own
	// body, because Box3D recooks a mesh attached with a non-identity pose.
	ownBody  bool
	shape    p.ShapeDesc
	local    p.Pose
	rotation mat3 // collider frame relative to the template root
	shared   bool
	key      string
	desc     p.ColliderDesc
}

// prepare validates a 3D template with the physics content contract and
// records how each body and collider is constructed at placement time.
func (c *Catalog) prepare(t *Template, limits Limits) error {
	assets := map[string]content.Asset{}
	var list []content.Asset
	for _, collider := range t.Colliders {
		if collider.AssetID == "" {
			continue
		}
		if _, ok := assets[collider.AssetID]; ok {
			continue
		}
		asset, ok := c.physicsAsset(collider.AssetID)
		if !ok {
			if _, exists := c.geometry[collider.AssetID]; exists {
				return invalid("collider %s references float64 navigation geometry", collider.ID)
			}
			continue
		}
		assets[asset.ID] = asset
		list = append(list, asset)
	}
	source := &content.Source{Generator: content.GeneratorV2, Version: 2, WorldID: c.CatalogID, Revision: c.Revision, CoordinateLimit: p.CoordinateLimit,
		LayerCollisionMasks: c.LayerCollisionMasks, Bodies: t.Bodies, Colliders: t.Colliders, Assets: list, Materials: c.Materials}
	if err := source.Validate(content.Limits{Bytes: limits.Bytes, Bodies: limits.Bodies, Colliders: limits.Colliders, Assets: limits.Assets, Vertices: limits.Vertices, Triangles: limits.Triangles}); err != nil {
		return err
	}
	order := make([]int, len(t.Bodies))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return t.Bodies[order[a]].ID < t.Bodies[order[b]].ID })
	index := map[string]int{}
	for _, i := range order {
		body := &t.Bodies[i]
		if body.Kind != "static" && body.Kind != "kinematic" {
			return fmt.Errorf("%w: body %s kind %s; templates hold stationary objects", p.ErrUnsupported, body.ID, body.Kind)
		}
		mode := p.Static
		if body.Kind == "kinematic" {
			mode = p.Kinematic
		}
		desc := p.DefaultBody(mode)
		desc.Pose = p.Pose{Position: body.Position, Rotation: body.Rotation}
		desc.Enabled = body.Enabled && body.Active
		desc.GravityScale = 0
		index[body.ID] = len(t.bodies)
		t.bodies = append(t.bodies, bodyPlan{source: body, desc: desc, rotation: rotation3(body.Rotation)})
	}
	order = make([]int, len(t.Colliders))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return t.Colliders[order[a]].ID < t.Colliders[order[b]].ID })
	for _, i := range order {
		collider := &t.Colliders[i]
		if collider.Character != nil {
			return fmt.Errorf("%w: collider %s is a character controller", p.ErrUnsupported, collider.ID)
		}
		b := index[collider.BodyID]
		body := t.bodies[b]
		shape, local, shared, err := collider.Placement(assets, *body.source)
		if err != nil {
			return fmt.Errorf("collider %s: %w", collider.ID, err)
		}
		plan := colliderPlan{source: collider, body: b, shape: shape, local: local, shared: shared,
			rotation: body.rotation.mul(rotation3(local.Rotation))}
		plan.ownBody = shape.Kind == p.TriangleMesh && body.desc.Mode == p.Static && local != p.Identity()
		if shared {
			plan.key = "asset/" + collider.AssetID + "/" + collider.Shape
			base := p.ShapeDesc{ID: p.ContentID(plan.key), Kind: shape.Kind, Vertices: shape.Vertices, Indices: shape.Indices}
			c.addBase(plan.key, base)
		} else {
			plan.key = "collider/" + t.ID + "/" + collider.ID
			shape.ID = p.ContentID(t.ID + "/" + collider.ID + "/geometry")
			plan.shape = shape
			c.addBase(plan.key, shape)
		}
		desc := p.DefaultCollider(p.BodyHandle{}, p.ShapeHandle{})
		desc.Enabled = collider.Enabled && collider.Active
		desc.Roles = p.Roles{Solid: collider.Solid, Sensor: collider.Sensor, Query: collider.Query}
		desc.Filter = p.CollisionFilter{Category: uint64(1) << collider.Layer, Mask: uint64(c.LayerCollisionMasks[collider.Layer])}
		desc.Material = c.materials[collider.MaterialID]
		plan.desc = desc
		t.colliders = append(t.colliders, plan)
	}
	return nil
}

func (c *Catalog) addBase(key string, shape p.ShapeDesc) {
	if c.bases == nil {
		c.bases = map[string]p.ShapeDesc{}
		c.baseOrder = nil
	}
	if _, ok := c.bases[key]; ok {
		return
	}
	c.bases[key] = shape
	c.baseOrder = append(c.baseOrder, key)
}

// Cooked holds catalog shapes cooked once outside a world.
type Cooked struct {
	catalog *Catalog
	shapes  map[string]p.PreparedShape
}

// Cook prepares the unit-scale shapes of templateIDs, or of every template when
// none are given, with up to workers concurrent preparer calls. Shapes that
// share an asset are cooked once for the catalog. A placer creates any other
// shape in its world when first placed.
func (c *Catalog) Cook(ctx context.Context, preparer p.ShapePreparer, workers int, templateIDs ...string) (*Cooked, error) {
	if c == nil || preparer == nil || ctx == nil || c.Dimensions != 3 {
		return nil, ErrInvalid
	}
	keys := c.baseOrder
	if len(templateIDs) > 0 {
		selected := map[string]bool{}
		keys = nil
		for _, id := range templateIDs {
			t, ok := c.Template(id)
			if !ok {
				return nil, invalid("unknown template %s", id)
			}
			for _, plan := range t.colliders {
				if !selected[plan.key] {
					selected[plan.key] = true
					keys = append(keys, plan.key)
				}
			}
		}
	}
	cooked := &Cooked{catalog: c, shapes: make(map[string]p.PreparedShape, len(keys))}
	results := make([]p.PreparedShape, len(keys))
	next := make(chan int)
	var failure error
	var once sync.Once
	var wait sync.WaitGroup
	for range max(1, workers) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := range next {
				shape, err := preparer.PrepareShape(c.bases[keys[i]])
				if err != nil {
					once.Do(func() { failure = fmt.Errorf("geometry %s: %w", keys[i], err) })
					continue
				}
				results[i] = shape
			}
		}()
	}
	var stopped error
	for i := range keys {
		if err := ctx.Err(); err != nil {
			stopped = err
			break
		}
		next <- i
	}
	close(next)
	wait.Wait()
	if failure != nil {
		return nil, failure
	}
	if stopped != nil {
		return nil, stopped
	}
	for i, key := range keys {
		cooked.shapes[key] = results[i]
	}
	return cooked, nil
}
