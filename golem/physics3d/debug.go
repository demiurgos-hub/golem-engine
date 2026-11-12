package physics3d

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const DebugVersion = 1
const MaxDebugBytes int64 = 32 << 20

// DebugOptions bounds an explicit tick-owned capture. Zero limits are invalid.
// Frame/queries/targets are caller-supplied observations, never an implicit log.
type DebugOptions struct {
	MaxBodies, MaxColliders, MaxGeometry, MaxVertices, MaxTriangles, MaxRecords int
	IncludeUncommitted                                                          bool
	SourceSnapshot                                                              string
	Frame                                                                       *FrameResult
	FrameAccepted                                                               bool
	Queries                                                                     []DebugQuery
	Targets                                                                     []DebugTarget
}

// DefaultDebugOptions limits copies before geometry allocation.
func DefaultDebugOptions() DebugOptions {
	return DebugOptions{MaxBodies: 256, MaxColliders: 512, MaxGeometry: 1024, MaxVertices: 100000, MaxTriangles: 100000, MaxRecords: 4096}
}

// Validate checks hard capture resource limits.
func (o DebugOptions) Validate() error {
	if o.MaxBodies < 1 || o.MaxBodies > 4096 || o.MaxColliders < 1 || o.MaxColliders > 8192 || o.MaxGeometry < 1 || o.MaxGeometry > 8192 || o.MaxVertices < 1 || o.MaxVertices > 200000 || o.MaxTriangles < 1 || o.MaxTriangles > 200000 || o.MaxRecords < 1 || o.MaxRecords > 65536 || len(o.SourceSnapshot) > 4096 {
		return ErrInvalid
	}
	return nil
}

// DebugWorld optionally exposes copied diagnostic state, including after a fault.
// Capture must run on the world's owner goroutine outside an active Simulate call.
type DebugWorld interface {
	CaptureDebug(DebugOptions) (DebugCapture, error)
}

// DebugCapture is a versioned visualization document, never a checkpoint.
// Geometry is exact included native collider geometry in body-origin local space.
// Omitted leaves are absent, not replaced with bounding geometry.
type DebugCapture struct {
	Version                                int
	Config                                 Config
	Backend, SourceSnapshot, Status, Fault string
	Revision, AcceptedFrame                uint64
	Incomplete                             bool
	Omitted                                DebugOmissions
	Bodies                                 []DebugBody
	Colliders                              []DebugCollider
	Geometry                               []DebugGeometry
	Frame                                  uint64
	FrameAccepted                          bool
	Paths                                  []BodyPath
	Contacts                               []ContactEvent
	Queries                                []DebugQuery
	Targets                                []DebugTarget
}
type DebugOmissions struct{ Bodies, Colliders, Geometry, Vertices, Triangles, Records int }
type DebugBody struct {
	State          BodyState
	HasUncommitted bool
	Uncommitted    *BodyState
}
type DebugCollider struct {
	Handle          ColliderHandle
	Body            BodyHandle
	Shape           ShapeHandle
	ShapeID         ContentID
	ID              ContentID
	Owner           int64
	Enabled         bool
	Roles           Roles
	GeometryOmitted bool
}
type DebugGeometry struct {
	Collider          ColliderHandle
	Child             uint32
	BackendShapeIndex uint32 // zero-based diagnostic index, not a durable ID
	Kind              ShapeKind
	Radius            float32
	PointA, PointB    Vec3
	Vertices          []Vec3
	Indices           []uint32
}

// DebugQuery owns the original query and the returned subset of its hits.
// QueryResult.Incomplete remains distinct from capture-resource omissions.
type DebugQuery struct {
	Label  string
	Query  Query
	Result QueryResult
	Hits   []Hit
	Error  string
}
type DebugTarget struct {
	Body         BodyHandle
	From, Target Vec3
	Label        string
}

// CopyAnnotations copies bounded frame paths, contacts, queries and targets.
// FrameAccepted must only be true after the host accepts that exact result.
func (c *DebugCapture) CopyAnnotations(o DebugOptions) error {
	if err := o.Validate(); err != nil {
		return err
	}
	c.Paths, c.Contacts, c.Queries, c.Targets = nil, nil, nil, nil
	c.Frame, c.FrameAccepted, c.Omitted.Records = 0, false, 0
	remaining := o.MaxRecords
	vertices, triangles := o.MaxVertices, o.MaxTriangles
	for _, g := range c.Geometry {
		vertices -= len(g.Vertices)
		triangles -= len(g.Indices) / 3
	}
	take := func(n int) int { k := min(n, remaining); remaining -= k; c.Omitted.Records += n - k; return k }
	if o.Frame != nil {
		c.Frame, c.FrameAccepted = o.Frame.ID, o.FrameAccepted
		for _, path := range o.Frame.Paths {
			if remaining == 0 {
				c.Omitted.Records += 1 + len(path.Positions)
				continue
			}
			remaining--
			n := take(len(path.Positions))
			v := BodyPath{Body: path.Body, Positions: append([]Vec3(nil), path.Positions[:n]...)}
			v.Poses = append([]Pose(nil), path.Poses[:min(n, len(path.Poses))]...)
			v.Fractions = append([]float32(nil), path.Fractions[:min(n, len(path.Fractions))]...)
			c.Paths = append(c.Paths, v)
		}
		n := take(len(o.Frame.Events))
		c.Contacts = append([]ContactEvent(nil), o.Frame.Events[:n]...)
	}
	for _, q := range o.Queries {
		if remaining == 0 {
			c.Omitted.Records += 1 + len(q.Hits)
			continue
		}
		// Query geometry is bounded independently before copying caller arrays.
		filters := len(q.Query.Filter.ExcludeBodies) + len(q.Query.Filter.ExcludeColliders) + len(q.Query.Filter.ExcludeOwners)
		if len(q.Query.Shape.Vertices) > vertices || len(q.Query.Shape.Indices)/3 > triangles || len(q.Query.Shape.Children) > 0 || filters >= remaining {
			c.Omitted.Records += 1 + len(q.Hits)
			continue
		}
		remaining--
		remaining -= filters
		vertices -= len(q.Query.Shape.Vertices)
		triangles -= len(q.Query.Shape.Indices) / 3
		n := take(len(q.Hits))
		q.Hits = append([]Hit(nil), q.Hits[:n]...)
		q.Query.Shape.Vertices = append([]Vec3(nil), q.Query.Shape.Vertices...)
		q.Query.Shape.Indices = append([]uint32(nil), q.Query.Shape.Indices...)
		q.Query.Filter.ExcludeBodies = append([]BodyHandle(nil), q.Query.Filter.ExcludeBodies...)
		q.Query.Filter.ExcludeColliders = append([]ColliderHandle(nil), q.Query.Filter.ExcludeColliders...)
		q.Query.Filter.ExcludeOwners = append([]int64(nil), q.Query.Filter.ExcludeOwners...)
		if q.Query.Filter.PhysicalFilter != nil {
			v := *q.Query.Filter.PhysicalFilter
			q.Query.Filter.PhysicalFilter = &v
		}
		c.Queries = append(c.Queries, q)
	}
	n := take(len(o.Targets))
	c.Targets = append([]DebugTarget(nil), o.Targets[:n]...)
	c.Incomplete = c.Omitted != (DebugOmissions{})
	return c.Validate()
}

// Validate rejects malformed geometry and documents beyond hard viewer limits.
func (c DebugCapture) Validate() error {
	bad := func() error { return fmt.Errorf("%w: physics debug capture", ErrInvalid) }
	if err := c.Config.Validate(); err != nil {
		return bad()
	}
	if c.Version != DebugVersion || len(c.Bodies) > 4096 || len(c.Colliders) > 8192 || len(c.Geometry) > 8192 || len(c.Fault) > 65536 || len(c.SourceSnapshot) > 4096 || len(c.Backend) > 4096 {
		return bad()
	}
	if c.Status != "accepted" && c.Status != "pending" && c.Status != "faulted" && c.Status != "unsynchronized" {
		return bad()
	}
	if c.FrameAccepted && c.Frame != c.AcceptedFrame {
		return bad()
	}
	o := c.Omitted
	if o.Bodies < 0 || o.Colliders < 0 || o.Geometry < 0 || o.Vertices < 0 || o.Triangles < 0 || o.Records < 0 || c.Incomplete != (o != (DebugOmissions{})) {
		return bad()
	}
	bodies := map[BodyHandle]bool{}
	colliders := map[ColliderHandle]bool{}
	poseOK := func(v Pose) bool { return v.Position.Valid() && v.Rotation.Valid() }
	for _, v := range c.Bodies {
		s := v.State
		if s.Handle == (BodyHandle{}) || bodies[s.Handle] || !poseOK(s.Pose) || !poseOK(s.Anchor) || !s.LinearVelocity.Valid() || !s.AngularVelocity.Valid() || !s.CenterOfMass.Valid() || !Finite(s.Mass) || len(s.ID) > 4096 {
			return bad()
		}
		bodies[s.Handle] = true
		if v.HasUncommitted != (v.Uncommitted != nil) {
			return bad()
		}
		if v.Uncommitted != nil && (v.Uncommitted.Handle != s.Handle || !poseOK(v.Uncommitted.Pose) || !poseOK(v.Uncommitted.Anchor) || !v.Uncommitted.LinearVelocity.Valid() || !v.Uncommitted.AngularVelocity.Valid() || !v.Uncommitted.CenterOfMass.Valid() || !Finite(v.Uncommitted.Mass) || c.Status == "accepted") {
			return bad()
		}
		if !s.Support.Point.Valid() || !s.Support.Normal.Valid() {
			return bad()
		}
	}
	for _, v := range c.Colliders {
		if v.Handle == (ColliderHandle{}) || colliders[v.Handle] || !bodies[v.Body] || len(v.ID) > 4096 {
			return bad()
		}
		colliders[v.Handle] = true
	}
	vertices, triangles, records := 0, 0, len(c.Contacts)+len(c.Targets)+len(c.Queries)+len(c.Paths)
	for _, g := range c.Geometry {
		vertices += len(g.Vertices)
		triangles += len(g.Indices) / 3
		if !colliders[g.Collider] || len(g.Indices)%3 != 0 || !g.PointA.Valid() || !g.PointB.Valid() || !Finite(g.Radius) {
			return bad()
		}
		switch g.Kind {
		case Sphere, Capsule:
			if g.Radius <= 0 || len(g.Vertices) != 0 || len(g.Indices) != 0 {
				return bad()
			}
		case Convex, TriangleMesh:
			if len(g.Vertices) < 3 || len(g.Indices) < 3 {
				return bad()
			}
		default:
			return bad()
		}
		for _, v := range g.Vertices {
			if !v.Valid() {
				return bad()
			}
		}
		for _, i := range g.Indices {
			if int(i) >= len(g.Vertices) {
				return bad()
			}
		}
	}
	for _, path := range c.Paths {
		records += len(path.Positions)
		if len(path.Poses) != 0 && len(path.Poses) != len(path.Positions) || len(path.Fractions) != 0 && len(path.Fractions) != len(path.Positions) {
			return bad()
		}
		for _, v := range path.Positions {
			if !v.Valid() {
				return bad()
			}
		}
		for _, v := range path.Poses {
			if !poseOK(v) {
				return bad()
			}
		}
		for _, v := range path.Fractions {
			if !Finite(v) || v < 0 || v > 1 {
				return bad()
			}
		}
	}
	for _, v := range c.Contacts {
		if !v.Point.Valid() || !v.Normal.Valid() || !v.Force.Valid() || !Finite(v.Depth) || !Finite(v.Fraction) {
			return bad()
		}
	}
	for _, q := range c.Queries {
		records += len(q.Hits)
		records += len(q.Query.Filter.ExcludeBodies) + len(q.Query.Filter.ExcludeColliders) + len(q.Query.Filter.ExcludeOwners)
		vertices += len(q.Query.Shape.Vertices)
		triangles += len(q.Query.Shape.Indices) / 3
		if len(q.Label) > 4096 || len(q.Error) > 65536 || !poseOK(q.Query.Pose) || !q.Query.Translation.Valid() || q.Query.Kind < Ray || q.Query.Kind > Overlap || len(q.Query.Shape.Children) > 0 {
			return bad()
		}
		if q.Query.Kind != Ray {
			if err := q.Query.Shape.Validate(); err != nil {
				return bad()
			}
		}
		if q.Result.Count < 0 || q.Result.Total < q.Result.Count || len(q.Hits) > q.Result.Count {
			return bad()
		}
		for _, h := range q.Hits {
			if !h.Point.Valid() || !h.Normal.Valid() || !Finite(h.Depth) || !Finite(h.Distance) || !Finite(h.Fraction) {
				return bad()
			}
		}
	}
	for _, v := range c.Targets {
		if !v.From.Valid() || !v.Target.Valid() || len(v.Label) > 4096 {
			return bad()
		}
	}
	if vertices > 200000 || triangles > 200000 || records > 65536 {
		return bad()
	}
	return nil
}

// WriteDebug writes validated versioned JSON with a hard byte limit.
func WriteDebug(w io.Writer, c DebugCapture) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(data))+1 > MaxDebugBytes {
		return fmt.Errorf("%w: debug byte limit", ErrInvalid)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ReadDebug bounds input bytes before parsing and rejects unknown/trailing JSON.
func ReadDebug(r io.Reader) (DebugCapture, error) {
	var c DebugCapture
	data, err := io.ReadAll(io.LimitReader(r, MaxDebugBytes+1))
	if err != nil {
		return c, err
	}
	if int64(len(data)) > MaxDebugBytes {
		return c, fmt.Errorf("%w: debug byte limit", ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return c, ErrInvalid
	}
	return c, c.Validate()
}
