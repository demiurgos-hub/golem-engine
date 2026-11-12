using System;
using System.Collections.Generic;
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
        [Serializable] private sealed class Query { public string Name, Type; public Vector3 From, Direction, WorldOffset; public float Radius, Distance; public Hit[] Hits; }
        [Serializable] private sealed class Hit { public string ColliderID; public float Distance; public Vector3 Point, Normal; }
        [Serializable] private sealed class TerrainAudit { public string UnityVersion, Asset; public int HeightmapResolution, TreeInstances, TreePrototypes; public Vector3 Size; }

        /// <summary>Reads an explicitly staged TerrainData asset without modifying it, recording native tree counts for content qualification.</summary>
        public static void AuditTerrain()
        {
            var asset = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_AUDIT_TERRAIN");
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            if (string.IsNullOrWhiteSpace(asset) || !asset.StartsWith("Assets/", StringComparison.Ordinal) || asset.Split('/').Any(p => p == ".."))
                throw new InvalidOperationException("GOLEM_PHYSICS_AUDIT_TERRAIN must name an asset copied into the engine-owned validation project.");
            if (string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set GOLEM_PHYSICS_FIXTURE_OUTPUT to a validation output directory.");
            var data = AssetDatabase.LoadAssetAtPath<TerrainData>(asset);
            if (!data) throw new InvalidOperationException("Cannot load staged TerrainData: " + asset);
            Directory.CreateDirectory(output);
            var result = new TerrainAudit { UnityVersion = Application.unityVersion, Asset = asset, HeightmapResolution = data.heightmapResolution,
                Size = data.size, TreeInstances = data.treeInstanceCount, TreePrototypes = data.treePrototypes.Length };
            using (var stream = new FileStream(Path.Combine(output, "terrain-audit.json"), FileMode.CreateNew))
            using (var writer = new StreamWriter(stream)) writer.Write(JsonUtility.ToJson(result, true));
        }

        [Serializable] private sealed class TerrainInput { public string Asset; public Vector3 Position; }
        [Serializable] private sealed class TerrainInputs { public TerrainInput[] Terrains; }
        [Serializable] private sealed class TerrainCoverage { public int TerrainCount, HoleCells; public Vector3[] CoordinateOffsets; public string Scope = "Actual copied terrain data at supplied authoring poses; repeated coordinate offsets test float32 placement, not additional resident world area."; }

        /// <summary>Exports explicitly staged terrain tiles and observes native rays/sweeps through interiors, edges, holes and repeated coordinate offsets.</summary>
        public static void GenerateTerrainReference()
        {
            var inputPath = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_TERRAIN_INPUT");
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            if (string.IsNullOrWhiteSpace(inputPath) || string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set terrain input and fixture output paths.");
            var input = JsonUtility.FromJson<TerrainInputs>(File.ReadAllText(inputPath));
            if (input?.Terrains == null || input.Terrains.Length == 0 || input.Terrains.Length > 16) throw new InvalidOperationException("Expected one to sixteen staged terrain tiles.");
            Directory.CreateDirectory(output);
            const string assets = "Assets/GolemTerrainReference";
            if (!AssetDatabase.IsValidFolder(assets)) AssetDatabase.CreateFolder("Assets", "GolemTerrainReference");
            var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            var world = new GameObject("Terrain Reference").AddComponent<GolemPhysicsWorld>(); world.WorldID = "copied-terrain-reference";
            var origins = new List<Vector3>();
            var names = new List<string>();
            var holesTotal = 0;
            for (var tile = 0; tile < input.Terrains.Length; tile++)
            {
                var item = input.Terrains[tile];
                if (item.Asset == null || !item.Asset.StartsWith("Assets/", StringComparison.Ordinal) || item.Asset.Split('/').Any(p => p == "..")) throw new InvalidOperationException("Terrain must be staged under Assets.");
                var data = AssetDatabase.LoadAssetAtPath<TerrainData>(item.Asset);
                if (!data) throw new InvalidOperationException("Missing staged TerrainData: " + item.Asset);
                var terrain = new GameObject("Terrain " + tile); terrain.transform.SetParent(world.transform); terrain.transform.position = item.Position;
                terrain.AddComponent<TerrainCollider>().terrainData = data;
                foreach (var coordinate in new[] { new Vector2(0.17313f, 0.29171f), new Vector2(0.41719f, 0.61317f), new Vector2(0.71813f, 0.83719f),
                    new Vector2(0.00002f, 0.33713f), new Vector2(0.99998f, 0.33713f), new Vector2(0.33713f, 0.00002f), new Vector2(0.33713f, 0.99998f) })
                {
                    origins.Add(item.Position + new Vector3(coordinate.x * data.size.x, data.size.y + 10, coordinate.y * data.size.z));
                    names.Add("tile-" + tile + "-sample-" + names.Count);
                }
                var holes = data.GetHoles(0, 0, data.holesResolution, data.holesResolution); var sampledHole = false;
                for (var z = 0; z < data.holesResolution; z++) for (var x = 0; x < data.holesResolution; x++)
                    if (!holes[z, x])
                    {
                        holesTotal++;
                        if (!sampledHole) { origins.Add(item.Position + new Vector3((x + 0.5f) * data.size.x / data.holesResolution, data.size.y + 10, (z + 0.5f) * data.size.z / data.holesResolution)); names.Add("tile-" + tile + "-hole"); sampledHole = true; }
                    }
            }
            if (!EditorSceneManager.SaveScene(scene, assets + "/terrain-reference.unity")) throw new InvalidOperationException("Cannot save terrain reference scene.");
            WriteNew(Path.Combine(output, "unity-physics.source.json"), GolemPhysicsDocument.Build(GolemPhysicsExporter.Capture(world)));
            var offsets = new[] { Vector3.zero, new Vector3(768, 0, 768), new Vector3(2304, 0, 2304) };
            var queries = new List<Query>();
            foreach (var offset in offsets)
            {
                world.transform.position = offset; UnityEngine.Physics.SyncTransforms();
                for (var i = 0; i < origins.Count; i++) foreach (var type in new[] { "ray", "sphere" })
                {
                    var query = Observe(names[i] + "-" + type + "-offset-" + offset.x, type, origins[i] + offset, Vector3.down, type == "sphere" ? 0.25f : 0, 512);
                    query.WorldOffset = offset; queries.Add(query);
                }
            }
            WriteNew(Path.Combine(output, "unity-physics.reference.json"), JsonUtility.ToJson(new ReferenceDocument { UnityVersion = Application.unityVersion, Queries = queries.ToArray() }, true));
            WriteNew(Path.Combine(output, "terrain-coverage.json"), JsonUtility.ToJson(new TerrainCoverage { TerrainCount = input.Terrains.Length, HoleCells = holesTotal, CoordinateOffsets = offsets }, true));
        }

        private static void WriteNew(string path, string value)
        {
            using (var stream = new FileStream(path, FileMode.CreateNew))
            using (var writer = new StreamWriter(stream)) writer.Write(value);
        }

        [Serializable] private sealed class PrefabInput { public string Asset, ParentAsset; public Vector3 Position; }
        [Serializable] private sealed class PrefabInputs { public PrefabInput[] Prefabs; }

        /// <summary>Captures copied resolved prefab geometry and native queries without running game scripts or changing the original project.</summary>
        public static void GeneratePrefabReference()
        {
            var inputPath = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_PREFAB_INPUT");
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            if (string.IsNullOrWhiteSpace(inputPath) || string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set prefab input and fixture output paths.");
            var input = JsonUtility.FromJson<PrefabInputs>(File.ReadAllText(inputPath));
            if (input?.Prefabs == null || input.Prefabs.Length == 0 || input.Prefabs.Length > 16) throw new InvalidOperationException("Expected one to sixteen staged prefabs.");
            Directory.CreateDirectory(output);
            const string assets = "Assets/GolemPrefabReference";
            if (!AssetDatabase.IsValidFolder(assets)) AssetDatabase.CreateFolder("Assets", "GolemPrefabReference");
            var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            var world = new GameObject("Prefab Reference").AddComponent<GolemPhysicsWorld>(); world.WorldID = "copied-prefab-reference";
            foreach (var item in input.Prefabs)
            {
                var instance = InstantiateStagedPrefab(item.Asset);
                if (!string.IsNullOrEmpty(item.ParentAsset)) { var parent = InstantiateStagedPrefab(item.ParentAsset); instance.transform.SetParent(parent.transform, false); instance = parent; }
                instance.transform.SetParent(world.transform); instance.transform.position = item.Position;
            }
            if (!EditorSceneManager.SaveScene(scene, assets + "/prefab-reference.unity")) throw new InvalidOperationException("Cannot save prefab reference scene.");
            var source = GolemPhysicsExporter.Capture(world);
            WriteNew(Path.Combine(output, "unity-physics.source.json"), GolemPhysicsDocument.Build(source));
            UnityEngine.Physics.SyncTransforms();
            var colliders = world.GetComponentsInChildren<Collider>(true);
            if (colliders.Length > 256) throw new InvalidOperationException("Prefab reference collider budget exceeded.");
            var queries = new List<Query>();
            for (var i = 0; i < colliders.Length; i++)
            {
                var collider = colliders[i]; if (!collider.enabled || !collider.gameObject.activeInHierarchy) continue;
                var bounds = collider.bounds;
                foreach (var axis in new[] { 0, 1, 2 })
                {
                    var direction = Vector3.zero; direction[axis] = -1;
                    var distance = 2 * (bounds.extents[axis] + 1);
                    var origin = bounds.center - direction * (bounds.extents[axis] + 1);
                    foreach (var type in new[] { "ray", "sphere" }) queries.Add(Observe("collider-" + i + "-axis-" + axis + "-" + type, type, origin, direction, type == "sphere" ? 0.25f : 0, distance));
                }
            }
            WriteNew(Path.Combine(output, "unity-physics.reference.json"), JsonUtility.ToJson(new ReferenceDocument { UnityVersion = Application.unityVersion, Queries = queries.ToArray() }, true));
        }

        private static GameObject InstantiateStagedPrefab(string asset)
        {
            if (asset == null || !asset.StartsWith("Assets/", StringComparison.Ordinal) || asset.Split('/').Any(p => p == "..")) throw new InvalidOperationException("Prefab must be copied under validation Assets.");
            var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(asset);
            if (!prefab) throw new InvalidOperationException("Missing staged prefab: " + asset);
            return (GameObject)PrefabUtility.InstantiatePrefab(prefab);
        }

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
