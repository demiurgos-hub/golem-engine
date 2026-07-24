using System;
using System.Collections.Generic;
using System.Globalization;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Converts Unity colliders under a prefab root into exact root-local footprint shapes.
    /// Supports only geometry and transforms that Golem can represent without approximation.
    /// </summary>
    public static class GolemFootprintConverter
    {
        /// <summary>Absolute tolerance for axis alignment and scale uniformity checks.</summary>
        public const float Tolerance = 1e-4f;

        /// <summary>
        /// Collects exportable shapes from <paramref name="root"/>, including inactive children.
        /// Enabled colliders on inactive GameObjects are exported; disabled <see cref="Collider"/> /
        /// <see cref="Collider2D"/> components are skipped. Colliders outside
        /// <paramref name="includedLayers"/> are ignored; unsupported colliders inside the mask
        /// yield errors (no partial shape list for that prefab).
        /// </summary>
        public static bool TryConvert(
            Transform root,
            int dimensions,
            LayerMask includedLayers,
            out List<GolemFootprintShapeModel> shapes,
            out List<string> errors)
        {
            shapes = new List<GolemFootprintShapeModel>();
            errors = new List<string>();
            if (root == null)
            {
                errors.Add("Prefab root transform is required.");
                return false;
            }

            if (dimensions != 2 && dimensions != 3)
            {
                errors.Add("simulation.dimensions must be 2 or 3, got " + dimensions + ".");
                return false;
            }

            var colliders2D = root.GetComponentsInChildren<Collider2D>(true);
            var colliders3D = root.GetComponentsInChildren<Collider>(true);

            if (dimensions == 2)
            {
                foreach (var collider in colliders2D)
                {
                    if (!ShouldConsiderCollider(collider, includedLayers))
                    {
                        continue;
                    }

                    if (!TryConvert2D(root, collider, out var shape, out var error))
                    {
                        errors.Add(FormatColliderError(collider, error));
                        continue;
                    }

                    shapes.Add(shape);
                }

                foreach (var collider in colliders3D)
                {
                    if (!ShouldConsiderCollider(collider, includedLayers))
                    {
                        continue;
                    }

                    errors.Add(
                        FormatColliderError(
                            collider,
                            "3D collider is not valid when simulation.dimensions=2."));
                }
            }
            else
            {
                foreach (var collider in colliders3D)
                {
                    if (!ShouldConsiderCollider(collider, includedLayers))
                    {
                        continue;
                    }

                    if (!TryConvert3D(root, collider, out var shape, out var error))
                    {
                        errors.Add(FormatColliderError(collider, error));
                        continue;
                    }

                    shapes.Add(shape);
                }

                foreach (var collider in colliders2D)
                {
                    if (!ShouldConsiderCollider(collider, includedLayers))
                    {
                        continue;
                    }

                    errors.Add(
                        FormatColliderError(
                            collider,
                            "2D collider is not valid when simulation.dimensions=3."));
                }
            }

            if (errors.Count > 0)
            {
                shapes.Clear();
                return false;
            }

            return true;
        }

        /// <summary>True when <paramref name="layer"/> is set in <paramref name="mask"/>.</summary>
        public static bool IsLayerIncluded(int layer, LayerMask mask)
        {
            if (layer < 0 || layer > 31)
            {
                return false;
            }

            return (mask.value & (1 << layer)) != 0;
        }

        /// <summary>
        /// True when a 3D collider should be considered for export: non-null, enabled, and on an included layer.
        /// Inactive GameObjects are still considered when the component itself is enabled.
        /// </summary>
        public static bool ShouldConsiderCollider(Collider collider, LayerMask includedLayers)
        {
            return collider != null &&
                   collider.enabled &&
                   IsLayerIncluded(collider.gameObject.layer, includedLayers);
        }

        /// <summary>
        /// True when a 2D collider should be considered for export: non-null, enabled, and on an included layer.
        /// Inactive GameObjects are still considered when the component itself is enabled.
        /// </summary>
        public static bool ShouldConsiderCollider(Collider2D collider, LayerMask includedLayers)
        {
            return collider != null &&
                   collider.enabled &&
                   IsLayerIncluded(collider.gameObject.layer, includedLayers);
        }

        /// <summary>Resolves a Unity layer name; rejects unnamed layers.</summary>
        public static bool TryGetLayerName(int layer, out string name, out string error)
        {
            name = null;
            error = null;
            if (layer < 0 || layer > 31)
            {
                error = "layer index " + layer + " is out of range.";
                return false;
            }

            name = LayerMask.LayerToName(layer);
            if (string.IsNullOrEmpty(name))
            {
                error = "Unity layer " + layer + " has no name.";
                return false;
            }

            return true;
        }

        internal static bool TryConvert2D(
            Transform root,
            Collider2D collider,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            error = null;

            if (collider is CircleCollider2D circle)
            {
                return TryConvertCircle2D(root, circle, out shape, out error);
            }

            if (collider is BoxCollider2D box)
            {
                return TryConvertBox2D(root, box, out shape, out error);
            }

            error = "unsupported 2D collider type " + collider.GetType().Name +
                    " (only CircleCollider2D and BoxCollider2D are supported).";
            return false;
        }

        internal static bool TryConvert3D(
            Transform root,
            Collider collider,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            error = null;

            if (collider is SphereCollider sphere)
            {
                return TryConvertSphere3D(root, sphere, out shape, out error);
            }

            if (collider is BoxCollider box)
            {
                return TryConvertBox3D(root, box, out shape, out error);
            }

            error = "unsupported 3D collider type " + collider.GetType().Name +
                    " (only SphereCollider and BoxCollider are supported).";
            return false;
        }

        private static bool TryConvertCircle2D(
            Transform root,
            CircleCollider2D circle,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            if (!TryGetLayerName(circle.gameObject.layer, out var layer, out error))
            {
                return false;
            }

            if (!TryBuildLocalToRoot(root, circle.transform, out var localToRoot, out error))
            {
                return false;
            }

            if (!TryExtractScale2D(localToRoot, out var sx, out var sy, out error))
            {
                return false;
            }

            if (!AreNearlyEqual(sx, sy))
            {
                error = "CircleCollider2D requires uniform XY hierarchy scale; got (" +
                        Format(sx) + ", " + Format(sy) + ").";
                return false;
            }

            if (!TryExtractQuarterTurn2D(localToRoot, sx, sy, out _, out error))
            {
                return false;
            }

            var center = localToRoot.MultiplyPoint3x4(new Vector3(circle.offset.x, circle.offset.y, 0f));
            if (!IsFinite(center.x) || !IsFinite(center.y))
            {
                error = "circle center offset is not finite.";
                return false;
            }

            var radius = circle.radius * sx;
            if (!(radius > 0f) || !IsFinite(radius))
            {
                error = "circle radius must be > 0 after hierarchy scale.";
                return false;
            }

            // Circles are rotation-invariant; Z offset is discarded in 2D root-local space.
            shape = new GolemFootprintShapeModel
            {
                Type = "circle",
                R = radius,
                OffsetX = center.x,
                OffsetY = center.y,
                Trigger = circle.isTrigger,
                Layer = layer
            };
            return true;
        }

        private static bool TryConvertBox2D(
            Transform root,
            BoxCollider2D box,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            if (!TryGetLayerName(box.gameObject.layer, out var layer, out error))
            {
                return false;
            }

            if (Mathf.Abs(box.edgeRadius) > Tolerance)
            {
                error = "BoxCollider2D.edgeRadius must be 0 for exact export.";
                return false;
            }

            if (!TryBuildLocalToRoot(root, box.transform, out var localToRoot, out error))
            {
                return false;
            }

            if (!TryExtractScale2D(localToRoot, out var sx, out var sy, out error))
            {
                return false;
            }

            if (!TryExtractQuarterTurn2D(localToRoot, sx, sy, out var turn, out error))
            {
                return false;
            }

            var center = localToRoot.MultiplyPoint3x4(new Vector3(box.offset.x, box.offset.y, 0f));
            if (!IsFinite(center.x) || !IsFinite(center.y))
            {
                error = "box center offset is not finite.";
                return false;
            }

            var size = box.size;
            if (!(size.x > 0f) || !(size.y > 0f) || !IsFinite(size.x) || !IsFinite(size.y))
            {
                error = "BoxCollider2D.size components must be > 0.";
                return false;
            }

            var vx = localToRoot.MultiplyVector(new Vector3(size.x, 0f, 0f));
            var vy = localToRoot.MultiplyVector(new Vector3(0f, size.y, 0f));
            if (!IsAxisAligned2D(vx, vy, out error))
            {
                return false;
            }

            var w = Mathf.Abs(vx.x) + Mathf.Abs(vy.x);
            var h = Mathf.Abs(vx.y) + Mathf.Abs(vy.y);
            if (!(w > 0f) || !(h > 0f) || !IsFinite(w) || !IsFinite(h))
            {
                error = "box extents must be > 0 after hierarchy scale.";
                return false;
            }

            // turn is validated for supported hierarchy rotation; extents already account for swaps.
            _ = turn;

            shape = new GolemFootprintShapeModel
            {
                Type = "aabb",
                W = w,
                H = h,
                OffsetX = center.x,
                OffsetY = center.y,
                Trigger = box.isTrigger,
                Layer = layer
            };
            return true;
        }

        private static bool TryConvertSphere3D(
            Transform root,
            SphereCollider sphere,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            if (!TryGetLayerName(sphere.gameObject.layer, out var layer, out error))
            {
                return false;
            }

            if (!TryBuildLocalToRoot(root, sphere.transform, out var localToRoot, out error))
            {
                return false;
            }

            if (!TryExtractScale3D(localToRoot, out var sx, out var sy, out var sz, out error))
            {
                return false;
            }

            if (!AreNearlyEqual(sx, sy) || !AreNearlyEqual(sy, sz))
            {
                error = "SphereCollider requires uniform XYZ hierarchy scale; got (" +
                        Format(sx) + ", " + Format(sy) + ", " + Format(sz) + ").";
                return false;
            }

            if (!TryExtractYawQuarterTurn3D(localToRoot, sx, sy, sz, out _, out error))
            {
                return false;
            }

            var center = localToRoot.MultiplyPoint3x4(sphere.center);
            if (!IsFinite(center.x) || !IsFinite(center.y) || !IsFinite(center.z))
            {
                error = "sphere center offset is not finite.";
                return false;
            }

            var radius = sphere.radius * sx;
            if (!(radius > 0f) || !IsFinite(radius))
            {
                error = "sphere radius must be > 0 after hierarchy scale.";
                return false;
            }

            shape = new GolemFootprintShapeModel
            {
                Type = "sphere",
                R = radius,
                OffsetX = center.x,
                OffsetY = center.y,
                OffsetZ = center.z,
                Trigger = sphere.isTrigger,
                Layer = layer
            };
            return true;
        }

        private static bool TryConvertBox3D(
            Transform root,
            BoxCollider box,
            out GolemFootprintShapeModel shape,
            out string error)
        {
            shape = null;
            if (!TryGetLayerName(box.gameObject.layer, out var layer, out error))
            {
                return false;
            }

            if (!TryBuildLocalToRoot(root, box.transform, out var localToRoot, out error))
            {
                return false;
            }

            if (!TryExtractScale3D(localToRoot, out var sx, out var sy, out var sz, out error))
            {
                return false;
            }

            if (!TryExtractYawQuarterTurn3D(localToRoot, sx, sy, sz, out _, out error))
            {
                return false;
            }

            var center = localToRoot.MultiplyPoint3x4(box.center);
            if (!IsFinite(center.x) || !IsFinite(center.y) || !IsFinite(center.z))
            {
                error = "box center offset is not finite.";
                return false;
            }

            var size = box.size;
            if (!(size.x > 0f) || !(size.y > 0f) || !(size.z > 0f) ||
                !IsFinite(size.x) || !IsFinite(size.y) || !IsFinite(size.z))
            {
                error = "BoxCollider.size components must be > 0.";
                return false;
            }

            var vx = localToRoot.MultiplyVector(new Vector3(size.x, 0f, 0f));
            var vy = localToRoot.MultiplyVector(new Vector3(0f, size.y, 0f));
            var vz = localToRoot.MultiplyVector(new Vector3(0f, 0f, size.z));
            if (!IsAxisAligned3D(vx, vy, vz, out error))
            {
                return false;
            }

            var w = Mathf.Abs(vx.x) + Mathf.Abs(vy.x) + Mathf.Abs(vz.x);
            var h = Mathf.Abs(vx.y) + Mathf.Abs(vy.y) + Mathf.Abs(vz.y);
            var d = Mathf.Abs(vx.z) + Mathf.Abs(vy.z) + Mathf.Abs(vz.z);
            if (!(w > 0f) || !(h > 0f) || !(d > 0f) || !IsFinite(w) || !IsFinite(h) || !IsFinite(d))
            {
                error = "box extents must be > 0 after hierarchy scale.";
                return false;
            }

            shape = new GolemFootprintShapeModel
            {
                Type = "aabb",
                W = w,
                H = h,
                D = d,
                OffsetX = center.x,
                OffsetY = center.y,
                OffsetZ = center.z,
                Trigger = box.isTrigger,
                Layer = layer
            };
            return true;
        }

        private static bool TryBuildLocalToRoot(
            Transform root,
            Transform source,
            out Matrix4x4 localToRoot,
            out string error)
        {
            localToRoot = default;
            error = null;
            if (source == null)
            {
                error = "collider transform is missing.";
                return false;
            }

            // Prefab-root local space: inverse(root) * source, Unity Y-up.
            localToRoot = root.worldToLocalMatrix * source.localToWorldMatrix;
            return true;
        }

        private static bool TryExtractScale2D(
            Matrix4x4 localToRoot,
            out float sx,
            out float sy,
            out string error)
        {
            error = null;
            var xAxis = new Vector3(localToRoot.m00, localToRoot.m10, localToRoot.m20);
            var yAxis = new Vector3(localToRoot.m01, localToRoot.m11, localToRoot.m21);
            sx = xAxis.magnitude;
            sy = yAxis.magnitude;
            if (!(sx > 0f) || !(sy > 0f) || !IsFinite(sx) || !IsFinite(sy))
            {
                error = "hierarchy scale must be positive and finite on X/Y.";
                return false;
            }

            // Reject Z shear into the 2D plane (non-zero Z basis contribution on XY columns).
            if (Mathf.Abs(xAxis.z) > Tolerance || Mathf.Abs(yAxis.z) > Tolerance)
            {
                error = "2D collider hierarchy must not shear/tilt out of the XY plane.";
                return false;
            }

            return true;
        }

        private static bool TryExtractScale3D(
            Matrix4x4 localToRoot,
            out float sx,
            out float sy,
            out float sz,
            out string error)
        {
            error = null;
            var xAxis = new Vector3(localToRoot.m00, localToRoot.m10, localToRoot.m20);
            var yAxis = new Vector3(localToRoot.m01, localToRoot.m11, localToRoot.m21);
            var zAxis = new Vector3(localToRoot.m02, localToRoot.m12, localToRoot.m22);
            sx = xAxis.magnitude;
            sy = yAxis.magnitude;
            sz = zAxis.magnitude;
            if (!(sx > 0f) || !(sy > 0f) || !(sz > 0f) ||
                !IsFinite(sx) || !IsFinite(sy) || !IsFinite(sz))
            {
                error = "hierarchy scale must be positive and finite on X/Y/Z.";
                return false;
            }

            return true;
        }

        /// <summary>
        /// Validates that the rotation part is an exact Z quarter-turn (0/90/180/270) with no shear.
        /// Returns turn index 0..3.
        /// </summary>
        internal static bool TryExtractQuarterTurn2D(
            Matrix4x4 localToRoot,
            float sx,
            float sy,
            out int turn,
            out string error)
        {
            turn = 0;
            error = null;
            var rx = new Vector3(localToRoot.m00, localToRoot.m10, 0f) / sx;
            var ry = new Vector3(localToRoot.m01, localToRoot.m11, 0f) / sy;

            if (!IsUnitAxis2D(rx, out _) || !IsUnitAxis2D(ry, out _))
            {
                error = "2D collider rotation must be an exact quarter turn around Z (0/90/180/270).";
                return false;
            }

            // Expected: ry = rot90(rx) for a proper rotation (determinant +1).
            var expectedY = new Vector2(-rx.y, rx.x);
            if (!AreNearlyEqual(expectedY.x, ry.x) || !AreNearlyEqual(expectedY.y, ry.y))
            {
                error = "2D collider hierarchy has unsupported reflection/shear; only proper Z quarter turns are allowed.";
                return false;
            }

            turn = QuarterTurnIndexFromXAxis2D(rx);
            return true;
        }

        /// <summary>
        /// Validates yaw-only quarter turns around Y (Unity Y-up). Pitch, roll, and arbitrary angles fail.
        /// </summary>
        internal static bool TryExtractYawQuarterTurn3D(
            Matrix4x4 localToRoot,
            float sx,
            float sy,
            float sz,
            out int turn,
            out string error)
        {
            turn = 0;
            error = null;
            var rx = new Vector3(localToRoot.m00, localToRoot.m10, localToRoot.m20) / sx;
            var ry = new Vector3(localToRoot.m01, localToRoot.m11, localToRoot.m21) / sy;
            var rz = new Vector3(localToRoot.m02, localToRoot.m12, localToRoot.m22) / sz;

            // Y must stay +Y (no pitch/roll).
            if (!AreNearlyEqual(ry.x, 0f) || !AreNearlyEqual(ry.z, 0f) || !AreNearlyEqual(ry.y, 1f))
            {
                error = "3D collider rotation must be yaw-only around Y (no pitch/roll); got unsupported tilt.";
                return false;
            }

            if (!IsUnitAxisHorizontal(rx, out _) || !IsUnitAxisHorizontal(rz, out _))
            {
                error = "3D collider rotation must be an exact yaw quarter turn (0/90/180/270).";
                return false;
            }

            // Proper rotation: rz should equal yaw90(rx) with Y-up: (x,z) -> (z, -x) for +90° yaw
            // matching golem/footprint transformPoint3D case 1: x'=z, z'=-x.
            // At identity: rx=(1,0,0), rz=(0,0,1).
            // After +90° yaw: rx=(0,0,-1), rz=(1,0,0) => x'=z, z'=-x of original point.
            var expectedZ = new Vector3(-rx.z, 0f, rx.x);
            if (!AreNearlyEqual(expectedZ.x, rz.x) || !AreNearlyEqual(expectedZ.z, rz.z))
            {
                // Also allow the opposite convention if determinant matches via -expected?
                error = "3D collider hierarchy has unsupported reflection/shear; only proper Y yaw quarter turns are allowed.";
                return false;
            }

            turn = QuarterTurnIndexFromXAxis3D(rx);
            return true;
        }

        private static int QuarterTurnIndexFromXAxis2D(Vector3 rx)
        {
            // Map where +X goes: (1,0)->0, (0,1)->1, (-1,0)->2, (0,-1)->3
            if (AreNearlyEqual(rx.x, 1f) && AreNearlyEqual(rx.y, 0f))
            {
                return 0;
            }

            if (AreNearlyEqual(rx.x, 0f) && AreNearlyEqual(rx.y, 1f))
            {
                return 1;
            }

            if (AreNearlyEqual(rx.x, -1f) && AreNearlyEqual(rx.y, 0f))
            {
                return 2;
            }

            if (AreNearlyEqual(rx.x, 0f) && AreNearlyEqual(rx.y, -1f))
            {
                return 3;
            }

            return 0;
        }

        private static int QuarterTurnIndexFromXAxis3D(Vector3 rx)
        {
            // Identity: +X stays +X. +90° yaw (Unity Y-up): +X -> -Z => rx=(0,0,-1)
            if (AreNearlyEqual(rx.x, 1f) && AreNearlyEqual(rx.z, 0f))
            {
                return 0;
            }

            if (AreNearlyEqual(rx.x, 0f) && AreNearlyEqual(rx.z, -1f))
            {
                return 1;
            }

            if (AreNearlyEqual(rx.x, -1f) && AreNearlyEqual(rx.z, 0f))
            {
                return 2;
            }

            if (AreNearlyEqual(rx.x, 0f) && AreNearlyEqual(rx.z, 1f))
            {
                return 3;
            }

            return 0;
        }

        private static bool IsUnitAxis2D(Vector3 v, out int axis)
        {
            axis = -1;
            if (AreNearlyEqual(Mathf.Abs(v.x), 1f) && AreNearlyEqual(v.y, 0f))
            {
                axis = 0;
                return true;
            }

            if (AreNearlyEqual(v.x, 0f) && AreNearlyEqual(Mathf.Abs(v.y), 1f))
            {
                axis = 1;
                return true;
            }

            return false;
        }

        private static bool IsUnitAxisHorizontal(Vector3 v, out int axis)
        {
            axis = -1;
            if (!AreNearlyEqual(v.y, 0f))
            {
                return false;
            }

            if (AreNearlyEqual(Mathf.Abs(v.x), 1f) && AreNearlyEqual(v.z, 0f))
            {
                axis = 0;
                return true;
            }

            if (AreNearlyEqual(v.x, 0f) && AreNearlyEqual(Mathf.Abs(v.z), 1f))
            {
                axis = 2;
                return true;
            }

            return false;
        }

        private static bool IsAxisAligned2D(Vector3 vx, Vector3 vy, out string error)
        {
            error = null;
            if (!IsParallelToRootAxis2D(vx) || !IsParallelToRootAxis2D(vy))
            {
                error = "BoxCollider2D axes must remain axis-aligned after supported quarter turns.";
                return false;
            }

            return true;
        }

        private static bool IsAxisAligned3D(Vector3 vx, Vector3 vy, Vector3 vz, out string error)
        {
            error = null;
            if (!IsParallelToRootAxis3D(vx) || !IsParallelToRootAxis3D(vy) || !IsParallelToRootAxis3D(vz))
            {
                error = "BoxCollider axes must remain axis-aligned after supported yaw quarter turns.";
                return false;
            }

            return true;
        }

        private static bool IsParallelToRootAxis2D(Vector3 v)
        {
            var ax = Mathf.Abs(v.x);
            var ay = Mathf.Abs(v.y);
            var az = Mathf.Abs(v.z);
            if (az > Tolerance)
            {
                return false;
            }

            var dominant = Mathf.Max(ax, ay);
            if (dominant <= Tolerance)
            {
                return false;
            }

            return (ax <= Tolerance || AreNearlyEqual(ax, dominant)) &&
                   (ay <= Tolerance || AreNearlyEqual(ay, dominant)) &&
                   ((ax <= Tolerance) || (ay <= Tolerance));
        }

        private static bool IsParallelToRootAxis3D(Vector3 v)
        {
            var ax = Mathf.Abs(v.x);
            var ay = Mathf.Abs(v.y);
            var az = Mathf.Abs(v.z);
            var dominant = Mathf.Max(ax, Mathf.Max(ay, az));
            if (dominant <= Tolerance)
            {
                return false;
            }

            var nonzero = 0;
            if (ax > Tolerance)
            {
                nonzero++;
            }

            if (ay > Tolerance)
            {
                nonzero++;
            }

            if (az > Tolerance)
            {
                nonzero++;
            }

            return nonzero == 1;
        }

        internal static bool AreNearlyEqual(float a, float b)
        {
            return Mathf.Abs(a - b) <= Tolerance;
        }

        private static bool IsFinite(float v)
        {
            return !float.IsNaN(v) && !float.IsInfinity(v);
        }

        private static string Format(float v)
        {
            return v.ToString("G9", CultureInfo.InvariantCulture);
        }

        private static string FormatColliderError(Component collider, string message)
        {
            var path = collider != null ? GetTransformPath(collider.transform) : "<null>";
            var typeName = collider != null ? collider.GetType().Name : "Collider";
            return path + " (" + typeName + "): " + message;
        }

        private static string GetTransformPath(Transform t)
        {
            if (t == null)
            {
                return "<null>";
            }

            var stack = new Stack<string>();
            var current = t;
            while (current != null)
            {
                stack.Push(current.name);
                current = current.parent;
            }

            return string.Join("/", stack);
        }
    }
}
