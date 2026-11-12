package golem

import (
	"math"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PhysicsTrajectoryCheckpoint preserves active serializable intent without
// runtime body handles or cancellation tokens. Restore issues a fresh token.
type PhysicsTrajectoryCheckpoint struct {
	EntityID int64
	Request  PhysicsTrajectory
	Start    p.Pose
	Elapsed  float32
	Status   PhysicsTrajectoryStatus
}

func (ps *physicsService) captureTrajectories() []PhysicsTrajectoryCheckpoint {
	var result []PhysicsTrajectoryCheckpoint
	for _, id := range ps.ordered() {
		a := ps.trajectories[id]
		if !a.active() {
			continue
		}
		request := a.request
		request.Segments = append([]PhysicsTrajectorySegment(nil), request.Segments...)
		result = append(result, PhysicsTrajectoryCheckpoint{EntityID: id, Request: request, Start: a.start, Elapsed: a.state.Elapsed, Status: a.state.Status})
	}
	return result
}

func validateTrajectoryCheckpoints(records []PhysicsTrajectoryCheckpoint, bindings []PhysicsAttachmentCheckpoint, bodies map[p.ContentID]p.BodyDesc, hasNavigation bool) error {
	if len(records) > len(bindings) {
		return p.ErrInvalid
	}
	owners := map[int64]p.BodyDesc{}
	for _, binding := range bindings {
		owners[binding.EntityID] = bodies[binding.BodyID]
	}
	seen := map[int64]bool{}
	segments := 0
	for _, record := range records {
		duration, err := record.Request.validate(record.Start)
		body, exists := owners[record.EntityID]
		segments += len(record.Request.Segments)
		if err != nil || !exists || seen[record.EntityID] || segments > 1_000_000 || body.Mode != p.ScriptedAction && body.Mode != p.ExternalPose || !p.Finite(record.Elapsed) || record.Elapsed < 0 || record.Elapsed >= duration || record.Status != PhysicsTrajectoryRunning && record.Status != PhysicsTrajectoryPaused || hasNavigation && record.Request.SafetyProfile == "" || !record.Request.contains(body.Pose.Compose(body.Anchor).Position) {
			return p.ErrInvalid
		}
		seen[record.EntityID] = true
	}
	return nil
}

func (ps *physicsService) restoreTrajectoryCandidates(records []PhysicsTrajectoryCheckpoint, agents map[int64]*physicsAttachment, bindings []PhysicsAttachmentCheckpoint, prepared p.PreparedRestore) (map[int64]*physicsTrajectory, uint64, error) {
	result := map[int64]*physicsTrajectory{}
	generation := ps.trajectoryGeneration
	capability, ok := prepared.(p.PoseMotionWorld)
	if len(records) > 0 {
		if !ok || !capability.SupportsPoseMotion() {
			return nil, 0, p.ErrUnsupported
		}
	}
	identities := map[int64]p.ContentID{}
	for _, binding := range bindings {
		identities[binding.EntityID] = binding.BodyID
	}
	for _, record := range records {
		a := agents[record.EntityID]
		if a == nil || generation == math.MaxUint64 {
			return nil, 0, p.ErrInvalid
		}
		generation++
		request := record.Request
		request.Segments = append([]PhysicsTrajectorySegment(nil), request.Segments...)
		duration, err := request.validate(record.Start)
		if err != nil {
			return nil, 0, err
		}
		state, exists := prepared.Body(identities[record.EntityID])
		if !exists {
			return nil, 0, p.ErrStale
		}
		from := state.Pose
		var elapsed float32
		var segments []p.PoseMotionSegment
		for _, segment := range request.Segments {
			end := elapsed + segment.Duration
			if record.Elapsed < end {
				target := segment.Target.Compose(a.localAnchor.Inverse())
				segments = append(segments, p.PoseMotionSegment{From: from, Target: target, Delta: end - max(elapsed, record.Elapsed)})
				from = target
			}
			elapsed = end
		}
		if err := capability.ValidatePoseMotion(a.body, segments); err != nil {
			return nil, 0, err
		}
		result[record.EntityID] = &physicsTrajectory{request: request, start: record.Start, state: PhysicsTrajectoryState{Token: PhysicsTrajectoryToken{EntityID: record.EntityID, Generation: generation, Body: a.body}, Status: record.Status, Elapsed: record.Elapsed, Duration: duration}}
	}
	return result, generation, nil
}
