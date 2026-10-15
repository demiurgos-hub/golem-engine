using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemBakeCommandTests
    {
        [TestCase(null)]
        [TestCase("")]
        [TestCase(" \t ")]
        [TestCase("\"unterminated")]
        [TestCase("\"\" args")]
        public void RejectsMissingExecutable(string command)
        {
            Assert.That(GolemBakeCommand.TryParse(command, out _, out _), Is.False);
        }

        [TestCase("  go run ./cmd  ", "go", "run ./cmd")]
        [TestCase("Tools/bake.exe\t--flag \"two words\"", "Tools/bake.exe", "--flag \"two words\"")]
        [TestCase("\"C:\\Some Tools\\bake.exe\" --flag", "C:\\Some Tools\\bake.exe", "--flag")]
        [TestCase("\"bake\"suffix", "bake", "suffix")]
        [TestCase("'bake file' arg", "'bake", "file' arg")]
        [TestCase("bake", "bake", "")]
        public void PreservesExecutableAndArgumentGrammar(string command, string executable, string arguments)
        {
            Assert.That(GolemBakeCommand.TryParse(command, out var actualExecutable, out var actualArguments), Is.True);
            Assert.That(actualExecutable, Is.EqualTo(executable));
            Assert.That(actualArguments, Is.EqualTo(arguments));
        }
    }
}
