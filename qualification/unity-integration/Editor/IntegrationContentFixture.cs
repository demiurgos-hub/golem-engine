using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using GolemEngine.Unity;
using GolemEngine.Unity.Editor;
using GolemEngine.Unity.ScribeFixtures;
using UnityEditor;
using UnityEngine;

namespace Golem.Integration.Fixture.Editor
{
    /// <summary>Reuses the bounded content fixture's two prefab archetypes and shared profile for an actual consumer export.</summary>
    public static class IntegrationContentFixture
    {
        private const string Folder = "Assets/Integration/Resources/Integration";
        private const string SetPath = Folder + "/Content.asset";
        private const string MonsterAdapterKey = "integration-monster";
        private const string ProfileAdapterKey = "integration-profile";

        [Serializable]
        private sealed class ExportEvidence
        {
            public string unity_version;
            public string content_set;
            public string schema_path;
            public string bundle_path;
            public string schema_sha256;
            public string bundle_sha256;
            public string[] selected_roots;
            public bool guard_is_variant;
            public bool repeated_export_changed;
            public string scout_mesh, guard_mesh;
            public bool visible_presentations;
        }

        private sealed class MonsterAdapter : IGolemContentAdapter
        {
            public void Capture(UnityEngine.Object source, GolemContentBuilder builder)
            {
                var monster = ((GameObject)source).GetComponent<ContentMonsterSource>();
                if (monster == null) throw new InvalidOperationException("Selected prefab has no content fixture source.");
                DeclareTypes(builder);
                builder.Definition("Monster", monster.Key, Values(
                    "health", monster.Health,
                    "stats", Values("speed", monster.Stats.Speed, "armor", monster.Stats.Armor),
                    "movement", builder.Reference(monster.Movement, "MovementProfile"),
                    "traits", new[] { "Grounded" }, "tags", Array.Empty<string>()), "monster");
            }
        }

        private sealed class ProfileAdapter : IGolemContentAdapter
        {
            public void Capture(UnityEngine.Object source, GolemContentBuilder builder)
            {
                var profile = (ContentMovementProfileSource)source;
                DeclareTypes(builder);
                builder.Definition("MovementProfile", profile.Key, Values("clearance", profile.Clearance));
            }
        }

        /// <summary>Creates or reuses only this fixture's saved roots, exports through Scribe, and retains new-run evidence.</summary>
        public static void Export()
        {
            var root = RequiredEnvironment("GOLEM_INTEGRATION_CONSUMER");
            var evidenceDirectory = RequiredEnvironment("GOLEM_INTEGRATION_EVIDENCE");
            if (!File.Exists(Path.Combine(root, "golem.yaml"))) throw new InvalidOperationException("Consumer golem.yaml is missing: " + root);
            if (!File.Exists(Path.Combine(root, "schemas/entities/monster.yaml"))) throw new InvalidOperationException("Consumer Monster entity schema is missing.");
            var settings = GolemUnityEditorSettings.instance;
            var previousAutoExport = settings.AutoExportOnAssetChange;
            var previousAutoBake = settings.AutoBakeOnExport;
            try
            {
                settings.AutoExportOnAssetChange = false;
                settings.AutoBakeOnExport = false;
                GolemContentAdapters.Register(MonsterAdapterKey, new MonsterAdapter());
                GolemContentAdapters.Register(ProfileAdapterKey, new ProfileAdapter());
                GolemScribeScheduler.RunSuppressed(() =>
                {
                    var set = AssetDatabase.LoadAssetAtPath<GolemContentExportSet>(SetPath) ?? CreateSet();
                    Require(set.Roots.Count == 3, "Fixture must retain exactly two archetypes and one shared profile.");
                    Require(set.Namespace == "demo" && set.Output == "content/demo", "Unexpected retained fixture export configuration.");
                    EnsureVisiblePresentations(set);
                    var exported = GolemContentExporter.Export(set, root);
                    Require(exported.Errors.Count == 0, string.Join("; ", exported.Errors));
                    var validation = GolemContentExporter.Validate(set, root);
                    Require(validation.Errors.Count == 0, string.Join("; ", validation.Errors));
                    var repeated = GolemContentExporter.Export(set, root);
                    Require(repeated.Errors.Count == 0 && !repeated.Changed, "Repeated export was not an unchanged successful export: " + string.Join("; ", repeated.Errors));
                    var guard = AssetDatabase.LoadAssetAtPath<GameObject>(Folder + "/Guard.prefab");
                    Require(PrefabUtility.GetPrefabAssetType(guard) == PrefabAssetType.Variant, "Guard must remain a resolved prefab variant.");
                    var schemaPath = Path.Combine(root, "content/demo.schema.json");
                    var bundlePath = Path.Combine(root, "content/demo.bundle.json");
                    var evidence = new ExportEvidence
                    {
                        unity_version = Application.unityVersion,
                        content_set = SetPath,
                        schema_path = schemaPath,
                        bundle_path = bundlePath,
                        schema_sha256 = Hash(schemaPath),
                        bundle_sha256 = Hash(bundlePath),
                        selected_roots = set.Roots.Select(x => AssetDatabase.GetAssetPath(x.Source)).ToArray(),
                        guard_is_variant = true,
                        repeated_export_changed = repeated.Changed,
                        scout_mesh = AssetDatabase.LoadAssetAtPath<GameObject>(Folder + "/Scout.prefab").GetComponent<MeshFilter>().sharedMesh.name,
                        guard_mesh = guard.GetComponent<MeshFilter>().sharedMesh.name,
                        visible_presentations = true
                    };
                    Directory.CreateDirectory(evidenceDirectory);
                    using (var stream = new FileStream(Path.Combine(evidenceDirectory, "unity-content-export.json"), FileMode.CreateNew))
                    using (var writer = new StreamWriter(stream, new UTF8Encoding(false))) writer.Write(JsonUtility.ToJson(evidence, true));
                    Debug.Log("Integration content export validated: two archetypes, one shared profile, unchanged repeat export.");
                });
            }
            finally
            {
                GolemContentAdapters.Unregister(MonsterAdapterKey);
                GolemContentAdapters.Unregister(ProfileAdapterKey);
                settings.AutoExportOnAssetChange = previousAutoExport;
                settings.AutoBakeOnExport = previousAutoBake;
            }
        }

        private static GolemContentExportSet CreateSet()
        {
            EnsureFolder(Folder);
            foreach (var file in new[] { "Ground.asset", "Scout.prefab", "Guard.prefab", "Content.asset" })
                Require(AssetDatabase.LoadMainAssetAtPath(Folder + "/" + file) == null, "Refusing to replace a partial retained fixture: " + file);
            var profile = ScriptableObject.CreateInstance<ContentMovementProfileSource>();
            AssetDatabase.CreateAsset(profile, Folder + "/Ground.asset");
            GameObject instance = null;
            try
            {
                instance = new GameObject("Scout");
                instance.AddComponent<ContentMonsterSource>().Movement = profile;
                var binding = new SerializedObject(instance.AddComponent<GolemTransformBinding>());
                binding.FindProperty("smooth").boolValue = false;
                binding.ApplyModifiedPropertiesWithoutUndo();
                instance.AddComponent<ObservedDefinitionView>().Presentation = "scout";
                var scout = PrefabUtility.SaveAsPrefabAsset(instance, Folder + "/Scout.prefab");
                UnityEngine.Object.DestroyImmediate(instance);
                instance = (GameObject)PrefabUtility.InstantiatePrefab(scout);
                instance.name = "Guard";
                var guardSource = instance.GetComponent<ContentMonsterSource>();
                guardSource.Key = "guard"; guardSource.Health = 160; guardSource.Stats.Speed = 3; guardSource.Stats.Armor = 5;
                PrefabUtility.RecordPrefabInstancePropertyModifications(guardSource);
                var view = instance.GetComponent<ObservedDefinitionView>();
                view.Presentation = "guard";
                PrefabUtility.RecordPrefabInstancePropertyModifications(view);
                var guard = PrefabUtility.SaveAsPrefabAsset(instance, Folder + "/Guard.prefab");
                var set = ScriptableObject.CreateInstance<GolemContentExportSet>();
                set.Namespace = "demo"; set.Output = "content/demo";
                set.Roots.Add(new GolemContentRoot { Source = scout, Adapter = MonsterAdapterKey });
                set.Roots.Add(new GolemContentRoot { Source = guard, Adapter = MonsterAdapterKey });
                set.Roots.Add(new GolemContentRoot { Source = profile, Adapter = ProfileAdapterKey });
                set.EntitySchemas.Add(new GolemContentEntityBinding { Entity = "Monster", SchemaFile = "schemas/entities/monster.yaml" });
                AssetDatabase.CreateAsset(set, SetPath);
                AssetDatabase.SaveAssets();
                return set;
            }
            finally { if (instance != null) UnityEngine.Object.DestroyImmediate(instance); }
        }

        private static void DeclareTypes(GolemContentBuilder builder)
        {
            var stats = new GolemContentType { Name = "Stats" };
            stats.Fields.Add(new GolemContentField("speed", "float32"));
            stats.Fields.Add(new GolemContentField("armor", "int32").WithDefault(0));
            var profile = new GolemContentType { Name = "MovementProfile", Definition = true };
            profile.Fields.Add(new GolemContentField("clearance", "float32"));
            var flags = new GolemContentType { Name = "Traits", Kind = "flags" };
            flags.Values.AddRange(new[] { "Hostile", "Grounded" });
            var monster = new GolemContentType { Name = "Monster", Definition = true, Entity = "Monster" };
            monster.Fields.AddRange(new[]
            {
                new GolemContentField("health", "int32"), new GolemContentField("stats", "Stats"),
                new GolemContentField("movement", "ref:MovementProfile"), new GolemContentField("traits", "Traits"),
                new GolemContentField("tags", "[]string"), new GolemContentField("note", "string", true)
            });
            foreach (var type in new[] { monster, stats, profile, flags }) builder.Type(type);
        }

        private static void EnsureVisiblePresentations(GolemContentExportSet set)
        {
            foreach (var key in new[] { "Scout", "Guard" })
            {
                var path = Folder + "/" + key + ".prefab";
                var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(path);
                Require(prefab != null && set.Roots.Count(x => x.Source == prefab && x.Adapter == MonsterAdapterKey) == 1,
                    "Selected fixture set does not contain its expected prefab: " + path);
                Require(prefab.GetComponent<ContentMonsterSource>()?.Key == key.ToLowerInvariant() &&
                    prefab.GetComponent<ObservedDefinitionView>()?.Presentation == key.ToLowerInvariant() &&
                    prefab.GetComponent<GolemTransformBinding>() != null, "Refusing to alter an unrecognized fixture prefab: " + path);
            }
            EnsurePresentation("Scout", PrimitiveType.Capsule, null);
            var scoutMesh = AssetDatabase.LoadAssetAtPath<GameObject>(Folder + "/Scout.prefab").GetComponent<MeshFilter>().sharedMesh;
            EnsurePresentation("Guard", PrimitiveType.Cube, scoutMesh);
            var guard = AssetDatabase.LoadAssetAtPath<GameObject>(Folder + "/Guard.prefab");
            Require(guard.GetComponent<MeshFilter>().sharedMesh != scoutMesh, "The two definition presentations must have distinct visible meshes.");
        }

        private static void EnsurePresentation(string key, PrimitiveType primitiveType, Mesh inheritedMesh)
        {
            var path = Folder + "/" + key + ".prefab";
            var originalGuid = AssetDatabase.AssetPathToGUID(path);
            var primitive = GameObject.CreatePrimitive(primitiveType);
            var mesh = primitive.GetComponent<MeshFilter>().sharedMesh;
            var material = primitive.GetComponent<MeshRenderer>().sharedMaterial;
            UnityEngine.Object.DestroyImmediate(primitive);
            var contents = PrefabUtility.LoadPrefabContents(path);
            try
            {
                var filter = contents.GetComponent<MeshFilter>();
                var renderer = contents.GetComponent<MeshRenderer>();
                Require((filter == null) == (renderer == null), "Refusing to replace a partially authored fixture presentation: " + path);
                var changed = false;
                if (filter == null)
                {
                    filter = contents.AddComponent<MeshFilter>();
                    renderer = contents.AddComponent<MeshRenderer>();
                    filter.sharedMesh = mesh;
                    renderer.sharedMaterial = material;
                    changed = true;
                }
                else if (filter.sharedMesh != mesh)
                {
                    var meshProperty = new SerializedObject(filter).FindProperty("m_Mesh");
                    Require(inheritedMesh != null && filter.sharedMesh == inheritedMesh && !meshProperty.prefabOverride &&
                        PrefabUtility.GetCorrespondingObjectFromSource(filter) != null,
                        "Refusing to replace an authored mesh override in fixture presentation: " + path);
                    filter.sharedMesh = mesh;
                    PrefabUtility.RecordPrefabInstancePropertyModifications(filter);
                    changed = true;
                }
                Require(renderer.enabled && renderer.sharedMaterial != null, "Fixture presentation must have an enabled renderer and material: " + path);
                Require(contents.GetComponent<Collider>() == null, "The presentation-only fixture must not add Unity colliders.");
                if (changed)
                {
                    Require(PrefabUtility.SaveAsPrefabAsset(contents, path) != null, "Could not save fixture presentation: " + path);
                    AssetDatabase.SaveAssets();
                }
            }
            finally { PrefabUtility.UnloadPrefabContents(contents); }
            Require(AssetDatabase.AssetPathToGUID(path) == originalGuid, "Updating fixture presentation changed its saved asset identity.");
        }

        private static Dictionary<string, object> Values(params object[] pairs)
        {
            var values = new Dictionary<string, object>(StringComparer.Ordinal);
            for (var i = 0; i < pairs.Length; i += 2) values.Add((string)pairs[i], pairs[i + 1]);
            return values;
        }

        private static void EnsureFolder(string path)
        {
            if (AssetDatabase.IsValidFolder(path)) return;
            var parent = path.Substring(0, path.LastIndexOf('/'));
            EnsureFolder(parent);
            AssetDatabase.CreateFolder(parent, path.Substring(parent.Length + 1));
        }

        private static string RequiredEnvironment(string name)
        {
            var value = Environment.GetEnvironmentVariable(name);
            if (string.IsNullOrWhiteSpace(value)) throw new InvalidOperationException(name + " is required.");
            return Path.GetFullPath(value);
        }

        private static string Hash(string path)
        {
            using (var hash = SHA256.Create()) return string.Concat(hash.ComputeHash(File.ReadAllBytes(path)).Select(b => b.ToString("x2")));
        }

        private static void Require(bool condition, string message)
        { if (!condition) throw new InvalidOperationException(message); }
    }
}
