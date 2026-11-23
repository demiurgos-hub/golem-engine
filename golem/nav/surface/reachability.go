package surface

// ReachabilityQuery optionally reports in constant time whether any route can
// join two locations of one retained view, comparing connected regions computed
// in advance as A* IsPathPossible does. Complete means possibly reachable,
// because regions treat directed links as two-way and ignore filters.
// Unreachable proves no route exists. Unloaded means the regions differ but may
// meet through unloaded data. Released or foreign handles return Stale. It runs
// no search and allocates nothing.
type ReachabilityQuery interface {
	PathPossible(from, to Location) Status
}
