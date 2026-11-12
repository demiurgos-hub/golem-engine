using System;
using System.IO;
using System.Linq;
using System.Runtime.CompilerServices;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemPhysicsDocumentTests
    {
        internal static PhysicsSource Fixture() => new PhysicsSource
        {
            WorldID = "physics-fixture",
            Bodies = new[] { new PhysicsBody { ID = "actor", OwnerID = "actor-owner", Kind = "dynamic", Position = new PhysicsVector(0, 2, 0), UseGravity = true } },
            Colliders = new[] { new PhysicsCollider { ID = "actor/sphere", BodyID = "actor", OwnerID = "actor-owner", SourceID = "fixture/sphere", Shape = "sphere", Radius = 0.5f, MaterialID = "default", ContactOffset = 0.01f } },
            Materials = new[] { new PhysicsMaterial { ID = "default" } }
        };

        [Test]
        public void PortableSourceMatchesCrossLanguageFixture()
        {
            var source = Fixture();
            var content = GolemPhysicsDocument.Build(source);
            Assert.That(content, Is.EqualTo(File.ReadAllText(FixturePath()).Replace("\r\n", "\n")));
            Assert.That(GolemYamlWriter.IsScribeOwned(content), Is.True);
            Assert.That(GolemPhysicsDocument.IsOwned(content.Replace("physics source v1", "physics source v2")), Is.False);
        }

        [Test]
        public void InvalidMetadataAndTransformedWorldEnvelopeCannotBeExported()
        {
            var source = Fixture(); source.Colliders[0].Metadata = new[] { new PhysicsProperty { Key = "block", Value = "yes" }, new PhysicsProperty { Key = "block", Value = "no" } };
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
            source.Colliders[0].Metadata = Array.Empty<PhysicsProperty>(); source.Bodies[0].Position.X = 4096;
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
            source.Bodies[0].Position.X = float.NaN;
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
        }

        [Test]
        public void MissingAssetsNonUnitPosesAndUnsupportedJointsFailClosed()
        {
            var source = Fixture(); source.Colliders[0].Shape = "mesh";
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
            source = Fixture(); source.Bodies[0].Rotation.W = 2;
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
            source = Fixture(); source.Joints = new[] { new PhysicsJoint { ID = "unsupported", Kind = "unsupported", BodyID = "actor" } };
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Build(source));
        }

        [Test]
        public void OrderingDoesNotMutateCallerMetadata()
        {
            var source = Fixture(); source.Colliders[0].Metadata = new[] { new PhysicsProperty { Key = "z", Value = "last" }, new PhysicsProperty { Key = "a", Value = "first" } };
            var content = GolemPhysicsDocument.Build(source);
            Assert.That(content.IndexOf("\"Key\":\"a\"", StringComparison.Ordinal), Is.LessThan(content.IndexOf("\"Key\":\"z\"", StringComparison.Ordinal)));
            Assert.That(source.Colliders[0].Metadata.First().Key, Is.EqualTo("z"));
        }

        [Test]
        public void ZeroAreaNormalizationRetainsOrdinalsAndExactInputHash()
        {
            var source = Fixture(); source.Bodies[0].Kind = "static"; source.Colliders[0].Shape = "mesh"; source.Colliders[0].AssetID = "mesh";
            source.Assets = new[] { new PhysicsAsset { ID = "mesh", SourceID = "original",
                Vertices = new[] { new PhysicsVector(), new PhysicsVector(1), new PhysicsVector(0, 0, 1), new PhysicsVector(2), new PhysicsVector(0, 0, 0.000000000001f) },
                Indices = new uint[] { 0, 1, 1, 0, 2, 1, 0, 1, 3, 0, 4, 1 }
            } };
            var original = source.Assets[0].Indices.ToArray();
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Validate(source));
            var normalized = GolemPhysicsDocument.Normalize(source);
            Assert.That(normalized.Assets[0].Indices, Is.EqualTo(new uint[] { 0, 2, 1, 0, 4, 1 }));
            Assert.That(source.Assets[0].Indices, Is.EqualTo(original));
            Assert.That(source.Colliders[0].Metadata, Is.Empty);
            var metadata = normalized.Colliders[0].Metadata.ToDictionary(p => p.Key, p => p.Value);
            Assert.That(metadata[GolemPhysicsDocument.NormalizationPrefix + "removedTriangleOrdinals"], Is.EqualTo("0,2"));
            Assert.That(metadata[GolemPhysicsDocument.NormalizationPrefix + "originalTriangleCount"], Is.EqualTo("4"));
            var hash = metadata[GolemPhysicsDocument.NormalizationPrefix + "originalGeometrySHA256"];
            Assert.That(hash, Does.Match("^[0-9a-f]{64}$"));
            Assert.That(GolemPhysicsDocument.Build(normalized), Is.EqualTo(GolemPhysicsDocument.Build(source)));
            source.Assets[0].Indices[1] = 0;
            var changed = GolemPhysicsDocument.Normalize(source).Colliders[0].Metadata.Single(p => p.Key.EndsWith("originalGeometrySHA256", StringComparison.Ordinal)).Value;
            Assert.That(changed, Is.Not.EqualTo(hash));
        }

        [Test]
        public void ZeroAreaNormalizationRejectsEmptySurfaceAndConflictingProvenance()
        {
            var source = Fixture(); source.Bodies[0].Kind = "static"; source.Colliders[0].Shape = "mesh"; source.Colliders[0].AssetID = "mesh";
            source.Assets = new[] { new PhysicsAsset { ID = "mesh", Vertices = new[] { new PhysicsVector(), new PhysicsVector(1), new PhysicsVector(0, 0, 1) }, Indices = new uint[] { 0, 1, 1 } } };
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Normalize(source));
            source.Assets[0].Indices = new uint[] { 0, 1, 1, 0, 2, 1 };
            source.Colliders[0].Metadata = new[] { new PhysicsProperty { Key = GolemPhysicsDocument.NormalizationPrefix + "originalTriangleCount", Value = "wrong" } };
            Assert.Throws<InvalidOperationException>(() => GolemPhysicsDocument.Normalize(source));
        }

        [Test]
        public void PhysicsSourceTransactionPreservesOtherWorldAndRollsBackOnFailure()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-physics-transaction-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            try
            {
                var content = GolemPhysicsDocument.Build(Fixture());
                var first = Record("first", "physics/first.json", content);
                var second = Record("second", "physics/second.json", content);
                var committed = GolemScribeArtifacts.ReconcileKind(root, GolemScribeConstants.ArtifactKindPhysicsSource,
                    Array.Empty<GolemScribeArtifactRecord>(), new[] { first, second });
                Assert.That(committed.Errors, Is.Empty);
                var priorManifest = File.ReadAllText(GolemScribeManifest.ManifestPath(root));
                var changed = Fixture(); changed.Revision = 2;
                first = Record("first", "physics/first.json", GolemPhysicsDocument.Build(changed));
                GolemScribeArtifacts.AfterWriteHookForTests = _ => throw new IOException("injected failure");
                var failed = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindPhysicsSource,
                    "first", committed.ManifestRecords, new[] { first });
                Assert.That(failed.Errors, Is.Not.Empty); Assert.That(failed.AnyBytesChanged, Is.False);
                Assert.That(File.ReadAllText(Path.Combine(root, "physics/first.json")), Is.EqualTo(content));
                Assert.That(File.ReadAllText(Path.Combine(root, "physics/second.json")), Is.EqualTo(content));
                Assert.That(File.ReadAllText(GolemScribeManifest.ManifestPath(root)), Is.EqualTo(priorManifest));
                GolemScribeArtifacts.AfterWriteHookForTests = null;
                var success = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindPhysicsSource,
                    "first", committed.ManifestRecords, new[] { first });
                Assert.That(success.Errors, Is.Empty); Assert.That(success.ManifestRecords.Count, Is.EqualTo(2));
                Assert.That(File.ReadAllText(Path.Combine(root, "physics/second.json")), Is.EqualTo(content));
            }
            finally
            {
                GolemScribeArtifacts.AfterWriteHookForTests = null;
                Directory.Delete(root, true);
            }
        }

        private static GolemScribeArtifactRecord Record(string id, string path, string content) => new GolemScribeArtifactRecord
        { Kind = GolemScribeConstants.ArtifactKindPhysicsSource, SourceGuid = id, Entity = id, Path = path, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content) };

        private static string FixturePath([CallerFilePath] string sourceFile = "")
        {
            var copied = Path.Combine(TestContext.CurrentContext.TestDirectory, "physics-source.json");
            return File.Exists(copied) ? copied : Path.GetFullPath(Path.Combine(Path.GetDirectoryName(sourceFile), "../Fixtures/physics-source.json"));
        }
    }
}
