package objecttemplate

import (
	"fmt"
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

// NavigationAssets returns every asset referenced by template navigation, in
// ID order. Heightfields expand with the navigation diagonal. Each call builds
// new slices, so hosts should call it once per navigation world.
func (c *Catalog) NavigationAssets() []geom.Asset {
	if c == nil {
		return nil
	}
	out := make([]geom.Asset, 0, len(c.navigation))
	for _, id := range c.navigation {
		asset := geometry.ExpandHeightfield(c.geometry[id], false)
		value := geom.Asset{ID: asset.ID, Vertices: make([]geom.Vec3, len(asset.Vertices)), Triangles: make([]geom.Triangle, len(asset.Triangles))}
		for i, v := range asset.Vertices {
			value.Vertices[i] = geom.Vec3{X: v.X, Y: v.Y, Z: v.Z}
		}
		for i, t := range asset.Triangles {
			value.Triangles[i] = geom.Triangle{Indices: t.Indices, Area: t.Area}
		}
		out = append(out, value)
	}
	return out
}

// PlaceNavigation returns world-space navigation for one placement. Element IDs
// are prefixed with placementID and carry revision. Volumes and links support
// yaw-only rotation with equal X and Z scale; instances accept any transform.
func (t *Template) PlaceNavigation(placementID string, at Transform, revision uint64) (Navigation, error) {
	var out Navigation
	if t == nil || t.Navigation == nil {
		return out, nil
	}
	if placementID == "" || revision == 0 {
		return out, invalid("navigation placement needs an ID and revision")
	}
	if err := at.validate(); err != nil {
		return out, err
	}
	root := rootMatrix(at)
	for _, instance := range t.Navigation.Instances {
		instance.ID = placementID + "/" + instance.ID
		instance.Revision = revision
		instance.Transform = multiply(root, instance.Transform)
		out.Instances = append(out.Instances, instance)
	}
	if len(t.Navigation.Volumes) == 0 && len(t.Navigation.Links) == 0 {
		return out, nil
	}
	scale := at.scale()
	r := rotation3(at.Rotation)
	const epsilon = 1e-6
	if math.Abs(r[1]) > epsilon || math.Abs(r[3]) > epsilon || math.Abs(r[5]) > epsilon || math.Abs(r[7]) > epsilon || scale.X != scale.Z {
		return Navigation{}, fmt.Errorf("%w: navigation volumes and links need yaw-only rotation and equal X/Z scale", p.ErrUnsupported)
	}
	horizontal := float64(scale.X)
	quarter := math.Abs(r[0]) < epsilon || math.Abs(r[2]) < epsilon
	for _, volume := range t.Navigation.Volumes {
		placed, err := placeVolume(volume, root, horizontal, quarter)
		if err != nil {
			return Navigation{}, err
		}
		placed.ID = placementID + "/" + volume.ID
		placed.Revision = revision
		out.Volumes = append(out.Volumes, placed)
	}
	for _, link := range t.Navigation.Links {
		link.ID = placementID + "/" + link.ID
		link.Revision = revision
		link.Start, link.End = root.Point(link.Start), root.Point(link.End)
		link.Radius *= horizontal
		out.Links = append(out.Links, link)
	}
	return out, nil
}

func placeVolume(v geom.Volume, root geom.Transform, horizontal float64, quarter bool) (geom.Volume, error) {
	minY, maxY := root.Point(v.Bounds.Min).Y, root.Point(v.Bounds.Max).Y
	switch v.Shape {
	case "cylinder":
		center := root.Point(geom.Vec3{X: (v.Bounds.Min.X + v.Bounds.Max.X) / 2, Z: (v.Bounds.Min.Z + v.Bounds.Max.Z) / 2})
		halfX, halfZ := (v.Bounds.Max.X-v.Bounds.Min.X)/2*horizontal, (v.Bounds.Max.Z-v.Bounds.Min.Z)/2*horizontal
		if !quarter {
			halfX = max(halfX, halfZ)
			halfZ = halfX
		} else if math.Abs(root[0]) < 1e-6 {
			halfX, halfZ = halfZ, halfX
		}
		v.Radius *= horizontal
		v.Bounds = geom.Bounds{Min: geom.Vec3{X: center.X - halfX, Y: minY, Z: center.Z - halfZ}, Max: geom.Vec3{X: center.X + halfX, Y: maxY, Z: center.Z + halfZ}}
		return v, nil
	case "box":
		corners := []geom.Vec3{{X: v.Bounds.Min.X, Z: v.Bounds.Min.Z}, {X: v.Bounds.Max.X, Z: v.Bounds.Min.Z}, {X: v.Bounds.Max.X, Z: v.Bounds.Max.Z}, {X: v.Bounds.Min.X, Z: v.Bounds.Max.Z}}
		points := transformPoints(root, corners, minY)
		v.Bounds = pointBounds(points, minY, maxY)
		if !quarter {
			v.Shape, v.Points = "convex", points
		}
		return v, nil
	case "convex":
		v.Points = transformPoints(root, v.Points, minY)
		v.Bounds = pointBounds(v.Points, minY, maxY)
		return v, nil
	}
	return v, fmt.Errorf("%w: volume %s shape %q", ErrInvalid, v.ID, v.Shape)
}

func transformPoints(root geom.Transform, points []geom.Vec3, y float64) []geom.Vec3 {
	out := make([]geom.Vec3, len(points))
	for i, point := range points {
		out[i] = root.Point(geom.Vec3{X: point.X, Z: point.Z})
		out[i].Y = y
	}
	return out
}

func pointBounds(points []geom.Vec3, minY, maxY float64) geom.Bounds {
	b := geom.Bounds{Min: geom.Vec3{X: math.Inf(1), Y: minY, Z: math.Inf(1)}, Max: geom.Vec3{X: math.Inf(-1), Y: maxY, Z: math.Inf(-1)}}
	for _, point := range points {
		b.Min.X, b.Max.X = min(b.Min.X, point.X), max(b.Max.X, point.X)
		b.Min.Z, b.Max.Z = min(b.Min.Z, point.Z), max(b.Max.Z, point.Z)
	}
	return b
}

func rootMatrix(at Transform) geom.Transform {
	r := rotation3(at.Rotation).mul(diagonal3(at.scale()))
	return geom.Transform{
		r[0], r[1], r[2], float64(at.Position.X),
		r[3], r[4], r[5], float64(at.Position.Y),
		r[6], r[7], r[8], float64(at.Position.Z),
		0, 0, 0, 1,
	}
}

func multiply(a, b geom.Transform) geom.Transform {
	var out geom.Transform
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			out[r*4+c] = a[r*4]*b[c] + a[r*4+1]*b[4+c] + a[r*4+2]*b[8+c] + a[r*4+3]*b[12+c]
		}
	}
	return out
}

// Changes adds or replaces the placement's navigation in a dynamic world.
func (n Navigation) Changes() dynamic.Changes {
	return dynamic.Changes{Instances: n.Instances, Volumes: n.Volumes, Links: n.Links}
}

// Removal removes the placement's navigation from a dynamic world.
func (n Navigation) Removal() dynamic.Changes {
	var ch dynamic.Changes
	for _, v := range n.Instances {
		ch.RemoveInstances = append(ch.RemoveInstances, v.ID)
	}
	for _, v := range n.Volumes {
		ch.RemoveVolumes = append(ch.RemoveVolumes, v.ID)
	}
	for _, v := range n.Links {
		ch.RemoveLinks = append(ch.RemoveLinks, v.ID)
	}
	return ch
}
