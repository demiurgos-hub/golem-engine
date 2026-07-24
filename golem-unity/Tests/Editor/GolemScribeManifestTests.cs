using System;
using System.Collections.Generic;
using System.IO;
using GolemEngine.Unity.Editor;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeManifestTests
    {
        [TearDown]
        public void TearDown()
        {
            GolemScribeArtifacts.AfterWriteHookForTests = null;
        }

        [Test]
        public void ReconcileKind_NeverDeletesHandwrittenFiles()
        {
            var root = CreateTempRoot();
            try
            {
                var relative = "schemas/entities/player.yaml";
                var absolute = GolemScribeArtifacts.ToAbsolutePath(root, relative);
                Directory.CreateDirectory(Path.GetDirectoryName(absolute));
                File.WriteAllText(absolute, "entity: Player\nvars: {}\n");

                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        Entity = "Player",
                        Path = relative,
                        Hash = "deadbeef"
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    previous,
                    new List<GolemScribeArtifactRecord>());

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(absolute), Is.True, "handwritten file must remain");
                Assert.That(result.Warnings, Has.Some.Contain("handwritten"));
                Assert.That(File.ReadAllText(absolute), Does.Not.Contain(GolemScribeConstants.MarkerLine));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_DeletesOrphanedScribeOwnedFiles()
        {
            var root = CreateTempRoot();
            try
            {
                var relative = "schemas/entities/player.yaml";
                var absolute = GolemScribeArtifacts.ToAbsolutePath(root, relative);
                Directory.CreateDirectory(Path.GetDirectoryName(absolute));
                var content = GolemYamlWriter.BuildDocument(new[]
                {
                    "entity: Player",
                    "vars:",
                    "  {}"
                });
                File.WriteAllText(absolute, content);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                        Entity = "Player",
                        Path = relative,
                        Hash = GolemScribeManifest.ComputeContentHash(content)
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    previous,
                    new List<GolemScribeArtifactRecord>());

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(absolute), Is.False);
                Assert.That(result.EntitySchemaBytesChanged, Is.True);
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_RefusesToOverwriteHandwrittenPath()
        {
            var root = CreateTempRoot();
            try
            {
                var relative = "schemas/entities/player.yaml";
                var absolute = GolemScribeArtifacts.ToAbsolutePath(root, relative);
                Directory.CreateDirectory(Path.GetDirectoryName(absolute));
                File.WriteAllText(absolute, "entity: Handwritten\n");

                var desiredContent = GolemYamlWriter.BuildDocument(new[]
                {
                    "entity: Player",
                    "vars:",
                    "  {}"
                });
                var desired = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "cccccccccccccccccccccccccccccccc",
                        Entity = "Player",
                        Path = relative,
                        PendingContent = desiredContent
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    new List<GolemScribeArtifactRecord>(),
                    desired);

                Assert.That(result.Errors, Has.Some.Contain("handwritten"));
                Assert.That(File.ReadAllText(absolute), Is.EqualTo("entity: Handwritten\n"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_RejectsPathTraversalAndAbsolutePaths()
        {
            var root = CreateTempRoot();
            try
            {
                var desired = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "dddddddddddddddddddddddddddddddd",
                        Entity = "Evil",
                        Path = "../outside.yaml",
                        PendingContent = GolemYamlWriter.BuildDocument(new[] { "entity: Evil", "vars:", "  {}" })
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    new List<GolemScribeArtifactRecord>(),
                    desired);
                Assert.That(result.Errors, Has.Some.Contain("escapes"));

                desired[0].Path = Path.Combine(root, "abs.yaml");
                result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    new List<GolemScribeArtifactRecord>(),
                    desired);
                Assert.That(result.Errors, Has.Some.Matches<string>(e =>
                    e.IndexOf("absolute", System.StringComparison.OrdinalIgnoreCase) >= 0 ||
                    e.IndexOf("project-relative", System.StringComparison.OrdinalIgnoreCase) >= 0));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_RejectsHostilePriorManifestPaths()
        {
            var root = CreateTempRoot();
            try
            {
                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
                        Entity = "Evil",
                        Path = "../../Windows/System32/evil.yaml",
                        Hash = "abcd"
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    previous,
                    new List<GolemScribeArtifactRecord>());
                Assert.That(result.Errors, Has.Some.Matches<string>(e =>
                    e.IndexOf("Hostile", System.StringComparison.Ordinal) >= 0 ||
                    e.IndexOf("escapes", System.StringComparison.Ordinal) >= 0));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_RollsBackPartialWriteFailures()
        {
            var root = CreateTempRoot();
            try
            {
                var first = "schemas/entities/alpha.yaml";
                var second = "schemas/entities/beta.yaml";
                var writes = 0;
                GolemScribeArtifacts.AfterWriteHookForTests = _ =>
                {
                    writes++;
                    if (writes >= 2)
                    {
                        throw new IOException("forced write failure");
                    }
                };

                var desired = new List<GolemScribeArtifactRecord>
                {
                    MakeDesired("Alpha", first, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
                    MakeDesired("Beta", second, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindEntitySchema,
                    new List<GolemScribeArtifactRecord>(),
                    desired);

                Assert.That(result.Errors, Has.Some.Contain("forced write failure"));
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, first)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, second)), Is.False);
                Assert.That(File.Exists(GolemScribeManifest.ManifestPath(root)), Is.False);
                Assert.That(result.AnyBytesChanged, Is.False);
            }
            finally
            {
                GolemScribeArtifacts.AfterWriteHookForTests = null;
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void Manifest_RoundTripPreservesEscapedScalarsAndFieldOrderIndependence()
        {
            var records = new List<GolemScribeArtifactRecord>
            {
                new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                    Entity = "Player\"Hero",
                    SourceGuid = "ffffffffffffffffffffffffffffffff",
                    Path = "schemas/entities/player.yaml",
                    Hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
                }
            };

            var document = GolemScribeManifest.BuildDocument(records);
            Assert.That(GolemYamlWriter.IsScribeOwned(document), Is.True);

            // Reorder fields within the artifact block; parser must accept any order.
            var reordered = GolemYamlWriter.BuildDocument(new[]
            {
                "version: 1",
                "artifacts:",
                "  - kind: entity_schema",
                "    hash: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
                "    path: schemas/entities/player.yaml",
                "    source_guid: ffffffffffffffffffffffffffffffff",
                "    entity: " + GolemYamlWriter.FormatScalar("Player\"Hero")
            });

            var parsed = GolemScribeManifest.Parse(reordered);
            Assert.That(parsed, Has.Count.EqualTo(1));
            Assert.That(parsed[0].Entity, Is.EqualTo("Player\"Hero"));
            Assert.That(parsed[0].Path, Is.EqualTo("schemas/entities/player.yaml"));
            Assert.That(parsed[0].SourceGuid, Is.EqualTo("ffffffffffffffffffffffffffffffff"));
            Assert.That(parsed[0].Hash, Is.EqualTo("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"));
        }

        [Test]
        public void Manifest_ParseRejectsMalformedAndNonOwnedDocuments()
        {
            Assert.Throws<InvalidOperationException>(() => GolemScribeManifest.Parse(
                GolemYamlWriter.BuildDocument(new[]
                {
                    "version: 1",
                    "artifacts:",
                    "  - kind: entity_schema",
                    "    path: schemas/entities/player.yaml"
                })));

            var root = CreateTempRoot();
            try
            {
                File.WriteAllText(GolemScribeManifest.ManifestPath(root), "artifacts: []\n");
                Assert.Throws<InvalidOperationException>(() => GolemScribeManifest.Load(root));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void Load_MissingManifestReturnsEmpty()
        {
            var root = CreateTempRoot();
            try
            {
                var records = GolemScribeManifest.Load(root);
                Assert.That(records, Is.Empty);
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        private static GolemScribeArtifactRecord MakeDesired(string entity, string path, string guid)
        {
            var yaml = GolemYamlWriter.BuildDocument(new[]
            {
                "entity: " + entity,
                "vars:",
                "  {}"
            });
            return new GolemScribeArtifactRecord
            {
                Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                SourceGuid = guid,
                Entity = entity,
                Path = path,
                PendingContent = yaml,
                Hash = GolemScribeManifest.ComputeContentHash(yaml)
            };
        }

        private static string CreateTempRoot()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-scribe-tests-" + Path.GetRandomFileName());
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
