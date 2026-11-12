package physics3d

import "sort"

type QueryKind uint8

const (
	Ray QueryKind = iota + 1
	Sweep
	Overlap
)

type QueryMode uint8

const (
	Nearest QueryMode = iota
	Any
	All
)

type InitialOverlapPolicy uint8

const (
	ReportInitial InitialOverlapPolicy = iota
	IgnoreInitial
	BlockInitial
)

type BackfacePolicy uint8

const (
	CullBackfaces BackfacePolicy = iota
	IncludeBackfaces
)

type SensorPolicy uint8

const (
	ExcludeSensors SensorPolicy = iota
	IncludeSensors
	OnlySensors
)

type QueryFilter struct {
	Mask             uint64
	Sensors          SensorPolicy
	RequireSolid     bool
	IncludeNonQuery  bool // reserved for physical movement/support policy
	ExcludeBodies    []BodyHandle
	ExcludeColliders []ColliderHandle
	ExcludeOwners    []int64
}
type Query struct {
	Kind        QueryKind
	Mode        QueryMode
	Shape       ShapeDesc
	Pose        Pose
	Translation Vec3
	Filter      QueryFilter
	Initial     InitialOverlapPolicy
	Backfaces   BackfacePolicy
}

// Feature identifies a child and/or triangle within the exact collider.
// Valid distinguishes unavailable feature information from feature zero.
type Feature struct {
	Child, Triangle uint32
	Valid           bool
}
type Hit struct {
	Collider                                         ColliderHandle
	Body                                             BodyHandle
	Owner                                            int64
	Content                                          ContentID
	Feature                                          Feature
	Point, Normal, SurfaceNormal                     Vec3
	HasSurfaceNormal                                 bool
	Distance, Fraction, Depth                        float32
	Initial, Touching, Penetrating, Sensor, Blocking bool
	Revision                                         uint64
}

// QueryResult never interprets a full buffer as clear. Total is the number of
// matching hits; Count is the number written. No result slice aliases backend scratch.
type QueryResult struct {
	Count, Total        int
	Incomplete, Blocked bool
	Revision            uint64
}
type PenetrationResult struct {
	A, B                   ColliderHandle
	Normal, PointA, PointB Vec3
	Depth                  float32
	Touching, Penetrating  bool
	FeatureA, FeatureB     Feature
	Revision               uint64
}

// SortHits orders by distance, collider generation and exact feature identity.
func SortHits(h []Hit) {
	sort.SliceStable(h, func(i, j int) bool {
		return HitLess(h[i], h[j])
	})
}

// HitLess compares hits using the distance and identity ordering of SortHits.
func HitLess(a, b Hit) bool {
	if a.Distance != b.Distance {
		return a.Distance < b.Distance
	}
	if a.Collider != b.Collider {
		return lessHandle(a.Collider, b.Collider)
	}
	if a.Feature.Child != b.Feature.Child {
		return a.Feature.Child < b.Feature.Child
	}
	return a.Feature.Triangle < b.Feature.Triangle
}

// AggregateOwners retains the first ordered hit per nonzero owner; unowned
// geometry remains distinct by collider. The caller chooses this loss of detail.
func AggregateOwners(hits []Hit, dst []Hit) QueryResult {
	seen := map[int64]bool{}
	colliders := map[ColliderHandle]bool{}
	n := 0
	for _, h := range hits {
		if h.Owner != 0 {
			if seen[h.Owner] {
				continue
			}
			seen[h.Owner] = true
		} else {
			if colliders[h.Collider] {
				continue
			}
			colliders[h.Collider] = true
		}
		if n < len(dst) {
			dst[n] = h
		}
		n++
	}
	return QueryResult{Count: min(n, len(dst)), Total: n, Incomplete: n > len(dst)}
}
