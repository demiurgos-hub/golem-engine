package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
	"sort"
)

// CaptureDebug copies bounded accepted state and exact live native geometry.
// Unlike gameplay queries it permits faulted/pending worlds for diagnosis, but
// must be called by the owner outside Simulate. Native candidate poses are never
// presented as accepted. Active bodies precede statics when the body cap is hit.
func (w *World) CaptureDebug(o p.DebugOptions) (p.DebugCapture, error) {
	c := p.DebugCapture{Version: p.DebugVersion, Config: w.config, Backend: "box3d-go", SourceSnapshot: o.SourceSnapshot, Status: "accepted", Revision: w.revision, AcceptedFrame: w.lastFrame}
	if w.closed {
		return c, p.ErrStale
	}
	if err := o.Validate(); err != nil {
		return c, err
	}
	if w.dirty {
		c.Status = "unsynchronized"
	}
	if w.pending != nil {
		c.Status = "pending"
	}
	if w.stats.Fault != nil {
		c.Status = "faulted"
		c.Fault = w.stats.Fault.Error()
	}
	keys := make([]p.BodyHandle, 0, len(w.bodies))
	for h := range w.bodies {
		keys = append(keys, h)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, z := w.bodies[keys[i]], w.bodies[keys[j]]
		if (a.desc.Mode == p.Static) != (z.desc.Mode == p.Static) {
			return a.desc.Mode != p.Static
		}
		return keys[i].Index < keys[j].Index
	})
	selected := map[p.BodyHandle]bool{}
	for i, h := range keys {
		if i >= o.MaxBodies {
			c.Omitted.Bodies++
			continue
		}
		v := w.bodies[h]
		d := p.DebugBody{State: v.committed}
		if o.IncludeUncommitted && c.Status != "accepted" {
			native := w.bodyState(h, v)
			d.HasUncommitted = true
			d.Uncommitted = &native
		}
		c.Bodies = append(c.Bodies, d)
		selected[h] = true
	}
	colliders := make([]p.ColliderHandle, 0, len(w.colliders))
	for h := range w.colliders {
		colliders = append(colliders, h)
	}
	sort.Slice(colliders, func(i, j int) bool { return colliders[i].Index < colliders[j].Index })
	vertices, triangles := 0, 0
	for _, h := range colliders {
		v := w.colliders[h]
		if !selected[v.desc.Body] || len(c.Colliders) >= o.MaxColliders {
			c.Omitted.Colliders++
			c.Omitted.Geometry += len(v.shapes)
			continue
		}
		d := p.DebugCollider{Handle: h, Body: v.desc.Body, Shape: v.desc.Shape, ShapeID: w.shapes[v.desc.Shape].desc.ID, ID: v.desc.ID, Owner: v.desc.Owner, Enabled: v.desc.Enabled, Roles: v.desc.Roles}
		if d.Owner == 0 {
			d.Owner = w.bodies[v.desc.Body].desc.Owner
		}
		for _, id := range v.shapes {
			nv, nt := debugGeometrySize(id)
			if nv < 0 || len(c.Geometry) >= o.MaxGeometry || nv > o.MaxVertices-vertices || nt > o.MaxTriangles-triangles {
				d.GeometryOmitted = true
				c.Omitted.Geometry++
				c.Omitted.Vertices += max(0, nv)
				c.Omitted.Triangles += max(0, nt)
				continue
			}
			g := debugGeometry(id)
			g.Collider = h
			g.Child = v.children[id]
			g.BackendShapeIndex = uint32(id.Index1 - 1)
			c.Geometry = append(c.Geometry, g)
			vertices += nv
			triangles += nt
		}
		// Disabled colliders have no native shape; their absence is explicit.
		if len(v.shapes) == 0 {
			d.GeometryOmitted = true
			c.Omitted.Geometry++
		}
		c.Colliders = append(c.Colliders, d)
	}
	if err := c.CopyAnnotations(o); err != nil {
		return c, err
	}
	return c, nil
}

func debugGeometrySize(id b.ShapeID) (vertices, triangles int) {
	switch id.GetType() {
	case b.SphereShape, b.CapsuleShape:
		return 0, 0
	case b.HullShape:
		h := id.GetHull()
		edges := b.GetHullEdges(h)
		for _, face := range b.GetHullFaces(h) {
			n := 1
			for next := edges[face.Edge].Next; next != face.Edge; next = edges[next].Next {
				n++
			}
			triangles += n - 2
		}
		return int(h.VertexCount), triangles
	case b.MeshShape:
		m := id.GetMesh()
		return int(m.Data.VertexCount), int(m.Data.TriangleCount)
	default:
		return -1, -1
	}
}

func debugGeometry(id b.ShapeID) p.DebugGeometry {
	var g p.DebugGeometry
	switch id.GetType() {
	case b.SphereShape:
		s := id.GetSphere()
		g.Kind = p.Sphere
		g.PointA = pv(s.Center)
		g.Radius = s.Radius
	case b.CapsuleShape:
		s := id.GetCapsule()
		g.Kind = p.Capsule
		g.PointA = pv(s.Center1)
		g.PointB = pv(s.Center2)
		g.Radius = s.Radius
	case b.HullShape:
		g.Kind = p.Convex
		h := id.GetHull()
		for _, v := range b.GetHullPoints(h) {
			g.Vertices = append(g.Vertices, pv(v))
		}
		edges := b.GetHullEdges(h)
		for _, face := range b.GetHullFaces(h) {
			first := edges[face.Edge].Origin
			edge := edges[face.Edge].Next
			previous := edges[edge].Origin
			for edge = edges[edge].Next; edge != face.Edge; edge = edges[edge].Next {
				next := edges[edge].Origin
				g.Indices = append(g.Indices, uint32(first), uint32(previous), uint32(next))
				previous = next
			}
		}
	case b.MeshShape:
		g.Kind = p.TriangleMesh
		m := id.GetMesh()
		for _, v := range b.GetMeshVertices(m.Data) {
			g.Vertices = append(g.Vertices, p.Vec3{X: v.X * m.Scale.X, Y: v.Y * m.Scale.Y, Z: v.Z * m.Scale.Z})
		}
		for _, t := range b.GetMeshTriangles(m.Data) {
			a, z := t.Index2, t.Index3
			if m.Scale.X*m.Scale.Y*m.Scale.Z < 0 {
				a, z = z, a
			}
			g.Indices = append(g.Indices, uint32(t.Index1), uint32(a), uint32(z))
		}
	}
	return g
}
