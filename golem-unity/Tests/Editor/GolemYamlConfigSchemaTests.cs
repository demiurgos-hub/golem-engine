using System.IO;
using GolemEngine.Unity.Editor;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemYamlConfigSchemaTests
    {
        [Test]
        public void TryGetProjectSchema_AppliesDefaultsAndDimensions()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-yaml-config-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                File.WriteAllText(Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 3\nproto:\n  out: x.proto\n");
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out var config, out var error), Is.True);
                Assert.That(error, Is.Null);
                Assert.That(config.Dimensions, Is.EqualTo(3));
                Assert.That(config.EntitySchema, Is.EqualTo("schemas/entities/"));
                Assert.That(config.TypesSchema, Is.EqualTo("schemas/types/"));
                Assert.That(config.WorldSchema, Is.EqualTo("schemas/world/"));
            }
            finally
            {
                Directory.Delete(root, true);
            }
        }

        [Test]
        public void TryGetProjectSchema_ReadsExplicitSchemaPaths()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-yaml-config-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                File.WriteAllText(
                    Path.Combine(root, "golem.yaml"),
                    "entity_schema: custom/entities/\ntypes_schema: custom/types/\nworld_schema: custom/world/\nsimulation:\n  dimensions: 2\n");
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out var config, out _), Is.True);
                Assert.That(config.EntitySchema, Is.EqualTo("custom/entities/"));
                Assert.That(config.TypesSchema, Is.EqualTo("custom/types/"));
                Assert.That(config.WorldSchema, Is.EqualTo("custom/world/"));
                Assert.That(config.Dimensions, Is.EqualTo(2));
            }
            finally
            {
                Directory.Delete(root, true);
            }
        }

        [Test]
        public void TryGetProjectSchema_RejectsInvalidAndExplicitZeroDimensions()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-yaml-config-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                File.WriteAllText(Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 4\n");
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out _, out var error), Is.False);
                Assert.That(error, Does.Contain("dimensions"));

                File.WriteAllText(Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 0\n");
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out _, out error), Is.False);
                Assert.That(error, Does.Contain("0"));
            }
            finally
            {
                Directory.Delete(root, true);
            }
        }

        [Test]
        public void TryGetProjectSchema_ParsesQuotedScalarsAndIgnoresNestedKeys()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-yaml-config-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                File.WriteAllText(
                    Path.Combine(root, "golem.yaml"),
                    "entity_schema: \"custom/entities/\" # trail\n" +
                    "nested:\n" +
                    "  simulation:\n" +
                    "    dimensions: 3\n" +
                    "simulation:\n" +
                    "  dimensions: \"2\" # comment\n" +
                    "  nested:\n" +
                    "    dimensions: 3\n");
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out var config, out _), Is.True);
                Assert.That(config.EntitySchema, Is.EqualTo("custom/entities/"));
                Assert.That(config.Dimensions, Is.EqualTo(2));
            }
            finally
            {
                Directory.Delete(root, true);
            }
        }

        [Test]
        public void TryGetProjectSchema_MissingConfigFails()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-yaml-config-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            try
            {
                Assert.That(GolemYamlConfig.TryGetProjectSchema(root, out _, out var error), Is.False);
                Assert.That(error, Does.Contain("golem.yaml"));
            }
            finally
            {
                Directory.Delete(root, true);
            }
        }

        [Test]
        public void TryParseScalarToken_OnlyStripsValidUnquotedComments()
        {
            Assert.That(GolemYamlWriter.TryParseScalarToken("schemas/foo#bar", out var embedded), Is.True);
            Assert.That(embedded, Is.EqualTo("schemas/foo#bar"));

            Assert.That(GolemYamlWriter.TryParseScalarToken("schemas/foo # trailing", out var commented), Is.True);
            Assert.That(commented, Is.EqualTo("schemas/foo"));

            Assert.That(GolemYamlWriter.TryParseScalarToken("\"#not-a-comment\" # trail", out var quoted), Is.True);
            Assert.That(quoted, Is.EqualTo("#not-a-comment"));
        }
    }
}
