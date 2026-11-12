using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    /// <summary>Resolves copied server prefabs and explicit host state recipes without importing or executing game scripts.</summary>
    public static class GolemPhysicsCatalogFixture
    {
        [Serializable] private sealed class Inputs { public Item[] Prefabs; }
        [Serializable] private sealed class Item
        {
            public string Key, Asset, SourceGUID;
            public bool ApplyPose;
            public Vector3 Position, Euler, Scale = Vector3.one;
            public State[] States = Array.Empty<State>();
            public Identity[] Absent = Array.Empty<Identity>();
        }
        [Serializable] private sealed class Identity { public string GUID, FileID; }
        [Serializable] private sealed class State { public string Name; public Toggle[] Toggles = Array.Empty<Toggle>(); public Layer[] Layers = Array.Empty<Layer>(); }
        [Serializable] private sealed class Toggle { public Identity Object; public bool Active; }
        [Serializable] private sealed class Layer { public Identity Object; public int Value; }
        [Serializable] private sealed class ObjectRecord
        {
            public string ID, Path;
            public Identity[] Sources;
            public bool ActiveSelf, ActiveHierarchy;
            public int Layer;
        }
        [Serializable] private sealed class ColliderRecord
        {
            public string ID, ObjectID, Type, BodyID, MeshAsset;
            public Identity[] Sources;
            public bool Enabled, Active, Trigger, MissingMesh;
            public int Layer;
        }
        [Serializable] private sealed class Capture
        {
            public string State, Error;
            public int Bodies, Colliders, ActiveColliders, Queries, ActivationAssertions, LayerAssertions;
            public bool StableColliderIdentities, StableEnabledFlags;
            public ObjectRecord[] Objects;
            public ColliderRecord[] Leaves;
        }
        [Serializable] private sealed class Result { public string Key, Asset, SourceGUID, Error; public int AbsentAssertions; public Capture[] Captures; }
        [Serializable] private sealed class Report { public string Version = "unity-resolved-catalog-v1", UnityVersion; public bool Qualified; public Result[] Results; }
        [Serializable] private sealed class Reference { public string Version = "unity-physics-reference-v1", UnityVersion; public Query[] Queries; }
        [Serializable] private sealed class Query { public string Name, Type; public Vector3 From, Direction, WorldOffset; public float Radius, Distance; public Hit[] Hits; }
        [Serializable] private sealed class Hit { public string ColliderID; public float Distance; public Vector3 Point, Normal; }
        [Serializable] private sealed class ReadabilityChange { public string Asset, BeforeMetadataSHA256, AfterMetadataSHA256; public bool Before, After; }
        [Serializable] private sealed class ReadabilityReport { public string Scope = "Explicit isReadable-only qualification override on copied ModelImporter assets; original authoring export failures remain preserved."; public ReadabilityChange[] Changes; }

        /// <summary>Enables CPU mesh access only on copied catalog model assets and records every metadata change.</summary>
        public static void PrepareReadableCopies()
        {
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            RequireValidationWorkspace(output);
            var changes = new List<ReadabilityChange>();
            foreach (var guid in AssetDatabase.FindAssets("t:Model", new[] { "Assets/ResolvedCatalog" }))
            {
                var path = AssetDatabase.GUIDToAssetPath(guid);
                if (!path.StartsWith("Assets/ResolvedCatalog/", StringComparison.Ordinal)) throw new InvalidOperationException("Readability override escaped copied input root.");
                var importer = AssetImporter.GetAtPath(path) as ModelImporter;
                if (!importer || importer.isReadable) continue;
                var record = new ReadabilityChange { Asset = path, Before = importer.isReadable, BeforeMetadataSHA256 = Hash(File.ReadAllBytes(path + ".meta")) };
                importer.isReadable = true; importer.SaveAndReimport();
                record.After = importer.isReadable; record.AfterMetadataSHA256 = Hash(File.ReadAllBytes(path + ".meta"));
                if (!record.After) throw new InvalidOperationException("Readability override failed: " + path);
                changes.Add(record);
            }
            Directory.CreateDirectory(output);
            WriteNew(Path.Combine(output, "readability-overrides.json"), JsonUtility.ToJson(new ReadabilityReport { Changes = changes.ToArray() }, true));
        }

        /// <summary>Records resolved collider state even when missing geometry prevents portable source export.</summary>
        public static void AuditResolved()
        {
            var input = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_CATALOG_INPUT");
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            RequireValidationWorkspace(output);
            var items = JsonUtility.FromJson<Inputs>(File.ReadAllText(input));
            if (items?.Prefabs == null || items.Prefabs.Length == 0 || items.Prefabs.Length > 16) throw new InvalidOperationException("Audit requires one to sixteen roots.");
            const string scenes = "Assets/GolemCatalogAudits";
            if (!AssetDatabase.IsValidFolder(scenes)) AssetDatabase.CreateFolder("Assets", "GolemCatalogAudits");
            var results = new List<Result>();
            foreach (var item in items.Prefabs)
            {
                if (item.Asset == null || !item.Asset.StartsWith("Assets/ResolvedCatalog/", StringComparison.Ordinal) || item.Asset.Split('/').Any(x => x == "..") || string.IsNullOrEmpty(item.Key) || item.Key.Any(c => !char.IsLetterOrDigit(c) && c != '-' && c != '_')) throw new InvalidOperationException("Audit input must be copied catalog geometry.");
                var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
                var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(item.Asset);
                if (!prefab || AssetDatabase.AssetPathToGUID(item.Asset) != item.SourceGUID) throw new InvalidOperationException("Copied source GUID mismatch.");
                var root = (GameObject)PrefabUtility.InstantiatePrefab(prefab);
                if (!EditorSceneManager.SaveScene(scene, scenes + "/" + item.Key + ".unity")) throw new InvalidOperationException("Audit scene save failed.");
                var colliders = root.GetComponentsInChildren<Collider>(true);
                var capture = new Capture { State = "resolved-inventory", Colliders = colliders.Length,
                    Objects = root.GetComponentsInChildren<Transform>(true).Select(x => new ObjectRecord { ID = ID(x.gameObject), Path = Hierarchy(x, root.transform), Sources = Sources(x.gameObject), ActiveSelf = x.gameObject.activeSelf, ActiveHierarchy = x.gameObject.activeInHierarchy, Layer = x.gameObject.layer }).ToArray(),
                    Leaves = colliders.Select(x => new ColliderRecord { ID = ID(x), ObjectID = ID(x.gameObject), Sources = Sources(x), Type = x.GetType().Name, Enabled = x.enabled, Active = x.gameObject.activeInHierarchy, Trigger = x.isTrigger, Layer = x.gameObject.layer, MissingMesh = x is MeshCollider mesh && !mesh.sharedMesh, MeshAsset = x is MeshCollider collider && collider.sharedMesh ? AssetDatabase.GetAssetPath(collider.sharedMesh) : "" }).ToArray() };
                results.Add(new Result { Key = item.Key, Asset = item.Asset, SourceGUID = item.SourceGUID, Captures = new[] { capture } });
            }
            Directory.CreateDirectory(output);
            WriteNew(Path.Combine(output, "resolved-inventory.json"), JsonUtility.ToJson(new Report { UnityVersion = Application.unityVersion, Results = results.ToArray() }, true));
        }

        /// <summary>Captures at most sixteen staged roots, retaining independent failures and full state/identity evidence.</summary>
        public static void Generate()
        {
            var inputPath = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_CATALOG_INPUT");
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            RequireValidationWorkspace(output);
            if (string.IsNullOrWhiteSpace(inputPath) || string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set catalog input and fixture output paths.");
            var inputs = JsonUtility.FromJson<Inputs>(File.ReadAllText(inputPath));
            if (inputs?.Prefabs == null || inputs.Prefabs.Length == 0 || inputs.Prefabs.Length > 16) throw new InvalidOperationException("Catalog batches require one to sixteen roots.");
            Directory.CreateDirectory(output);
            const string scenes = "Assets/GolemCatalogCaptures";
            if (!AssetDatabase.IsValidFolder(scenes)) AssetDatabase.CreateFolder("Assets", "GolemCatalogCaptures");
            var results = new List<Result>();
            foreach (var item in inputs.Prefabs)
            {
                var result = new Result { Key = item.Key, Asset = item.Asset, SourceGUID = item.SourceGUID }; results.Add(result);
                var captures = new List<Capture>();
                try
                {
                    if (string.IsNullOrEmpty(item.Key) || item.Key.Any(c => !char.IsLetterOrDigit(c) && c != '-' && c != '_')) throw new InvalidOperationException("Invalid capture key.");
                    if (item.Asset == null || !item.Asset.StartsWith("Assets/", StringComparison.Ordinal) || item.Asset.Split('/').Any(x => x == "..")) throw new InvalidOperationException("Inputs must be copied under Assets.");
                    if (AssetDatabase.AssetPathToGUID(item.Asset) != item.SourceGUID) throw new InvalidOperationException("Copied source GUID differs from manifest.");
                    var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(item.Asset);
                    if (!prefab) throw new InvalidOperationException("Missing staged root.");
                    var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
                    var world = new GameObject("Catalog " + item.Key).AddComponent<GolemPhysicsWorld>(); world.WorldID = "catalog-" + item.Key;
                    var instance = (GameObject)PrefabUtility.InstantiatePrefab(prefab); instance.transform.SetParent(world.transform, false);
                    if (item.ApplyPose) { instance.transform.SetPositionAndRotation(item.Position, Quaternion.Euler(item.Euler)); instance.transform.localScale = item.Scale; }
                    var objects = instance.GetComponentsInChildren<Transform>(true).Select(x => x.gameObject).ToArray();
                    var colliders = instance.GetComponentsInChildren<Collider>(true);
                    if (objects.Length > 16384 || colliders.Length > 2048) throw new InvalidOperationException("Resolved root exceeds explicit capture budget.");
                    var active = objects.ToDictionary(x => x, x => x.activeSelf);
                    var layers = objects.ToDictionary(x => x, x => x.layer);
                    var enabled = colliders.ToDictionary(x => x, x => x.enabled);
                    foreach (var absent in item.Absent ?? Array.Empty<Identity>())
                    {
                        if (objects.Any(x => HasSource(x, absent)) || colliders.Any(x => HasSource(x, absent))) throw new InvalidOperationException("Removed object/component unexpectedly resolved: " + absent.GUID + ":" + absent.FileID);
                        result.AbsentAssertions++;
                    }
                    var states = new[] { new State { Name = "authored" } }.Concat(item.States ?? Array.Empty<State>()).ToArray();
                    if (states.Length > 64 || states.Select(x => x.Name).Distinct().Count() != states.Length) throw new InvalidOperationException("Invalid state recipe count/identity.");
                    string[] initialIDs = null;
                    foreach (var state in states)
                    {
                        var capture = new Capture { State = state.Name }; captures.Add(capture);
                        try
                        {
                            if (string.IsNullOrEmpty(state.Name) || state.Name.Any(c => !char.IsLetterOrDigit(c) && c != '-' && c != '_')) throw new InvalidOperationException("Invalid state name.");
                            foreach (var obj in objects) { obj.SetActive(active[obj]); obj.layer = layers[obj]; }
                            var expectedActive = new Dictionary<GameObject, bool>(active);
                            var expectedLayers = new Dictionary<GameObject, int>(layers);
                            foreach (var toggle in state.Toggles ?? Array.Empty<Toggle>())
                            {
                                var obj = ResolveObject(objects, toggle.Object); obj.SetActive(toggle.Active); expectedActive[obj] = toggle.Active;
                            }
                            foreach (var layer in state.Layers ?? Array.Empty<Layer>())
                            {
                                if (layer.Value < 0 || layer.Value > 31) throw new InvalidOperationException("Invalid recipe layer.");
                                var obj = ResolveObject(objects, layer.Object); obj.layer = layer.Value; expectedLayers[obj] = layer.Value;
                            }
                            foreach (var obj in objects)
                            {
                                if (obj.activeSelf != expectedActive[obj] || obj.layer != expectedLayers[obj]) throw new InvalidOperationException("State changed an undeclared object.");
                                capture.ActivationAssertions++; capture.LayerAssertions++;
                            }
                            EditorSceneManager.MarkSceneDirty(scene);
                            if (!EditorSceneManager.SaveScene(scene, scenes + "/" + item.Key + ".unity")) throw new InvalidOperationException("Cannot save capture scene.");
                            var source = GolemPhysicsExporter.Capture(world);
                            var ids = source.Colliders.Select(x => x.ID).OrderBy(x => x, StringComparer.Ordinal).ToArray();
                            if (initialIDs == null) initialIDs = ids;
                            capture.StableColliderIdentities = ids.SequenceEqual(initialIDs);
                            capture.StableEnabledFlags = colliders.All(x => x.enabled == enabled[x]);
                            if (!capture.StableColliderIdentities || !capture.StableEnabledFlags || source.Colliders.Length != colliders.Length) throw new InvalidOperationException("State recipe changed collider identities/enabled flags.");
                            foreach (var leaf in source.Colliders)
                            {
                                var collider = colliders.Single(x => ID(x) == leaf.ID);
                                if (leaf.Active != collider.gameObject.activeInHierarchy || leaf.Enabled != collider.enabled) throw new InvalidOperationException("Exporter lost resolved activation/enabled state.");
                            }
                            var folder = Path.Combine(output, item.Key, state.Name); Directory.CreateDirectory(folder);
                            WriteNew(Path.Combine(folder, "unity-physics.source.json"), GolemPhysicsDocument.Build(source));
                            UnityEngine.Physics.SyncTransforms();
                            var queries = Observe(colliders);
                            WriteNew(Path.Combine(folder, "unity-physics.reference.json"), JsonUtility.ToJson(new Reference { UnityVersion = Application.unityVersion, Queries = queries }, true));
                            capture.Bodies = source.Bodies.Length; capture.Colliders = colliders.Length; capture.ActiveColliders = colliders.Count(x => x.enabled && x.gameObject.activeInHierarchy); capture.Queries = queries.Length;
                            capture.Objects = objects.Select(x => new ObjectRecord { ID = ID(x), Path = Hierarchy(x.transform, instance.transform), Sources = Sources(x), ActiveSelf = x.activeSelf, ActiveHierarchy = x.activeInHierarchy, Layer = x.layer }).ToArray();
                            capture.Leaves = colliders.Select(x => new ColliderRecord { ID = ID(x), ObjectID = ID(x.gameObject), Sources = Sources(x), Type = x.GetType().Name, Enabled = x.enabled, Active = x.gameObject.activeInHierarchy, Trigger = x.isTrigger, BodyID = source.Colliders.Single(y => y.ID == ID(x)).BodyID }).ToArray();
                            WriteNew(Path.Combine(folder, "resolved.json"), JsonUtility.ToJson(capture, true));
                        }
                        catch (Exception error) { capture.Error = error.ToString(); }
                    }
                }
                catch (Exception error) { result.Error = error.ToString(); }
                result.Captures = captures.ToArray();
            }
            WriteNew(Path.Combine(output, "catalog-result.json"), JsonUtility.ToJson(new Report { UnityVersion = Application.unityVersion, Results = results.ToArray() }, true));
            if (results.Any(x => x.Error != null || x.Captures.Any(y => y.Error != null))) throw new InvalidOperationException("Catalog capture has explicit unresolved failures; see catalog-result.json.");
        }

        private static string ID(UnityEngine.Object obj) => "unity-object:" + GlobalObjectId.GetGlobalObjectIdSlow(obj);
        private static void RequireValidationWorkspace(string output)
        {
            const string allowed = "D:/Demiurgos/tests/physics-validation/";
            if (string.IsNullOrWhiteSpace(output) || !Path.GetFullPath(output).Replace('\\', '/').StartsWith(allowed, StringComparison.OrdinalIgnoreCase) || !Path.GetFullPath(Application.dataPath).Replace('\\', '/').StartsWith(allowed, StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("Catalog helpers require a copied project and output under the D physics validation workspace.");
        }
        private static string Hash(byte[] bytes) { using (var algorithm = SHA256.Create()) return BitConverter.ToString(algorithm.ComputeHash(bytes)).Replace("-", "").ToLowerInvariant(); }
        private static string Hierarchy(Transform obj, Transform root) => obj == root ? "0:" + obj.name : Hierarchy(obj.parent, root) + "/" + obj.GetSiblingIndex() + ":" + obj.name;
        private static Identity[] Sources(UnityEngine.Object obj)
        {
            var result = new List<Identity>(); var seen = new HashSet<int>();
            while (obj && seen.Add(obj.GetInstanceID()))
            {
                if (EditorUtility.IsPersistent(obj) && AssetDatabase.TryGetGUIDAndLocalFileIdentifier(obj, out string guid, out long fileID)) result.Add(new Identity { GUID = guid, FileID = fileID.ToString(System.Globalization.CultureInfo.InvariantCulture) });
                obj = PrefabUtility.GetCorrespondingObjectFromSource(obj);
            }
            return result.ToArray();
        }
        private static bool HasSource(UnityEngine.Object obj, Identity id) => Sources(obj).Any(x => x.GUID == id.GUID && x.FileID == id.FileID);
        private static GameObject ResolveObject(GameObject[] objects, Identity id)
        {
            var matches = objects.Where(x => HasSource(x, id) || x.GetComponents<Component>().Any(component => component && HasSource(component, id))).ToArray();
            if (matches.Length != 1) throw new InvalidOperationException("Recipe object must resolve exactly once: " + id.GUID + ":" + id.FileID + "; matches=" + matches.Length);
            return matches[0];
        }
        private static Query[] Observe(Collider[] colliders)
        {
            var queries = new List<Query>();
            for (var i = 0; i < colliders.Length; i++)
            {
                var collider = colliders[i]; if (!collider.enabled || !collider.gameObject.activeInHierarchy) continue;
                var bounds = collider.bounds;
                foreach (var axis in new[] { 0, 1, 2 }) foreach (var type in new[] { "ray", "sphere" })
                {
                    var direction = Vector3.zero; direction[axis] = -1;
                    var from = bounds.center - direction * (bounds.extents[axis] + 1); var distance = 2 * (bounds.extents[axis] + 1); var radius = type == "sphere" ? 0.25f : 0;
                    var hits = type == "ray" ? UnityEngine.Physics.RaycastAll(from, direction, distance, ~0, QueryTriggerInteraction.Collide) : UnityEngine.Physics.SphereCastAll(from, radius, direction, distance, ~0, QueryTriggerInteraction.Collide);
                    queries.Add(new Query { Name = "collider-" + i + "-axis-" + axis + "-" + type, Type = type, From = from, Direction = direction, Radius = radius, Distance = distance,
                        Hits = hits.OrderBy(x => x.distance).ThenBy(x => ID(x.collider), StringComparer.Ordinal).Select(x => new Hit { ColliderID = ID(x.collider), Distance = x.distance, Point = x.point, Normal = x.normal }).ToArray() });
                }
            }
            return queries.ToArray();
        }
        private static void WriteNew(string path, string value)
        {
            using (var stream = new FileStream(path, FileMode.CreateNew)) using (var writer = new StreamWriter(stream)) writer.Write(value);
        }
    }
}
