using System.IO;
using System.Text.RegularExpressions;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Reads Unity editor-facing values from golem.yaml.</summary>
    public static class GolemYamlConfig
    {
        public static bool TryGetCSharpClientAssetOut(string projectRoot, out string assetPath)
        {
            assetPath = string.Empty;
            if (!TryGetIntegrationOut(projectRoot, "csharp-client", out var rawPath))
            {
                return false;
            }

            assetPath = GolemUnityEditorSettings.ToUnityAssetPath(rawPath);
            return !string.IsNullOrEmpty(assetPath);
        }

        public static bool TryGetIntegrationOut(string projectRoot, string integrationName, out string rawPath)
        {
            rawPath = string.Empty;
            var configPath = Path.Combine(projectRoot, "golem.yaml");
            if (!File.Exists(configPath))
            {
                return false;
            }

            var lines = File.ReadAllLines(configPath);
            var inIntegrations = false;
            var inIntegration = false;
            var integrationsIndent = -1;
            var integrationIndent = -1;

            foreach (var line in lines)
            {
                var trimmed = line.Trim();
                if (trimmed.Length == 0 || trimmed.StartsWith("#", System.StringComparison.Ordinal))
                {
                    continue;
                }

                var indent = line.Length - line.TrimStart().Length;
                if (trimmed == "integrations:")
                {
                    inIntegrations = true;
                    integrationsIndent = indent;
                    continue;
                }

                if (inIntegrations && indent <= integrationsIndent)
                {
                    inIntegrations = false;
                    inIntegration = false;
                }

                if (!inIntegrations)
                {
                    continue;
                }

                if (Regex.IsMatch(trimmed, "^" + Regex.Escape(integrationName) + "\\s*:"))
                {
                    inIntegration = true;
                    integrationIndent = indent;
                    continue;
                }

                if (inIntegration && indent <= integrationIndent)
                {
                    inIntegration = false;
                    continue;
                }

                if (inIntegration && trimmed.StartsWith("out:", System.StringComparison.Ordinal))
                {
                    rawPath = trimmed.Substring("out:".Length).Trim().Trim('"', '\'');
                    return rawPath.Length > 0;
                }
            }

            return false;
        }
    }
}
