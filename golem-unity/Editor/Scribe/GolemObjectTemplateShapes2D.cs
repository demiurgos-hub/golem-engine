using System;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Converts one Collider2D below a root at the identity transform into an exact root-local shape.
    /// Only circles and boxes under quarter-turn Z rotations are representable.
    /// </summary>
    internal static class GolemObjectTemplateShapes2D
    {
        private const float Tolerance = 1e-4f;

        internal static ObjectTemplateShape Capture(Collider2D collider)
        {
            try
            {
                var layer = LayerMask.LayerToName(collider.gameObject.layer);
                if (string.IsNullOrEmpty(layer)) throw new InvalidOperationException("Unity layer " + collider.gameObject.layer + " has no name.");
                var matrix = collider.transform.localToWorldMatrix;
                var x = (Vector3)matrix.GetColumn(0); var y = (Vector3)matrix.GetColumn(1);
                var sx = x.magnitude; var sy = y.magnitude;
                if (!(sx > 0) || !(sy > 0) || Mathf.Abs(x.z) > Tolerance || Mathf.Abs(y.z) > Tolerance)
                    throw new InvalidOperationException("hierarchy scale must be positive and stay in the XY plane.");
                var rx = new Vector2(x.x, x.y) / sx; var ry = new Vector2(y.x, y.y) / sy;
                if (!QuarterAxis(rx) || !Near(ry.x, -rx.y) || !Near(ry.y, rx.x))
                    throw new InvalidOperationException("rotation must be an exact quarter turn around Z without reflection.");
                AxisScales(collider.transform, out sx, out sy);
                if (collider is CircleCollider2D circle)
                {
                    if (!Near(sx, sy)) throw new InvalidOperationException("CircleCollider2D needs uniform XY scale.");
                    var center = matrix.MultiplyPoint3x4(circle.offset);
                    return new ObjectTemplateShape { Kind = "circle", Radius = Positive(circle.radius * sx), OffsetX = center.x, OffsetY = center.y,
                        Layer = layer, Trigger = circle.isTrigger };
                }
                if (collider is BoxCollider2D box)
                {
                    if (Mathf.Abs(box.edgeRadius) > Tolerance) throw new InvalidOperationException("BoxCollider2D.edgeRadius must be 0.");
                    var center = matrix.MultiplyPoint3x4(box.offset);
                    var width = Positive(box.size.x * sx); var height = Positive(box.size.y * sy);
                    var quarter = Mathf.Abs(rx.x) < 0.5f;
                    return new ObjectTemplateShape { Kind = "box", Width = quarter ? height : width, Height = quarter ? width : height,
                        OffsetX = center.x, OffsetY = center.y, Layer = layer, Trigger = box.isTrigger };
                }
                throw new InvalidOperationException("only CircleCollider2D and BoxCollider2D are supported.");
            }
            catch (InvalidOperationException ex) { throw new InvalidOperationException(collider.name + " (" + collider.GetType().Name + "): " + ex.Message, ex); }
        }

        // Matrix column lengths carry quaternion rounding; multiplying authored scales keeps exact sizes.
        private static void AxisScales(Transform transform, out float sx, out float sy)
        {
            sx = 1; sy = 1;
            var swapped = false;
            for (var t = transform; t != null; t = t.parent)
            {
                var scale = t.localScale;
                sx *= Mathf.Abs(swapped ? scale.y : scale.x);
                sy *= Mathf.Abs(swapped ? scale.x : scale.y);
                if (Mathf.Abs((t.localRotation * Vector3.right).x) < 0.5f) swapped = !swapped;
            }
        }

        private static bool QuarterAxis(Vector2 axis) =>
            Near(Mathf.Abs(axis.x), 1) && Near(axis.y, 0) || Near(axis.x, 0) && Near(Mathf.Abs(axis.y), 1);

        private static bool Near(float a, float b) => Mathf.Abs(a - b) <= Tolerance;

        private static float Positive(float value)
        {
            if (!(value > 0) || float.IsInfinity(value)) throw new InvalidOperationException("collider size must be positive after hierarchy scale.");
            return value;
        }
    }
}
