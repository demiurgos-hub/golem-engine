using System;
using System.Collections.Generic;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    internal static class GolemPhysicsDebugView
    {
        internal static void Draw(GoPhysicsDebugCapture capture, bool candidates, bool labels, int triangleLimit)
        {
            var bodies = new Dictionary<string, GoDebugBody>(); var colliders = new Dictionary<string, GoDebugCollider>();
            foreach (var b in capture.Bodies) bodies.Add(b.State.Handle.Key, b);
            foreach (var c in capture.Colliders) colliders.Add(c.Handle.Key, c);
            foreach (var body in capture.Bodies)
            {
                var state = body.State; Handles.color = Color.white;
                if (labels) Handles.Label(state.Anchor.Position.Vector, state.ID + " body " + state.Handle.Key + " accepted frame " + capture.AcceptedFrame);
                if (state.Support != null && state.Support.Grounded)
                {
                    Handles.color = Color.green; var point = state.Support.Point.Vector;
                    Handles.DrawLine(point, point + state.Support.Normal.Vector * .7f);
                    Handles.DrawWireDisc(point, state.Support.Normal.Vector, .12f);
                    if (labels) Handles.Label(point, "support " + state.Support.Collider.Key);
                }
            }
            var remaining = triangleLimit;
            foreach (var geometry in capture.Geometry)
            {
                var count = geometry.Indices.Length / 3;
                if (count > remaining) continue; // Whole omitted leaves remain declared in the window.
                remaining -= count;
                var collider = colliders[geometry.Collider.Key]; var body = bodies[collider.Body.Key];
                Handles.color = !collider.Enabled || !body.State.Enabled ? Color.gray : collider.Roles != null && collider.Roles.Sensor ? Color.cyan : collider.Roles != null && collider.Roles.Solid ? Color.green : Color.yellow;
                using (new Handles.DrawingScope(body.State.Pose.Matrix)) DrawGeometry(geometry);
                if (labels) Handles.Label(body.State.Pose.Position.Vector, collider.ID + " collider " + collider.Handle.Key + " shape " + collider.ShapeID + " " + collider.Shape.Key + " child " + geometry.Child + " native " + geometry.BackendShapeIndex);
                if (candidates && body.Uncommitted != null)
                {
                    Handles.color = new Color(1, .35f, 0);
                    using (new Handles.DrawingScope(body.Uncommitted.Pose.Matrix)) DrawGeometry(geometry);
                    if (labels) Handles.Label(body.Uncommitted.Pose.Position.Vector, "UNCOMMITTED " + body.State.ID);
                }
            }
            Handles.color = capture.FrameAccepted ? new Color(.2f, .7f, 1) : new Color(1, .35f, 0);
            foreach (var path in capture.Paths)
            {
                for (var i = 1; i < path.Positions.Length; i++) Handles.DrawLine(path.Positions[i-1].Vector, path.Positions[i].Vector);
                if (labels && path.Positions.Length != 0) Handles.Label(path.Positions[path.Positions.Length-1].Vector, (capture.FrameAccepted ? "accepted movement " : "uncommitted attempted movement ") + path.Body.Key);
            }
            Handles.color = Color.red;
            foreach (var contact in capture.Contacts)
            {
                var point = contact.Point.Vector; Handles.DrawLine(point, point + contact.Normal.Vector * .7f);
                Handles.DrawWireDisc(point, contact.Normal.Vector.sqrMagnitude > 0 ? contact.Normal.Vector : Vector3.up, .06f);
                if (labels) Handles.Label(point, "contact " + contact.Pair.A.Key + " / " + contact.Pair.B.Key + " phase " + contact.Phase + " depth " + contact.Depth.ToString("0.#####") + "m" + (capture.FrameAccepted ? " accepted" : " uncommitted"));
            }
            Handles.color = Color.blue;
            foreach (var target in capture.Targets) { Handles.DrawDottedLine(target.From.Vector, target.Target.Vector, 4); if (labels) Handles.Label(target.Target.Vector, target.Label + " " + target.Body.Key); }
            foreach (var q in capture.Queries)
            {
                Handles.color = Color.yellow; var end = q.Query.Pose.Position.Vector + q.Query.Translation.Vector;
                Handles.DrawLine(q.Query.Pose.Position.Vector, end);
                if (q.Query.Kind != 1)
                {
                    using (new Handles.DrawingScope(q.Query.Pose.Matrix)) DrawQuery(q.Query.Shape);
                    using (new Handles.DrawingScope(Matrix4x4.TRS(end, q.Query.Pose.Rotation.Rotation, Vector3.one))) DrawQuery(q.Query.Shape);
                }
                if (labels) Handles.Label(q.Query.Pose.Position.Vector, q.Label + " hits " + q.Hits.Length + "/" + q.Result.Total + (q.Result.Incomplete ? " INCOMPLETE query" : "") + (q.Hits.Length < q.Result.Count ? " capture omitted hits" : "") + " " + q.Error);
                Handles.color = Color.magenta;
                foreach (var h in q.Hits) { Handles.DrawLine(h.Point.Vector, h.Point.Vector + h.Normal.Vector * .7f); if (labels) Handles.Label(h.Point.Vector, h.Content + " " + h.Collider.Key + " distance " + h.Distance.ToString("0.#####") + "m depth " + h.Depth.ToString("0.#####") + (h.Initial ? " initial" : "")); }
            }
        }
        private static void DrawQuery(GoDebugShape shape)
        {
            if (shape.Kind == 1) Sphere(Vector3.zero, shape.Radius);
            else if (shape.Kind == 2) Capsule(shape.PointA.Vector, shape.PointB.Vector, shape.Radius);
            else if (shape.Kind == 3) Handles.DrawWireCube(Vector3.zero, shape.HalfExtents.Vector * 2);
            else // Convex query input is a support-point cloud, not a cooked topology.
            { foreach (var point in shape.Vertices) Handles.DotHandleCap(0, point.Vector, Quaternion.identity, .025f, EventType.Repaint); }
        }
        private static void DrawGeometry(GoDebugGeometry geometry)
        {
            if (geometry.Kind == 1) Sphere(geometry.PointA.Vector, geometry.Radius);
            else if (geometry.Kind == 2) Capsule(geometry.PointA.Vector, geometry.PointB.Vector, geometry.Radius);
            else for (var i = 0; i < geometry.Indices.Length; i += 3)
            {
                var a = geometry.Vertices[geometry.Indices[i]].Vector; var b = geometry.Vertices[geometry.Indices[i+1]].Vector; var c = geometry.Vertices[geometry.Indices[i+2]].Vector;
                Handles.DrawLine(a, b); Handles.DrawLine(b, c); Handles.DrawLine(c, a);
            }
        }
        private static void Capsule(Vector3 a, Vector3 b, float radius)
        {
            Sphere(a, radius); Sphere(b, radius); var axis = (b-a).normalized;
            var side = Vector3.Cross(axis, Math.Abs(axis.y) < .9f ? Vector3.up : Vector3.right).normalized * radius;
            var other = Vector3.Cross(axis, side); foreach (var offset in new[] { side, -side, other, -other }) Handles.DrawLine(a+offset, b+offset);
        }
        private static void Sphere(Vector3 point, float radius) { Handles.DrawWireDisc(point, Vector3.up, radius); Handles.DrawWireDisc(point, Vector3.right, radius); Handles.DrawWireDisc(point, Vector3.forward, radius); }
    }
}
