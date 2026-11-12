package surface

import "context"

// CornerAppender optionally copies route corners into caller-owned capacity.
// Returned links are independently owned; appending never modifies links from
// an earlier result. The caller may reuse its own discarded result's slice.
type CornerAppender interface {
	AppendCorners([]Corner) []Corner
}

// RegionalBackend exposes an immutable, coherent capture of committed geometry
// and desired changes. Safety must be recaptured after a host callback that can
// change geometry and before accepting movement or asynchronous query results.
type RegionalBackend interface {
	Safety(ProfileID) RegionalSafety
}

// RegionalSafety qualifies retained handles against one committed snapshot and
// its pending quarantine. An older handle is safe when its dependencies remain
// unchanged. Checks return false for unknown, foreign, or released handles and
// invalid coordinates. Captures remain immutable after later publication.
type RegionalSafety interface {
	CommittedRevision() uint64
	DesiredRevision() uint64
	// Token changes whenever captured safety changes, including publication,
	// desired edits and retirement of a protected old view. It is meaningful
	// only for captures from the same backend/profile.
	Token() uint64
	SafeLocation(Location) bool
	SafeRoute(Route) bool
	// SafeSegment checks the whole swept center segment against conservative
	// profile-clearance quarantine. It is not a collision or walkability test.
	SafeSegment(from, to Vec3) bool
	// SafeBounds checks a consumer-declared traversal volume, including every
	// point of an off-mesh action rather than only its endpoints.
	SafeBounds(min, max Vec3) bool
}

// RetainedRegionalSafety optionally permits bounded rebasing while unaffected
// agents continue using retained old views. When ProtectsRetiredViews is true,
// all checks quarantine the union of changed columns since every still-retained
// generation, including bake/clearance influence. In particular, segment and
// bounds checks protect deviations from an old route after newer publication.
// Releasing the last relevant old view changes Token. Captures stay immutable.
type RetainedRegionalSafety interface {
	RegionalSafety
	ProtectsRetiredViews() bool
	// HasQuarantine can remain true with equal committed/desired revisions
	// while old views drain, or while published columns remain unloaded.
	HasQuarantine() bool
}

// ViewRegionalSafety optionally narrows retirement quarantine to one retained
// view. Pending edits and unavailable columns still apply to every view. The
// returned immutable capture accepts handles only from the selected generation;
// unknown, foreign or released views produce a capture that rejects all checks.
// Selection is cached by generation and token, not by agent. Hosts must recapture
// and reselect after publication or callbacks that can change geometry or views.
type ViewRegionalSafety interface {
	RetainedRegionalSafety
	ForView(View) RegionalSafety
}

// RebaseResult transfers retained support and an optional corridor into the
// query's view. A partial route does not mean the requested goal was reached.
type RebaseResult struct {
	Status
	Location Location
	Route    Route
}

// RebaseQuery transfers unchanged support without selecting a different floor.
// The old view must remain retained until the result has been accepted. A nil
// route transfers only support. Actual position must remain within tolerance of
// that exact support polygon; changed support returns Stale or NoSurface.
// Corridor repair may change corners or trim a suffix and return Partial. Use
// CorridorTransferQuery when preserving the entire corridor and its cursor.
type RebaseQuery interface {
	RebasePath(context.Context, Route, Location, Vec3, float64, Filter) RebaseResult
}

// CorridorTransferQuery optionally transfers an entire unchanged corridor to
// the query's view without repairing, trimming or recomputing corners. Geometry,
// corner order and partial status are preserved, so the caller keeps its corner
// cursor. Changed dependencies or unsupported body positions reject transfer;
// callers can then choose RebaseQuery's repair policy. Retain the old view until
// accepting the result. A nil route transfers only exact support.
type CorridorTransferQuery interface {
	TransferPath(context.Context, Route, Location, Vec3, float64, Filter) RebaseResult
}
