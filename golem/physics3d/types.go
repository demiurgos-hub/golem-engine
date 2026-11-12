package physics3d

// Handle is world-qualified and generational. Zero is never a live handle.
type Handle struct {
	World             uint64
	Index, Generation uint32
}
type BodyHandle Handle
type ColliderHandle Handle
type ShapeHandle Handle
type JointHandle Handle
type ContentID string

type MotionMode uint8

const (
	Static MotionMode = iota
	Kinematic
	DrivenCharacter
	FreeDynamic
	ScriptedAction
	ExternalPose
)

type ShapeKind uint8

const (
	Sphere ShapeKind = iota + 1
	Capsule
	Box
	Convex
	TriangleMesh
	Compound
)

// ShapeDesc is portable geometry. Box uses half extents; capsule endpoints are
// sphere centres. Mesh indices describe triangles with counter-clockwise winding.
// Compound children own their local transforms. Implementations copy all slices.
type ShapeDesc struct {
	ID                          ContentID
	Kind                        ShapeKind
	Radius                      float32
	PointA, PointB, HalfExtents Vec3
	Vertices                    []Vec3
	Indices                     []uint32
	Children                    []ShapeChild
}
type ShapeChild struct {
	ID    ContentID
	Pose  Pose
	Shape ShapeDesc
}
type Material struct {
	FrictionCombine, RestitutionCombine      CombineMode
	Friction, Restitution, RollingResistance float32
	ID                                       uint64
}
type CombineMode uint8

const (
	DefaultCombine CombineMode = iota
	Average
	Minimum
	Multiply
	Maximum
)

type Roles struct{ Solid, Sensor, Query bool }
type CollisionFilter struct {
	Category, Mask uint64
	Group          int32
}
type MotionLocks struct{ LinearX, LinearY, LinearZ, AngularX, AngularY, AngularZ bool }
type BodyDesc struct {
	Controller                                        CharacterControllerConfig
	ID                                                ContentID
	Owner                                             int64
	Mode                                              MotionMode
	Pose                                              Pose
	Anchor                                            Pose // body-local transform to gameplay/navigation anchor, not centre of mass
	LinearVelocity, AngularVelocity                   Vec3
	Mass, GravityScale, LinearDamping, AngularDamping float32
	Enabled, Awake, AllowSleep                        bool
	// Continuous selects the backend's per-body bullet policy. In Box3D it adds
	// built-in sweeps against non-bullet dynamic/kinematic targets; it does not
	// toggle world CCD or replace the targeted moving-pair coverage.
	Continuous bool
	Locks      MotionLocks
	// Manual mass properties are body-local. InertiaTensor contains principal
	// moments about CenterOfMass, rotated into the body frame by InertiaRotation.
	ManualCenterOfMass, ManualInertia bool
	CenterOfMass, InertiaTensor       Vec3
	InertiaRotation                   Quat
}
type ColliderDesc struct {
	ID        ContentID
	Owner     int64
	Body      BodyHandle
	Shape     ShapeHandle
	LocalPose Pose
	Enabled   bool
	Roles     Roles
	Filter    CollisionFilter
	Material  Material
	Density   float32
}
type BodyState struct {
	Support                                       CharacterSupport
	Handle                                        BodyHandle
	ID                                            ContentID
	Owner                                         int64
	Mode                                          MotionMode
	Pose, Anchor                                  Pose
	LinearVelocity, AngularVelocity, CenterOfMass Vec3
	Mass                                          float32
	Enabled, Awake                                bool
}
type Config struct {
	Gravity    Vec3
	Substeps   int
	TickRate   int
	AllowSleep bool
	// Continuous enables world CCD. The Box3D adapter combines native static sweeps
	// with targeted predictive contacts for fast moving pairs, and admits bounded
	// pose trajectories/continuous moving-mesh and sensor handling only in this mode.
	Continuous bool
	Workers    uint32
	// Solver iterations refine native constraints without advancing physical time.
	SolverVelocityIterations, SolverRelaxIterations int
}

// DefaultConfig returns 20 Hz with four native solver steps and native constraint
// iteration counts. Retained gameplay trajectory boundaries may add intervals.
func DefaultConfig() Config {
	return Config{Gravity: Vec3{Y: -9.81}, Substeps: 4, TickRate: 20, AllowSleep: true, Continuous: true, Workers: 1, SolverVelocityIterations: 1, SolverRelaxIterations: 1}
}

// DefaultBody returns a live body description with explicit identity transforms.
func DefaultBody(mode MotionMode) BodyDesc {
	return BodyDesc{Mode: mode, Pose: Identity(), Anchor: Identity(), Mass: 1, GravityScale: 1, Enabled: true, Awake: true, AllowSleep: true}
}

// DefaultCollider returns a solid/query collider on all collision masks.
func DefaultCollider(body BodyHandle, shape ShapeHandle) ColliderDesc {
	return ColliderDesc{Body: body, Shape: shape, LocalPose: Identity(), Enabled: true, Roles: Roles{Solid: true, Query: true}, Filter: CollisionFilter{Category: 1, Mask: ^uint64(0)}, Material: Material{Friction: 0.5}, Density: 1}
}

type JointKind uint8

const (
	Weld JointKind = iota + 1
	Distance
	Hinge
	Slider
	Spherical
	Motor
	Upright
	Wheel
)

// JointDesc uses body-origin local frames. Hinge rotates about local Z, slider
// translates along local X, wheel suspends along A.X and spins about B.Z.
type JointDesc struct {
	ID                                                               ContentID
	Kind                                                             JointKind
	A, B                                                             BodyHandle
	FrameA, FrameB                                                   Pose
	CollideConnected                                                 bool
	Length, Target, Lower, Upper                                     float32
	Spring, Limit, Drive                                             bool
	Hertz, Damping, MaxForce, MaxTorque, Speed                       float32
	LinearVelocity, AngularVelocity                                  Vec3
	ConeLimit, TwistLimit                                            bool
	ConeAngle, LowerTwist, UpperTwist                                float32
	TargetRotation                                                   Quat
	Steering                                                         bool
	SteeringAngle, SteeringHertz, SteeringDamping, MaxSteeringTorque float32
}
type JointState struct {
	Handle        JointHandle
	ID            ContentID
	Force, Torque Vec3
}

// World is tick-owned. Simulate freezes mutations; Accept publishes the complete
// pending result. Reject faults the world without publishing a partial frame.
// Synchronize is the explicit visibility boundary after direct pose changes.
type World interface {
	CreateShape(ShapeDesc) (ShapeHandle, error)
	DestroyShape(ShapeHandle) error
	CreateBody(BodyDesc) (BodyHandle, error)
	DestroyBody(BodyHandle, CleanupReason) error
	CreateCollider(ColliderDesc) (ColliderHandle, error)
	DestroyCollider(ColliderHandle, CleanupReason) error
	CreateJoint(JointDesc) (JointHandle, error)
	DestroyJoint(JointHandle) error
	Body(BodyHandle) (BodyState, error)
	SetPose(BodyHandle, Pose) error
	SetVelocity(BodyHandle, Vec3, Vec3) error
	SetEnabled(BodyHandle, bool) error
	ApplyForce(BodyHandle, Vec3, Vec3) error
	ApplyTorque(BodyHandle, Vec3) error
	ApplyImpulse(BodyHandle, Vec3, Vec3) error
	Synchronize() error
	Query(Query, []Hit) (QueryResult, error)
	ClosestPoint(ColliderHandle, Vec3) (Hit, error)
	Penetration(ColliderHandle, ColliderHandle) (PenetrationResult, error)
	Simulate(Frame) (FrameResult, error)
	Accept(uint64) error
	Reject(uint64, error) error
	Metrics() Metrics
	Close() error
}
type Motion struct {
	Body          BodyHandle
	Velocity      Vec3
	SurfaceNormal Vec3
	MaxDriveForce float32
	Segments      []MotionSegment
	PoseSegments  []PoseMotionSegment
	Frozen        bool // captured quarantine: immovable constraint; momentum is discarded
	// Validate is a captured, immutable safety predicate for every swept segment.
	// It must not mutate the world or call gameplay callbacks.
	Validate func(from, to Vec3) bool
	// ValidateBounds additionally protects curved anchor paths between retained
	// rotation samples. It has the same immutable, callback-free requirements.
	ValidateBounds func(min, max Vec3) bool
}

// MotionSegment is retained intent, not an independent physics time step.
// Projection requests a zero-time, clearance-validated initial alignment.
type MotionSegment struct {
	From, Target, SurfaceNormal Vec3
	Delta                       float32
	Projection                  bool
}

// PoseMotionSegment is a timed body-origin trajectory, including shortest-arc
// rotation. It cannot be mixed with navigation MotionSegments in one Motion.
type PoseMotionSegment struct {
	From, Target Pose
	Delta        float32
}

// PoseMotionWorld identifies backends that admit complete rotating trajectories.
// A backend without this capability must not silently consume positions alone.
type PoseMotionWorld interface {
	SupportsPoseMotion() bool
	ValidatePoseMotion(BodyHandle, []PoseMotionSegment) error
}
type Frame struct {
	ID      uint64
	Delta   float32
	Motions []Motion
}
type FrameResult struct {
	ID, Revision uint64
	Bodies       []BodyState
	Events       []ContactEvent
	Substeps     int
	// Microsteps counts actual native solver calls. The retained global-guard
	// counters remain zero with native stepping.
	Microsteps, MotionPairTests, MotionPathRefinements int
	// Targeted native CCD counts sum each attempted step, including steps later
	// rejected by safety validation: unique overlapping swept moving-pair candidates,
	// native TOI calls after policy filters, and predictive solver manifolds
	// (not gameplay events). Built-in static sweep work is separate.
	CCDCandidatePairs, CCDPairTests, CCDPredictiveContacts int
	Intervals                                              int // shared subdivisions; their durations still sum to Elapsed
	Elapsed                                                float32
	Paths                                                  []BodyPath
	RejectedMotions                                        []MotionRejection
}

// MotionRejection records an intent denied before the interacting frame solved.
type MotionRejection struct {
	Body   BodyHandle
	Reason string
}

// BodyPath records every simulated anchor pose, including the frame's start.
type BodyPath struct {
	Body      BodyHandle
	Positions []Vec3
	// Poses are body-origin transforms; Positions remain gameplay anchors.
	// Fractions records their time within the accepted frame, including zero.
	Poses     []Pose
	Fractions []float32
}
type Metrics struct {
	Revision, AcceptedFrames, RejectedFrames           uint64
	Bodies, Colliders, Shapes, Joints, Awake, Substeps int
	SimulatedSeconds                                   float64
	Queries, Overflows                                 uint64
	Fault                                              error
}

// OwnerBinder optionally assigns an entity owner to a body before server
// attachment. Zero-owner colliders inherit the body owner in queries and events;
// colliders with their own nonzero owner keep it. The operation is atomic and
// obeys the same tick ownership and pending-frame mutation rules as World.
type OwnerBinder interface {
	SetOwner(BodyHandle, int64) error
}
