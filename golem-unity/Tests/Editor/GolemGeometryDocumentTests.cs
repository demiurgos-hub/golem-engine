using System;
using System.IO;
using System.Linq;
using System.Runtime.CompilerServices;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemGeometryDocumentTests
    {
        internal static GolemHeightfield Terrain() => new GolemHeightfield { Resolution = 3,
            SizeX = 4, SizeY = 8, SizeZ = 4, Samples = new[] { 0f, .1f, .2f, .3f, .4f, .5f, .6f, .7f, .8f }, Surface = new byte[] { 13 }, Area = 1 };
        internal static PhysicsAsset Mesh() => new PhysicsAsset { ID = "mesh", SourceID = "authored-mesh",
            Vertices = new[] { new PhysicsVector(), new PhysicsVector(1), new PhysicsVector(0, 0, 1) }, Indices = new uint[] { 0, 2, 1 } };

        [Test]
        public void BinaryOutputMatchesSharedGoFixturesWithoutPrecisionLoss()
        {
            var physics = GolemGeometryDocument.BuildPhysics(new[] { new PhysicsAsset { ID = "terrain", SourceID = "authored-terrain", Heightfield = Terrain() }, Mesh() });
            Assert.That(physics, Is.EqualTo(File.ReadAllBytes(FixturePath("geometry-physics.golemgeo"))));
            var navigation = GolemGeometryDocument.BuildNavigation(new[] { new NavigationAsset { ID = "terrain", Heightfield = Terrain() },
                new NavigationAsset { ID = "precise", Vertices = new[] { new NavigationVector(1.0000000000000002), new NavigationVector(2), new NavigationVector(2, 0, 1) },
                    Triangles = new[] { new NavigationTriangle { Indices = new uint[] { 0, 2, 1 }, Area = 63 } } } });
            Assert.That(navigation, Is.EqualTo(File.ReadAllBytes(FixturePath("geometry-navigation.golemgeo"))));
        }

        [Test]
        public void VersionTwoSourcesMatchSharedGoFixtures()
        {
            var physics = GolemPhysicsDocumentTests.Fixture(); physics.Version = 2; physics.Bodies[0].Kind = "static";
            var mesh = Mesh(); mesh.Indices = new uint[] { 0, 2, 1, 0, 1, 1 };
            physics.Assets = new[] { mesh, new PhysicsAsset { ID = "terrain", SourceID = "authored-terrain", Heightfield = Terrain() } };
            var first = physics.Colliders[0]; first.Shape = "mesh"; first.AssetID = "mesh";
            first.GeometryTransform = new[] { 2f, 0, 0, 0, 0, 3, 0, 0, 0, 0, 4, 0, 0, 0, 0, 1 };
            physics.Colliders = new[] { first, new PhysicsCollider { ID = "reflected", BodyID = first.BodyID, MaterialID = first.MaterialID,
                Shape = "mesh", AssetID = "mesh", GeometryTransform = new[] { -2f, 0, 0, 0, 0, 3, 0, 0, 0, 0, 4, 0, 0, 0, 0, 1 } },
                new PhysicsCollider { ID = "terrain", BodyID = first.BodyID, MaterialID = first.MaterialID, Shape = "terrain", AssetID = "terrain",
                    GeometryTransform = new[] { 1f, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1 } } };
            var bytes = GolemGeometryDocument.BuildPhysics(physics.Assets);
            physics.Geometry = new PhysicsGeometryReference { Path = "physics-source-v2.golemgeo", SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) };
            CompareFixture("physics-source-v2.golemgeo", bytes);
            CompareFixture("physics-source-v2.json", System.Text.Encoding.UTF8.GetBytes(GolemPhysicsDocument.Build(physics)));
            var navigation = GolemNavigationDocumentTests.Scene();
            navigation.Assets = new[] { new NavigationAsset { ID = "terrain", Heightfield = Terrain() } };
            navigation.Instances[0].AssetID = "terrain";
            bytes = GolemGeometryDocument.BuildNavigation(navigation.Assets);
            CompareFixture("navigation-source-v2.golemgeo", bytes);
            CompareFixture("navigation-source-v2.json", System.Text.Encoding.UTF8.GetBytes(GolemNavigationDocument.Build(navigation, GolemNavigationDocumentTests.Config(),
                new NavigationGeometryReference { Path = "navigation-source-v2.golemgeo", SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) })));
        }

        private static void CompareFixture(string name, byte[] bytes)
        {
            var output = Environment.GetEnvironmentVariable("GOLEM_GEOMETRY_FIXTURE_OUTPUT");
            if (!string.IsNullOrEmpty(output)) { Directory.CreateDirectory(output); File.WriteAllBytes(Path.Combine(output, name), bytes); return; }
            Assert.That(bytes, Is.EqualTo(File.ReadAllBytes(FixturePath(name))));
        }

        [Test]
        public void AffineNormalizationRoundsOnceAcrossManagedAndGoRuntimes()
        {
            var source = GolemPhysicsDocumentTests.Fixture(); source.Version = 2; source.Bodies[0].Kind = "static";
            var mesh = new PhysicsAsset { ID = "rounding", SourceID = "rounding-regression",
                Vertices = new[] { new PhysicsVector(1.7730124f, 5.692442f, .85120434f),
                    new PhysicsVector(1.5172065f, 5.651523f, .72839475f), new PhysicsVector(1.4783936f, 5.6366806f, .81535506f) },
                Indices = new uint[] { 0, 0, 1, 0, 1, 2 } };
            source.Assets = new[] { mesh };
            var collider = source.Colliders[0]; collider.Shape = "mesh"; collider.AssetID = mesh.ID;
            collider.GeometryTransform = new[] { 1f, 0, -5.5432547e-11f, -2.8654754e-6f, 0, 1, 0, 0,
                5.0236793e-9f, 0, 1, -3.493546e-6f, 0, 0, 0, 1 };
            var transformed = GolemPhysicsDocument.TransformPoint(collider.GeometryTransform, mesh.Vertices[0]);
            Assert.That(BitConverter.ToUInt32(BitConverter.GetBytes(transformed.Z), 0), Is.EqualTo(1062856781u));
            var bytes = GolemGeometryDocument.BuildPhysics(source.Assets);
            source.Geometry = new PhysicsGeometryReference { Path = "physics-rounding-source-v2.golemgeo", SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) };
            CompareFixture("physics-rounding-source-v2.golemgeo", bytes);
            var json = GolemPhysicsDocument.Build(source);
            const string hash = "7b13a1e2dd8345c0cbe6b92145f8cbfae6b2d11d8bf62d0ca8ab880cc33a9a42";
            Assert.That(json, Does.Contain(hash));
            // Mono and .NET print different equivalent round-trip decimals;
            // compare canonical binary/normalized bits, not that JSON spelling.
            if (!string.IsNullOrEmpty(Environment.GetEnvironmentVariable("GOLEM_GEOMETRY_FIXTURE_OUTPUT")))
                CompareFixture("physics-rounding-source-v2.json", System.Text.Encoding.UTF8.GetBytes(json));
            else Assert.That(File.ReadAllText(FixturePath("physics-rounding-source-v2.json")), Does.Contain(hash));
        }

        [Test]
        public void TerrainSizeDependsOnSamplesRatherThanExpandedTriangles()
        {
            var terrain = Terrain(); terrain.Resolution = 513; terrain.Samples = new float[513 * 513]; terrain.Surface = Enumerable.Repeat((byte)255, 512 * 512 / 8).ToArray();
            var bytes = GolemGeometryDocument.BuildPhysics(new[] { new PhysicsAsset { ID = "terrain", SourceID = "terrain", Heightfield = terrain } });
            Assert.That(bytes.Length, Is.LessThan(1100000));
            terrain.Surface[0] = 254;
            Assert.That(GolemGeometryDocument.BuildPhysics(new[] { new PhysicsAsset { ID = "terrain", Heightfield = terrain } }), Is.Not.EqualTo(bytes));
            terrain.Samples[0] = float.NaN;
            Assert.Throws<InvalidOperationException>(() => GolemGeometryDocument.BuildPhysics(new[] { new PhysicsAsset { ID = "terrain", Heightfield = terrain } }));
        }

        [Test]
        public void SharedGeometryRemainsCanonicalAcrossReflectedScaledPlacements()
        {
            var source = GolemPhysicsDocumentTests.Fixture(); source.Version = 2; source.Bodies[0].Kind = "static";
            var mesh = Mesh(); mesh.Indices = new uint[] { 0, 2, 1, 0, 1, 1 }; source.Assets = new[] { mesh };
            var first = source.Colliders[0]; first.Shape = "mesh"; first.AssetID = "mesh";
            first.GeometryTransform = new[] { 2f, 0, 0, 0, 0, 3, 0, 0, 0, 0, 4, 0, 0, 0, 0, 1 };
            source.Colliders = new[] { first, new PhysicsCollider { ID = "reflected", BodyID = first.BodyID, MaterialID = first.MaterialID,
                Shape = "mesh", AssetID = "mesh", GeometryTransform = new[] { -2f, 0, 0, 0, 0, 3, 0, 0, 0, 0, 4, 0, 0, 0, 0, 1 } } };
            var bytes = GolemGeometryDocument.BuildPhysics(source.Assets);
            source.Geometry = new PhysicsGeometryReference { Path = "shared.golemgeo", SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) };
            var normalized = GolemPhysicsDocument.Normalize(source);
            Assert.That(normalized.Assets.Length, Is.EqualTo(1)); Assert.That(mesh.Vertices[1].X, Is.EqualTo(1)); Assert.That(mesh.Indices.Length, Is.EqualTo(6));
            Assert.That(normalized.Colliders.All(c => c.Metadata.Single(m => m.Key.EndsWith("removedTriangleOrdinals", StringComparison.Ordinal)).Value == "1"), Is.True);
            Assert.That(normalized.Colliders[0].Metadata.Single(m => m.Key.EndsWith("originalGeometrySHA256", StringComparison.Ordinal)).Value,
                Is.Not.EqualTo(normalized.Colliders[1].Metadata.Single(m => m.Key.EndsWith("originalGeometrySHA256", StringComparison.Ordinal)).Value));
            var json = GolemPhysicsDocument.Build(source);
            Assert.That(json, Does.Contain("\"Assets\": []")); Assert.That(json, Does.Not.Contain("\"Vertices\""));
            Assert.That(GolemPhysicsDocument.Build(normalized), Is.EqualTo(json));
        }

        [TestCase("world.golemgeo")]
        [TestCase("world.json")]
        [TestCase("scribe.golem.yaml")]
        public void BinaryAndDescriptorRollbackTogether(string failurePath)
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-compact-transaction-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(root);
            try
            {
                var source = GolemPhysicsDocumentTests.Fixture(); source.Version = 2;
                var bytes = GolemGeometryDocument.BuildPhysics(new[] { Mesh() });
                source.Geometry = new PhysicsGeometryReference { Path = "world.golemgeo", SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) };
                var records = new[] { new GolemScribeArtifactRecord { Kind = "physics_source", SourceGuid = "world", Entity = "world", Path = "world.json", PendingContent = GolemPhysicsDocument.Build(source) },
                    new GolemScribeArtifactRecord { Kind = "physics_source", SourceGuid = "world", Entity = "world", Path = "world.golemgeo", PendingBytes = bytes } };
                var first = GolemScribeArtifacts.ReconcileSource(root, "physics_source", "world", Array.Empty<GolemScribeArtifactRecord>(), records);
                Assert.That(first.Errors, Is.Empty);
                var before = Directory.GetFiles(root).ToDictionary(Path.GetFileName, File.ReadAllBytes);
                var second = GolemScribeArtifacts.ReconcileSource(root, "physics_source", "world", first.ManifestRecords, records);
                Assert.That(second.Errors, Is.Empty); Assert.That(second.AnyBytesChanged, Is.False);
                var changed = Mesh(); changed.Vertices[1].X = 2; records[1].PendingBytes = GolemGeometryDocument.BuildPhysics(new[] { changed });
                source.Geometry.SHA256 = GolemScribeManifest.ComputeBytesHash(records[1].PendingBytes); source.Revision++;
                records[0].PendingContent = GolemPhysicsDocument.Build(source);
                GolemScribeFileTransaction.MutationHookForTests = (stage, path) => { if (stage == ScribeMutationStage.AfterWrite && Path.GetFileName(path) == failurePath) throw new IOException("injected compact failure"); };
                var failed = GolemScribeArtifacts.ReconcileSource(root, "physics_source", "world", first.ManifestRecords, records);
                Assert.That(failed.Errors, Has.Some.Contain("injected compact failure")); Assert.That(failed.AnyBytesChanged, Is.False);
                foreach (var entry in before) Assert.That(File.ReadAllBytes(Path.Combine(root, entry.Key)), Is.EqualTo(entry.Value));
                GolemScribeFileTransaction.MutationHookForTests = null;
                File.WriteAllBytes(Path.Combine(root, "world.golemgeo"), new byte[] { 1, 2, 3 });
                Assert.That(GolemScribeArtifacts.ReconcileSource(root, "physics_source", "world", first.ManifestRecords, records).Errors, Is.Not.Empty);
            }
            finally { GolemScribeFileTransaction.MutationHookForTests = null; Directory.Delete(root, true); }
        }

        private static string FixturePath(string name, [CallerFilePath] string sourceFile = "")
        {
            var copied = Path.Combine(TestContext.CurrentContext.TestDirectory, name);
            return File.Exists(copied) ? copied : Path.GetFullPath(Path.Combine(Path.GetDirectoryName(sourceFile), "../Fixtures/" + name));
        }
    }
}
