package physics3d

// PlacementValidator optionally validates a body's full solid volume at a candidate
// body-origin pose. Call after Synchronize. It must not mutate pose or momentum;
// incomplete queries and unsupported geometry return errors, never clearance.
// Max penetration is a geometric admission tolerance, independent of solver slop.
type PlacementValidator interface {
	ValidatePlacement(body BodyHandle, pose Pose, maxPenetration float32) (bool, error)
}
