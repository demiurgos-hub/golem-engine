package physics3d

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const MaxCheckpointBytes = 512 << 20

// CheckpointVersion identifies fixed native stepping and explicit solver settings.
// Version 1 used the retired, unreleased global motion-refinement configuration.
const CheckpointVersion uint32 = 2

// Checkpoint is an owned, portable accepted-world capture. References are stable
// content IDs; native/generational handles, contact caches and solver scratch are
// never persisted. Rebuilding a checkpoint does not promise bitwise continuation.
type Checkpoint struct {
	Version             uint32 `json:"version"`
	ID                  string `json:"checkpoint_id"`
	SourceFingerprint   string `json:"source_fingerprint"`
	Revision, LastFrame uint64
	Config              Config
	Shapes              []ShapeDesc
	Bodies              []CheckpointBody
	Colliders           []CheckpointCollider
	Joints              []CheckpointJoint
}

// CheckpointForce retains an unconsumed force and its world-space application point.
type CheckpointForce struct{ Force, Point Vec3 }

// CheckpointBody stores current pose, activation, velocities and pending forces.
// Description.Anchor remains the body-local gameplay-anchor transform.
type CheckpointBody struct {
	Description         BodyDesc
	Forces              []CheckpointForce
	Torque              Vec3
	ControllerFallSpeed float32
	// External velocities describe the last accepted timed path, not autonomous
	// motion after restoration. A new timed proposal is required to move again.
	ExternalVelocity, ExternalAngularVelocity Vec3
}

// CheckpointCollider replaces body/shape handles with stable content references.
type CheckpointCollider struct {
	ID, Body, Shape ContentID
	Owner           int64
	LocalPose       Pose
	Enabled         bool
	Roles           Roles
	Filter          CollisionFilter
	Material        Material
	Density         float32
}

// CheckpointJoint keeps authored joint parameters in a handle-free JSON object.
// DecodeJoint validates the exact current JointDesc schema and restores references
// supplied by the new world, never handle values from the persisted bytes.
type CheckpointJoint struct {
	ID, A, B   ContentID
	Parameters json.RawMessage
}

// CheckpointWorld optionally captures and privately prepares whole-world recovery.
// Preparation and commitment are tick-owned; preparation does not mutate live state.
type CheckpointWorld interface {
	CaptureCheckpoint(id, sourceFingerprint string) (Checkpoint, error)
	PrepareRestore(Checkpoint) (PreparedRestore, error)
}

// PreparedRestore owns an isolated replacement. Body lookups return fresh handles
// valid after Commit. Commit rejects intervening world mutations and otherwise
// replaces the whole world atomically. Discard is idempotent, including after Commit.
type PreparedRestore interface {
	ContentLookup
	Body(ContentID) (BodyState, bool)
	Commit() error
	Discard()
}

// ContentLookup resolves stable identities in a privately prepared replacement.
// Missing or ambiguous IDs must not resolve to an arbitrary runtime object.
type ContentLookup interface {
	FindBody(ContentID) (BodyState, bool)
	FindShape(ContentID) (ShapeHandle, bool)
	FindCollider(ContentID) (ColliderHandle, bool)
	FindJoint(ContentID) (JointHandle, bool)
}

// CaptureCollider converts live collider references to stable checkpoint IDs.
func CaptureCollider(d ColliderDesc, body, shape ContentID) CheckpointCollider {
	return CheckpointCollider{ID: d.ID, Body: body, Shape: shape, Owner: d.Owner, LocalPose: d.LocalPose,
		Enabled: d.Enabled, Roles: d.Roles, Filter: d.Filter, Material: d.Material, Density: d.Density}
}

// Description reconstructs a collider using handles from the replacement world.
func (c CheckpointCollider) Description(body BodyHandle, shape ShapeHandle) ColliderDesc {
	return ColliderDesc{ID: c.ID, Body: body, Shape: shape, Owner: c.Owner, LocalPose: c.LocalPose,
		Enabled: c.Enabled, Roles: c.Roles, Filter: c.Filter, Material: c.Material, Density: c.Density}
}

// CaptureJoint captures all current joint parameters while removing native references.
func CaptureJoint(d JointDesc, a, b ContentID) (CheckpointJoint, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return CheckpointJoint{}, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return CheckpointJoint{}, err
	}
	delete(fields, "A")
	delete(fields, "B")
	delete(fields, "ID")
	data, err = json.Marshal(fields)
	return CheckpointJoint{ID: d.ID, A: a, B: b, Parameters: data}, err
}

// DecodeJoint validates persisted parameters and supplies new body handles.
func (j CheckpointJoint) DecodeJoint(a, b BodyHandle) (JointDesc, error) {
	var fields map[string]json.RawMessage
	if len(j.Parameters) > 64<<10 || json.Unmarshal(j.Parameters, &fields) != nil || fields == nil {
		return JointDesc{}, ErrInvalid
	}
	for key := range fields {
		if strings.EqualFold(key, "A") || strings.EqualFold(key, "B") || strings.EqualFold(key, "ID") {
			return JointDesc{}, fmt.Errorf("%w: joint contains runtime identity", ErrInvalid)
		}
	}
	var d JointDesc
	decoder := json.NewDecoder(bytes.NewReader(j.Parameters))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return JointDesc{}, err
	}
	d.ID, d.A, d.B = j.ID, a, b
	return d, nil
}

// Validate checks identity, resource bounds and references before restoration.
// The selected backend must additionally qualify collider and joint semantics.
func (c Checkpoint) Validate() error {
	if err := validateCheckpointVersion(c.Version); err != nil {
		return err
	}
	if c.ID == "" || len(c.ID) > 4096 || c.SourceFingerprint == "" || len(c.SourceFingerprint) > 4096 || c.Revision == 0 {
		return ErrInvalid
	}
	if len(c.Shapes) > 200000 || len(c.Bodies) > 200000 || len(c.Colliders) > 500000 || len(c.Joints) > 500000 {
		return fmt.Errorf("%w: checkpoint object limit", ErrInvalid)
	}
	if err := c.Config.Validate(); err != nil {
		return err
	}
	shapes, bodies, colliders, joints := map[ContentID]bool{}, map[ContentID]bool{}, map[ContentID]bool{}, map[ContentID]bool{}
	claim := func(ids map[ContentID]bool, id ContentID) bool {
		if id == "" || len(id) > 4096 || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	remaining := 10_000_000
	for _, s := range c.Shapes {
		if !claim(shapes, s.ID) {
			return fmt.Errorf("%w: shape identity", ErrInvalid)
		}
		if err := validateShape(s, 0, &remaining); err != nil {
			return err
		}
	}
	forces := 0
	for _, body := range c.Bodies {
		if !body.ExternalVelocity.Valid() || !body.ExternalAngularVelocity.Valid() || body.Description.Mode != ExternalPose && (body.ExternalVelocity != (Vec3{}) || body.ExternalAngularVelocity != (Vec3{})) {
			return ErrInvalid
		}
		if !claim(bodies, body.Description.ID) || body.Description.Validate() != nil || !body.Torque.Valid() || !Finite(body.ControllerFallSpeed) {
			return fmt.Errorf("%w: checkpoint body", ErrInvalid)
		}
		forces += len(body.Forces)
		if forces > 1000000 {
			return fmt.Errorf("%w: checkpoint force limit", ErrInvalid)
		}
		for _, force := range body.Forces {
			if !force.Force.Valid() || !force.Point.InBounds() {
				return ErrInvalid
			}
		}
	}
	for _, collider := range c.Colliders {
		if !claim(colliders, collider.ID) || !bodies[collider.Body] || !shapes[collider.Shape] || !collider.LocalPose.Valid() || !Finite(collider.Density) || collider.Density < 0 {
			return fmt.Errorf("%w: checkpoint collider", ErrInvalid)
		}
	}
	for _, joint := range c.Joints {
		if !claim(joints, joint.ID) || !bodies[joint.A] || !bodies[joint.B] || joint.A == joint.B {
			return fmt.Errorf("%w: checkpoint joint", ErrInvalid)
		}
		if _, err := joint.DecodeJoint(BodyHandle{}, BodyHandle{}); err != nil {
			return err
		}
	}
	return nil
}

// EncodeCheckpoint validates and encodes portable state under the byte limit.
func EncodeCheckpoint(c Checkpoint) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if len(data) > MaxCheckpointBytes {
		return nil, fmt.Errorf("%w: checkpoint byte limit", ErrInvalid)
	}
	return data, err
}

// DecodeCheckpoint rejects unknown fields, trailing data, oversized captures and
// obsolete checkpoint versions rather than ignoring their old solver settings.
func DecodeCheckpoint(data []byte) (Checkpoint, error) {
	var c Checkpoint
	if len(data) > MaxCheckpointBytes {
		return c, fmt.Errorf("%w: checkpoint byte limit", ErrInvalid)
	}
	var header struct {
		Version uint32 `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return c, err
	}
	if err := validateCheckpointVersion(header.Version); err != nil {
		return c, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Checkpoint{}, fmt.Errorf("%w: trailing checkpoint data", ErrInvalid)
	}
	if err := c.Validate(); err != nil {
		return Checkpoint{}, err
	}
	return c, nil
}

func validateCheckpointVersion(version uint32) error {
	if version == 1 {
		return fmt.Errorf("%w: checkpoint version 1 uses retired global motion-refinement settings; an explicit state migration is required", ErrUnsupported)
	}
	if version != CheckpointVersion {
		return fmt.Errorf("%w: checkpoint version %d (required %d)", ErrUnsupported, version, CheckpointVersion)
	}
	return nil
}
