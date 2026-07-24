using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using GolemEngine.Unity;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    /// <summary>
    /// End-to-end Scribe fixture: player + monster schemas/registry, monster catalog GUID ref,
    /// non-entity wall footprint — no scene artifacts and no wall entity/replication.
    /// </summary>
    public sealed class GolemScribeEndToEndTests
    {
        private const string AssetFolder = "Assets/_GolemScribePhase5E2E";
        private readonly List<string> _createdPaths = new List<string>();
        private string _previousProjectRoot;
        private string _previousFootprintsPath;
        private bool _previousAutoExport;
        private bool _previousAutoBake;
        private string _tempProjectRoot;
        private GolemPrefabRegistry _hostRegistry;
        private readonly List<string> _registryKeysToRemove = new List<string>();
        private readonly List<UnityEngine.Object> _owned = new List<UnityEngine.Object>();

        [GolemEntity("ScribeE2EPlayer", Global = true)]
        private sealed class ScribeE2EPlayerEntity : MonoBehaviour
        {
            [GolemVar(1, GolemSync.Tick)]
            public int health;
        }

        [GolemEntity("ScribeE2EMonster")]
        private sealed class ScribeE2EMonsterEntity : MonoBehaviour
        {
            [GolemVar(1, GolemSync.Tick)]
            public int health;

            [GolemVar(2, GolemSync.Once)]
            public string definitionId;
        }

        [GolemCatalog("Id")]
        private sealed class ScribeE2EMonsterDefinition : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemField(2)]
            public int Health;

            [GolemAssetRef(3)]
            public GameObject Prefab;
        }

        [SetUp]
        public void SetUp()
        {
            var settings = GolemUnityEditorSettings.instance;
            _previousProjectRoot = settings.ProjectRoot;
            _previousFootprintsPath = settings.FootprintsPath;
            _previousAutoExport = settings.AutoExportOnAssetChange;
            _previousAutoBake = settings.AutoBakeOnExport;

            settings.AutoExportOnAssetChange = false;
            settings.AutoBakeOnExport = false;
            GolemUnityEditorSettings.Save();
            GolemScribeScheduler.ResetForTests();
        }

        [TearDown]
        public void TearDown()
        {
            GolemScribeScheduler.RunSuppressed(() =>
            {
                if (_hostRegistry != null && _registryKeysToRemove.Count > 0)
                {
                    foreach (var key in _registryKeysToRemove)
                    {
                        _hostRegistry.Remove(key);
                    }

                    EditorUtility.SetDirty(_hostRegistry);
                    AssetDatabase.SaveAssets();
                }

                foreach (var path in _createdPaths.AsEnumerable().Reverse())
                {
                    if (!string.IsNullOrEmpty(path))
                    {
                        AssetDatabase.DeleteAsset(path);
                    }
                }

                if (AssetDatabase.IsValidFolder(AssetFolder))
                {
                    AssetDatabase.DeleteAsset(AssetFolder);
                }

                AssetDatabase.SaveAssets();
                AssetDatabase.Refresh();
            });

            foreach (var obj in _owned)
            {
                if (obj != null)
                {
                    UnityEngine.Object.DestroyImmediate(obj);
                }
            }

            _owned.Clear();
            _createdPaths.Clear();
            _registryKeysToRemove.Clear();
            _hostRegistry = null;

            var settings = GolemUnityEditorSettings.instance;
            settings.ProjectRoot = _previousProjectRoot;
            settings.FootprintsPath = _previousFootprintsPath;
            settings.AutoExportOnAssetChange = _previousAutoExport;
            settings.AutoBakeOnExport = _previousAutoBake;
            GolemUnityEditorSettings.Save();
            GolemScribeScheduler.ResetForTests();

            if (!string.IsNullOrEmpty(_tempProjectRoot) && Directory.Exists(_tempProjectRoot))
            {
                Directory.Delete(_tempProjectRoot, recursive: true);
            }

            _tempProjectRoot = null;
        }

        [Test]
        public void EndToEnd_PlayerMonsterWall_RegistryParity_NoWallEntityOrSceneArtifacts()
        {
            // Test-assembly MonoBehaviours do not survive PrefabUtility reload, so entity schemas are
            // built from attributed types directly. Wall uses Runtime GolemFootprint and real export.
            _tempProjectRoot = Path.Combine(Path.GetTempPath(), "golem-scribe-e2e-" + Path.GetRandomFileName());
            Directory.CreateDirectory(_tempProjectRoot);
            File.WriteAllText(
                Path.Combine(_tempProjectRoot, "golem.yaml"),
                string.Join(
                    "\n",
                    "entity_schema: schemas/entities/",
                    "types_schema: schemas/types/",
                    "world_schema: schemas/world/",
                    "simulation:",
                    "  dimensions: 2",
                    ""));

            EnsureFolder(AssetFolder);
            EnsureFolder(AssetFolder + "/Prefabs");

            var playerGo = new GameObject("ScribeE2EPlayer");
            var monsterGo = new GameObject("ScribeE2EMonster");
            _owned.Add(playerGo);
            _owned.Add(monsterGo);

            var playerModel = GolemEntitySchemaBuilder.Build(typeof(ScribeE2EPlayerEntity), 2, "schemas/entities/");
            var monsterModel = GolemEntitySchemaBuilder.Build(typeof(ScribeE2EMonsterEntity), 2, "schemas/entities/");
            Assert.That(playerModel.Errors, Is.Empty, string.Join("; ", playerModel.Errors));
            Assert.That(monsterModel.Errors, Is.Empty, string.Join("; ", monsterModel.Errors));

            var playerYaml = GolemEntitySchemaBuilder.BuildYaml(playerModel);
            var monsterYaml = GolemEntitySchemaBuilder.BuildYaml(monsterModel);
            var entityDesired = new List<GolemScribeArtifactRecord>
            {
                Artifact(GolemScribeConstants.ArtifactKindEntitySchema, playerModel.RelativePath, playerYaml, "ScribeE2EPlayer"),
                Artifact(GolemScribeConstants.ArtifactKindEntitySchema, monsterModel.RelativePath, monsterYaml, "ScribeE2EMonster")
            };

            var entityReconcile = GolemScribeArtifacts.ReconcileKind(
                _tempProjectRoot,
                GolemScribeConstants.ArtifactKindEntitySchema,
                Array.Empty<GolemScribeArtifactRecord>(),
                entityDesired);
            Assert.That(entityReconcile.Errors, Is.Empty, string.Join("; ", entityReconcile.Errors));

            var wallPrefab = CreateWallFootprintPrefab(AssetFolder + "/Prefabs/ScribeE2EWall.prefab");
            AssetDatabase.SaveAssets();
            AssetDatabase.Refresh();

            Assert.That(
                GolemPrefabRegistryUtil.TryGetUniqueRegistry(out _hostRegistry, out var registryError),
                Is.True,
                registryError);
            _hostRegistry.Upsert("ScribeE2EPlayer", playerGo);
            _hostRegistry.Upsert("ScribeE2EMonster", monsterGo);
            EditorUtility.SetDirty(_hostRegistry);
            AssetDatabase.SaveAssets();
            _registryKeysToRemove.Add("ScribeE2EPlayer");
            _registryKeysToRemove.Add("ScribeE2EMonster");

            var settings = GolemUnityEditorSettings.instance;
            settings.ProjectRoot = _tempProjectRoot;
            settings.FootprintsPath = "footprints.golem.yaml";
            GolemUnityEditorSettings.Save();

            var footprintExport = GolemFootprintExporter.ExportAll();
            var fixtureFootprintErrors = footprintExport.Errors
                .Where(e => e.IndexOf("ScribeE2E", StringComparison.OrdinalIgnoreCase) >= 0 ||
                            e.IndexOf("ScribeE2EWall", StringComparison.OrdinalIgnoreCase) >= 0 ||
                            e.IndexOf(AssetFolder, StringComparison.OrdinalIgnoreCase) >= 0)
                .ToList();
            Assert.That(fixtureFootprintErrors, Is.Empty, string.Join("\n", fixtureFootprintErrors));

            Assert.That(File.Exists(Path.Combine(_tempProjectRoot, playerModel.RelativePath)), Is.True);
            Assert.That(File.Exists(Path.Combine(_tempProjectRoot, monsterModel.RelativePath)), Is.True);
            Assert.That(File.ReadAllText(Path.Combine(_tempProjectRoot, playerModel.RelativePath)), Does.Contain("entity: ScribeE2EPlayer"));
            Assert.That(File.ReadAllText(Path.Combine(_tempProjectRoot, monsterModel.RelativePath)), Does.Contain("entity: ScribeE2EMonster"));

            var entityDir = Path.Combine(_tempProjectRoot, "schemas", "entities");
            foreach (var file in Directory.GetFiles(entityDir, "*.yaml"))
            {
                var text = File.ReadAllText(file);
                Assert.That(text, Does.Not.Contain("entity: Wall"));
                Assert.That(text, Does.Not.Contain("entity: ScribeE2EWall"));
                Assert.That(Path.GetFileName(file).ToLowerInvariant(), Does.Not.Contain("wall"));
            }

            var footprintsPath = Path.Combine(_tempProjectRoot, "footprints.golem.yaml");
            Assert.That(File.Exists(footprintsPath), Is.True);
            var footprintsText = File.ReadAllText(footprintsPath);
            var wallGuid = AssetDatabase.AssetPathToGUID(AssetDatabase.GetAssetPath(wallPrefab));
            Assert.That(footprintsText, Does.Contain(wallGuid));
            Assert.That(footprintsText, Does.Contain("alias: scribe_e2e_wall"));
            Assert.That(footprintsText, Does.Contain("version: 1"));
            Assert.That(footprintsText, Does.Contain("dimensions: 2"));
            Assert.That(footprintsText, Does.Not.Contain("entity:"));

            Assert.That(Directory.Exists(Path.Combine(_tempProjectRoot, "schemas", "scenes")), Is.False);
            Assert.That(File.Exists(Path.Combine(_tempProjectRoot, "scenes.golem.yaml")), Is.False);

            Assert.That(_hostRegistry.GetPrefab("ScribeE2EPlayer"), Is.SameAs(playerGo));
            Assert.That(_hostRegistry.GetPrefab("ScribeE2EMonster"), Is.SameAs(monsterGo));
            Assert.That(_hostRegistry.GetPrefab("ScribeE2EWall"), Is.Null);

            // Dry-run compare for the fixture entity + footprint artifacts (no writes).
            var previous = GolemScribeManifest.Load(_tempProjectRoot);
            GolemFootprintExporter.CollectFootprints(
                2,
                "footprints.golem.yaml",
                previous,
                _tempProjectRoot,
                out var footprintDesired,
                out _,
                out var collectErrors);
            Assert.That(
                collectErrors.Where(e => e.IndexOf("ScribeE2E", StringComparison.OrdinalIgnoreCase) >= 0).ToList(),
                Is.Empty);

            var desired = entityDesired.Concat(footprintDesired).ToList();
            var validation = new GolemScribeValidator.ValidationResult();
            GolemScribeValidator.CompareDesiredToDisk(
                _tempProjectRoot,
                previous,
                desired,
                new[]
                {
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    GolemScribeConstants.ArtifactKindFootprint
                },
                validation);
            Assert.That(validation.IsClean, Is.True,
                string.Join("; ", validation.Errors.Concat(validation.Missing).Concat(validation.Stale)
                    .Concat(validation.Orphaned).Concat(validation.ManuallyModified)));
        }

        [Test]
        public void FixtureComposition_WallIsFootprintOnly_CatalogGuidIsOpaqueString()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-scribe-e2e-comp-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                var playerModel = GolemEntitySchemaBuilder.Build(typeof(ScribeE2EPlayerEntity), 2, "schemas/entities/");
                var monsterModel = GolemEntitySchemaBuilder.Build(typeof(ScribeE2EMonsterEntity), 2, "schemas/entities/");
                Assert.That(playerModel.Errors, Is.Empty);
                Assert.That(monsterModel.Errors, Is.Empty);

                var catalogModel = GolemCatalogSchemaBuilder.Build(
                    typeof(ScribeE2EMonsterDefinition), "schemas/types/", "schemas/world/", root);
                Assert.That(catalogModel.Errors, Is.Empty);

                var monsterGuid = "0123456789abcdef0123456789abcdef";
                var rows = new List<GolemCatalogRowModel>
                {
                    new GolemCatalogRowModel
                    {
                        SortKey = "goblin",
                        Values =
                        {
                            ("id", GolemYamlWriter.FormatScalar("goblin")),
                            ("health", GolemYamlWriter.FormatInt(12)),
                            ("prefab", GolemYamlWriter.FormatScalar(monsterGuid))
                        }
                    }
                };

                var wall = new GolemFootprintModel
                {
                    Guid = "fedcba9876543210fedcba9876543210",
                    Name = "ScribeE2EWall",
                    AssetPath = "Assets/Prefabs/ScribeE2EWall.prefab",
                    Alias = "scribe_e2e_wall"
                };
                wall.Shapes.Add(new GolemFootprintShapeModel
                {
                    Type = "aabb", W = 2f, H = 1f, OffsetX = 0f, OffsetY = 0.5f, Trigger = false, Layer = "Default"
                });

                var desired = new List<GolemScribeArtifactRecord>
                {
                    Artifact(GolemScribeConstants.ArtifactKindEntitySchema, playerModel.RelativePath,
                        GolemEntitySchemaBuilder.BuildYaml(playerModel), "ScribeE2EPlayer"),
                    Artifact(GolemScribeConstants.ArtifactKindEntitySchema, monsterModel.RelativePath,
                        GolemEntitySchemaBuilder.BuildYaml(monsterModel), "ScribeE2EMonster"),
                    Artifact(GolemScribeConstants.ArtifactKindTypeSchema, catalogModel.TypesRelativePath,
                        GolemCatalogSchemaBuilder.BuildTypeYaml(catalogModel), catalogModel.TypeName),
                    Artifact(GolemScribeConstants.ArtifactKindWorldSchema, catalogModel.WorldRelativePath,
                        GolemCatalogSchemaBuilder.BuildWorldYaml(catalogModel), catalogModel.TypeName),
                    Artifact(GolemScribeConstants.ArtifactKindCatalogData, catalogModel.CatalogDataRelativePath,
                        GolemCatalogSchemaBuilder.BuildCatalogDataYaml(rows), catalogModel.TypeName),
                    Artifact(GolemScribeConstants.ArtifactKindFootprint, "footprints.golem.yaml",
                        GolemFootprintYamlBuilder.BuildYaml(2, new[] { wall }), null)
                };

                var reconcile = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindEntitySchema,
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData,
                        GolemScribeConstants.ArtifactKindFootprint
                    },
                    Array.Empty<GolemScribeArtifactRecord>(),
                    desired);
                Assert.That(reconcile.Errors, Is.Empty, string.Join("; ", reconcile.Errors));

                var catalogData = File.ReadAllText(Path.Combine(root, catalogModel.CatalogDataRelativePath));
                Assert.That(catalogData, Does.Contain("id: goblin"));
                Assert.That(catalogData, Does.Contain(monsterGuid));
                Assert.That(catalogData, Does.Not.Contain("entity:"));

                var entityFiles = Directory.GetFiles(Path.Combine(root, "schemas", "entities"), "*.yaml");
                Assert.That(entityFiles, Has.Length.EqualTo(2));
                foreach (var file in entityFiles)
                {
                    Assert.That(File.ReadAllText(file), Does.Not.Contain("Wall"));
                }

                var footprints = File.ReadAllText(Path.Combine(root, "footprints.golem.yaml"));
                Assert.That(footprints, Does.Contain("alias: scribe_e2e_wall"));
                Assert.That(footprints, Does.Not.Contain("entity:"));
                Assert.That(Directory.Exists(Path.Combine(root, "schemas", "scenes")), Is.False);

                var previous = GolemScribeManifest.Load(root);
                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.CompareDesiredToDisk(
                    root,
                    previous,
                    desired,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindEntitySchema,
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData,
                        GolemScribeConstants.ArtifactKindFootprint
                    },
                    result);
                Assert.That(result.IsClean, Is.True, string.Join("; ", result.Errors.Concat(result.Stale)));

                var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
                var player = new GameObject("p");
                var monster = new GameObject("m");
                try
                {
                    registry.Upsert("ScribeE2EPlayer", player);
                    registry.Upsert("ScribeE2EMonster", monster);
                    Assert.That(registry.GetPrefab("ScribeE2EPlayer"), Is.SameAs(player));
                    Assert.That(registry.GetPrefab("ScribeE2EMonster"), Is.SameAs(monster));
                    Assert.That(registry.GetPrefab("ScribeE2EWall"), Is.Null);
                }
                finally
                {
                    UnityEngine.Object.DestroyImmediate(player);
                    UnityEngine.Object.DestroyImmediate(monster);
                    UnityEngine.Object.DestroyImmediate(registry);
                }
            }
            finally
            {
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, recursive: true);
                }
            }
        }

        private GameObject CreateWallFootprintPrefab(string path)
        {
            var go = new GameObject("ScribeE2EWall");
            var marker = go.AddComponent<GolemFootprint>();
            marker.Alias = "scribe_e2e_wall";
            marker.IncludedLayers = ~0;
            var box = go.AddComponent<BoxCollider2D>();
            box.size = new Vector2(2f, 1f);
            box.offset = new Vector2(0f, 0.5f);
            var prefab = PrefabUtility.SaveAsPrefabAsset(go, path);
            UnityEngine.Object.DestroyImmediate(go);
            Track(path);
            return prefab;
        }

        private void EnsureFolder(string path)
        {
            if (AssetDatabase.IsValidFolder(path))
            {
                return;
            }

            var parent = Path.GetDirectoryName(path)?.Replace('\\', '/');
            var name = Path.GetFileName(path);
            if (!string.IsNullOrEmpty(parent) && parent != "Assets" && !AssetDatabase.IsValidFolder(parent))
            {
                EnsureFolder(parent);
            }

            AssetDatabase.CreateFolder(parent ?? "Assets", name);
            Track(path);
        }

        private void Track(string path)
        {
            if (!string.IsNullOrEmpty(path) && !_createdPaths.Contains(path))
            {
                _createdPaths.Add(path);
            }
        }

        private static GolemScribeArtifactRecord Artifact(string kind, string path, string content, string entity)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = kind,
                SourceGuid = string.IsNullOrEmpty(entity)
                    ? GolemScribeConstants.FootprintAggregateSourceGuid
                    : "cccccccccccccccccccccccccccccccc",
                Entity = entity,
                Path = path,
                PendingContent = content,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            };
        }
    }
}
