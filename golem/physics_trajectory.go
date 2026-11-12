package golem

import (
	"errors"
	"fmt"
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PhysicsTrajectorySegment targets a gameplay-anchor pose over Duration seconds.
// Rotation follows the shortest arc; movement remains collision constrained.
type PhysicsTrajectorySegment struct {
	Target   p.Pose
	Duration float32
}

// PhysicsTrajectory owns a bounded, copied sequence of timed anchor poses.
// Bounds contain every anchor position. SafetyProfile selects regional quarantine
// for bodies without navigation attachments; omit it when navigation is absent.
type PhysicsTrajectory struct {
	Segments      []PhysicsTrajectorySegment
	Min, Max      p.Vec3
	SafetyProfile surface.ProfileID
}

// PhysicsTrajectoryToken rejects delayed cancellation after replacement,
// attachment removal, pooled-body reuse or checkpoint restoration.
type PhysicsTrajectoryToken struct {
	EntityID   int64
	Generation uint64
	Body       p.BodyHandle
}

// PhysicsTrajectoryStatus distinguishes running, quarantined and terminal intent.
type PhysicsTrajectoryStatus uint8

const (
	PhysicsTrajectoryRunning PhysicsTrajectoryStatus = iota + 1
	PhysicsTrajectoryPaused
	PhysicsTrajectoryCompleted
	PhysicsTrajectoryBlocked
	PhysicsTrajectoryCancelled
)

// PhysicsTrajectoryState reports accepted progress, never speculative time.
type PhysicsTrajectoryState struct {
	Token             PhysicsTrajectoryToken
	Status            PhysicsTrajectoryStatus
	Elapsed, Duration float32
	Reason            string
}

type physicsTrajectory struct {
	request PhysicsTrajectory
	start   p.Pose
	state   PhysicsTrajectoryState
}

func (a *physicsTrajectory) active() bool {
	return a != nil && (a.state.Status == PhysicsTrajectoryRunning || a.state.Status == PhysicsTrajectoryPaused)
}
func (a PhysicsTrajectory) contains(v p.Vec3) bool {
	return v.X >= a.Min.X && v.Y >= a.Min.Y && v.Z >= a.Min.Z && v.X <= a.Max.X && v.Y <= a.Max.Y && v.Z <= a.Max.Z
}
func (a PhysicsTrajectory) validate(start p.Pose) (float32, error) {
	if !start.Valid() || !a.Min.InBounds() || !a.Max.InBounds() || a.Min.X > a.Max.X || a.Min.Y > a.Max.Y || a.Min.Z > a.Max.Z || !a.contains(start.Position) || len(a.Segments) < 1 || len(a.Segments) > 4096 || len(a.SafetyProfile) > 256 {
		return 0, p.ErrInvalid
	}
	var total float32
	for _, segment := range a.Segments {
		if !segment.Target.Valid() || !a.contains(segment.Target.Position) || !p.Finite(segment.Duration) || segment.Duration < .00001 || segment.Duration > 30 {
			return 0, p.ErrInvalid
		}
		total += segment.Duration
	}
	if !p.Finite(total) || total > 60 {
		return 0, p.ErrInvalid
	}
	return total, nil
}

// StartPhysicsTrajectory replaces scripted/external movement on the tick owner.
// It stops navigation destination ownership while retaining support and safety.
// At most 4,096 segments and 60 seconds are admitted; caller slices are copied.
// Box3D accepts convex/compound bodies with continuous motion enabled. Upright
// capsule controllers retain their orientation. Blocked actions keep actual
// physical readback; this method does not teleport or choose client authority.
func (s *Server) StartPhysicsTrajectory(id int64, request PhysicsTrajectory) (PhysicsTrajectoryToken, error) {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	if s.navigation != nil && s.navigation.frame != nil {
		return PhysicsTrajectoryToken{}, p.ErrBusy
	}
	a := ps.agents[id]
	if a == nil {
		return PhysicsTrajectoryToken{}, p.ErrStale
	}
	state, err := ps.world.Body(a.body)
	if err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	if state.Mode != p.ScriptedAction && state.Mode != p.ExternalPose {
		return PhysicsTrajectoryToken{}, p.ErrUnsupported
	}
	capability, ok := ps.world.(p.PoseMotionWorld)
	if !ok || !capability.SupportsPoseMotion() {
		return PhysicsTrajectoryToken{}, p.ErrUnsupported
	}
	duration, err := request.validate(state.Anchor)
	if err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	segments := make([]p.PoseMotionSegment, 0, len(request.Segments))
	from := state.Pose
	for _, segment := range request.Segments {
		target := segment.Target.Compose(a.localAnchor.Inverse())
		low, high := p.AnchorMotionBounds(from, target, a.localAnchor.Position)
		if !request.contains(low) || !request.contains(high) {
			return PhysicsTrajectoryToken{}, fmt.Errorf("%w: trajectory bounds omit a rotating anchor arc", p.ErrInvalid)
		}
		segments = append(segments, p.PoseMotionSegment{From: from, Target: target, Delta: segment.Duration})
		from = target
	}
	if err = ps.world.Synchronize(); err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	if err = capability.ValidatePoseMotion(a.body, segments); err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	if n := s.navigation; n != nil {
		if agent := n.agents[id]; agent != nil {
			if !agent.config.StagedPhysics {
				return PhysicsTrajectoryToken{}, p.ErrUnsupported
			}
			if request.SafetyProfile != "" && request.SafetyProfile != agent.config.Profile {
				return PhysicsTrajectoryToken{}, p.ErrInvalid
			}
			request.SafetyProfile = agent.config.Profile
		} else if request.SafetyProfile == "" {
			return PhysicsTrajectoryToken{}, fmt.Errorf("%w: trajectory requires a navigation safety profile", p.ErrInvalid)
		}
	}
	if ps.trajectoryGeneration == math.MaxUint64 {
		return PhysicsTrajectoryToken{}, p.ErrInvalid
	}
	generation := ps.trajectoryGeneration
	if err = ps.world.SetVelocity(a.body, p.Vec3{}, p.Vec3{}); err != nil {
		return PhysicsTrajectoryToken{}, err
	}
	if writer, ok := ps.world.(p.CharacterVelocityWriter); ok {
		if err = writer.SetCharacterVelocity(a.body, p.Vec3{}); err != nil && !errors.Is(err, p.ErrUnsupported) {
			return PhysicsTrajectoryToken{}, err
		}
	}
	if n := s.navigation; n != nil && n.agents[id] != nil {
		if err = s.StopNavigation(id); err != nil {
			return PhysicsTrajectoryToken{}, err
		}
	}
	current, readErr := ps.world.Body(a.body)
	if ps.agents[id] != a || ps.trajectoryGeneration != generation || readErr != nil || current.Anchor != state.Anchor {
		return PhysicsTrajectoryToken{}, p.ErrStale
	}
	ps.trajectoryGeneration++
	token := PhysicsTrajectoryToken{EntityID: id, Generation: ps.trajectoryGeneration, Body: a.body}
	request.Segments = append([]PhysicsTrajectorySegment(nil), request.Segments...)
	if ps.trajectories == nil {
		ps.trajectories = map[int64]*physicsTrajectory{}
	}
	ps.trajectories[id] = &physicsTrajectory{request: request, start: state.Anchor, state: PhysicsTrajectoryState{Token: token, Status: PhysicsTrajectoryRunning, Duration: duration}}
	return token, nil
}

// CancelPhysicsTrajectory stops future ownership without rewinding any body or
// removing momentum already transferred to other bodies. Stale tokens fail.
func (s *Server) CancelPhysicsTrajectory(token PhysicsTrajectoryToken) error {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	a := ps.trajectories[token.EntityID]
	if a == nil || a.state.Token != token || ps.agents[token.EntityID] == nil || ps.agents[token.EntityID].body != token.Body {
		return p.ErrStale
	}
	if !a.active() {
		return nil
	}
	if err := ps.world.SetVelocity(token.Body, p.Vec3{}, p.Vec3{}); err != nil {
		return err
	}
	a.state.Status, a.state.Reason = PhysicsTrajectoryCancelled, "cancelled"
	return nil
}

// PhysicsTrajectoryState returns a copied state on the tick owner.
func (s *Server) PhysicsTrajectoryState(id int64) (PhysicsTrajectoryState, error) {
	if s.physics == nil || s.physics.trajectories[id] == nil {
		return PhysicsTrajectoryState{}, p.ErrStale
	}
	return s.physics.trajectories[id].state, nil
}

type physicsTrajectoryFrame struct {
	action         *physicsTrajectory
	start          p.Vec3
	expected       p.Pose
	elapsed        float32
	frozen         bool
	validate       func(p.Vec3, p.Vec3) bool
	validateBounds func(p.Vec3, p.Vec3) bool
	rotating       bool
}

func physicsInterpolatePose(a, z p.Pose, f float32) p.Pose {
	q := z.Rotation.Mul(a.Rotation.Conjugate())
	if q.W < 0 {
		q = p.Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: -q.W}
	}
	v := p.Vec3{X: q.X, Y: q.Y, Z: q.Z}
	rotation := a.Rotation
	if sine := v.Length(); sine > 1e-8 {
		angle := float32(math.Atan2(float64(sine), float64(q.W))) * f
		axis := v.Mul(float32(math.Sin(float64(angle))) / sine)
		rotation = p.Quat{X: axis.X, Y: axis.Y, Z: axis.Z, W: float32(math.Cos(float64(angle)))}.Mul(a.Rotation)
	}
	return p.Pose{Position: a.Position.Add(z.Position.Sub(a.Position).Mul(f)), Rotation: rotation}
}

func (ps *physicsService) prepareTrajectory(id int64, state p.BodyState, f *NavigationFrame, dt float32) (p.Motion, *physicsTrajectoryFrame, error) {
	a := ps.trajectories[id]
	if !a.active() {
		return p.Motion{}, nil, nil
	}
	if a.state.Token.Body != state.Handle {
		return p.Motion{}, nil, p.ErrStale
	}
	ready := true
	var safety surface.RegionalSafety
	if n := ps.server.navigation; n != nil && !f.Contains(id) {
		safety = n.regionalSafety(a.request.SafetyProfile)
		ready = !n.navigationFrameBarrier() && n.backend.CurrentRevision(a.request.SafetyProfile) != 0
		if safety == nil {
			ready = ready && !n.pending(a.request.SafetyProfile)
		} else {
			ready = ready && safety.SafeSegment(physicsNavPoint(state.Anchor.Position), physicsNavPoint(state.Anchor.Position))
		}
	}
	if f.Contains(id) {
		ready = !f.Frozen(id)
	}
	start := state.Anchor.Position
	validate := func(from, to p.Vec3) bool {
		if !ready {
			return from == start && to == start
		}
		if !a.request.contains(from) || !a.request.contains(to) {
			return false
		}
		if f.Contains(id) {
			return f.SafeSegment(id, physicsNavPoint(from), physicsNavPoint(to))
		}
		return safety == nil || safety.SafeSegment(physicsNavPoint(from), physicsNavPoint(to))
	}
	plan := &physicsTrajectoryFrame{action: a, start: state.Anchor.Position, expected: state.Anchor, elapsed: a.state.Elapsed, frozen: !ready, validate: validate}
	validateBounds := func(low, high p.Vec3) bool {
		if !ready {
			return low == start && high == start
		}
		if !a.request.contains(low) || !a.request.contains(high) {
			return false
		}
		if f.Contains(id) {
			return f.SafeBounds(id, physicsNavPoint(low), physicsNavPoint(high))
		}
		return safety == nil || safety.SafeBounds(physicsNavPoint(low), physicsNavPoint(high))
	}
	plan.validateBounds = validateBounds
	m := p.Motion{Body: state.Handle, Frozen: !ready, Validate: validate, ValidateBounds: validateBounds}
	if !ready {
		return m, plan, nil
	}
	remaining := min(dt, a.state.Duration-a.state.Elapsed)
	previous := a.start
	offset := float32(0)
	from := state.Pose
	for _, segment := range a.request.Segments {
		end := offset + segment.Duration
		if a.state.Elapsed >= end {
			offset = end
			previous = segment.Target
			continue
		}
		within := max(float32(0), plan.elapsed-offset)
		part := min(remaining, segment.Duration-within)
		if part <= 0 {
			break
		}
		anchor := physicsInterpolatePose(previous, segment.Target, min(float32(1), (within+part)/segment.Duration))
		target := anchor.Compose(ps.agents[id].localAnchor.Inverse())
		low, high := p.AnchorMotionBounds(from, target, ps.agents[id].localAnchor.Position)
		if !validate(from.Compose(ps.agents[id].localAnchor).Position, anchor.Position) || !validateBounds(low, high) {
			plan.frozen = true
			plan.elapsed = a.state.Elapsed
			m.Frozen = true
			m.PoseSegments = nil
			return m, plan, nil
		}
		plan.rotating = plan.rotating || from.Rotation != target.Rotation
		m.PoseSegments = append(m.PoseSegments, p.PoseMotionSegment{From: from, Target: target, Delta: part})
		plan.elapsed += part
		plan.expected = anchor
		remaining -= part
		from = target
		previous = segment.Target
		offset = end
		if remaining <= 0 {
			break
		}
	}
	return m, plan, nil
}

func (ps *physicsService) validateTrajectoryResults(plans map[int64]*physicsTrajectoryFrame, states map[p.BodyHandle]p.BodyState, paths map[p.BodyHandle][]surface.Vec3, physicalPaths map[p.BodyHandle]p.BodyPath) error {
	for id, plan := range plans {
		if ps.trajectories[id] != plan.action || ps.agents[id] == nil || ps.agents[id].body != plan.action.state.Token.Body {
			return p.ErrStale
		}
		state, ok := states[plan.action.state.Token.Body]
		if !ok {
			return p.ErrInvalid
		}
		path := paths[state.Handle]
		if len(path) == 0 || len(path) > 16384 || physicsPoint(path[0]) != plan.start || physicsPoint(path[len(path)-1]) != state.Anchor.Position {
			return p.ErrInvalid
		}
		for i := 1; i < len(path); i++ {
			if !plan.validate(physicsPoint(path[i-1]), physicsPoint(path[i])) {
				return fmt.Errorf("physics trajectory left captured safety")
			}
		}
		poses := physicalPaths[state.Handle].Poses
		if plan.rotating && len(poses) != len(path) {
			return p.ErrInvalid
		}
		if len(poses) > 0 {
			if len(poses) != len(path) {
				return p.ErrInvalid
			}
			local := ps.agents[id].localAnchor
			for i, pose := range poses {
				if !pose.Valid() || pose.Compose(local).Position.Sub(physicsPoint(path[i])).Length() > 1e-5 {
					return p.ErrInvalid
				}
				if i > 0 {
					low, high := p.AnchorMotionBounds(poses[i-1], pose, local.Position)
					if !plan.validateBounds(low, high) {
						return fmt.Errorf("rotating trajectory left captured safety")
					}
				}
			}
		}
	}
	return nil
}

func (ps *physicsService) acceptTrajectoryResults(plans map[int64]*physicsTrajectoryFrame, states map[p.BodyHandle]p.BodyState, rejected map[p.BodyHandle]string) {
	for _, plan := range plans {
		a := plan.action
		if plan.frozen {
			a.state.Status = PhysicsTrajectoryPaused
			continue
		}
		state := states[a.state.Token.Body]
		q := state.Anchor.Rotation.Mul(plan.expected.Rotation.Conjugate())
		if reason := rejected[state.Handle]; reason != "" {
			a.state.Status, a.state.Reason = PhysicsTrajectoryBlocked, reason
		} else if state.Anchor.Position.Sub(plan.expected.Position).Length() > .002 || math.Abs(float64(q.W)) < .999999 {
			a.state.Status, a.state.Reason = PhysicsTrajectoryBlocked, "physical path was obstructed"
		} else {
			a.state.Elapsed, a.state.Status = plan.elapsed, PhysicsTrajectoryRunning
			if a.state.Duration-a.state.Elapsed < 1e-6 {
				a.state.Elapsed, a.state.Status = a.state.Duration, PhysicsTrajectoryCompleted
			}
		}
	}
}
