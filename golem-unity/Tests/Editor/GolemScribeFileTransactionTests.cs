using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemScribeFileTransactionTests
    {
        private string _root;
        private const string Kind = GolemScribeConstants.ArtifactKindEntitySchema;

        [SetUp]
        public void SetUp()
        {
            _root = Path.Combine(Path.GetTempPath(), "scribe-transaction-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
        }

        [TearDown]
        public void TearDown()
        {
            GolemScribeFileTransaction.MutationHookForTests = null;
            GolemScribeArtifacts.AfterWriteHookForTests = null;
            Directory.Delete(_root, true);
        }

        [TestCase(ScribeMutationStage.BeforeReplace, "a.yaml")]
        [TestCase(ScribeMutationStage.AfterWrite, "b.yaml")]
        [TestCase(ScribeMutationStage.BeforeDelete, "old.yaml")]
        [TestCase(ScribeMutationStage.AfterDelete, "old.yaml")]
        [TestCase(ScribeMutationStage.BeforeReplace, GolemScribeConstants.ManifestFileName)]
        [TestCase(ScribeMutationStage.AfterWrite, GolemScribeConstants.ManifestFileName)]
        public void FailureRestoresOriginalBytesAndDeletesNewFiles(ScribeMutationStage stage, string fileName)
        {
            var previous = Seed();
            var before = Tree();
            GolemScribeFileTransaction.MutationHookForTests = (at, path) =>
            {
                if (at == stage && Path.GetFileName(path) == fileName) throw new IOException("injected failure");
            };
            var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, previous,
                new[] { Desired("a.yaml", "Changed"), Desired("b.yaml", "New") });
            Assert.That(result.Errors, Has.Some.Contain("injected failure"));
            Assert.That(result.Errors.Any(e => e.Contains("incomplete")), Is.False, string.Join("; ", result.Errors));
            Assert.That(result.AnyBytesChanged, Is.False);
            Assert.That(result.EntitySchemaBytesChanged, Is.False);
            Assert.That(result.ManifestRecords, Is.Empty);
            AssertTree(before);
        }

        [Test]
        public void RollbackReportsEveryFailedRestorationAndContinues()
        {
            var previous = Seed();
            var manifest = File.ReadAllBytes(GolemScribeManifest.ManifestPath(_root));
            GolemScribeFileTransaction.MutationHookForTests = (stage, path) =>
            {
                if (stage == ScribeMutationStage.AfterWrite && Path.GetFileName(path) == GolemScribeConstants.ManifestFileName)
                    throw new IOException("original failure");
                if (stage == ScribeMutationStage.BeforeRollback && Path.GetExtension(path) == ".yaml" &&
                    Path.GetFileName(path) != GolemScribeConstants.ManifestFileName)
                    throw new IOException("restoration failure");
            };
            var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, previous,
                new[] { Desired("a.yaml", "Changed"), Desired("b.yaml", "New") });
            Assert.That(result.Errors.First(), Does.Contain("original failure"));
            foreach (var name in new[] { "a.yaml", "b.yaml", "old.yaml" })
                Assert.That(result.Errors, Has.Some.Contains(name));
            Assert.That(result.Errors.Last(), Does.Contain("rollback is incomplete"));
            Assert.That(File.ReadAllBytes(GolemScribeManifest.ManifestPath(_root)), Is.EqualTo(manifest));
            Assert.That(result.AnyBytesChanged, Is.False);
            Assert.That(Directory.GetFiles(_root, "*.tmp", SearchOption.AllDirectories), Is.Empty);
        }

        [TestCase("b.yaml", ScribeMutationStage.BeforeReplace)]
        [TestCase("old.yaml", ScribeMutationStage.BeforeDelete)]
        [TestCase(GolemScribeConstants.ManifestFileName, ScribeMutationStage.BeforeReplace)]
        public void NewlyHandwrittenOccupantIsNotOverwrittenOrDeleted(string name, ScribeMutationStage stage)
        {
            var previous = Seed();
            var originalA = File.ReadAllBytes(Path.Combine(_root, "a.yaml"));
            GolemScribeFileTransaction.MutationHookForTests = (at, path) =>
            {
                if (at == stage && Path.GetFileName(path) == name) File.WriteAllText(path, "handwritten replacement");
            };
            var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, previous,
                new[] { Desired("a.yaml", "Changed"), Desired("b.yaml", "New") });
            Assert.That(result.Errors, Has.Some.Contain("handwritten"));
            Assert.That(File.ReadAllText(Path.Combine(_root, name)), Is.EqualTo("handwritten replacement"));
            Assert.That(File.ReadAllBytes(Path.Combine(_root, "a.yaml")), Is.EqualTo(originalA));
            Assert.That(Directory.GetFiles(_root, "*.tmp", SearchOption.AllDirectories), Is.Empty);
        }

        [Test]
        public void ReplacementNeverRemovesDestinationBeforeCommitAndNoOpRetainsTimestamp()
        {
            var record = Desired("a.yaml", "A");
            File.WriteAllText(Path.Combine(_root, "a.yaml"), record.PendingContent);
            var timestamp = File.GetLastWriteTimeUtc(Path.Combine(_root, "a.yaml"));
            Assert.That(GolemScribeArtifacts.WriteManagedFile(Path.Combine(_root, "a.yaml"), record.PendingContent), Is.False);
            Assert.That(File.GetLastWriteTimeUtc(Path.Combine(_root, "a.yaml")), Is.EqualTo(timestamp));
            GolemScribeFileTransaction.MutationHookForTests = (stage, path) =>
            {
                if (stage != ScribeMutationStage.BeforeReplace) return;
                Assert.That(File.ReadAllText(path), Is.EqualTo(record.PendingContent));
                Assert.That(Directory.GetFiles(_root, "*.tmp"), Has.Length.EqualTo(1));
                throw new IOException("replacement unavailable");
            };
            Assert.Throws<InvalidOperationException>(() => GolemScribeArtifacts.WriteManagedFile(
                Path.Combine(_root, "a.yaml"), Desired("a.yaml", "B").PendingContent));
            Assert.That(File.ReadAllText(Path.Combine(_root, "a.yaml")), Is.EqualTo(record.PendingContent));
            Assert.That(Directory.GetFiles(_root, "*.tmp"), Is.Empty);
        }

        [Test]
        public void ReaderDoesNotTreatUnreadableFileAsMissingOrPermitPartialWrites()
        {
            var blocked = Path.Combine(_root, "blocked.yaml");
            File.WriteAllText(blocked, GolemYamlWriter.BuildDocument(new[] { "entity: Blocked" }));
            using (var locked = new FileStream(blocked, FileMode.Open, FileAccess.ReadWrite, FileShare.None))
            {
                Assert.That(GolemScribeArtifactReader.Read(_root, "blocked.yaml").Status, Is.EqualTo(ScribeArtifactStatus.ReadFailure));
                var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, null,
                    new[] { Desired("a.yaml", "A"), Desired("blocked.yaml", "Blocked") });
                Assert.That(result.Errors, Has.Some.Contain("Failed to read"));
                Assert.That(File.Exists(Path.Combine(_root, "a.yaml")), Is.False);
            }
        }

        [TestCase(false)]
        [TestCase(true)]
        public void InvalidManifestPreflightDoesNotStartAnyMutation(bool locked)
        {
            var previous = Seed();
            var manifest = GolemScribeManifest.ManifestPath(_root);
            if (!locked) File.WriteAllText(manifest, "handwritten manifest");
            var before = Tree();
            var mutations = 0;
            GolemScribeFileTransaction.MutationHookForTests = (stage, path) => mutations++;
            using (var stream = locked ? new FileStream(manifest, FileMode.Open, FileAccess.ReadWrite, FileShare.None) : null)
            {
                var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, previous, new[] { Desired("a.yaml", "Changed") });
                Assert.That(result.Errors, Has.Some.Contain(locked ? "Failed to read" : "handwritten"));
                Assert.That(result.Errors, Has.Some.Contain(GolemScribeConstants.ManifestFileName));
                Assert.That(result.AnyBytesChanged, Is.False);
                Assert.That(mutations, Is.Zero);
            }
            AssertTree(before);
        }

        [Test]
        public void ReconcileRegistersMatchingContentAndPreservesUnrelatedUnknownKind()
        {
            var desired = Desired("a.yaml", "A");
            File.WriteAllText(Path.Combine(_root, "a.yaml"), desired.PendingContent);
            var timestamp = File.GetLastWriteTimeUtc(Path.Combine(_root, "a.yaml"));
            var unrelated = new GolemScribeArtifactRecord { Kind = "future_kind", Path = "future.yaml", SourceGuid = "shared", Hash = "old" };
            var result = GolemScribeArtifacts.ReconcileKind(_root, Kind, new[] { unrelated }, new[] { desired });
            Assert.That(result.Errors, Is.Empty);
            Assert.That(result.EntitySchemaBytesChanged, Is.False);
            Assert.That(result.AnyBytesChanged, Is.True);
            Assert.That(File.GetLastWriteTimeUtc(Path.Combine(_root, "a.yaml")), Is.EqualTo(timestamp));
            Assert.That(result.ManifestRecords.Single(r => r.Kind == "future_kind").Hash, Is.EqualTo("old"));
            Assert.That(GolemScribeArtifacts.ReconcileKind(_root, Kind, result.ManifestRecords, new[] { desired }).AnyBytesChanged, Is.False);
        }

        private List<GolemScribeArtifactRecord> Seed()
        {
            var previous = new List<GolemScribeArtifactRecord> { Desired("a.yaml", "A"), Desired("old.yaml", "Old") };
            foreach (var record in previous)
                File.WriteAllText(Path.Combine(_root, record.Path), record.PendingContent, Encoding.Unicode);
            GolemScribeManifest.Write(_root, previous);
            return previous;
        }

        private static GolemScribeArtifactRecord Desired(string path, string name)
        {
            var content = GolemYamlWriter.BuildDocument(new[] { "entity: " + name, "vars: {}" });
            return new GolemScribeArtifactRecord { Kind = Kind, SourceGuid = "source", Entity = name, Path = path,
                PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content) };
        }

        private Dictionary<string, byte[]> Tree() => Directory.GetFiles(_root, "*", SearchOption.AllDirectories)
            .ToDictionary(p => p, File.ReadAllBytes, StringComparer.Ordinal);

        private void AssertTree(Dictionary<string, byte[]> before)
        {
            var after = Tree();
            Assert.That(after.Keys, Is.EquivalentTo(before.Keys));
            foreach (var pair in before) Assert.That(after[pair.Key], Is.EqualTo(pair.Value), pair.Key);
        }
    }
}
