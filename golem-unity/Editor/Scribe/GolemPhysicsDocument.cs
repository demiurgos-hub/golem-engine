using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Reflection;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    internal sealed class PhysicsVector
    {
        public float X, Y, Z;
        internal PhysicsVector(float x = 0, float y = 0, float z = 0) { X = x; Y = y; Z = z; }
    }
    internal sealed class PhysicsRotation
    {
        public float X, Y, Z, W = 1;
    }
    internal sealed class PhysicsProperty { public string Key, Value; }
    internal sealed class PhysicsBody
    {
        public string ID, OwnerID = "", Kind = "static", CollisionDetection = "discrete";
        public PhysicsVector Position = new PhysicsVector();
        public PhysicsRotation Rotation = new PhysicsRotation();
        public bool Enabled = true, Active = true, UseGravity;
        public float Mass = 1, LinearDamping, AngularDamping;
        public int Constraints;
        public bool AutomaticCenterOfMass = true, AutomaticInertiaTensor = true;
        public PhysicsVector CenterOfMass = new PhysicsVector(), InertiaTensor = new PhysicsVector(1, 1, 1);
        public PhysicsRotation InertiaRotation = new PhysicsRotation();
    }
    internal sealed class PhysicsCollider
    {
        public string ID, BodyID, OwnerID = "", SourceID = "", ParentID = "", LayerName = "", Shape, AssetID = "", MaterialID;
        public bool Enabled = true, Active = true, Solid = true, Sensor, Query = true;
        public int Layer, Axis = 1;
        public PhysicsVector Center = new PhysicsVector(), Size = new PhysicsVector();
        public PhysicsRotation Rotation = new PhysicsRotation();
        public float Radius, Height, ContactOffset;
        public PhysicsCharacter Character;
        public PhysicsProperty[] Metadata = Array.Empty<PhysicsProperty>();
    }
    internal sealed class PhysicsCharacter
    {
        // Distances are meters at the required unit controller scale; slope is in degrees.
        public float StepOffset, SlopeLimit, SkinWidth, MinimumMoveDistance;
        public bool DetectCollisions, EnableOverlapRecovery;
    }
    internal sealed class PhysicsJointLimit { public float Limit, Bounciness, BounceMinVelocity, ContactDistance, Spring, Damper; }
    internal sealed class PhysicsJointMotor { public float TargetVelocity, Force; public bool FreeSpin; }
    internal sealed class PhysicsJointSpring { public float Spring, Damper, TargetPosition, MinDistance, MaxDistance, Tolerance, RestDistance; }
    internal sealed class PhysicsJoint
    {
        // Anchors are rigid-body local, except an unconnected anchor is world-space. Angular values are degrees.
        public string ID, Kind, SourceKind, BodyID, ConnectedBodyID = "";
        public PhysicsVector Anchor = new PhysicsVector(), ConnectedAnchor = new PhysicsVector(), Axis = new PhysicsVector(1), SecondaryAxis = new PhysicsVector(0, 1);
        public PhysicsRotation ReferenceRotation = new PhysicsRotation();
        public bool Active = true, EnableCollision, EnablePreprocessing = true, UseLimits, UseMotor, UseSpring, EnableProjection;
        public float MassScale = 1, ConnectedMassScale = 1, ProjectionDistance, ProjectionAngle;
        public float? BreakForce, BreakTorque;
        public PhysicsJointLimit LowTwist = new PhysicsJointLimit(), HighTwist = new PhysicsJointLimit(), Swing1 = new PhysicsJointLimit(), Swing2 = new PhysicsJointLimit();
        public PhysicsJointMotor Motor = new PhysicsJointMotor();
        public PhysicsJointSpring Spring = new PhysicsJointSpring();
    }
    internal sealed class PhysicsAsset
    {
        public string ID, SourceID;
        public PhysicsVector[] Vertices = Array.Empty<PhysicsVector>();
        public uint[] Indices = Array.Empty<uint>();
    }
    internal sealed class PhysicsMaterial
    {
        public string ID, FrictionCombine = "average", BounceCombine = "average";
        public float StaticFriction = 0.6f, DynamicFriction = 0.6f, Restitution;
    }
    internal sealed class PhysicsSource
    {
        public int Version = 1;
        public string WorldID;
        public ulong Revision = 1;
        public float CoordinateLimit = 4096;
        public uint[] LayerCollisionMasks = Enumerable.Repeat(uint.MaxValue, 32).ToArray();
        public PhysicsBody[] Bodies = Array.Empty<PhysicsBody>();
        public PhysicsCollider[] Colliders = Array.Empty<PhysicsCollider>();
        public PhysicsAsset[] Assets = Array.Empty<PhysicsAsset>();
        public PhysicsMaterial[] Materials = Array.Empty<PhysicsMaterial>();
        public PhysicsJoint[] Joints = Array.Empty<PhysicsJoint>();
    }

    /// <summary>Validates and deterministically serializes portable physics source without Unity or filesystem access.</summary>
    internal static class GolemPhysicsDocument
    {
        internal const string Generator = "Golem Scribe physics source v1";
        internal const string OwnershipPrefix = "{\n  \"Generator\": \"" + Generator + "\",\n";
        internal static bool IsOwned(string text) => text != null && text.StartsWith(OwnershipPrefix, StringComparison.Ordinal);

        internal static string Build(PhysicsSource source)
        {
            Validate(source);
            var ordered = new PhysicsSource
            {
                Version = source.Version, WorldID = source.WorldID, Revision = source.Revision, CoordinateLimit = source.CoordinateLimit,
                LayerCollisionMasks = source.LayerCollisionMasks,
                Bodies = source.Bodies.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray(),
                Colliders = source.Colliders.OrderBy(x => x.ID, StringComparer.Ordinal).Select(CopyCollider).ToArray(),
                Assets = source.Assets.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray(),
                Materials = source.Materials.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray(), Joints = source.Joints.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray()
            };
            var json = new StringBuilder(OwnershipPrefix);
            var fields = ordered.GetType().GetFields().OrderBy(x => x.Name, StringComparer.Ordinal).ToArray();
            for (var i = 0; i < fields.Length; i++)
            {
                json.Append("  "); Quote(json, fields[i].Name); json.Append(": "); Write(json, fields[i].GetValue(ordered));
                json.Append(i == fields.Length - 1 ? "\n" : ",\n");
            }
            json.Append("}\n");
            Require(Encoding.UTF8.GetByteCount(json.ToString()) <= 256 * 1024 * 1024, "Physics source exceeds 256 MiB.");
            return json.ToString();
        }

        private static PhysicsCollider CopyCollider(PhysicsCollider x) => new PhysicsCollider
        {
            ID = x.ID, BodyID = x.BodyID, OwnerID = x.OwnerID, SourceID = x.SourceID, ParentID = x.ParentID,
            Layer = x.Layer, LayerName = x.LayerName, Shape = x.Shape, AssetID = x.AssetID, MaterialID = x.MaterialID,
            Enabled = x.Enabled, Active = x.Active, Solid = x.Solid, Sensor = x.Sensor, Query = x.Query,
            Center = x.Center, Rotation = x.Rotation, Size = x.Size, Radius = x.Radius, Height = x.Height,
            Axis = x.Axis, ContactOffset = x.ContactOffset, Character = x.Character, Metadata = x.Metadata.OrderBy(p => p.Key, StringComparer.Ordinal).ToArray()
        };

        internal static void Validate(PhysicsSource source)
        {
            Require(source != null && source.Version == 1 && !string.IsNullOrWhiteSpace(source.WorldID) && source.Revision > 0, "Invalid physics source identity/version.");
            Require(source.CoordinateLimit == 4096, "Physics v1 requires a 4096-meter absolute coordinate envelope.");
            Require(source.Bodies != null && source.Colliders != null && source.Assets != null && source.Materials != null && source.Joints != null, "Null physics source collection.");
            Require(source.LayerCollisionMasks != null && source.LayerCollisionMasks.Length == 32, "Exactly 32 layer masks are required.");
            for (var a = 0; a < 32; a++) for (var b = 0; b < 32; b++)
                Require(((source.LayerCollisionMasks[a] >> b) & 1) == ((source.LayerCollisionMasks[b] >> a) & 1), "Layer matrix must be symmetric.");
            var bodies = new Dictionary<string, PhysicsBody>(StringComparer.Ordinal);
            foreach (var body in source.Bodies)
            {
                Require(body != null && !string.IsNullOrWhiteSpace(body.ID) && !bodies.ContainsKey(body.ID), "Duplicate/missing body identity.");
                bodies.Add(body.ID, body); Vector(body.Position); Rotation(body.Rotation); Envelope(body.Position, source.CoordinateLimit);
                Vector(body.CenterOfMass); Vector(body.InertiaTensor); Rotation(body.InertiaRotation);
                Require(body.AutomaticInertiaTensor || (body.InertiaTensor.X > 0 && body.InertiaTensor.Y > 0 && body.InertiaTensor.Z > 0), "Explicit inertia tensor must be positive: " + body.ID);
                Require(body.Kind == "static" || body.Kind == "kinematic" || body.Kind == "dynamic", "Unknown body kind: " + body.ID);
                Require(Finite(body.Mass) && body.Mass > 0 && Nonnegative(body.LinearDamping) && Nonnegative(body.AngularDamping), "Invalid body mass/damping: " + body.ID);
                Require(body.Constraints >= 0 && (body.Constraints & ~126) == 0, "Invalid body constraints: " + body.ID);
                Require(new[] { "discrete", "continuous", "continuousdynamic", "continuousspeculative" }.Contains(body.CollisionDetection), "Unknown collision detection mode: " + body.ID);
            }
            var assets = new Dictionary<string, PhysicsAsset>(StringComparer.Ordinal);
            foreach (var asset in source.Assets)
            {
                Require(asset != null && !string.IsNullOrWhiteSpace(asset.ID) && !assets.ContainsKey(asset.ID), "Duplicate/missing geometry identity.");
                assets.Add(asset.ID, asset);
                Require(asset.Vertices != null && asset.Indices != null && asset.Vertices.Length <= 8 * 1024 * 1024 && asset.Indices.Length <= 24 * 1024 * 1024 && asset.Indices.Length % 3 == 0, "Invalid/oversized geometry: " + asset.ID);
                foreach (var v in asset.Vertices) Vector(v);
                foreach (var index in asset.Indices) Require(index < asset.Vertices.Length, "Geometry index out of range: " + asset.ID);
                for (var i = 0; i < asset.Indices.Length; i += 3)
                {
                    var a = asset.Vertices[asset.Indices[i]]; var b = asset.Vertices[asset.Indices[i + 1]]; var c = asset.Vertices[asset.Indices[i + 2]];
                    double ux = b.X - (double)a.X, uy = b.Y - (double)a.Y, uz = b.Z - (double)a.Z;
                    double vx = c.X - (double)a.X, vy = c.Y - (double)a.Y, vz = c.Z - (double)a.Z;
                    Require(uy * vz - uz * vy != 0 || uz * vx - ux * vz != 0 || ux * vy - uy * vx != 0, "Degenerate triangle: " + asset.ID);
                }
            }
            var materials = new HashSet<string>(StringComparer.Ordinal);
            foreach (var material in source.Materials)
            {
                Require(material != null && !string.IsNullOrWhiteSpace(material.ID) && materials.Add(material.ID), "Duplicate/missing material identity.");
                Require(Nonnegative(material.StaticFriction) && Nonnegative(material.DynamicFriction) && Nonnegative(material.Restitution) && material.Restitution <= 1, "Invalid friction/restitution: " + material.ID);
                var modes = new[] { "average", "minimum", "maximum", "multiply" };
                Require(modes.Contains(material.FrictionCombine) && modes.Contains(material.BounceCombine), "Unknown material combination: " + material.ID);
            }
            var colliders = new HashSet<string>(StringComparer.Ordinal);
            foreach (var collider in source.Colliders)
            {
                Require(collider != null && !string.IsNullOrWhiteSpace(collider.ID) && colliders.Add(collider.ID), "Duplicate/missing collider identity.");
                Require(bodies.ContainsKey(collider.BodyID ?? "") && materials.Contains(collider.MaterialID ?? ""), "Missing collider body/material: " + collider.ID);
                Require(collider.Layer >= 0 && collider.Layer < 32 && Nonnegative(collider.ContactOffset), "Invalid collider layer/contact offset: " + collider.ID);
                Vector(collider.Center); Vector(collider.Size); Rotation(collider.Rotation);
                Require(Nonnegative(collider.Radius) && Nonnegative(collider.Height), "Invalid collider dimensions: " + collider.ID);
                Require(collider.Metadata != null && collider.Metadata.All(p => p != null && !string.IsNullOrWhiteSpace(p.Key) && p.Value != null) && collider.Metadata.Select(p => p.Key).Distinct(StringComparer.Ordinal).Count() == collider.Metadata.Length, "Duplicate/invalid collider metadata: " + collider.ID);
                if (collider.Character != null)
                {
                    var c = collider.Character;
                    Require(collider.Shape == "capsule" && bodies[collider.BodyID].Kind == "kinematic" && Nonnegative(c.StepOffset) && Nonnegative(c.SkinWidth) && Nonnegative(c.MinimumMoveDistance) && Nonnegative(c.SlopeLimit) && c.SlopeLimit <= 90, "Invalid character motor configuration: " + collider.ID);
                }
                var body = bodies[collider.BodyID];
                if (collider.Shape == "sphere") Require(collider.Radius > 0, "Sphere radius must be positive: " + collider.ID);
                else if (collider.Shape == "capsule") Require(collider.Radius > 0 && collider.Height >= 2 * collider.Radius && collider.Axis >= 0 && collider.Axis <= 2, "Invalid capsule: " + collider.ID);
                else if (collider.Shape == "box") Require(collider.Size.X > 0 && collider.Size.Y > 0 && collider.Size.Z > 0, "Box size must be positive: " + collider.ID);
                else
                {
                    Require(collider.Shape == "mesh" || collider.Shape == "convex" || collider.Shape == "terrain", "Unsupported shape: " + collider.Shape);
                    Require(assets.ContainsKey(collider.AssetID ?? ""), "Missing collider geometry: " + collider.ID);
                    Require(body.Kind != "dynamic" || collider.Shape == "convex", "Dynamic non-convex geometry is unsupported: " + collider.ID);
                    Require(collider.Center.X == 0 && collider.Center.Y == 0 && collider.Center.Z == 0 && collider.Rotation.X == 0 && collider.Rotation.Y == 0 && collider.Rotation.Z == 0 && collider.Rotation.W == 1, "Mesh geometry must already be body-local: " + collider.ID);
                    foreach (var v in assets[collider.AssetID].Vertices) Envelope(WorldPoint(body, v), source.CoordinateLimit);
                    continue;
                }
                var center = WorldPoint(body, collider.Center);
                var radius = collider.Shape == "sphere" ? collider.Radius : collider.Shape == "capsule" ? collider.Height / 2 : (float)Math.Sqrt(collider.Size.X * collider.Size.X + collider.Size.Y * collider.Size.Y + collider.Size.Z * collider.Size.Z) / 2;
                // Conservative containment is intentional: every accepted primitive lies inside the qualified envelope.
                Envelope(new PhysicsVector(Math.Abs(center.X) + radius, Math.Abs(center.Y) + radius, Math.Abs(center.Z) + radius), source.CoordinateLimit);
            }
            var jointIDs = new HashSet<string>(StringComparer.Ordinal);
            foreach (var joint in source.Joints)
            {
                Require(joint != null && !string.IsNullOrWhiteSpace(joint.ID) && jointIDs.Add(joint.ID), "Duplicate/missing joint identity.");
                Require(bodies.ContainsKey(joint.BodyID ?? "") && (joint.ConnectedBodyID == "" || bodies.ContainsKey(joint.ConnectedBodyID ?? "")) && joint.BodyID != joint.ConnectedBodyID, "Invalid joint body binding: " + joint.ID);
                Require(new[] { "fixed", "hinge", "spring", "character" }.Contains(joint.Kind), "Unsupported joint kind: " + joint.Kind);
                Vector(joint.Anchor); Vector(joint.ConnectedAnchor); Vector(joint.Axis); Vector(joint.SecondaryAxis); Rotation(joint.ReferenceRotation);
                Require(Nonnegative(joint.MassScale) && Nonnegative(joint.ConnectedMassScale) && (!joint.BreakForce.HasValue || Nonnegative(joint.BreakForce.Value)) && (!joint.BreakTorque.HasValue || Nonnegative(joint.BreakTorque.Value)), "Invalid joint mass/break setting: " + joint.ID);
                Require(Nonnegative(joint.ProjectionDistance) && Nonnegative(joint.ProjectionAngle), "Invalid joint projection: " + joint.ID);
                foreach (var limit in new[] { joint.LowTwist, joint.HighTwist, joint.Swing1, joint.Swing2 })
                    Require(limit != null && Finite(limit.Limit) && Nonnegative(limit.Bounciness) && limit.Bounciness <= 1 && Nonnegative(limit.BounceMinVelocity) && Nonnegative(limit.ContactDistance) && Nonnegative(limit.Spring) && Nonnegative(limit.Damper), "Invalid joint limit: " + joint.ID);
                Require(joint.Motor != null && Finite(joint.Motor.TargetVelocity) && Nonnegative(joint.Motor.Force), "Invalid joint motor: " + joint.ID);
                var spring = joint.Spring;
                Require(spring != null && Nonnegative(spring.Spring) && Nonnegative(spring.Damper) && Finite(spring.TargetPosition) && Nonnegative(spring.MinDistance) && Nonnegative(spring.MaxDistance) && spring.MaxDistance >= spring.MinDistance && Nonnegative(spring.Tolerance) && Nonnegative(spring.RestDistance), "Invalid joint spring: " + joint.ID);
            }
        }

        private static PhysicsVector WorldPoint(PhysicsBody body, PhysicsVector v)
        {
            var q = body.Rotation;
            var tx = 2 * (q.Y * v.Z - q.Z * v.Y); var ty = 2 * (q.Z * v.X - q.X * v.Z); var tz = 2 * (q.X * v.Y - q.Y * v.X);
            return new PhysicsVector(body.Position.X + v.X + q.W * tx + q.Y * tz - q.Z * ty,
                body.Position.Y + v.Y + q.W * ty + q.Z * tx - q.X * tz, body.Position.Z + v.Z + q.W * tz + q.X * ty - q.Y * tx);
        }
        private static void Vector(PhysicsVector v) => Require(v != null && Finite(v.X) && Finite(v.Y) && Finite(v.Z), "Non-finite physics vector.");
        private static void Envelope(PhysicsVector v, float limit) => Require(Math.Abs(v.X) <= limit && Math.Abs(v.Y) <= limit && Math.Abs(v.Z) <= limit, "Physics geometry exceeds the qualified +/-4096m envelope.");
        private static void Rotation(PhysicsRotation q) => Require(q != null && Finite(q.X) && Finite(q.Y) && Finite(q.Z) && Finite(q.W) && Math.Abs(q.X * q.X + q.Y * q.Y + q.Z * q.Z + q.W * q.W - 1) < 0.0001f, "Rotation must be a unit quaternion.");
        private static bool Finite(float x) => !float.IsNaN(x) && !float.IsInfinity(x);
        private static bool Nonnegative(float x) => Finite(x) && x >= 0;
        private static void Require(bool ok, string message) { if (!ok) throw new InvalidOperationException(message); }
        private static void Write(StringBuilder output, object value)
        {
            if (value == null) { output.Append("null"); return; }
            if (value is string text) { Quote(output, text); return; }
            if (value is bool boolean) { output.Append(boolean ? "true" : "false"); return; }
            if (value is float number) { Require(Finite(number), "Non-finite JSON number."); output.Append(number.ToString("R", CultureInfo.InvariantCulture)); return; }
            if (value is int || value is uint || value is ulong) { output.Append(Convert.ToString(value, CultureInfo.InvariantCulture)); return; }
            if (value is IEnumerable items)
            {
                output.Append('['); var separator = "";
                foreach (var item in items) { output.Append(separator); Write(output, item); separator = ","; }
                output.Append(']'); return;
            }
            Require(value.GetType().Namespace == typeof(PhysicsSource).Namespace && value.GetType().Name.StartsWith("Physics", StringComparison.Ordinal), "Unsupported physics JSON type.");
            output.Append('{'); var comma = "";
            foreach (var field in value.GetType().GetFields(BindingFlags.Public | BindingFlags.Instance).OrderBy(x => x.Name, StringComparer.Ordinal))
            { output.Append(comma); Quote(output, field.Name); output.Append(':'); Write(output, field.GetValue(value)); comma = ","; }
            output.Append('}');
        }
        private static void Quote(StringBuilder output, string value)
        {
            output.Append('"');
            foreach (var c in value)
            {
                if (c == '"' || c == '\\') { output.Append('\\'); output.Append(c); }
                else if (c < 32 || char.IsSurrogate(c)) output.Append("\\u").Append(((int)c).ToString("x4", CultureInfo.InvariantCulture));
                else output.Append(c);
            }
            output.Append('"');
        }
    }
}
