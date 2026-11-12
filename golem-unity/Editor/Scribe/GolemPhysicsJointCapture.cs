using System;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    internal static class GolemPhysicsJointCapture
    {
        internal static PhysicsJoint Capture(Joint joint, GolemPhysicsWorld world)
        {
            var body = joint.GetComponent<Rigidbody>();
            if (!body) throw new InvalidOperationException(joint.name + ": joint requires a Rigidbody.");
            if (joint.connectedArticulationBody) throw new InvalidOperationException(joint.name + ": articulation bodies are unsupported.");
            var connected = joint.connectedBody;
            if (connected && connected.GetComponentInParent<GolemPhysicsWorld>(true) != world)
                throw new InvalidOperationException(joint.name + ": connected body is outside the selected physics world.");
            var local = Matrix4x4.TRS(body.position, body.rotation, Vector3.one).inverse * joint.transform.localToWorldMatrix;
            var anchorWorld = joint.transform.TransformPoint(joint.anchor);
            var connectedWorld = connected ? connected.transform.TransformPoint(joint.connectedAnchor) : joint.connectedAnchor;
            var result = new PhysicsJoint
            {
                ID = GolemPhysicsExporter.Identity(joint), SourceKind = joint.GetType().Name, BodyID = GolemPhysicsExporter.Identity(body),
                ConnectedBodyID = connected ? GolemPhysicsExporter.Identity(connected) : "",
                Anchor = GolemPhysicsExporter.Vector(local.MultiplyPoint3x4(joint.anchor)),
                ConnectedAnchor = GolemPhysicsExporter.Vector(connected ? Quaternion.Inverse(connected.rotation) * (connectedWorld - connected.position) : connectedWorld),
                Axis = GolemPhysicsExporter.Vector(local.MultiplyVector(joint.axis).normalized),
                ReferenceRotation = GolemPhysicsExporter.Rotation((connected ? Quaternion.Inverse(connected.rotation) : Quaternion.identity) * body.rotation),
                Active = joint.gameObject.activeInHierarchy, EnableCollision = joint.enableCollision, EnablePreprocessing = joint.enablePreprocessing,
                MassScale = joint.massScale, ConnectedMassScale = joint.connectedMassScale,
                BreakForce = Unbounded(joint.breakForce), BreakTorque = Unbounded(joint.breakTorque)
            };
            if (joint is FixedJoint) result.Kind = "fixed";
            else if (joint is HingeJoint hinge)
            {
                result.Kind = "hinge"; result.UseLimits = hinge.useLimits; result.UseMotor = hinge.useMotor; result.UseSpring = hinge.useSpring;
                var limits = hinge.limits;
                result.LowTwist = new PhysicsJointLimit { Limit = limits.min, Bounciness = limits.bounciness, BounceMinVelocity = limits.bounceMinVelocity, ContactDistance = limits.contactDistance };
                result.HighTwist = new PhysicsJointLimit { Limit = limits.max, Bounciness = limits.bounciness, BounceMinVelocity = limits.bounceMinVelocity, ContactDistance = limits.contactDistance };
                result.Motor = new PhysicsJointMotor { TargetVelocity = hinge.motor.targetVelocity, Force = hinge.motor.force, FreeSpin = hinge.motor.freeSpin };
                result.Spring = new PhysicsJointSpring { Spring = hinge.spring.spring, Damper = hinge.spring.damper, TargetPosition = hinge.spring.targetPosition };
                var serialized = new SerializedObject(hinge);
                if ((serialized.FindProperty("m_ExtendedLimits")?.boolValue ?? false) || (serialized.FindProperty("m_UseAcceleration")?.boolValue ?? false))
                    throw new InvalidOperationException(joint.name + ": extended hinge limits and acceleration-mode springs are unsupported.");
            }
            else if (joint is SpringJoint spring)
            {
                result.Kind = "spring"; result.UseSpring = true;
                result.Spring = new PhysicsJointSpring
                {
                    Spring = spring.spring, Damper = spring.damper, MinDistance = spring.minDistance, MaxDistance = spring.maxDistance,
                    Tolerance = spring.tolerance, RestDistance = Vector3.Distance(anchorWorld, connectedWorld)
                };
            }
            else if (joint is CharacterJoint character)
            {
                result.Kind = "character"; result.UseLimits = true;
                result.SecondaryAxis = GolemPhysicsExporter.Vector(local.MultiplyVector(character.swingAxis).normalized);
                result.LowTwist = Limit(character.lowTwistLimit, character.twistLimitSpring); result.HighTwist = Limit(character.highTwistLimit, character.twistLimitSpring);
                result.Swing1 = Limit(character.swing1Limit, character.swingLimitSpring); result.Swing2 = Limit(character.swing2Limit, character.swingLimitSpring);
                result.EnableProjection = character.enableProjection; result.ProjectionDistance = character.projectionDistance; result.ProjectionAngle = character.projectionAngle;
            }
            else if (joint is ConfigurableJoint configurable)
            {
                if (configurable.xMotion != ConfigurableJointMotion.Locked || configurable.yMotion != ConfigurableJointMotion.Locked || configurable.zMotion != ConfigurableJointMotion.Locked ||
                    configurable.angularXMotion != ConfigurableJointMotion.Locked || configurable.angularYMotion != ConfigurableJointMotion.Locked || configurable.angularZMotion != ConfigurableJointMotion.Locked ||
                    configurable.swapBodies || configurable.configuredInWorldSpace || HasDrive(configurable.xDrive) || HasDrive(configurable.yDrive) || HasDrive(configurable.zDrive) ||
                    HasDrive(configurable.angularXDrive) || HasDrive(configurable.angularYZDrive) || HasDrive(configurable.slerpDrive) || configurable.targetPosition != Vector3.zero ||
                    configurable.targetVelocity != Vector3.zero || configurable.targetAngularVelocity != Vector3.zero || configurable.targetRotation != Quaternion.identity)
                    throw new InvalidOperationException(joint.name + ": ConfigurableJoint supports only six locked axes without drives, targets, swapped bodies, or world-space configuration; author the supported fixed/hinge/spring/character contract explicitly.");
                result.Kind = "fixed"; result.EnableProjection = configurable.projectionMode != JointProjectionMode.None;
                result.ProjectionDistance = configurable.projectionDistance; result.ProjectionAngle = configurable.projectionAngle;
                result.SecondaryAxis = GolemPhysicsExporter.Vector(local.MultiplyVector(configurable.secondaryAxis).normalized);
            }
            else throw new InvalidOperationException(joint.name + ": unsupported joint type " + joint.GetType().Name);
            return result;
        }

        private static bool HasDrive(JointDrive drive) => drive.positionSpring != 0 || drive.positionDamper != 0;
        private static float? Unbounded(float value) => float.IsPositiveInfinity(value) ? (float?)null : value;
        private static PhysicsJointLimit Limit(SoftJointLimit limit, SoftJointLimitSpring spring) => new PhysicsJointLimit
        { Limit = limit.limit, Bounciness = limit.bounciness, ContactDistance = limit.contactDistance, Spring = spring.spring, Damper = spring.damper };
    }
}
