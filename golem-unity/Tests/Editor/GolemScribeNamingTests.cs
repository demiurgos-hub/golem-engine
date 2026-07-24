using GolemEngine.Unity.Editor;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeNamingTests
    {
        [Test]
        public void ToSnakeCase_MatchesBakeNaming()
        {
            Assert.That(GolemScribeNaming.ToSnakeCase("Player"), Is.EqualTo("player"));
            Assert.That(GolemScribeNaming.ToSnakeCase("displayName"), Is.EqualTo("display_name"));
            Assert.That(GolemScribeNaming.ToSnakeCase("HP"), Is.EqualTo("h_p"));
        }

        [Test]
        public void IsPascalCaseIdentifier_RejectsInvalidNames()
        {
            Assert.That(GolemScribeNaming.IsPascalCaseIdentifier("Player"), Is.True);
            Assert.That(GolemScribeNaming.IsPascalCaseIdentifier("player"), Is.False);
            Assert.That(GolemScribeNaming.IsPascalCaseIdentifier("Player-1"), Is.False);
            Assert.That(GolemScribeNaming.IsPascalCaseIdentifier(""), Is.False);
        }

        [Test]
        public void EntitySchemaFileName_UsesSnakeCaseYaml()
        {
            Assert.That(GolemScribeNaming.EntitySchemaFileName("Player"), Is.EqualTo("player.yaml"));
        }

        [Test]
        public void CatalogFileNames_UseSnakeCaseYamlAndGolemYaml()
        {
            Assert.That(GolemScribeNaming.CatalogSchemaFileName("MonsterDefinition"),
                Is.EqualTo("monster_definition.yaml"));
            Assert.That(GolemScribeNaming.CatalogDataFileName("MonsterDefinition"),
                Is.EqualTo("monster_definition.golem.yaml"));
        }
    }
}
