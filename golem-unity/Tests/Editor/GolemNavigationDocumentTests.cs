using System;
using System.Globalization;
using System.IO;
using System.Linq;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemNavigationDocumentTests
    {
        internal static NavigationScene Scene() => new NavigationScene
        {
            Assets = new[] { new NavigationAsset
            {
                ID = "asset:shared-ground", Vertices = new[] { new NavigationVector(0, 0, 0), new NavigationVector(0, 0, 8), new NavigationVector(8, 0, 0), new NavigationVector(8, 0, 8) },
                Triangles = new[] { new NavigationTriangle { Indices = new uint[] { 0, 1, 2 }, Area = 1 }, new NavigationTriangle { Indices = new uint[] { 2, 1, 3 }, Area = 1 } }
            } },
            Instances = new[] { new NavigationInstance { ID = "object:ground", AssetID = "asset:shared-ground", Revision = 1, Transform = new double[] { 1, 0, 0, -4, 0, 1, 0, 0, 0, 0, 1, -4, 0, 0, 0, 1 } } }
        };

        internal static NavigationConfig Config() => new NavigationConfig
        {
            WorldID = "scribe-fixture", Revision = 1,
            Profile = new NavigationProfile { ID = "human", Radius = 0.4, Height = 1.8, MaxSlope = 45, MaxClimb = 0.4 },
            Settings = new NavigationSettings
            {
                Origin = new NavigationVector(), Bounds = new NavigationBounds { Min = new NavigationVector(-4, -1, -4), Max = new NavigationVector(4, 3, 4) },
                CellSize = 0.2, CellHeight = 0.1, TileSize = 32, Partition = "layers", MergeRegionArea = 400, MaxEdgeLength = 60,
                MaxSimplificationError = 1.3, DetailSampleDistance = 1.2, DetailSampleMaxError = 0.1, BuildLayers = true
            }
        };

        [Test]
        public void JsonIsDeterministicInvariantAndRecognizablyOwned()
        {
            var original = CultureInfo.CurrentCulture;
            try
            {
                CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("nl-NL");
                var text = GolemNavigationDocument.Build(Scene(), Config());
                Assert.That(text, Does.StartWith(GolemNavigationDocument.OwnershipPrefix));
                Assert.That(text, Does.Contain("\"CellSize\":0.2"));
                Assert.That(text, Does.Contain("\"Indices\":[0,1,2]"));
                Assert.That(text, Does.Contain("\"Transform\":[1,0,0,-4,0,1,0,0,0,0,1,-4,0,0,0,1]"));
                Assert.That(GolemYamlWriter.IsScribeOwned(text), Is.True);
                Assert.That(GolemYamlWriter.IsScribeOwned("{\"note\":\"" + GolemNavigationDocument.Generator + "\"}"), Is.False);
                Assert.That(GolemYamlWriter.IsScribeOwned(text.Replace("source v1", "source v2")), Is.False);
                CultureInfo.CurrentCulture = CultureInfo.InvariantCulture;
                Assert.That(GolemNavigationDocument.Build(Scene(), Config()), Is.EqualTo(text));
                // Optional cross-language harness input; no ordinary test requires Go or Unity.
                var fixture = Environment.GetEnvironmentVariable("GOLEM_NAVIGATION_EXPORT_FIXTURE");
                if (!string.IsNullOrEmpty(fixture)) File.WriteAllText(fixture, text, new System.Text.UTF8Encoding(false));
            }
            finally { CultureInfo.CurrentCulture = original; }
        }

        [Test]
        public void ReflectedInstancesAndTraversalMetadataRemainExplicit()
        {
            var scene = Scene(); scene.Instances[0].Transform[0] = -1;
            scene.Links = new[] { new NavigationLink { ID = "door", Revision = 2, Start = new NavigationVector(), End = new NavigationVector(0, 0, 2), Radius = 0.3, Flags = 2, Area = 4, Action = "open\n\"door\"", Profiles = new[] { "large", "human" }, Disabled = true } };
            var text = GolemNavigationDocument.Build(scene, Config());
            Assert.That(text, Does.Contain("\"Transform\":[-1,0,0,-4"));
            Assert.That(text, Does.Contain("\"Indices\":[0,1,2]")); // The Go resolver flips once for reflection.
            Assert.That(text, Does.Contain("\"Profiles\":[\"human\",\"large\"]"));
            Assert.That(text, Does.Contain("open\\u000a\\\"door\\\""));
            Assert.That(scene.Links[0].Profiles, Is.EqualTo(new[] { "large", "human" }));
        }

#if !UNITY_5_3_OR_NEWER
        [Test]
        public void GoInteropFixtureIsExactCurrentEmitterOutput()
        {
            // The Go adapter's decoder/baker test consumes this same ordinary-Git fixture.
            // Keep the producer comparison live so changes cannot silently leave a stale fixture.
            var fixture = Path.Combine(TestContext.CurrentContext.TestDirectory, "scribe-source.json");
            Assert.That(File.ReadAllText(fixture), Is.EqualTo(GolemNavigationDocument.Build(Scene(), Config())));
        }
#endif

        [Test]
        public void OrderingDoesNotMutateTheSnapshot()
        {
            var scene = Scene(); var first = scene.Instances[0]; first.ID = "z";
            var second = new NavigationInstance { ID = "a", AssetID = first.AssetID, Revision = 1, Transform = (double[])first.Transform.Clone() };
            scene.Instances = new[] { first, second };
            var text = GolemNavigationDocument.Build(scene, Config());
            Assert.That(text.IndexOf("\"ID\":\"a\"", StringComparison.Ordinal), Is.LessThan(text.IndexOf("\"ID\":\"z\"", StringComparison.Ordinal)));
            Assert.That(scene.Instances[0], Is.SameAs(first));
        }

        [Test]
        public void InvalidSourcesFailBeforeAnyOutputIsProduced()
        {
            var scene = Scene(); scene.Assets[0].Triangles[0].Indices[2] = 99;
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(scene, Config()));
            scene = Scene(); scene.Instances[0].Transform[15] = 0;
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(scene, Config()));
            scene = Scene(); scene.Assets[0].Vertices[0].X = double.NaN;
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(scene, Config()));
            scene = Scene(); scene.Assets[0].Triangles[0].Indices = new uint[] { 0, 0, 1 };
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(scene, Config()));
            scene.Instances[0].Disabled = true;
            Assert.DoesNotThrow(() => GolemNavigationDocument.Build(scene, Config()));
            scene = Scene(); scene.Instances = new[] { scene.Instances[0], scene.Instances[0] };
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(scene, Config()));
            var config = Config(); config.Settings.BuildLayers = true; config.Settings.TileSize = 256;
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(Scene(), config));
            config = Config(); config.AreaFlags = new[] { 1 };
            Assert.Throws<InvalidOperationException>(() => GolemNavigationDocument.Build(Scene(), config));
        }

        [Test]
        public void ScopedTransactionsPreserveOtherSourcesAndRollbackJson()
        {
            var root = Path.Combine(Path.GetTempPath(), "scribe-navigation-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(root);
            try
            {
                var content = GolemNavigationDocument.Build(Scene(), Config());
                GolemScribeArtifactRecord Record(string id, string path) => new GolemScribeArtifactRecord
                { Kind = GolemScribeConstants.ArtifactKindNavigationSource, SourceGuid = id, Path = path, PendingContent = content };
                var seed = GolemScribeArtifacts.ReconcileKind(root, GolemScribeConstants.ArtifactKindNavigationSource, null, new[] { Record("a", "a.json"), Record("b", "b.json") });
                Assert.That(seed.Errors, Is.Empty);
                var before = Directory.GetFiles(root).ToDictionary(Path.GetFileName, File.ReadAllText);
                var conflict = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "a", seed.ManifestRecords, new[] { Record("a", "b.json") });
                Assert.That(conflict.Errors, Has.Some.Contain("preserved Scribe source"));
                var caseConflict = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "b", seed.ManifestRecords, new[] { Record("b", "A.json") });
                Assert.That(caseConflict.Errors, Has.Some.Contain("preserved Scribe source"));
                var caseRename = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "a", seed.ManifestRecords, new[] { Record("a", "A.json") });
                Assert.That(caseRename.Errors, Has.Some.Contain("Case-only"));
                Assert.That(Directory.GetFiles(root).ToDictionary(Path.GetFileName, File.ReadAllText), Is.EquivalentTo(before));
                GolemScribeArtifacts.AfterWriteHookForTests = _ => throw new IOException("injected navigation export failure");
                var failed = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "a", seed.ManifestRecords, new[] { Record("a", "renamed.json") });
                Assert.That(failed.Errors, Has.Some.Contain("injected navigation"));
                Assert.That(Directory.GetFiles(root).ToDictionary(Path.GetFileName, File.ReadAllText), Is.EquivalentTo(before));
                GolemScribeArtifacts.AfterWriteHookForTests = null;
                var changed = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "a", seed.ManifestRecords, new[] { Record("a", "renamed.json") });
                Assert.That(changed.Errors, Is.Empty); Assert.That(File.Exists(Path.Combine(root, "a.json")), Is.False);
                Assert.That(File.ReadAllText(Path.Combine(root, "b.json")), Is.EqualTo(before["b.json"]));
                Assert.That(changed.ManifestRecords.Select(r => r.SourceGuid), Is.EquivalentTo(new[] { "a", "b" }));
                File.WriteAllText(Path.Combine(root, "renamed.json"), "{\"handwritten\":true}");
                var protectedResult = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindNavigationSource, "a", changed.ManifestRecords, new[] { Record("a", "renamed.json") });
                Assert.That(protectedResult.Errors, Has.Some.Contain("handwritten"));
            }
            finally { GolemScribeArtifacts.AfterWriteHookForTests = null; Directory.Delete(root, true); }
        }
    }
}
