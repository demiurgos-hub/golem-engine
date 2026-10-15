using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemScribeArtifactPlanTests
    {
        private const string Kind = GolemScribeConstants.ArtifactKindEntitySchema;
        private readonly string _root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "scribe-plan-nonexistent-" + Guid.NewGuid().ToString("N")));

        [TestCase(ScribeArtifactStatus.Missing, false, false, true, false, false, GolemScribeArtifactPlan.Operation.Write)]
        [TestCase(ScribeArtifactStatus.Handwritten, false, false, false, true, false, GolemScribeArtifactPlan.Operation.Blocked)]
        [TestCase(ScribeArtifactStatus.Owned, true, false, false, false, true, GolemScribeArtifactPlan.Operation.Write)]
        [TestCase(ScribeArtifactStatus.Owned, false, false, false, true, false, GolemScribeArtifactPlan.Operation.Write)]
        [TestCase(ScribeArtifactStatus.Owned, true, true, false, false, false, GolemScribeArtifactPlan.Operation.None)]
        [TestCase(ScribeArtifactStatus.Owned, false, true, false, true, false, GolemScribeArtifactPlan.Operation.None)]
        public void DesiredClassificationSeparatesDriftFromWriterPolicy(ScribeArtifactStatus status, bool hashMatches,
            bool contentMatches, bool missing, bool manual, bool stale, GolemScribeArtifactPlan.Operation operation)
        {
            var disk = GolemYamlWriter.BuildDocument(new[] { "entity: Old" });
            var desired = Record(contentMatches ? disk : GolemYamlWriter.BuildDocument(new[] { "entity: New" }));
            var prior = Record(disk);
            if (!hashMatches) prior.Hash = "changed";
            var prepared = GolemScribeArtifactPlan.Prepare(_root, new[] { Kind }, new[] { prior }, new[] { desired });
            var plan = GolemScribeArtifactPlan.Build(prepared, new Dictionary<string, ScribeArtifactSnapshot>
            {
                ["a.yaml"] = new ScribeArtifactSnapshot("a.yaml", Path.Combine(_root, "a.yaml"), status,
                    status == ScribeArtifactStatus.Missing ? null : disk)
            });
            var entry = plan.Entries.Single();
            Assert.That(entry.Missing, Is.EqualTo(missing));
            Assert.That(entry.ManuallyModified, Is.EqualTo(manual));
            Assert.That(entry.Stale, Is.EqualTo(stale));
            Assert.That(entry.ProposedOperation, Is.EqualTo(operation));
            Assert.That(Directory.Exists(_root), Is.False, "Pure planning must not need or create a root directory.");
        }

        [Test]
        public void PreparationCopiesRecordsAndRejectsDuplicateNormalizedPaths()
        {
            var original = Record("content");
            original.Path = "./dir//a.yaml";
            var duplicate = Record("other");
            duplicate.Path = "dir\\a.yaml";
            var prepared = GolemScribeArtifactPlan.Prepare(_root, new[] { Kind }, null, new[] { original, duplicate });
            original.Path = "changed.yaml";
            original.PendingContent = "changed";
            Assert.That(prepared.Desired.Single().Path, Is.EqualTo("dir/a.yaml"));
            Assert.That(prepared.Desired.Single().Content, Is.EqualTo("content"));
            Assert.That(prepared.Issues.Single().WriterError, Does.Contain("Duplicate"));
        }

        [TestCase("../a.yaml")]
        [TestCase("dir/../a.yaml")]
        [TestCase("/a.yaml")]
        [TestCase("C:\\a.yaml")]
        [TestCase("C:a.yaml")]
        [TestCase(".")]
        [TestCase("")]
        public void LexicalContainmentRejectsUnsafeOrEmptyPaths(string path)
        {
            Assert.That(GolemScribePaths.TryResolveRelative(_root, path, out _, out _, out _), Is.False);
        }

        [Test]
        public void RelativeJoinRetainsItsSeparateNonValidatingContract()
        {
            Assert.That(GolemScribePaths.CombineRelative("/schemas\\entities/", "a.yaml"), Is.EqualTo("schemas/entities/a.yaml"));
            Assert.That(GolemScribePaths.CombineRelative(null, "a.yaml"), Is.EqualTo("a.yaml"));
            Assert.That(GolemScribePaths.CombineRelative("../schemas", "a.yaml"), Is.EqualTo("../schemas/a.yaml"));
        }

        [TestCase(ScribeArtifactStatus.Missing, true, false, GolemScribeArtifactPlan.Operation.None)]
        [TestCase(ScribeArtifactStatus.Handwritten, false, false, GolemScribeArtifactPlan.Operation.None)]
        [TestCase(ScribeArtifactStatus.Owned, false, true, GolemScribeArtifactPlan.Operation.Delete)]
        public void PriorOnlyClassification(ScribeArtifactStatus status, bool missing, bool orphan, GolemScribeArtifactPlan.Operation operation)
        {
            var prior = Record("old");
            var prepared = GolemScribeArtifactPlan.Prepare(_root, new[] { Kind }, new[] { prior }, null);
            var entry = GolemScribeArtifactPlan.Build(prepared, new Dictionary<string, ScribeArtifactSnapshot>
            {
                ["a.yaml"] = new ScribeArtifactSnapshot("a.yaml", "unused", status, "modified")
            }).Entries.Single();
            Assert.That(entry.Missing, Is.EqualTo(missing));
            Assert.That(entry.Orphaned, Is.EqualTo(orphan));
            Assert.That(entry.ManuallyModified, Is.False);
            Assert.That(entry.ProposedOperation, Is.EqualTo(operation));
        }

        [Test]
        public void MatchingUnregisteredContentNeedsOnlyManifestRegistration()
        {
            var record = Record("content");
            var prepared = GolemScribeArtifactPlan.Prepare(_root, new[] { Kind }, null, new[] { record });
            var entry = GolemScribeArtifactPlan.Build(prepared, new Dictionary<string, ScribeArtifactSnapshot>
            {
                ["a.yaml"] = new ScribeArtifactSnapshot("a.yaml", "unused", ScribeArtifactStatus.Owned, "content")
            }).Entries.Single();
            Assert.That(entry.MissingManifest, Is.True);
            Assert.That(entry.Stale, Is.True);
            Assert.That(entry.ProposedOperation, Is.EqualTo(GolemScribeArtifactPlan.Operation.None));
        }

        [TestCase(null)]
        [TestCase("")]
        public void AbsentPriorHashDoesNotClassifyGeneratedContentAsManual(string hash)
        {
            var prior = Record("old");
            prior.Hash = hash;
            var prepared = GolemScribeArtifactPlan.Prepare(_root, new[] { Kind }, new[] { prior }, new[] { Record("new") });
            var entry = GolemScribeArtifactPlan.Build(prepared, new Dictionary<string, ScribeArtifactSnapshot>
            {
                ["a.yaml"] = new ScribeArtifactSnapshot("a.yaml", "unused", ScribeArtifactStatus.Owned, "old")
            }).Entries.Single();
            Assert.That(entry.ManuallyModified, Is.False);
            Assert.That(entry.Stale, Is.True);
            Assert.That(entry.ProposedOperation, Is.EqualTo(GolemScribeArtifactPlan.Operation.Write));
        }

        private static GolemScribeArtifactRecord Record(string content) => new GolemScribeArtifactRecord
        {
            Kind = Kind, SourceGuid = "source", Entity = "A", Path = "a.yaml", PendingContent = content,
            Hash = GolemScribeManifest.ComputeContentHash(content)
        };
    }
}
