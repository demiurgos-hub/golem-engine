using System;
using System.Collections.Generic;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Draws authoring/reference geometry and explicitly loaded bounded Go server debug captures.</summary>
    public sealed class GolemPhysicsPreview : EditorWindow
    {
        private PhysicsSource source;
        private string error;
        private bool showInactive;
        private Vector3 from = new Vector3(0, 2, -5), direction = Vector3.forward;
        private float distance = 10, radius;
        private RaycastHit[] hits = Array.Empty<RaycastHit>();
        private bool queried;
        private const int TrianglePreviewLimit = 50000;
        private GoPhysicsDebugCapture server;
        private bool serverCandidates, serverLabels = true, showServer = true;

        [MenuItem("Tools/Golem/Scribe/Preview Physics World")]
        private static void Open() => GetWindow<GolemPhysicsPreview>("Golem Physics");

        private void OnEnable() => SceneView.duringSceneGui += Draw;
        private void OnDisable() => SceneView.duringSceneGui -= Draw;

        private void OnGUI()
        {
            EditorGUILayout.LabelField("Go server debug capture", EditorStyles.boldLabel);
            if (GUILayout.Button("Load Go physics debug JSON"))
            {
                var path = EditorUtility.OpenFilePanel("Go physics debug capture", "", "json");
                if (!string.IsNullOrEmpty(path))
                {
                    try { server = GolemPhysicsDebug.ReadFile(path); error = null; SceneView.RepaintAll(); }
                    catch (Exception ex) { error = ex.Message; }
                }
            }
            if (server != null)
            {
                EditorGUILayout.LabelField(server.Status + ": accepted frame " + server.AcceptedFrame + ", annotation frame " + server.Frame);
                if (server.Config != null) EditorGUILayout.LabelField(server.Config.TickRate + " Hz / " + server.Config.Substeps + " substeps / " + server.Config.Workers + " workers / continuous " + server.Config.Continuous);
                showServer = EditorGUILayout.Toggle("Show server capture", showServer);
                serverLabels = EditorGUILayout.Toggle("Show server identities", serverLabels);
                serverCandidates = EditorGUILayout.Toggle("Show UNCOMMITTED poses", serverCandidates);
                EditorGUILayout.HelpBox("Green/cyan/yellow: accepted solid/sensor/query geometry. Orange: UNCOMMITTED native poses and attempted paths. Blue: accepted movement / dotted navigation intents. Red: contacts; magenta: query normals. Convex query inputs display their support points. Captures never modify the scene.", MessageType.Info);
                if (!string.IsNullOrEmpty(server.Fault)) EditorGUILayout.HelpBox(server.Fault, MessageType.Warning);
                if (server.Incomplete) EditorGUILayout.HelpBox("INCOMPLETE capture: omitted " + server.Omitted.Bodies + " bodies, " + server.Omitted.Colliders + " colliders, " + server.Omitted.Geometry + " geometry leaves, " + server.Omitted.Vertices + " vertices, " + server.Omitted.Triangles + " triangles, " + server.Omitted.Records + " annotation records. Omitted geometry is not represented by boxes.", MessageType.Warning);
                if (server.Geometry.Sum(g => (long)g.Indices.Length / 3) > TrianglePreviewLimit) EditorGUILayout.HelpBox("Scene view has a separate 50,000-triangle budget and omits whole geometry leaves beyond it. The loaded capture remains unchanged.", MessageType.Warning);
            }
            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Portable collider geometry", EditorStyles.boldLabel);
            if (GUILayout.Button("Capture selected saved Physics World"))
            {
                try
                {
                    var world = Selection.activeGameObject ? Selection.activeGameObject.GetComponentInParent<GolemPhysicsWorld>(true) : null;
                    source = GolemPhysicsExporter.Capture(world); error = null; SceneView.RepaintAll();
                }
                catch (Exception ex) { error = ex.Message; }
            }
            if (!string.IsNullOrEmpty(error)) EditorGUILayout.HelpBox(error, MessageType.Error);
            if (source != null)
            {
                EditorGUILayout.LabelField(source.WorldID + ": " + source.Bodies.Length + " bodies, " + source.Colliders.Length + " colliders, " + source.Joints.Length + " joints");
                showInactive = EditorGUILayout.Toggle("Show inactive / disabled", showInactive);
                EditorGUILayout.HelpBox("Green: solid; cyan: sensor; yellow: query-only; gray: inactive. Geometry is captured body-local source transformed by exported body poses. Recapture after authoring changes.", MessageType.Info);
                if (source.Assets.Sum(a => (long)a.Indices.Length / 3) > TrianglePreviewLimit)
                    EditorGUILayout.HelpBox("Mesh preview displays at most 50,000 triangles. Export and server geometry remain complete.", MessageType.Warning);
            }
            EditorGUILayout.Space(); EditorGUILayout.LabelField("Unity reference query", EditorStyles.boldLabel);
            from = EditorGUILayout.Vector3Field("Start", from); direction = EditorGUILayout.Vector3Field("Direction", direction);
            distance = Mathf.Max(0, EditorGUILayout.FloatField("Distance", distance)); radius = Mathf.Max(0, EditorGUILayout.FloatField("Sphere radius (0 = ray)", radius));
            if (GUILayout.Button("Run query including sensors"))
            {
                UnityEngine.Physics.SyncTransforms();
                hits = radius == 0 ? UnityEngine.Physics.RaycastAll(from, direction.normalized, distance, ~0, QueryTriggerInteraction.Collide)
                    : UnityEngine.Physics.SphereCastAll(from, radius, direction.normalized, distance, ~0, QueryTriggerInteraction.Collide);
                queried = true; SceneView.RepaintAll();
            }
            if (queried) EditorGUILayout.LabelField(hits.Length + " Unity hits; magenta normals");
        }

        private void Draw(SceneView view)
        {
            if (server != null && showServer) GolemPhysicsDebugView.Draw(server, serverCandidates, serverLabels, TrianglePreviewLimit);
            if (source != null)
            {
                var bodies = source.Bodies.ToDictionary(b => b.ID); var assets = source.Assets.ToDictionary(a => a.ID);
                var triangles = 0;
                foreach (var collider in source.Colliders)
                {
                    var body = bodies[collider.BodyID]; var enabled = collider.Enabled && collider.Active && body.Active && body.Enabled;
                    if (!enabled && !showInactive) continue;
                    Handles.color = !enabled ? Color.gray : collider.Sensor ? Color.cyan : collider.Solid ? Color.green : Color.yellow;
                    var bodyMatrix = Matrix4x4.TRS(Vector(body.Position), Rotation(body.Rotation), Vector3.one);
                    var shapeMatrix = bodyMatrix * Matrix4x4.TRS(Vector(collider.Center), Rotation(collider.Rotation), Vector3.one);
                    using (new Handles.DrawingScope(shapeMatrix))
                    {
                        if (collider.Shape == "box") Handles.DrawWireCube(Vector3.zero, Vector(collider.Size));
                        else if (collider.Shape == "sphere") Sphere(Vector3.zero, collider.Radius);
                        else if (collider.Shape == "capsule")
                        {
                            var axis = collider.Axis == 0 ? Vector3.right : collider.Axis == 1 ? Vector3.up : Vector3.forward;
                            var half = axis * (collider.Height / 2 - collider.Radius); Sphere(half, collider.Radius); Sphere(-half, collider.Radius);
                            var side = Vector3.Cross(axis, collider.Axis == 1 ? Vector3.right : Vector3.up).normalized * collider.Radius;
                            var other = Vector3.Cross(axis, side);
                            foreach (var offset in new[] { side, -side, other, -other }) Handles.DrawLine(-half + offset, half + offset);
                        }
                        else if (assets.TryGetValue(collider.AssetID, out var geometry))
                        {
                            for (var i = 0; i < geometry.Indices.Length && triangles < TrianglePreviewLimit; i += 3, triangles++)
                            {
                                var a = Vector(geometry.Vertices[geometry.Indices[i]]); var b = Vector(geometry.Vertices[geometry.Indices[i + 1]]); var c = Vector(geometry.Vertices[geometry.Indices[i + 2]]);
                                Handles.DrawLine(a, b); Handles.DrawLine(b, c); Handles.DrawLine(c, a);
                            }
                        }
                    }
                }
            }
            if (!queried) return;
            Handles.color = Color.magenta; Handles.DrawLine(from, from + direction.normalized * distance);
            if (radius > 0) { Sphere(from, radius); Sphere(from + direction.normalized * distance, radius); }
            foreach (var hit in hits)
            {
                Handles.DrawLine(hit.point, hit.point + hit.normal * 0.7f);
                Handles.Label(hit.point, hit.collider ? hit.collider.name + " " + hit.distance.ToString("0.###") + "m" : "removed collider");
            }
        }

        private static void Sphere(Vector3 center, float size)
        {
            Handles.DrawWireDisc(center, Vector3.up, size); Handles.DrawWireDisc(center, Vector3.right, size); Handles.DrawWireDisc(center, Vector3.forward, size);
        }
        private static Vector3 Vector(PhysicsVector v) => new Vector3(v.X, v.Y, v.Z);
        private static Quaternion Rotation(PhysicsRotation q) => new Quaternion(q.X, q.Y, q.Z, q.W);
    }
}
