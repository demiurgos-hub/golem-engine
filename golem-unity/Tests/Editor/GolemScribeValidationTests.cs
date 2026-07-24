using System.Collections.Generic;
using System.IO;
using GolemEngine.Unity;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeValidationTests
    {
        private string _previousProjectRoot;
        private string _previousFootprintsPath;

        [SetUp]
        public void SetUp()
        {
            var settings = GolemUnityEditorSettings.instance;
            _previousProjectRoot = settings.ProjectRoot;
            _previousFootprintsPath = settings.FootprintsPath;
        }

        [TearDown]
        public void TearDown()
        {
            var settings = GolemUnityEditorSettings.instance;
            settings.ProjectRoot = _previousProjectRoot;
            settings.FootprintsPath = _previousFootprintsPath;
            GolemUnityEditorSettings.Save();
        }

        [Test]
        public void CompareDesiredToDisk_ReportsMissingStaleOrphanedAndManual()
        {
            var root = CreateTempRoot();
            try
            {
                var okPath = "schemas/entities/player.yaml";
                var stalePath = "schemas/entities/monster.yaml";
                var orphanPath = "schemas/entities/old.yaml";
                var manualPath = "schemas/entities/hand.yaml";
                var missingPath = "schemas/entities/new.yaml";

                var okYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Player", "vars:", "  {}" });
                var staleOld = GolemYamlWriter.BuildDocument(new[] { "entity: Monster", "vars:", "  {}" });
                var staleNew = GolemYamlWriter.BuildDocument(new[]
                {
                    "entity: Monster",
                    "vars:",
                    "  health:",
                    "    tag: 3",
                    "    type: int32",
                    "    sync: tick"
                });
                var orphanYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Old", "vars:", "  {}" });
                var manualOriginal = GolemYamlWriter.BuildDocument(new[] { "entity: Hand", "vars:", "  {}" });
                var manualEdited = manualOriginal + "# touched\n";
                var missingYaml = GolemYamlWriter.BuildDocument(new[] { "entity: New", "vars:", "  {}" });

                WriteOwned(root, okPath, okYaml);
                WriteOwned(root, stalePath, staleOld);
                WriteOwned(root, orphanPath, orphanYaml);
                WriteOwned(root, manualPath, manualEdited);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    Record(okPath, okYaml),
                    Record(stalePath, staleOld),
                    Record(orphanPath, orphanYaml),
                    Record(manualPath, manualOriginal)
                };

                var desired = new List<GolemScribeArtifactRecord>
                {
                    Desired(okPath, okYaml),
                    Desired(stalePath, staleNew),
                    Desired(manualPath, manualOriginal),
                    Desired(missingPath, missingYaml)
                };

                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.CompareDesiredToDisk(
                    root,
                    previous,
                    desired,
                    new[] { GolemScribeConstants.ArtifactKindEntitySchema },
                    result);

                Assert.That(result.Missing, Does.Contain(missingPath));
                Assert.That(result.Stale, Does.Contain(stalePath));
                Assert.That(result.Orphaned, Does.Contain(orphanPath));
                Assert.That(result.ManuallyModified, Does.Contain(manualPath));
                Assert.That(result.IsClean, Is.False);
                Assert.That(result.HasDrift, Is.True);

                Assert.That(File.ReadAllText(Path.Combine(root, stalePath)), Is.EqualTo(staleOld));
                Assert.That(File.Exists(Path.Combine(root, orphanPath)), Is.True);
                Assert.That(File.Exists(Path.Combine(root, missingPath)), Is.False);
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void CompareDesiredToDisk_ManifestOwnedPathMissingFromDisk_IsMissingEvenWhenNotDesired()
        {
            var root = CreateTempRoot();
            try
            {
                var gonePath = "schemas/entities/deleted.yaml";
                var goneYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Deleted", "vars:", "  {}" });
                var previous = new List<GolemScribeArtifactRecord> { Record(gonePath, goneYaml) };

                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.CompareDesiredToDisk(
                    root,
                    previous,
                    new List<GolemScribeArtifactRecord>(),
                    new[] { GolemScribeConstants.ArtifactKindEntitySchema },
                    result);

                Assert.That(result.Missing, Does.Contain(gonePath));
                Assert.That(result.Orphaned, Is.Empty);
                Assert.That(File.Exists(Path.Combine(root, gonePath)), Is.False);
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ValidateManifestRecords_RejectsUnknownKinds()
        {
            var root = CreateTempRoot();
            try
            {
                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.ValidateManifestRecords(
                    root,
                    new List<GolemScribeArtifactRecord>
                    {
                        new GolemScribeArtifactRecord
                        {
                            Kind = "scene_export",
                            SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                            Path = "schemas/scenes/level.yaml",
                            Hash = "abcd"
                        }
                    },
                    result);

                Assert.That(result.Errors, Has.Some.Contain("unknown kind"));
                Assert.That(result.Errors, Has.Some.Contain("scene_export"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void CompareDesiredToDisk_ReportsHandwrittenOccupancyAsManuallyModified()
        {
            var root = CreateTempRoot();
            try
            {
                var path = "schemas/entities/player.yaml";
                var desiredYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Player", "vars:", "  {}" });
                Directory.CreateDirectory(Path.Combine(root, "schemas", "entities"));
                File.WriteAllText(Path.Combine(root, path), "entity: Player\n");

                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.CompareDesiredToDisk(
                    root,
                    new List<GolemScribeArtifactRecord>(),
                    new List<GolemScribeArtifactRecord> { Desired(path, desiredYaml) },
                    new[] { GolemScribeConstants.ArtifactKindEntitySchema },
                    result);

                Assert.That(result.ManuallyModified, Does.Contain(path));
                Assert.That(result.Errors, Has.Some.Contain("handwritten"));
                Assert.That(File.ReadAllText(Path.Combine(root, path)), Is.EqualTo("entity: Player\n"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void Validate_EndToEnd_FlagsBogusKindHostilePathMissingFileAndFootprintDims()
        {
            var root = CreateTempRoot();
            try
            {
                WriteGolemYaml(root, dimensions: 2);
                var missingPath = "schemas/entities/gone.yaml";
                var goneYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Gone", "vars:", "  {}" });
                var badFootprints = GolemYamlWriter.BuildDocument(new[]
                {
                    "version: 1",
                    "dimensions: 3",
                    "footprints: {}"
                });
                WriteOwned(root, "footprints.golem.yaml", badFootprints);

                var manifestRecords = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = "not_a_real_kind",
                        SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        Path = "schemas/entities/bogus.yaml",
                        Hash = "00"
                    },
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                        Entity = "Gone",
                        Path = missingPath,
                        Hash = GolemScribeManifest.ComputeContentHash(goneYaml)
                    },
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "cccccccccccccccccccccccccccccccc",
                        Entity = "Escaped",
                        Path = "../outside.yaml",
                        Hash = "11"
                    },
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindFootprint,
                        SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                        Path = "footprints.golem.yaml",
                        Hash = GolemScribeManifest.ComputeContentHash(badFootprints)
                    }
                };
                File.WriteAllText(
                    GolemScribeManifest.ManifestPath(root),
                    GolemScribeManifest.BuildDocument(manifestRecords));

                var settings = GolemUnityEditorSettings.instance;
                settings.ProjectRoot = root;
                settings.FootprintsPath = "footprints.golem.yaml";
                GolemUnityEditorSettings.Save();

                var beforeManifest = File.ReadAllText(GolemScribeManifest.ManifestPath(root));
                var beforeFootprints = File.ReadAllText(Path.Combine(root, "footprints.golem.yaml"));

                var result = GolemScribeValidator.Validate(root);

                Assert.That(result.Errors, Has.Some.Contain("unknown kind"));
                Assert.That(result.Errors, Has.Some.Contain("Manifest ownership path rejected"));
                Assert.That(result.Missing, Does.Contain(missingPath));
                Assert.That(result.Errors, Has.Some.Contain("dimensions 3"));
                Assert.That(result.HasFailures, Is.True);

                // Dry-run must not mutate committed artifacts.
                Assert.That(File.ReadAllText(GolemScribeManifest.ManifestPath(root)), Is.EqualTo(beforeManifest));
                Assert.That(File.ReadAllText(Path.Combine(root, "footprints.golem.yaml")), Is.EqualTo(beforeFootprints));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ValidatePrefabRegistryParity_IsOneWay_ExtraHandwrittenEntriesAllowed()
        {
            var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var player = new GameObject("player");
            var handwritten = new GameObject("handwritten");
            try
            {
                registry.Upsert("Player", player);
                registry.Upsert("HandwrittenNpc", handwritten);

                var desired = new Dictionary<string, GameObject> { { "Player", player } };
                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.ValidatePrefabRegistryParity(desired, result, registry);

                Assert.That(result.Errors, Is.Empty);
                Assert.That(registry.GetPrefab("HandwrittenNpc"), Is.SameAs(handwritten));
            }
            finally
            {
                Object.DestroyImmediate(player);
                Object.DestroyImmediate(handwritten);
                Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void ValidatePrefabRegistryParity_ReportsMissingDesiredMapping()
        {
            var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var player = new GameObject("player");
            try
            {
                var desired = new Dictionary<string, GameObject> { { "Player", player } };
                var result = new GolemScribeValidator.ValidationResult();
                GolemScribeValidator.ValidatePrefabRegistryParity(desired, result, registry);

                Assert.That(result.Errors, Has.Some.Contain("missing entity 'Player'"));
            }
            finally
            {
                Object.DestroyImmediate(player);
                Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void ComputeExitCode_FailsOnPreDriftExportErrorsOrByteChanges()
        {
            var clean = new GolemScribeValidator.ValidationResult();
            var drifted = new GolemScribeValidator.ValidationResult();
            drifted.Stale.Add("schemas/entities/player.yaml");
            var exportOk = new GolemScribeScheduler.ImmediateExportResult();
            var exportChanged = new GolemScribeScheduler.ImmediateExportResult { AnyBytesChanged = true };
            var exportErrored = new GolemScribeScheduler.ImmediateExportResult();
            exportErrored.Errors.Add("boom");

            Assert.That(GolemScribeCI.ComputeExitCode(clean, exportOk, clean), Is.EqualTo(0));
            Assert.That(GolemScribeCI.ComputeExitCode(drifted, exportOk, clean), Is.EqualTo(1));
            Assert.That(GolemScribeCI.ComputeExitCode(clean, exportChanged, clean), Is.EqualTo(1));
            Assert.That(GolemScribeCI.ComputeExitCode(clean, exportErrored, clean), Is.EqualTo(1));
            Assert.That(GolemScribeCI.ComputeExitCode(null, exportOk, clean), Is.EqualTo(1));
        }

        [Test]
        public void ValidationResult_IsCleanOnlyWhenNoErrorsOrDrift()
        {
            var clean = new GolemScribeValidator.ValidationResult();
            Assert.That(clean.IsClean, Is.True);
            Assert.That(clean.HasFailures, Is.False);

            var drifted = new GolemScribeValidator.ValidationResult();
            drifted.Stale.Add("x");
            Assert.That(drifted.HasDrift, Is.True);
            Assert.That(drifted.HasFailures, Is.True);

            var errored = new GolemScribeValidator.ValidationResult();
            errored.Errors.Add("bad");
            Assert.That(errored.HasExporterErrors, Is.True);
            Assert.That(errored.HasFailures, Is.True);
        }

        private static void WriteGolemYaml(string root, int dimensions)
        {
            File.WriteAllText(
                Path.Combine(root, "golem.yaml"),
                string.Join(
                    "\n",
                    "entity_schema: schemas/entities/",
                    "types_schema: schemas/types/",
                    "world_schema: schemas/world/",
                    "simulation:",
                    "  dimensions: " + dimensions,
                    ""));
        }

        private static GolemScribeArtifactRecord Record(string path, string content)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                Entity = "E",
                Path = path,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            };
        }

        private static GolemScribeArtifactRecord Desired(string path, string content)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                SourceGuid = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                Entity = "E",
                Path = path,
                PendingContent = content,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            };
        }

        private static void WriteOwned(string root, string relative, string content)
        {
            var absolute = Path.Combine(root, relative.Replace('/', Path.DirectorySeparatorChar));
            Directory.CreateDirectory(Path.GetDirectoryName(absolute));
            File.WriteAllText(absolute, content);
        }

        private static string CreateTempRoot()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-scribe-validate-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            return root;
        }

        private static void DeleteTempRoot(string root)
        {
            if (!string.IsNullOrEmpty(root) && Directory.Exists(root))
            {
                Directory.Delete(root, recursive: true);
            }
        }
    }
}
