package box3d

import (
	"math"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// SupportsPoseMotion reports continuous relative-motion trajectory admission.
func (w *World) SupportsPoseMotion() bool { return w.config.Continuous }

// ValidatePoseMotion checks supported geometry and authority before intent is
// installed. Obstruction remains a swept frame result. Concave trajectory bodies
// require a separate transport policy and are explicitly unsupported here.
func (w *World) ValidatePoseMotion(h p.BodyHandle, segments []p.PoseMotionSegment) error {
	if err := w.queryReady(); err != nil {
		return err
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	if !w.config.Continuous || (v.desc.Mode != p.ScriptedAction && v.desc.Mode != p.ExternalPose) || len(segments) == 0 || len(segments) > 4096 {
		return p.ErrUnsupported
	}
	for _, ch := range v.colliders {
		if containsMesh(w.shapes[w.colliders[ch].desc.Shape].desc) {
			return p.ErrUnsupported
		}
	}
	pose := w.bodyState(h, v).Pose
	for _, segment := range segments {
		if !segment.From.Valid() || !segment.Target.Valid() || !p.Finite(segment.Delta) || segment.Delta <= 0 || segment.From.Position.Sub(pose.Position).Length() > 1e-5 || segment.From.Rotation != pose.Rotation || !w.bodyInBounds(v, segment.Target) {
			return p.ErrInvalid
		}
		if v.desc.Controller.Enabled {
			if segment.Target.Rotation != segment.From.Rotation {
				return p.ErrUnsupported
			}
			if _, err := w.controllerGeometryAt(h, segment.Target); err != nil {
				return err
			}
		}
		low, high := p.AnchorMotionBounds(segment.From, segment.Target, v.desc.Anchor.Position)
		if !low.InBounds() || !high.InBounds() {
			return p.ErrInvalid
		}
		for _, ch := range v.colliders {
			c := w.colliders[ch]
			if !c.desc.Enabled {
				continue
			}
			for _, shape := range c.shapes {
				bounds, e := shape.MotionBounds(b.WorldTransform{P: bp(segment.From.Position), Q: bq(segment.From.Rotation)}, b.WorldTransform{P: bp(segment.Target.Position), Q: bq(segment.Target.Rotation)})
				if e != nil {
					return e
				}
				if !pv(bounds.LowerBound).InBounds() || !pv(bounds.UpperBound).InBounds() {
					return p.ErrInvalid
				}
			}
		}
		pose = segment.Target
	}
	return nil
}

func interpolatePose(a, z p.Pose, f float32) p.Pose {
	q := z.Rotation.Mul(a.Rotation.Conjugate())
	if q.W < 0 {
		q = p.Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: -q.W}
	}
	v := p.Vec3{X: q.X, Y: q.Y, Z: q.Z}
	sine := v.Length()
	rotation := a.Rotation
	if sine > 1e-8 {
		angle := float32(math.Atan2(float64(sine), float64(q.W))) * f
		axis := v.Mul(float32(math.Sin(float64(angle))) / sine)
		rotation = p.Quat{X: axis.X, Y: axis.Y, Z: axis.Z, W: float32(math.Cos(float64(angle)))}.Mul(a.Rotation)
	}
	return p.Pose{Position: a.Position.Add(z.Position.Sub(a.Position).Mul(f)), Rotation: rotation}
}

func poseVelocity(m p.Motion, at, dt float32, current p.Pose) (p.Vec3, p.Vec3) {
	elapsed := float32(0)
	for _, segment := range m.PoseSegments {
		if at < elapsed+segment.Delta {
			target := interpolatePose(segment.From, segment.Target, min(float32(1), (at+dt-elapsed)/segment.Delta))
			q := target.Rotation.Mul(current.Rotation.Conjugate())
			if q.W < 0 {
				q = p.Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: -q.W}
			}
			angular := p.Vec3{X: q.X, Y: q.Y, Z: q.Z}
			if sine := angular.Length(); sine > 1e-8 {
				angular = angular.Mul(2 * float32(math.Atan2(float64(sine), float64(q.W))) / (sine * dt))
			}
			return target.Position.Sub(current.Position).Mul(1 / dt), angular
		}
		elapsed += segment.Delta
	}
	return p.Vec3{}, p.Vec3{}
}
