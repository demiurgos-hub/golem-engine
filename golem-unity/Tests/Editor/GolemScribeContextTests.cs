using System;
using System.IO;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeContextTests
    {
        private string _root;
        [SetUp] public void SetUp()
        {
            _root = Path.Combine(Path.GetTempPath(), "scribe-context-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
        }
        [TearDown] public void TearDown() => Directory.Delete(_root, true);

        [Test]
        public void SetupUsesRootThenConfigurationThenManifestAndDoesNotWrite()
        {
            Assert.That(GolemScribeContext.TryLoad(_root + "/absent", out _, out var error), Is.False);
            Assert.That(error, Does.Contain("root does not exist"));
            Assert.That(GolemScribeContext.TryLoad(_root, out _, out error), Is.False);
            Assert.That(error, Does.Contain("golem.yaml"));
            var config = Path.Combine(_root, "golem.yaml");
            File.WriteAllText(config, "simulation:\n  dimensions: 3\n");
            Assert.That(GolemScribeContext.TryLoad(_root, out var context, out error), Is.True, error);
            Assert.That(context.Schema.Dimensions, Is.EqualTo(3));
            Assert.That(context.Schema.EntitySchema, Is.EqualTo("schemas/entities/"));
            Assert.That(context.Previous, Is.Empty);
            Assert.That(Directory.GetFiles(_root), Has.Length.EqualTo(1));
            File.WriteAllText(GolemScribeManifest.ManifestPath(_root), "handwritten manifest");
            Assert.That(GolemScribeContext.TryLoad(_root, out _, out error), Is.False);
            Assert.That(error, Does.Contain("not Scribe-owned"));
            Assert.That(error, Does.Contain("scribe.golem.yaml"));
        }

        [TestCase("golem.yaml")]
        [TestCase(GolemScribeConstants.ManifestFileName)]
        public void ReadFailuresCarryInputPath(string file)
        {
            File.WriteAllText(Path.Combine(_root, "golem.yaml"), "simulation:\n  dimensions: 2\n");
            if (file != "golem.yaml") File.WriteAllText(Path.Combine(_root, file), GolemYamlWriter.BuildDocument(new[] { "artifacts: []" }));
            using (var locked = new FileStream(Path.Combine(_root, file), FileMode.Open, FileAccess.ReadWrite, FileShare.None))
            {
                Assert.That(GolemScribeContext.TryLoad(_root, out _, out var error), Is.False);
                Assert.That(error, Does.Contain(file));
                Assert.That(error, Does.Contain("Failed to load"));
            }
        }
    }
}
