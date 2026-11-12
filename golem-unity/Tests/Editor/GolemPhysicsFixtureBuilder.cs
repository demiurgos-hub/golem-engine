using System;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    /// <summary>Creates engine-owned authored geometry and Unity reference queries in the validation project.</summary>
    public static class GolemPhysicsFixtureBuilder
    {
        [Serializable] private sealed class ReferenceDocument { public string Version = "unity-physics-reference-v1"; public string UnityVersion; public Query[] Queries; }
        [Serializable] private sealed class Query { public string Name, Type; public Vector3 From, Direction; public float Radius, Distance; public Hit[] Hits; }
        [Serializable] private sealed class Hit { public string ColliderID; public float Distance; public Vector3 Point, Normal; }

        /// <summary>Builds a saved multi-shape scene and writes exported source plus authoritative Unity query observations.</summary>
        public static void Generate()
        {
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            if (string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set GOLEM_PHYSICS_FIXTURE_OUTPUT to a validation output directory.");
            Directory.CreateDirectory(output);
            const string assets = "Assets/GolemPhysicsFixture";
            if (!AssetDatabase.IsValidFolder(assets)) AssetDatabase.CreateFolder("Assets", "GolemPhysicsFixture");
            var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            var world = new GameObject("Physics Fixture").AddComponent<GolemPhysicsWorld>(); world.WorldID = "unity-physics-fixture";
            Box(world, "Ground", new Vector3(0, -0.5f, 0), new Vector3(32, 1, 32), Quaternion.identity);
            Box(world, "Rotated Wall", new Vector3(0, 2, 4), new Vector3(8, 4, 0.5f), Quaternion.Euler(0, 25, 0));
            Box(world, "Bridge", new Vector3(6, 3, 0), new Vector3(4, 0.5f, 12), Quaternion.identity);
            Box(world, "Sloped Ramp", new Vector3(-7, 1, 0), new Vector3(3, 0.4f, 6), Quaternion.Euler(20, 0, 0));
            for (var i = 0; i < 4; i++) Box(world, "Step " + i, new Vector3(-3, 0.15f * (i + 1), 3 + i * 0.7f), new Vector3(2, 0.3f * (i + 1), 0.7f), Quaternion.identity);
            var sensor = Box(world, "Sensor", new Vector3(0, 1, -3), new Vector3(4, 2, 0.2f), Quaternion.identity); sensor.isTrigger = true;
            var sphere = new GameObject("Sphere"); sphere.transform.SetParent(world.transform); sphere.transform.position = new Vector3(3, 1, -4); sphere.AddComponent<SphereCollider>().radius = 0.75f;
            var capsule = new GameObject("Capsule"); capsule.transform.SetParent(world.transform); capsule.transform.SetPositionAndRotation(new Vector3(-4, 1, -4), Quaternion.Euler(0, 0, 15));
            capsule.AddComponent<CapsuleCollider>().height = 2;
            var mesh = new Mesh { vertices = new[] { Vector3.zero, new Vector3(0, 0, 4), new Vector3(4, 2, 0) }, triangles = new[] { 0, 1, 2 } };
            AssetDatabase.CreateAsset(mesh, assets + "/ramp.asset");
            var ramp = new GameObject("Reflected mesh"); ramp.transform.SetParent(world.transform); ramp.transform.position = new Vector3(22, 0, 0); ramp.transform.localScale = new Vector3(-1, 1, 1); ramp.AddComponent<MeshCollider>().sharedMesh = mesh;
            var terrainData = new TerrainData { heightmapResolution = 33, size = new Vector3(32, 8, 32) };
            var holes = new bool[32, 32]; for (var z = 0; z < 32; z++) for (var x = 0; x < 32; x++) holes[z, x] = true;
            holes[4, 5] = false; terrainData.SetHoles(0, 0, holes); AssetDatabase.CreateAsset(terrainData, assets + "/terrain.asset");
            var terrain = new GameObject("Terrain with hole"); terrain.transform.SetParent(world.transform); terrain.transform.position = new Vector3(-40, 0, -40); terrain.AddComponent<TerrainCollider>().terrainData = terrainData;
            if (!EditorSceneManager.SaveScene(scene, assets + "/physics.unity")) throw new InvalidOperationException("Fixture scene save failed.");
            var source = GolemPhysicsExporter.Capture(world);
            File.WriteAllText(Path.Combine(output, "unity-physics.source.json"), GolemPhysicsDocument.Build(source));
            UnityEngine.Physics.SyncTransforms();
            var queries = new[]
            {
                Observe("stacked-floor-ray", "ray", new Vector3(6, 8, 0), Vector3.down, 0, 12),
                Observe("rotated-wall-sphere", "sphere", new Vector3(0, 1, -6), Vector3.forward, 0.5f, 12),
                Observe("capsule-ray", "ray", new Vector3(-4, 1, -8), Vector3.forward, 0, 8),
                Observe("sphere-sweep", "sphere", new Vector3(3, 1, -8), Vector3.forward, 0.25f, 8),
                Observe("ground-ray", "ray", new Vector3(-10, 4, 0), Vector3.down, 0, 8),
                Observe("reflected-mesh-ray", "ray", new Vector3(21, 4, 1), Vector3.down, 0, 8),
                Observe("terrain-ray", "ray", new Vector3(-30, 4, -30), Vector3.down, 0, 8),
                Observe("terrain-hole-ray", "ray", new Vector3(-34.5f, 4, -35.5f), Vector3.down, 0, 8),
                Observe("initial-overlap-sphere", "sphere", new Vector3(3, 1, -4), Vector3.forward, 0.25f, 2)
            };
            var reference = new ReferenceDocument { UnityVersion = Application.unityVersion, Queries = queries };
            File.WriteAllText(Path.Combine(output, "unity-physics.reference.json"), JsonUtility.ToJson(reference, true));
            Debug.Log("Golem physics fixture source and query observations written to " + output);
        }

        private static Query Observe(string name, string type, Vector3 from, Vector3 direction, float radius, float distance)
        {
            var hits = type == "ray" ? UnityEngine.Physics.RaycastAll(from, direction, distance, ~0, QueryTriggerInteraction.Collide)
                : UnityEngine.Physics.SphereCastAll(from, radius, direction, distance, ~0, QueryTriggerInteraction.Collide);
            return new Query
            {
                Name = name, Type = type, From = from, Direction = direction, Radius = radius, Distance = distance,
                Hits = hits.OrderBy(h => h.distance).ThenBy(h => GlobalObjectId.GetGlobalObjectIdSlow(h.collider).ToString(), StringComparer.Ordinal)
                    .Select(h => new Hit { ColliderID = "unity-object:" + GlobalObjectId.GetGlobalObjectIdSlow(h.collider), Distance = h.distance, Point = h.point, Normal = h.normal }).ToArray()
            };
        }

        private static BoxCollider Box(GolemPhysicsWorld world, string name, Vector3 position, Vector3 size, Quaternion rotation)
        {
            var item = new GameObject(name); item.transform.SetParent(world.transform); item.transform.SetPositionAndRotation(position, rotation);
            var collider = item.AddComponent<BoxCollider>(); collider.size = size; return collider;
        }
    }
}
