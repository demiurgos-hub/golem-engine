namespace GolemEngine.Unity.Editor
{
    internal static class GolemBakeCommand
    {
        internal static bool TryParse(string command, out string fileName, out string arguments)
        {
            fileName = string.Empty;
            arguments = string.Empty;
            command = command?.Trim();
            if (string.IsNullOrEmpty(command))
            {
                return false;
            }

            if (command[0] == '"')
            {
                var closingQuote = command.IndexOf('"', 1);
                if (closingQuote < 0)
                {
                    return false;
                }
                fileName = command.Substring(1, closingQuote - 1);
                arguments = command.Substring(closingQuote + 1).TrimStart();
                return !string.IsNullOrEmpty(fileName);
            }

            var firstSpace = command.IndexOfAny(new[] { ' ', '\t' });
            if (firstSpace < 0)
            {
                fileName = command;
                return true;
            }

            fileName = command.Substring(0, firstSpace);
            arguments = command.Substring(firstSpace + 1).TrimStart();
            return !string.IsNullOrEmpty(fileName);
        }
    }
}
