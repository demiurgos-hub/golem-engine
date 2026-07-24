using System.Collections.Generic;
using System.IO;
using System.Linq;
using GolemEngine.Unity.Editor;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemCatalogExportTests
    {
        [TearDown]
        public void TearDown()
        {
            GolemScribeArtifacts.AfterWriteHookForTests = null;
        }

        [Test]
        public void ReconcileKinds_DeletesAllThreeCatalogArtifactsWhenClassDisappears()
        {
            var root = CreateTempRoot();
            try
            {
                var typePath = "schemas/types/monster_definition.yaml";
                var worldPath = "schemas/world/monster_definition.yaml";
                var dataPath = "catalogs/monster_definition.golem.yaml";
                var typeYaml = GolemYamlWriter.BuildDocument(new[] { "type: MonsterDefinition", "fields:", "  {}" });
                var worldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: MonsterDefinition",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/monster_definition.golem.yaml",
                    "  type: MonsterDefinition",
                    "  key: id"
                });
                var dataYaml = GolemYamlWriter.BuildDocument(new[] { "- id: goblin", "  health: 10" });

                WriteOwned(root, typePath, typeYaml);
                WriteOwned(root, worldPath, worldYaml);
                WriteOwned(root, dataPath, dataYaml);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    Record(GolemScribeConstants.ArtifactKindTypeSchema, typePath, typeYaml),
                    Record(GolemScribeConstants.ArtifactKindWorldSchema, worldPath, worldYaml),
                    Record(GolemScribeConstants.ArtifactKindCatalogData, dataPath, dataYaml)
                };

                // Preserve an unrelated entity schema across catalog cleanup.
                var entityPath = "schemas/entities/player.yaml";
                var entityYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Player", "vars:", "  {}" });
                WriteOwned(root, entityPath, entityYaml);
                previous.Add(new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                    SourceGuid = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
                    Entity = "Player",
                    Path = entityPath,
                    Hash = GolemScribeManifest.ComputeContentHash(entityYaml)
                });

                var result = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData
                    },
                    previous,
                    new List<GolemScribeArtifactRecord>());

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, typePath)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, worldPath)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, dataPath)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, entityPath)), Is.True);
                Assert.That(result.TypeSchemaBytesChanged, Is.True);
                Assert.That(result.WorldSchemaBytesChanged, Is.True);
                Assert.That(result.CatalogDataBytesChanged, Is.True);
                Assert.That(result.EntitySchemaBytesChanged, Is.False);
                Assert.That(result.ManifestRecords, Has.Count.EqualTo(1));
                Assert.That(result.ManifestRecords[0].Kind, Is.EqualTo(GolemScribeConstants.ArtifactKindEntitySchema));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKinds_DataOnlyChangeDoesNotMarkSchemaBytesChanged()
        {
            var root = CreateTempRoot();
            try
            {
                var typePath = "schemas/types/item.yaml";
                var worldPath = "schemas/world/item.yaml";
                var dataPath = "catalogs/item.golem.yaml";
                var typeYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "type: Item",
                    "fields:",
                    "  id:",
                    "    tag: 1",
                    "    type: string"
                });
                var worldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: Item",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/item.golem.yaml",
                    "  type: Item",
                    "  key: id"
                });
                var oldData = GolemYamlWriter.BuildDocument(new[] { "- id: a", "  name: A" });
                var newData = GolemYamlWriter.BuildDocument(new[] { "- id: a", "  name: B" });

                WriteOwned(root, typePath, typeYaml);
                WriteOwned(root, worldPath, worldYaml);
                WriteOwned(root, dataPath, oldData);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    Record(GolemScribeConstants.ArtifactKindTypeSchema, typePath, typeYaml),
                    Record(GolemScribeConstants.ArtifactKindWorldSchema, worldPath, worldYaml),
                    Record(GolemScribeConstants.ArtifactKindCatalogData, dataPath, oldData)
                };

                var desired = new List<GolemScribeArtifactRecord>
                {
                    Desired(GolemScribeConstants.ArtifactKindTypeSchema, typePath, typeYaml),
                    Desired(GolemScribeConstants.ArtifactKindWorldSchema, worldPath, worldYaml),
                    Desired(GolemScribeConstants.ArtifactKindCatalogData, dataPath, newData)
                };

                var result = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData
                    },
                    previous,
                    desired);

                Assert.That(result.Errors, Is.Empty);
                Assert.That(result.AnyBytesChanged, Is.True);
                Assert.That(result.CatalogDataBytesChanged, Is.True);
                Assert.That(result.TypeSchemaBytesChanged, Is.False);
                Assert.That(result.WorldSchemaBytesChanged, Is.False);
                Assert.That(result.CatalogSchemaBytesChanged, Is.False);
                Assert.That(File.ReadAllText(GolemScribeArtifacts.ToAbsolutePath(root, dataPath)), Is.EqualTo(newData));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKinds_RefusesHandwrittenCatalogCollision()
        {
            var root = CreateTempRoot();
            try
            {
                var dataPath = "catalogs/item.golem.yaml";
                var absolute = GolemScribeArtifacts.ToAbsolutePath(root, dataPath);
                Directory.CreateDirectory(Path.GetDirectoryName(absolute));
                File.WriteAllText(absolute, "- id: handwritten\n");

                var desired = new List<GolemScribeArtifactRecord>
                {
                    Desired(
                        GolemScribeConstants.ArtifactKindCatalogData,
                        dataPath,
                        GolemYamlWriter.BuildDocument(new[] { "- id: scribe" }))
                };

                var result = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[] { GolemScribeConstants.ArtifactKindCatalogData },
                    new List<GolemScribeArtifactRecord>(),
                    desired);

                Assert.That(result.Errors, Has.Some.Contain("handwritten"));
                Assert.That(File.ReadAllText(absolute), Is.EqualTo("- id: handwritten\n"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ApplyCatalogCandidate_DropsBothSidesOfDuplicateTypeNames()
        {
            var desired = new List<GolemScribeArtifactRecord>();
            var sources = new Dictionary<string, string>();
            var conflicts = new HashSet<string>();
            var errors = new List<string>();

            var first = new[]
            {
                Desired(GolemScribeConstants.ArtifactKindTypeSchema, "schemas/types/item.yaml", "type: Item\n")
            };
            var second = new[]
            {
                Desired(GolemScribeConstants.ArtifactKindTypeSchema, "schemas/types/item.yaml", "type: Item\n")
            };

            GolemCatalogExporter.ApplyCatalogCandidate(
                "Item", "Ns.A.Item", first, sources, conflicts, desired, errors);
            GolemCatalogExporter.ApplyCatalogCandidate(
                "Item", "Ns.B.Item", second, sources, conflicts, desired, errors);

            Assert.That(desired, Is.Empty);
            Assert.That(conflicts, Does.Contain("Item"));
            Assert.That(errors, Has.Some.Contain("multiple classes"));
        }

        [Test]
        public void TryClaimCatalogOutputPaths_ExcludesOnlyCollidingCatalogs()
        {
            var root = CreateTempRoot();
            try
            {
                var pathToType = new Dictionary<string, string>(System.StringComparer.Ordinal);
                var conflicted = new HashSet<string>(System.StringComparer.Ordinal);
                var typeSources = new Dictionary<string, string>(System.StringComparer.Ordinal);
                var desired = new List<GolemScribeArtifactRecord>();
                var errors = new List<string>();
                var previous = new List<GolemScribeArtifactRecord>();

                Assert.That(
                    GolemCatalogExporter.TryClaimCatalogOutputPaths(
                        "Alpha",
                        "schemas/types/alpha.yaml",
                        "schemas/world/alpha.yaml",
                        "catalogs/alpha.golem.yaml",
                        pathToType,
                        conflicted,
                        typeSources,
                        desired,
                        previous,
                        root,
                        errors),
                    Is.True);

                // Peer with distinct paths still claims successfully.
                Assert.That(
                    GolemCatalogExporter.TryClaimCatalogOutputPaths(
                        "Beta",
                        "schemas/types/beta.yaml",
                        "schemas/world/beta.yaml",
                        "catalogs/beta.golem.yaml",
                        pathToType,
                        conflicted,
                        typeSources,
                        desired,
                        previous,
                        root,
                        errors),
                    Is.True);

                // Snake/path collision with Alpha: both conflict; Beta remains claimed.
                Assert.That(
                    GolemCatalogExporter.TryClaimCatalogOutputPaths(
                        "AlphaTwin",
                        "schemas/types/alpha.yaml",
                        "schemas/world/alpha_twin.yaml",
                        "catalogs/alpha_twin.golem.yaml",
                        pathToType,
                        conflicted,
                        typeSources,
                        desired,
                        previous,
                        root,
                        errors),
                    Is.False);

                Assert.That(conflicted, Does.Contain("Alpha"));
                Assert.That(conflicted, Does.Contain("AlphaTwin"));
                Assert.That(conflicted, Does.Not.Contain("Beta"));
                Assert.That(pathToType.ContainsKey("schemas/types/beta.yaml"), Is.True);
                Assert.That(pathToType.ContainsKey("schemas/types/alpha.yaml"), Is.False);
                Assert.That(errors, Has.Some.Contain("collides"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ClassRename_DeletesOldTypeArtifacts_DoesNotInferRename()
        {
            var root = CreateTempRoot();
            try
            {
                var oldType = "schemas/types/old_item.yaml";
                var oldWorld = "schemas/world/old_item.yaml";
                var oldData = "catalogs/old_item.golem.yaml";
                var newType = "schemas/types/new_item.yaml";
                var newWorld = "schemas/world/new_item.yaml";
                var newData = "catalogs/new_item.golem.yaml";

                var oldTypeYaml = GolemYamlWriter.BuildDocument(new[] { "type: OldItem", "fields:", "  {}" });
                var oldWorldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: OldItem",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/old_item.golem.yaml",
                    "  type: OldItem",
                    "  key: id"
                });
                var oldDataYaml = GolemYamlWriter.BuildDocument(new[] { "- id: a" });
                var newTypeYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "type: NewItem",
                    "fields:",
                    "  id:",
                    "    tag: 1",
                    "    type: string"
                });
                var newWorldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: NewItem",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/new_item.golem.yaml",
                    "  type: NewItem",
                    "  key: id"
                });
                var newDataYaml = GolemYamlWriter.BuildDocument(new[] { "- id: a" });

                WriteOwned(root, oldType, oldTypeYaml);
                WriteOwned(root, oldWorld, oldWorldYaml);
                WriteOwned(root, oldData, oldDataYaml);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    RecordFor("OldItem", GolemScribeConstants.ArtifactKindTypeSchema, oldType, oldTypeYaml),
                    RecordFor("OldItem", GolemScribeConstants.ArtifactKindWorldSchema, oldWorld, oldWorldYaml),
                    RecordFor("OldItem", GolemScribeConstants.ArtifactKindCatalogData, oldData, oldDataYaml)
                };

                // Renamed class is a new type name only — no OldItem preserve, no rename inference.
                var desired = new List<GolemScribeArtifactRecord>
                {
                    DesiredFor("NewItem", GolemScribeConstants.ArtifactKindTypeSchema, newType, newTypeYaml),
                    DesiredFor("NewItem", GolemScribeConstants.ArtifactKindWorldSchema, newWorld, newWorldYaml),
                    DesiredFor("NewItem", GolemScribeConstants.ArtifactKindCatalogData, newData, newDataYaml)
                };

                var result = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData
                    },
                    previous,
                    desired);

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, oldType)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, oldWorld)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, oldData)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, newType)), Is.True);
                Assert.That(result.ManifestRecords.Any(r => r.Entity == "OldItem"), Is.False);
                Assert.That(result.ManifestRecords.Count(r => r.Entity == "NewItem"), Is.EqualTo(3));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void InvalidCatalogClass_PreservesPreviousArtifacts_WhileDeletedClassIsRemoved()
        {
            var root = CreateTempRoot();
            try
            {
                var keepType = "schemas/types/item.yaml";
                var keepWorld = "schemas/world/item.yaml";
                var keepData = "catalogs/item.golem.yaml";
                var goneType = "schemas/types/gone.yaml";
                var goneWorld = "schemas/world/gone.yaml";
                var goneData = "catalogs/gone.golem.yaml";

                var keepTypeYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "type: Item",
                    "fields:",
                    "  id:",
                    "    tag: 1",
                    "    type: string"
                });
                var keepWorldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: Item",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/item.golem.yaml",
                    "  type: Item",
                    "  key: id"
                });
                var keepDataYaml = GolemYamlWriter.BuildDocument(new[] { "- id: a" });
                var goneTypeYaml = GolemYamlWriter.BuildDocument(new[] { "type: Gone", "fields:", "  {}" });
                var goneWorldYaml = GolemYamlWriter.BuildDocument(new[]
                {
                    "world: Gone",
                    "source:",
                    "  format: catalog",
                    "  file: catalogs/gone.golem.yaml",
                    "  type: Gone",
                    "  key: id"
                });
                var goneDataYaml = GolemYamlWriter.BuildDocument(new[] { "- id: z" });

                WriteOwned(root, keepType, keepTypeYaml);
                WriteOwned(root, keepWorld, keepWorldYaml);
                WriteOwned(root, keepData, keepDataYaml);
                WriteOwned(root, goneType, goneTypeYaml);
                WriteOwned(root, goneWorld, goneWorldYaml);
                WriteOwned(root, goneData, goneDataYaml);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    RecordFor("Item", GolemScribeConstants.ArtifactKindTypeSchema, keepType, keepTypeYaml),
                    RecordFor("Item", GolemScribeConstants.ArtifactKindWorldSchema, keepWorld, keepWorldYaml),
                    RecordFor("Item", GolemScribeConstants.ArtifactKindCatalogData, keepData, keepDataYaml),
                    RecordFor("Gone", GolemScribeConstants.ArtifactKindTypeSchema, goneType, goneTypeYaml),
                    RecordFor("Gone", GolemScribeConstants.ArtifactKindWorldSchema, goneWorld, goneWorldYaml),
                    RecordFor("Gone", GolemScribeConstants.ArtifactKindCatalogData, goneData, goneDataYaml)
                };

                // Simulate transient invalid Item (no new desired rows) while Gone is truly deleted.
                var desired = new List<GolemScribeArtifactRecord>();
                var preserveErrors = new List<string>();
                GolemCatalogExporter.AppendPreviousCatalogArtifacts(
                    previous, root, "Item", desired, preserveErrors);

                Assert.That(preserveErrors, Is.Empty);
                Assert.That(desired, Has.Count.EqualTo(3));

                var result = GolemScribeArtifacts.ReconcileKinds(
                    root,
                    new[]
                    {
                        GolemScribeConstants.ArtifactKindTypeSchema,
                        GolemScribeConstants.ArtifactKindWorldSchema,
                        GolemScribeConstants.ArtifactKindCatalogData
                    },
                    previous,
                    desired);

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, keepType)), Is.True);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, keepWorld)), Is.True);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, keepData)), Is.True);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, goneType)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, goneWorld)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, goneData)), Is.False);
                Assert.That(result.TypeSchemaBytesChanged, Is.True); // Gone deleted
                Assert.That(result.WorldSchemaBytesChanged, Is.True);
                Assert.That(result.CatalogDataBytesChanged, Is.True);
                Assert.That(result.ManifestRecords.Count(r => r.Entity == "Item"), Is.EqualTo(3));
                Assert.That(result.ManifestRecords.Any(r => r.Entity == "Gone"), Is.False);

                // Preserving identical Item bytes must not by itself require bake for Item content.
                Assert.That(File.ReadAllText(GolemScribeArtifacts.ToAbsolutePath(root, keepData)), Is.EqualTo(keepDataYaml));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        private static GolemScribeArtifactRecord Record(string kind, string path, string content)
        {
            return RecordFor("Item", kind, path, content);
        }

        private static GolemScribeArtifactRecord RecordFor(string entity, string kind, string path, string content)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = kind,
                SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                Entity = entity,
                Path = path,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            };
        }

        private static GolemScribeArtifactRecord Desired(string kind, string path, string content)
        {
            return DesiredFor("Item", kind, path, content);
        }

        private static GolemScribeArtifactRecord DesiredFor(string entity, string kind, string path, string content)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = kind,
                SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                Entity = entity,
                Path = path,
                PendingContent = content,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            };
        }

        private static void WriteOwned(string root, string relative, string content)
        {
            var absolute = GolemScribeArtifacts.ToAbsolutePath(root, relative);
            Directory.CreateDirectory(Path.GetDirectoryName(absolute));
            File.WriteAllText(absolute, content);
        }

        private static string CreateTempRoot()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-scribe-catalog-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            return root;
        }

        private static void DeleteTempRoot(string root)
        {
            if (Directory.Exists(root))
            {
                Directory.Delete(root, true);
            }
        }
    }
}
