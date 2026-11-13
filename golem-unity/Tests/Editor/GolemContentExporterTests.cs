using System;
using System.IO;
using System.Linq;
using GolemEngine.Unity.ScribeFixtures;
using NUnit.Framework;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemContentExporterTests
    {
        private sealed class MonsterAdapter : IGolemContentAdapter
        {
            public void Capture(UnityEngine.Object source, GolemContentBuilder builder)
            {
                var monster = ((GameObject)source).GetComponent<ContentMonsterSource>();
                foreach (var type in GolemContentDocumentTests.SampleTypes()) builder.Type(type);
                builder.Definition("Monster", monster.Key, GolemContentDocument.Object(
                    "health", monster.Health,
                    "stats", GolemContentDocument.Object("speed", monster.Stats.Speed, "armor", monster.Stats.Armor),
                    "movement", builder.Reference(monster.Movement, "MovementProfile"),
                    "traits", new[] { "Grounded" }, "tags", Array.Empty<string>()), "monster");
            }
        }

        private sealed class ProfileAdapter : IGolemContentAdapter
        {
            public void Capture(UnityEngine.Object source, GolemContentBuilder builder)
            {
                var profile = (ContentMovementProfileSource)source;
                foreach (var type in GolemContentDocumentTests.SampleTypes()) builder.Type(type);
                builder.Definition("MovementProfile", profile.Key, GolemContentDocument.Object("clearance", profile.Clearance));
            }
        }

        [Test]
        public void SelectedThreeRootsExportResolvedPrefabVariantAndSharedReference()
        {
            var folderName = "_GolemContent_" + Guid.NewGuid().ToString("N");
            var folder = "Assets/" + folderName;
            var root = Path.Combine(Path.GetTempPath(), "golem-content-editor-" + Guid.NewGuid().ToString("N"));
            var settings = GolemUnityEditorSettings.instance;
            var autoExport = settings.AutoExportOnAssetChange;
            var autoBake = settings.AutoBakeOnExport;
            GameObject instance = null;
            try
            {
                settings.AutoExportOnAssetChange = false; settings.AutoBakeOnExport = false;
                Directory.CreateDirectory(Path.Combine(root, "entities"));
                File.WriteAllText(Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 3\n");
                var entityPath = Path.Combine(root, "entities/monster.yaml");
                const string entitySchema = "entity: Monster\nvars:\n  definition_id:\n    tag: 1\n    type: string\n    sync: once\n  health:\n    tag: 2\n    type: int32\n    sync: tick\n";
                File.WriteAllText(entityPath, entitySchema);
                AssetDatabase.CreateFolder("Assets", folderName);
                GolemContentAdapters.Register("content-test-monster", new MonsterAdapter());
                GolemContentAdapters.Register("content-test-profile", new ProfileAdapter());
                var profile = ScriptableObject.CreateInstance<ContentMovementProfileSource>();
                AssetDatabase.CreateAsset(profile, folder + "/Ground.asset");
                instance = new GameObject("Scout");
                instance.AddComponent<ContentMonsterSource>().Movement = profile;
                var scout = PrefabUtility.SaveAsPrefabAsset(instance, folder + "/Scout.prefab");
                UnityEngine.Object.DestroyImmediate(instance);
                instance = (GameObject)PrefabUtility.InstantiatePrefab(scout);
                var variant = instance.GetComponent<ContentMonsterSource>();
                variant.Key = "guard"; variant.Health = 160; variant.Stats.Speed = 3; variant.Stats.Armor = 5;
                PrefabUtility.RecordPrefabInstancePropertyModifications(variant);
                var guard = PrefabUtility.SaveAsPrefabAsset(instance, folder + "/Guard.prefab");
                Assert.That(PrefabUtility.GetPrefabAssetType(guard), Is.EqualTo(PrefabAssetType.Variant));
                UnityEngine.Object.DestroyImmediate(instance); instance = null;
                var set = ScriptableObject.CreateInstance<GolemContentExportSet>();
                set.Namespace = "demo"; set.Output = "content/demo";
                set.Roots.Add(new GolemContentRoot { Source = scout, Adapter = "content-test-monster" });
                set.Roots.Add(new GolemContentRoot { Source = guard, Adapter = "content-test-monster" });
                set.Roots.Add(new GolemContentRoot { Source = profile, Adapter = "content-test-profile" });
                set.EntitySchemas.Add(new GolemContentEntityBinding { Entity = "Monster", SchemaFile = "entities/monster.yaml" });
                AssetDatabase.CreateAsset(set, folder + "/Content.asset");
                AssetDatabase.SaveAssets();
                var exported = GolemContentExporter.Export(set, root);
                Assert.That(exported.Errors, Is.Empty, string.Join("; ", exported.Errors));
                Assert.That(exported.Prepared.BundleJson, Does.Contain("\"health\":80").And.Contain("\"health\":160").And.Contain("\"speed\":3"));
                Assert.That(exported.Prepared.BundleJson.Split(new[] { "\"movement\":\"demo/MovementProfile/ground\"" }, StringSplitOptions.None).Length, Is.EqualTo(3));
                Assert.That(File.ReadAllText(entityPath), Is.EqualTo(entitySchema));
                Assert.That(GolemScribeManifest.Load(root).All(r => r.Kind == GolemScribeConstants.ArtifactKindContentSource), Is.True);
                var before = Directory.GetFiles(root, "*", SearchOption.AllDirectories).ToDictionary(p => p, File.ReadAllBytes);
                var validation = GolemContentExporter.Validate(set, root);
                Assert.That(validation.Errors, Is.Empty, string.Join("; ", validation.Errors));
                foreach (var pair in before) Assert.That(File.ReadAllBytes(pair.Key), Is.EqualTo(pair.Value));
                Assert.That(GolemContentExporter.Export(set, root).Changed, Is.False);
                set.Roots.RemoveAt(2);
                var invalid = GolemContentExporter.Export(set, root);
                Assert.That(invalid.Errors, Has.Some.Contains("outside explicit roots"));
                foreach (var pair in before) Assert.That(File.ReadAllBytes(pair.Key), Is.EqualTo(pair.Value));
            }
            finally
            {
                if (instance != null) UnityEngine.Object.DestroyImmediate(instance);
                GolemContentAdapters.Unregister("content-test-monster"); GolemContentAdapters.Unregister("content-test-profile");
                GolemScribeScheduler.RunSuppressed(() => AssetDatabase.DeleteAsset(folder));
                settings.AutoExportOnAssetChange = autoExport; settings.AutoBakeOnExport = autoBake;
                GolemScribeScheduler.ResetForTests();
                if (Directory.Exists(root)) Directory.Delete(root, true);
            }
        }
    }
}
