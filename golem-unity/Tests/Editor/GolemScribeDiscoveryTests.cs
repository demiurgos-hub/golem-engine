using System;
using System.IO;
using System.Linq;
using GolemEngine.Unity.ScribeFixtures;
using NUnit.Framework;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeDiscoveryTests
    {
        [Test]
        public void RealAssetDiscoveryExportsAndValidationDoesNotMutate()
        {
            const string folder = "Assets/_GolemScribeDiscovery";
            var root = Path.Combine(Path.GetTempPath(), "scribe-discovery-" + Guid.NewGuid().ToString("N"));
            var settings = GolemUnityEditorSettings.instance;
            var priorRoot = settings.ProjectRoot;
            var priorAutoExport = settings.AutoExportOnAssetChange;
            var priorAutoBake = settings.AutoBakeOnExport;
            var priorFootprints = settings.FootprintsPath;
            GameObject source = null;
            GolemPrefabRegistry registry = null;
            try
            {
                settings.AutoExportOnAssetChange = false;
                settings.AutoBakeOnExport = false;
                Directory.CreateDirectory(root);
                File.WriteAllText(Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 2\n");
                settings.ProjectRoot = root;
                settings.FootprintsPath = "footprints.golem.yaml";
                AssetDatabase.CreateFolder("Assets", "_GolemScribeDiscovery");
                source = new GameObject("Discovery");
                source.AddComponent<ScribeDiscoveryPlayer>();
                var prefab = PrefabUtility.SaveAsPrefabAsset(source, folder + "/Player.prefab");
                var asset = ScriptableObject.CreateInstance<ScribeDiscoveryCatalog>();
                asset.Id = "one";
                asset.Health = 42;
                AssetDatabase.CreateAsset(asset, folder + "/Catalog.asset");
                AssetDatabase.SaveAssets();
                AssetDatabase.Refresh();
                var export = GolemScribeScheduler.ExportAllImmediate(runBake: false);
                Assert.That(export.Errors, Is.Empty, string.Join("; ", export.Errors));
                Assert.That(GolemPrefabRegistryUtil.TryGetUniqueRegistry(out registry, out var registryError), Is.True, registryError);
                Assert.That(registry.GetPrefab("ScribeDiscoveryPlayer"), Is.EqualTo(prefab));
                var records = GolemScribeManifest.Load(root);
                Assert.That(records.Count(r => r.Entity == "ScribeDiscoveryPlayer"), Is.EqualTo(1));
                Assert.That(records.Count(r => r.Entity == "ScribeDiscoveryCatalog"), Is.EqualTo(3));
                Assert.That(File.ReadAllText(Path.Combine(root, "catalogs/scribe_discovery_catalog.golem.yaml")), Does.Contain("health: 42"));
                var before = Directory.GetFiles(root, "*", SearchOption.AllDirectories)
                    .ToDictionary(p => p, p => (bytes: File.ReadAllBytes(p), time: File.GetLastWriteTimeUtc(p)));
                var registryPath = AssetDatabase.GetAssetPath(registry);
                var registryBytes = File.ReadAllBytes(registryPath);
                var settingsBefore = EditorJsonUtility.ToJson(settings);
                var validation = GolemScribeValidator.Validate(root);
                Assert.That(validation.IsClean, Is.True, string.Join("; ", validation.Errors.Concat(validation.Stale).Concat(validation.Missing)));
                Assert.That(Directory.GetFiles(root, "*", SearchOption.AllDirectories), Is.EquivalentTo(before.Keys));
                foreach (var pair in before)
                {
                    Assert.That(File.ReadAllBytes(pair.Key), Is.EqualTo(pair.Value.bytes));
                    Assert.That(File.GetLastWriteTimeUtc(pair.Key), Is.EqualTo(pair.Value.time));
                }
                Assert.That(File.ReadAllBytes(registryPath), Is.EqualTo(registryBytes));
                Assert.That(EditorJsonUtility.ToJson(settings), Is.EqualTo(settingsBefore));
                Assert.That(GolemScribeScheduler.ExportAllImmediate(runBake: false).AnyBytesChanged, Is.False);
                var package = UnityEditor.PackageManager.PackageInfo.FindForAssembly(typeof(GolemScribeArtifacts).Assembly);
                Assert.That(package, Is.Not.Null);
                TestContext.WriteLine("Scribe source package: " + package.resolvedPath);
            }
            finally
            {
                GolemScribeScheduler.RunSuppressed(() =>
                {
                    if (registry != null)
                    {
                        registry.Remove("ScribeDiscoveryPlayer");
                        EditorUtility.SetDirty(registry);
                    }
                    AssetDatabase.DeleteAsset(folder);
                    AssetDatabase.SaveAssets();
                });
                if (source != null) UnityEngine.Object.DestroyImmediate(source);
                settings.ProjectRoot = priorRoot;
                settings.AutoExportOnAssetChange = priorAutoExport;
                settings.AutoBakeOnExport = priorAutoBake;
                settings.FootprintsPath = priorFootprints;
                GolemScribeScheduler.ResetForTests();
                Directory.Delete(root, true);
            }
        }
    }
}
