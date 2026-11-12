package surface

// SupportIdentity optionally compares opaque support identity independently of
// position. SameSupport returns true only for valid handles in the same retained
// view and on the same convex support polygon. Straight progress within that
// polygon must preserve a valid corridor funnel. It is not a proximity or
// reachability test.
type SupportIdentity interface {
	SameSupport(Location) bool
}
