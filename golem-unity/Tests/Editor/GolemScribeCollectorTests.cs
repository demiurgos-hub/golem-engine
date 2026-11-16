using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeCollectorTests
    {
        private string _root;
        private readonly List<UnityEngine.Object> _objects = new List<UnityEngine.Object>();

        [GolemCatalog("Id")]
        private sealed class InvalidCatalog : ScriptableObject
        {
            [GolemField(1)] public string Id;
            [GolemField(2)] public int[] Unsupported;
        }
        [GolemCatalog("Id")]
        private sealed class PeerCatalog : ScriptableObject { [GolemField(1)] public string Id; }
        [GolemCatalog("Id")]
        private sealed class URLThing : ScriptableObject { [GolemField(1)] public string Id; }
        [GolemCatalog("Id")]
        private sealed class UrlThing : ScriptableObject { [GolemField(1)] public string Id; }
        [GolemCatalog("Id")]
        private sealed class RowCatalog : ScriptableObject { [GolemField(1)] public string Id; }

        [SetUp]
        public void SetUp()
        {
            _root = Path.Combine(Path.GetTempPath(), "scribe-collectors-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
        }

        [TearDown]
        public void TearDown()
        {
            foreach (var obj in _objects) if (obj != null) UnityEngine.Object.DestroyImmediate(obj);
            _objects.Clear();
            Directory.Delete(_root, true);
        }

        [TestCase("owned", 1)]
        [TestCase("modified", 1)]
        [TestCase("handwritten", 0)]
        [TestCase("missing", 0)]
        public void InvalidCatalogPreservesOnlyExistingOwnedArtifactsAndValidPeersContinue(string occupant, int preservedCount)
        {
            var old = PriorCatalog("InvalidCatalog", "invalid.yaml");
            var gone = PriorCatalog("Gone", "gone.yaml");
            var original = old.PendingContent;
            if (occupant == "missing") File.Delete(Path.Combine(_root, old.Path));
            if (occupant == "handwritten") File.WriteAllText(Path.Combine(_root, old.Path), "handwritten");
            if (occupant == "modified") File.AppendAllText(Path.Combine(_root, old.Path), "# edited\n");
            GolemCatalogExporter.CollectCatalogs(new GolemYamlConfig.ProjectSchemaConfig(), new[] { old, gone }, _root,
                new[] { typeof(InvalidCatalog), typeof(PeerCatalog) }, _ => Array.Empty<ScriptableObject>(),
                out var desired, out _, out var errors);
            Assert.That(errors, Has.Some.Contain("unsupported type"));
            Assert.That(desired.Count(r => r.Entity == "InvalidCatalog"), Is.EqualTo(preservedCount));
            Assert.That(desired.Count(r => r.Entity == "PeerCatalog"), Is.EqualTo(3));
            Assert.That(desired.Where(r => r.Entity == "PeerCatalog").Select(r => r.SourceGuid).Distinct().Count(), Is.EqualTo(1));
            if (preservedCount > 0)
            {
                var preserved = desired.Single(r => r.Entity == "InvalidCatalog");
                Assert.That(preserved.PendingContent, Is.EqualTo(original + (occupant == "modified" ? "# edited\n" : "")));
                Assert.That(preserved.Hash, Is.EqualTo(GolemScribeManifest.ComputeContentHash(preserved.PendingContent)));
            }
            var reconcile = GolemScribeArtifacts.ReconcileKinds(_root, CatalogKinds, new[] { old, gone }, desired);
            Assert.That(reconcile.Errors, Is.Empty);
            Assert.That(File.Exists(Path.Combine(_root, gone.Path)), Is.False);
            if (occupant == "handwritten") Assert.That(File.ReadAllText(Path.Combine(_root, old.Path)), Is.EqualTo("handwritten"));
        }

        [Test]
        public void ProductionCatalogPathSelfCollisionExcludesEachBeforeLoadingAssets()
        {
            var schema = new GolemYamlConfig.ProjectSchemaConfig { TypesSchema = "schemas", WorldSchema = "./schemas/" };
            GolemCatalogExporter.CollectCatalogs(schema, null, _root,
                new[] { typeof(URLThing), typeof(UrlThing), typeof(PeerCatalog) }, _ => Array.Empty<ScriptableObject>(),
                out var desired, out _, out var errors);
            Assert.That(errors.Count(e => e.Contains("collide with each other")), Is.EqualTo(3));
            Assert.That(desired, Is.Empty);
        }

        [Test]
        public void RowFailureAndSelfCollisionUseProductionPreservation()
        {
            var old = PriorCatalog("RowCatalog", "old-row.yaml");
            var row = ScriptableObject.CreateInstance<RowCatalog>();
            _objects.Add(row);
            GolemCatalogExporter.CollectCatalogs(new GolemYamlConfig.ProjectSchemaConfig(), new[] { old }, _root,
                new[] { typeof(RowCatalog) }, _ => new[] { row }, out var desired, out _, out var errors);
            Assert.That(errors, Has.Some.Contain("missing or empty"));
            Assert.That(desired.Single().Path, Is.EqualTo(old.Path));

            var schema = new GolemYamlConfig.ProjectSchemaConfig { TypesSchema = "same", WorldSchema = "./same//" };
            GolemCatalogExporter.CollectCatalogs(schema, new[] { old }, _root, new[] { typeof(RowCatalog) },
                _ => throw new AssertionException("Rows must not load before output claims succeed."),
                out desired, out _, out errors);
            Assert.That(errors, Has.Some.Contain("collide with each other"));
            Assert.That(desired.Single().Path, Is.EqualTo(old.Path));
        }

        private GolemScribeArtifactRecord PriorCatalog(string type, string path)
        {
            var content = GolemYamlWriter.BuildDocument(new[] { "type: " + type, "fields: {}" });
            File.WriteAllText(Path.Combine(_root, path), content);
            return new GolemScribeArtifactRecord { Kind = GolemScribeConstants.ArtifactKindTypeSchema,
                Entity = type, SourceGuid = "prior", Path = path, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content) };
        }

        private static readonly string[] CatalogKinds = { GolemScribeConstants.ArtifactKindTypeSchema,
            GolemScribeConstants.ArtifactKindWorldSchema, GolemScribeConstants.ArtifactKindCatalogData };
    }
}
