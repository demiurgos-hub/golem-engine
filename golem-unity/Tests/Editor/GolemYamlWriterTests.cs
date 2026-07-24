using GolemEngine.Unity.Editor;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemYamlWriterTests
    {
        [Test]
        public void FormatScalar_QuotesAndEscapesControlCharacters()
        {
            Assert.That(GolemYamlWriter.FormatScalar("line\nbreak"), Is.EqualTo("\"line\\nbreak\""));
            Assert.That(GolemYamlWriter.FormatScalar("ret\rurn"), Is.EqualTo("\"ret\\rurn\""));
            Assert.That(GolemYamlWriter.FormatScalar("tab\there"), Is.EqualTo("\"tab\\there\""));
            Assert.That(GolemYamlWriter.FormatScalar("bell\a"), Is.EqualTo("\"bell\\x07\""));
            Assert.That(GolemYamlWriter.FormatScalar("mix\n\r\t\x01"), Is.EqualTo("\"mix\\n\\r\\t\\x01\""));
        }

        [Test]
        public void FormatScalar_ControlCharactersRoundTripThroughParse()
        {
            var values = new[]
            {
                "line\nbreak",
                "ret\rurn",
                "tab\there",
                "bell\a",
                "quote\"slash\\",
                "mix\n\r\t\x01"
            };

            foreach (var original in values)
            {
                var emitted = GolemYamlWriter.FormatScalar(original);
                Assert.That(emitted, Does.StartWith("\""), original);
                Assert.That(GolemYamlWriter.TryParseScalarToken(emitted, out var parsed), Is.True, original);
                Assert.That(parsed, Is.EqualTo(original), original);
            }
        }

        [Test]
        public void FormatScalar_PlainScalarsRemainUnquoted()
        {
            Assert.That(GolemYamlWriter.FormatScalar("goblin"), Is.EqualTo("goblin"));
            Assert.That(GolemYamlWriter.FormatScalar("has space"), Is.EqualTo("\"has space\""));
        }
    }
}
