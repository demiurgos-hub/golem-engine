package box3d

import (
	"fmt"
	"math"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/character"
	b "github.com/gataengine/box3d-go"
)

type controllerGeometry struct {
	cfg      character.Config
	collider p.ColliderHandle
	pose     p.Pose
	center   p.Vec3 // body-origin local capsule centre
	filter   p.QueryFilter
}

// SetCharacterVelocity changes persistent controller intent without moving a body.
func (w *World) SetCharacterVelocity(h p.BodyHandle, velocity p.Vec3) error {
	if err := w.guard(); err != nil {
		return err
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	if !v.desc.Controller.Enabled {
		return p.ErrUnsupported
	}
	if !velocity.Valid() {
		return p.ErrInvalid
	}
	v.desc.Controller.Velocity = velocity
	w.mutation++
	return nil
}

func (w *World) controllerGeometry(h p.BodyHandle) (controllerGeometry, error) {
	return w.controllerGeometryAt(h, w.bodyState(h, w.bodyForHandle(h)).Pose)
}

func (w *World) controllerGeometryAt(h p.BodyHandle, bodyPose p.Pose) (controllerGeometry, error) {
	return w.controllerGeometryFor(h, bodyPose, w.bodyForHandle(h).desc.Controller)
}

func (w *World) controllerGeometryFor(h p.BodyHandle, bodyPose p.Pose, cfg p.CharacterControllerConfig) (controllerGeometry, error) {
	v := w.bodyForHandle(h)
	var result controllerGeometry
	count := 0
	for _, ch := range v.colliders {
		c := w.colliderForHandle(ch)
		if !c.desc.Enabled || !c.desc.Roles.Solid {
			continue
		}
		count++
		s := w.shapeForHandle(c.desc.Shape).desc
		pose := bodyPose.Compose(c.desc.LocalPose)
		up := pose.Rotation.Rotate(p.Vec3{Y: 1})
		if s.Kind != p.Capsule || s.PointA.X != s.PointB.X || s.PointA.Z != s.PointB.Z || math.Abs(float64(up.X))+math.Abs(float64(up.Z)) > 1e-5 || up.Y < .99999 {
			return result, fmt.Errorf("%w: controller requires one upright solid capsule", p.ErrUnsupported)
		}
		center := s.PointA.Add(s.PointB).Mul(.5)
		result.center = c.desc.LocalPose.Transform(center)
		pose.Position = pose.Transform(center)
		result.cfg = character.Config{Radius: s.Radius, Height: s.PointB.Sub(s.PointA).Length() + 2*s.Radius, StepHeight: cfg.StepHeight, GroundDistance: cfg.GroundDistance, MaxSlopeDegrees: cfg.MaxSlopeDegrees, Skin: cfg.Skin, MaxDepenetration: cfg.MaxDepenetration, Iterations: cfg.Iterations, MaxHits: cfg.MaxHits}
		result.collider, result.pose = ch, pose
		result.filter = p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &c.desc.Filter, ExcludeBodies: []p.BodyHandle{h}, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors}
	}
	if count != 1 {
		return result, fmt.Errorf("%w: controller requires exactly one solid collider", p.ErrUnsupported)
	}
	return result, nil
}

func (w *World) probeController(h p.BodyHandle) (controllerGeometry, character.Support, error) {
	v := w.bodyForHandle(h)
	g, err := w.controllerGeometry(h)
	if err != nil {
		return g, character.Support{}, err
	}
	probe := g.cfg
	if v.desc.Mode == p.DrivenCharacter {
		probe.Skin = max(probe.Skin, v.desc.Controller.SupportPenetration)
	}
	support, grounded, err := probe.Probe(w, g.pose, g.filter)
	if err != nil {
		return g, support, err
	}
	v.support = p.CharacterSupport{}
	if grounded {
		collider, ok := w.colliders[support.Collider]
		if !ok {
			return g, support, p.ErrStale
		}
		body, ok := w.bodies[collider.desc.Body]
		if !ok {
			return g, support, p.ErrStale
		}
		pose := w.bodyState(collider.desc.Body, body).Pose
		v.support = p.CharacterSupport{Grounded: true, Collider: support.Collider, Body: collider.desc.Body, Feature: support.Feature, Point: support.Point, LocalPoint: pose.Inverse().Transform(support.Point), Normal: support.Normal}
	}
	return g, support, nil
}

func (w *World) supportVelocity(v *body) p.Vec3 {
	if !v.desc.Controller.FollowSupport || !v.support.Grounded {
		return p.Vec3{}
	}
	c, ok := w.colliders[v.support.Collider]
	if !ok || c.desc.Body != v.support.Body {
		return p.Vec3{}
	}
	support, ok := w.bodies[v.support.Body]
	if !ok {
		return p.Vec3{}
	}
	point := w.bodyState(v.support.Body, support).Pose.Transform(v.support.LocalPoint)
	return pv(support.id.GetLinearVelocity()).Add(pv(support.id.GetAngularVelocity()).Cross(point.Sub(pp(support.id.GetWorldCenter()))))
}

func predictSupportPose(v *body, dt float32) p.Pose {
	pose := p.Pose{Position: pp(v.id.GetPosition()), Rotation: pq(v.id.GetRotation())}
	center := pp(v.id.GetWorldCenter())
	localCenter := pose.Inverse().Transform(center)
	omega := pv(v.id.GetAngularVelocity())
	dq := p.Quat{X: omega.X * dt * .5, Y: omega.Y * dt * .5, Z: omega.Z * dt * .5}.Mul(pose.Rotation)
	q := p.Quat{X: pose.Rotation.X + dq.X, Y: pose.Rotation.Y + dq.Y, Z: pose.Rotation.Z + dq.Z, W: pose.Rotation.W + dq.W}
	q = pq(b.NormalizeQuat(bq(q)))
	center = center.Add(pv(v.id.GetLinearVelocity()).Mul(dt))
	return p.Pose{Position: center.Sub(q.Rotate(localCenter)), Rotation: q}
}

type kinematicPath struct {
	body           p.BodyHandle
	points         []p.Vec3
	ends           []float32
	angular        p.Vec3
	poseTrajectory bool
}

func timedKinematicPath(h p.BodyHandle, points []p.Vec3, dt float32) kinematicPath {
	path := kinematicPath{body: h, points: points}
	var length float32
	for i := 1; i < len(points); i++ {
		length += points[i].Sub(points[i-1]).Length()
	}
	var elapsed float32
	for i := 1; i < len(points); i++ {
		if length > 0 {
			elapsed += dt * points[i].Sub(points[i-1]).Length() / length
		}
		if i == len(points)-1 || length == 0 {
			elapsed = dt
		}
		path.ends = append(path.ends, elapsed)
	}
	if len(path.ends) == 0 {
		path.points = append(path.points, points[0])
		path.ends = []float32{dt}
	}
	return path
}

func (path kinematicPath) velocity(at float32) p.Vec3 {
	start := float32(0)
	for i, end := range path.ends {
		if at < end && end > start {
			return path.points[i+1].Sub(path.points[i]).Mul(1 / (end - start))
		}
		start = end
	}
	return p.Vec3{}
}

func nextMotionBoundary(motions []p.Motion, at, end float32) float32 {
	for _, m := range motions {
		elapsed := float32(0)
		for _, segment := range m.Segments {
			elapsed += segment.Delta
			if elapsed > at && elapsed < end {
				end = elapsed
			}
		}
		elapsed = 0
		for _, segment := range m.PoseSegments {
			elapsed += segment.Delta
			if elapsed > at && elapsed < end {
				end = elapsed
			}
		}
	}
	return end
}

func (w *World) controllerPath(h p.BodyHandle, velocity p.Vec3, dt float32) (kinematicPath, map[p.BodyHandle]float32, error) {
	v := w.bodyForHandle(h)
	g, support, err := w.probeController(h)
	if err != nil {
		return kinematicPath{}, nil, err
	}
	if v.desc.Controller.Gravity {
		velocity.Y += v.fallSpeed
	}
	locomotion, err := g.cfg.Move(w, g.pose, velocity.Mul(dt), g.filter)
	if err != nil {
		return kinematicPath{}, nil, err
	}
	points := locomotion.Path
	var retainedEnds []float32
	clamps := map[p.BodyHandle]float32{}
	if v.support.Grounded && v.desc.Controller.FollowSupport {
		platform := w.bodyForHandle(v.support.Body)
		if platform == nil {
			return kinematicPath{}, nil, p.ErrStale
		}
		now := w.bodyState(v.support.Body, platform).Pose
		carry := predictSupportPose(platform, dt).Transform(v.support.LocalPoint).Sub(now.Transform(v.support.LocalPoint))
		if support.Distance > g.cfg.Skin+1e-5 {
			carry.Y -= support.Distance - g.cfg.Skin
		}
		if carry.Length() > 1e-6 {
			// Transport runs throughout each locomotion phase. Sequencing a whole
			// carry before walking would descend through the platform mid-frame.
			var length float32
			for i := 1; i < len(points); i++ {
				length += points[i].Position.Sub(points[i-1].Position).Length()
			}
			if len(points) == 1 {
				points = append(points, points[0])
			}
			cfg := g.cfg
			cfg.StepHeight = 0
			filter := g.filter
			filter.ExcludeBodies = append(append([]p.BodyHandle(nil), filter.ExcludeBodies...), v.support.Body)
			transported := []p.Pose{g.pose}
			var along float32
			var previous float32
			for i := 1; i < len(points); i++ {
				along += points[i].Position.Sub(points[i-1].Position).Length()
				fraction := float32(1)
				if length > 0 {
					fraction = along / length
				}
				target := points[i].Position.Add(carry.Mul(fraction))
				start := transported[len(transported)-1]
				resolved, e := cfg.Move(w, start, target.Sub(start.Position), filter)
				if e != nil {
					return kinematicPath{}, nil, e
				}
				transported = append(transported, resolved.Path[1:]...)
				phasePositions := make([]p.Vec3, len(resolved.Path))
				for j, pose := range resolved.Path {
					phasePositions[j] = pose.Position
				}
				phase := timedKinematicPath(h, phasePositions, (fraction-previous)*dt)
				if len(resolved.Path) == 1 {
					transported = append(transported, resolved.Pose)
				}
				for _, end := range phase.ends {
					retainedEnds = append(retainedEnds, previous*dt+end)
				}
				previous = fraction
				if resolved.Blocked {
					if platform.desc.Mode == p.Kinematic || platform.desc.Mode == p.ScriptedAction || platform.desc.Mode == p.ExternalPose {
						lost := target.Sub(resolved.Pose.Position).Dot(carry) / carry.Dot(carry)
						scale := max(float32(0), min(float32(1), 1-lost))
						if resolved.Pose.Position.Sub(start.Position).Length() <= max(1e-5, g.cfg.Skin*.1) {
							scale = 0
						}
						if scale < 1-1e-5 {
							clamps[v.support.Body] = scale
						}
					}
					break
				}
			}
			if previous < 1 {
				transported = append(transported, transported[len(transported)-1])
				retainedEnds = append(retainedEnds, dt)
			}
			points = transported
		}
	}
	positions := make([]p.Vec3, 0, len(points))
	for _, pose := range points {
		positions = append(positions, pose.Position.Sub(pose.Rotation.Rotate(g.center)))
	}
	if len(retainedEnds) > 0 {
		return kinematicPath{body: h, points: positions, ends: retainedEnds}, clamps, nil
	}
	return timedKinematicPath(h, positions, dt), clamps, nil
}

// Kinematic pairs have no finite-mass solver response. Admit their relative
// trajectories before integration, including a wall moving into an idle rider.
// Blocking stops the participants and their carried passengers coherently.
func (w *World) constrainControllerPairs(keys []p.BodyHandle, dt float32, poseBodies map[p.BodyHandle]bool) error {
	for pass := 0; pass < 8; pass++ {
		changed := false
		for _, key := range keys {
			v := w.bodyForHandle(key)
			if v.frozen || (!v.desc.Controller.Enabled && !poseBodies[key]) || v.desc.Mode == p.DrivenCharacter {
				continue
			}
			var geometries []controllerGeometry
			if v.desc.Controller.Enabled {
				g, err := w.controllerGeometry(key)
				if err != nil {
					return err
				}
				geometries = append(geometries, g)
			} else {
				for _, ch := range v.colliders {
					c := w.colliderForHandle(ch)
					if !c.desc.Enabled || !c.desc.Roles.Solid {
						continue
					}
					geometries = append(geometries, controllerGeometry{collider: ch, cfg: character.Config{Skin: .001, MaxHits: 16384}, filter: p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &c.desc.Filter, ExcludeBodies: []p.BodyHandle{key}, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors}})
				}
			}
			for _, g := range geometries {
				filter := g.filter
				var targets []ColliderMotion
				for _, other := range keys {
					if other == key {
						continue
					}
					z := w.bodyForHandle(other)
					if z.desc.Mode == p.FreeDynamic || z.desc.Mode == p.DrivenCharacter || v.support.Grounded && v.support.Body == other {
						filter.ExcludeBodies = append(filter.ExcludeBodies, other)
						continue
					}
					from := w.bodyState(other, z).Pose
					to := predictSupportPose(z, dt)
					for _, c := range z.colliders {
						targets = append(targets, ColliderMotion{c, from, to})
					}
				}
				from, to := w.bodyState(key, v).Pose, predictSupportPose(v, dt)
				hits := make([]p.Hit, g.cfg.MaxHits)
				r, err := w.QueryColliderMotion(MotionQuery{Motion: ColliderMotion{g.collider, from, to}, Targets: targets, Filter: filter, Mode: p.All, Initial: p.ReportInitial, Backfaces: p.IncludeBackfaces, Tolerance: .00001}, hits)
				if err != nil {
					return err
				}
				if r.Incomplete {
					return fmt.Errorf("physics3d: relative controller query overflow")
				}
				for _, hit := range hits[:r.Count] {
					if !hit.Blocking {
						continue
					}
					other := w.colliderForHandle(hit.Collider).desc.Body
					z := w.bodyForHandle(other)
					zfrom, zto := w.bodyState(other, z).Pose, predictSupportPose(z, dt)
					da := to.Transform(from.Inverse().Transform(hit.Point)).Sub(hit.Point)
					dz := zto.Transform(zfrom.Inverse().Transform(hit.Point)).Sub(hit.Point)
					closing := -da.Sub(dz).Dot(hit.Normal)
					if closing <= 1e-6 {
						continue
					}
					scale := max(float32(0), min(float32(1), hit.Fraction-g.cfg.Skin/closing))
					if scale >= 1-1e-5 {
						continue
					}
					v.id.SetLinearVelocity(b.MulSV(scale, v.id.GetLinearVelocity()))
					v.id.SetAngularVelocity(b.MulSV(scale, v.id.GetAngularVelocity()))
					if z.desc.Mode != p.Static {
						z.id.SetLinearVelocity(b.MulSV(scale, z.id.GetLinearVelocity()))
						z.id.SetAngularVelocity(b.MulSV(scale, z.id.GetAngularVelocity()))
						for _, passenger := range keys {
							rider := w.bodyForHandle(passenger)
							if passenger != key && rider.desc.Controller.Enabled && rider.desc.Mode != p.DrivenCharacter && rider.support.Grounded && rider.support.Body == other {
								rider.id.SetLinearVelocity(b.MulSV(scale, rider.id.GetLinearVelocity()))
							}
						}
					}
					changed = true
					break
				}
			}
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("physics3d: relative controller constraints did not converge")
}

func (w *World) integrateIntervals(f p.Frame, motions []p.Motion, keys []p.BodyHandle, paths []p.BodyPath, result *p.FrameResult) error {
	contactState := w.newMotionContactState()
	pathIndex := map[p.BodyHandle]int{}
	for i, h := range keys {
		pathIndex[h] = i
	}
	inputs := map[p.BodyHandle]p.Motion{}
	poseBodies := map[p.BodyHandle]bool{}
	for _, m := range motions {
		inputs[m.Body] = m
		poseBodies[m.Body] = len(m.PoseSegments) > 0
	}
	for _, key := range keys {
		v := w.bodyForHandle(key)
		if _, exists := inputs[key]; !exists && v.desc.Controller.Enabled {
			m := p.Motion{Body: key, Velocity: v.desc.Controller.Velocity, MaxDriveForce: v.desc.Controller.MaxDriveForce, SurfaceNormal: p.Vec3{Y: 1}, Frozen: v.frozen}
			motions = append(motions, m)
			inputs[key] = m
		}
	}
	h := f.Delta / float32(w.config.Substeps)
	for sub := 0; sub < w.config.Substeps; sub++ {
		begin, end := float32(sub)*h, float32(sub+1)*h
		if sub == w.config.Substeps-1 {
			end = f.Delta
		}
		drives := map[p.BodyHandle]p.Vec3{}
		for _, key := range keys {
			v := w.bodyForHandle(key)
			if v.frozen || !v.desc.Controller.Enabled {
				continue
			}
			if _, _, err := w.probeController(key); err != nil {
				return err
			}
			if v.desc.Controller.Gravity && v.desc.Mode != p.DrivenCharacter {
				if v.support.Grounded {
					v.fallSpeed = 0
				} else {
					v.fallSpeed += w.config.Gravity.Y * v.desc.GravityScale * (end - begin)
				}
			}
		}
		for _, m := range motions {
			v := w.bodyForHandle(m.Body)
			if m.Frozen || v.desc.Mode != p.DrivenCharacter {
				continue
			}
			velocity, normal := motionVelocity(m, begin, h)
			velocity = velocity.Add(w.supportVelocity(v))
			n := normal.Unit()
			if v.desc.Controller.Enabled {
				n = p.Vec3{Y: 1}
				if v.support.Grounded {
					n = v.support.Normal.Unit()
				}
			}
			if n.Length() == 0 {
				n = p.Vec3{Y: 1}
			}
			delta := velocity.Sub(pv(v.id.GetLinearVelocity()))
			delta = delta.Sub(n.Mul(delta.Dot(n)))
			force := delta.Mul(v.id.GetMass() / (end - begin))
			if length := force.Length(); length > m.MaxDriveForce {
				force = force.Mul(m.MaxDriveForce / length)
			}
			drives[m.Body] = force
		}
		for at := begin; at < end; {
			boundary := nextMotionBoundary(motions, at, end)
			for _, key := range keys {
				v := w.bodyForHandle(key)
				if v.frozen || !v.desc.Controller.Enabled {
					continue
				}
				if _, _, err := w.probeController(key); err != nil {
					return err
				}
				if v.support.Grounded && v.desc.Controller.FollowSupport {
					platform := w.bodyForHandle(v.support.Body)
					if speed := pv(platform.id.GetAngularVelocity()).Length(); speed > 0 {
						boundary = min(boundary, at+v.desc.Controller.MaxSupportRotation/speed)
					}
				}
			}
			dt := boundary - at
			if dt <= 0 || at+dt == at {
				return fmt.Errorf("physics3d: controller interval underflow")
			}
			var plans []kinematicPath
			for _, m := range motions {
				v := w.bodyForHandle(m.Body)
				if m.Frozen || v.desc.Controller.Enabled && !poseBodies[m.Body] || v.desc.Mode == p.DrivenCharacter || v.desc.Mode == p.FreeDynamic {
					continue
				}
				velocity, _ := motionVelocity(m, at, dt)
				angular := p.Vec3{}
				if poseBodies[m.Body] {
					velocity, angular = poseVelocity(m, at, dt, w.bodyState(m.Body, v).Pose)
				} else {
					var err error
					velocity, err = w.clipKinematic(m.Body, velocity, dt)
					if err != nil {
						return err
					}
				}
				v.id.SetLinearVelocity(bv(velocity))
				if poseBodies[m.Body] || v.desc.Mode == p.ScriptedAction {
					v.id.SetAngularVelocity(bv(angular))
				}
				from := w.bodyState(m.Body, v).Pose.Position
				plan := timedKinematicPath(m.Body, []p.Vec3{from, from.Add(velocity.Mul(dt))}, dt)
				plan.angular, plan.poseTrajectory = angular, poseBodies[m.Body]
				plans = append(plans, plan)
			}
			basePlans := len(plans)
			for pass := 0; ; pass++ {
				if pass == 8 {
					return fmt.Errorf("physics3d: passenger constraint did not converge")
				}
				plans = plans[:basePlans]
				clamps := map[p.BodyHandle]float32{}
				for _, key := range keys {
					v := w.bodyForHandle(key)
					if v.frozen || !v.desc.Controller.Enabled || v.desc.Mode == p.DrivenCharacter || poseBodies[key] {
						continue
					}
					velocity, _ := motionVelocity(inputs[key], at, dt)
					plan, blocked, err := w.controllerPath(key, velocity, dt)
					if err != nil {
						return err
					}
					if validate := inputs[key].Validate; validate != nil {
						anchor := w.bodyState(key, v).Pose.Rotation.Rotate(v.desc.Anchor.Position)
						for i := 1; i < len(plan.points); i++ {
							if validate(plan.points[i-1].Add(anchor), plan.points[i].Add(anchor)) {
								continue
							}
							found := false
							for _, rejected := range result.RejectedMotions {
								found = found || rejected.Body == key
							}
							if !found {
								result.RejectedMotions = append(result.RejectedMotions, p.MotionRejection{Body: key, Reason: "controller path rejected by captured safety"})
							}
							plan = timedKinematicPath(key, []p.Vec3{plan.points[0]}, dt)
							if v.support.Grounded && v.desc.Controller.FollowSupport {
								platform := w.bodyForHandle(v.support.Body)
								if platform.desc.Mode != p.Static && platform.desc.Mode != p.FreeDynamic && platform.desc.Mode != p.DrivenCharacter && (platform.id.GetLinearVelocity() != (b.Vec3{}) || platform.id.GetAngularVelocity() != (b.Vec3{})) {
									blocked[v.support.Body] = 0
								}
							}
							break
						}
					}
					plans = append(plans, plan)
					for body, scale := range blocked {
						if previous, ok := clamps[body]; !ok || scale < previous {
							clamps[body] = scale
						}
					}
				}
				if len(clamps) == 0 {
					break
				}
				for body, scale := range clamps {
					v := w.bodyForHandle(body)
					v.id.SetLinearVelocity(b.MulSV(scale, v.id.GetLinearVelocity()))
					v.id.SetAngularVelocity(b.MulSV(scale, v.id.GetAngularVelocity()))
					for i := 0; i < basePlans; i++ {
						if plans[i].body == body {
							plans[i].angular = plans[i].angular.Mul(scale)
							start := plans[i].points[0]
							for j := 1; j < len(plans[i].points); j++ {
								plans[i].points[j] = start.Add(plans[i].points[j].Sub(start).Mul(scale))
							}
						}
					}
				}
			}
			for phase := float32(0); phase < dt; {
				next := dt
				for _, plan := range plans {
					for _, stop := range plan.ends {
						if stop > phase && stop < next {
							next = stop
						}
					}
				}
				interval := next - phase
				if interval <= 0 || result.Intervals >= 8192 {
					return fmt.Errorf("physics3d: shared motion interval limit")
				}
				for _, plan := range plans {
					w.bodyForHandle(plan.body).id.SetLinearVelocity(bv(plan.velocity(phase)))
					if plan.poseTrajectory {
						w.bodyForHandle(plan.body).id.SetAngularVelocity(bv(plan.angular))
					}
				}
				if err := w.constrainControllerPairs(keys, interval, poseBodies); err != nil {
					return err
				}
				for _, key := range keys {
					v := w.bodyForHandle(key)
					for _, force := range v.forces {
						v.id.ApplyForce(bv(force.force), bp(force.point), true)
					}
					if v.torque != (p.Vec3{}) {
						v.id.ApplyTorque(bv(v.torque), true)
					}
					if force := drives[key]; force != (p.Vec3{}) {
						v.id.ApplyForceToCenter(bv(force), true)
					}
				}
				var contactRecords []motionContactRecord
				stepped := false
				observe := func(sample b.MotionStepSample) error {
					stepped = true
					contactState.prepareBodies(sample)
					result.CCDCandidatePairs += sample.SpeculativeCCD.Candidates
					result.CCDPairTests += sample.SpeculativeCCD.PairTests
					result.CCDPredictiveContacts += sample.SpeculativeCCD.Contacts
					result.TOIContinuationCandidates += sample.TOIContinuation.Candidates
					result.TOIContinuationPairTests += sample.TOIContinuation.PairTests
					result.TOIContinuationEvents += sample.TOIContinuation.Events
					result.TOIContinuationIslandBodies += sample.TOIContinuation.IslandBodies
					result.TOIContinuationContactRows += sample.TOIContinuation.ContactRows
					if !p.Finite(sample.ImpulseDuration) || sample.ImpulseDuration <= 0 || !p.Finite(sample.Delta) || sample.Delta < 0 {
						return fmt.Errorf("physics3d: invalid native observation duration")
					}
					fraction := (at + phase + sample.EndFraction*interval) / f.Delta
					for i, key := range keys {
						body := w.bodyForHandle(key)
						if body.desc.Enabled && body.desc.Mode != p.Static {
							if _, ok := contactState.bodies[body.id]; !ok {
								return fmt.Errorf("physics3d: native observation omitted body %v", key)
							}
						}
						state := contactState.bodyState(w, key, body)
						if !w.bodyInBounds(body, state.Pose) || !state.Anchor.Position.InBounds() || !state.LinearVelocity.Valid() || !state.AngularVelocity.Valid() {
							return fmt.Errorf("physics3d: body %v left supported state", key)
						}
						if len(paths[i].Positions) >= 16384 {
							return fmt.Errorf("physics3d: physical path capacity exceeded")
						}
						paths[i].Positions = append(paths[i].Positions, state.Anchor.Position)
						paths[i].Poses = append(paths[i].Poses, state.Pose)
						paths[i].Fractions = append(paths[i].Fractions, fraction)
					}
					for _, m := range motions {
						if m.Validate != nil {
							path := paths[pathIndex[m.Body]].Positions
							if !m.Validate(path[len(path)-2], path[len(path)-1]) {
								return fmt.Errorf("physics3d: captured safety failed after microstep")
							}
						}
						if m.ValidateBounds != nil {
							poses := paths[pathIndex[m.Body]].Poses
							low, high := p.AnchorMotionBounds(poses[len(poses)-2], poses[len(poses)-1], w.bodyForHandle(m.Body).desc.Anchor.Position)
							if !m.ValidateBounds(low, high) {
								return fmt.Errorf("physics3d: captured curved-anchor safety failed after microstep")
							}
						}
					}
					if w.config.Continuous && len(w.sensorColliders) > 0 || len(w.policyContacts) > 0 {
						contactRecords = append(contactRecords, motionContactRecord{sample: sample, values: contactState.capturePrepared(w, sample, fraction), start: (at + phase + sample.StartFraction*interval) / f.Delta, end: fraction})
					} else {
						result.Events = append(result.Events, contactState.observePrepared(w, sample, f.ID, result.Revision, fraction)...)
					}
					return nil
				}
				result.Intervals++
				options := b.ObservedStepOptions{VelocityIterations: w.config.SolverVelocityIterations, RelaxIterations: w.config.SolverRelaxIterations}
				err := w.backend.StepObserved(interval, options, observe)
				if stepped {
					result.Microsteps++
					result.Elapsed += interval
				}
				if err != nil {
					return fmt.Errorf("physics3d: native step: %w", err)
				}
				for _, record := range contactRecords {
					events, err := contactState.replay(w, record, f.ID, result.Revision)
					if err != nil {
						return err
					}
					result.Events = append(result.Events, events...)
				}
				phase = next
			}
			for _, key := range keys {
				if w.bodyForHandle(key).desc.Controller.Enabled && !w.bodyForHandle(key).frozen {
					if _, _, err := w.probeController(key); err != nil {
						return err
					}
				}
			}
			at = boundary
		}
	}
	return nil
}
