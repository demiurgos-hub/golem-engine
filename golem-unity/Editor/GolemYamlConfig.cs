using System.Globalization;
using System.IO;
using System.Text.RegularExpressions;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Reads Unity editor-facing values from golem.yaml.</summary>
    public static class GolemYamlConfig
    {
        /// <summary>Schema directories and simulation dimensions from golem.yaml with Go loader defaults.</summary>
        public sealed class ProjectSchemaConfig
        {
            public string EntitySchema = "schemas/entities/";
            public string TypesSchema = "schemas/types/";
            public string WorldSchema = "schemas/world/";
            public int Dimensions = 2;
        }

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

        /// <summary>
        /// Reads entity_schema, types_schema, world_schema, and simulation.dimensions from golem.yaml.
        /// Paths are relative to <paramref name="projectRoot"/> (GolemUnityEditorSettings.ProjectRoot).
        /// </summary>
        public static bool TryGetProjectSchema(string projectRoot, out ProjectSchemaConfig config, out string error)
        {
            config = new ProjectSchemaConfig();
            error = null;

            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                error = $"Golem project root does not exist: {projectRoot}";
                return false;
            }

            var configPath = Path.Combine(projectRoot, "golem.yaml");
            if (!File.Exists(configPath))
            {
                error = $"Could not find golem.yaml at {configPath}.";
                return false;
            }

            var lines = File.ReadAllLines(configPath);
            var inSimulation = false;
            var simulationIndent = -1;
            var simulationChildIndent = -1;
            var dimensionsExplicit = false;

            foreach (var line in lines)
            {
                var trimmed = line.Trim();
                if (trimmed.Length == 0 || trimmed.StartsWith("#", System.StringComparison.Ordinal))
                {
                    continue;
                }

                var indent = line.Length - line.TrimStart().Length;

                if (inSimulation && indent <= simulationIndent)
                {
                    inSimulation = false;
                    simulationChildIndent = -1;
                }

                if (!inSimulation && indent == 0 && trimmed == "simulation:")
                {
                    inSimulation = true;
                    simulationIndent = 0;
                    simulationChildIndent = -1;
                    continue;
                }

                if (inSimulation)
                {
                    if (simulationChildIndent < 0)
                    {
                        simulationChildIndent = indent;
                    }

                    if (indent == simulationChildIndent &&
                        TryReadIntValue(trimmed, "dimensions", out var dimensions))
                    {
                        config.Dimensions = dimensions;
                        dimensionsExplicit = true;
                    }
                    continue;
                }

                if (indent != 0)
                {
                    continue;
                }

                if (TryReadStringValue(trimmed, "entity_schema", out var entitySchema))
                {
                    config.EntitySchema = entitySchema;
                }
                else if (TryReadStringValue(trimmed, "types_schema", out var typesSchema))
                {
                    config.TypesSchema = typesSchema;
                }
                else if (TryReadStringValue(trimmed, "world_schema", out var worldSchema))
                {
                    config.WorldSchema = worldSchema;
                }
            }

            if (string.IsNullOrWhiteSpace(config.EntitySchema))
            {
                config.EntitySchema = "schemas/entities/";
            }
            if (string.IsNullOrWhiteSpace(config.TypesSchema))
            {
                config.TypesSchema = "schemas/types/";
            }
            if (string.IsNullOrWhiteSpace(config.WorldSchema))
            {
                config.WorldSchema = "schemas/world/";
            }

            if (!dimensionsExplicit)
            {
                config.Dimensions = 2;
            }
            else if (config.Dimensions != 2 && config.Dimensions != 3)
            {
                error = $"simulation.dimensions must be 2 or 3, got {config.Dimensions}.";
                return false;
            }

            return true;
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
                    if (!GolemYamlWriter.TryParseScalarToken(trimmed.Substring("out:".Length), out rawPath))
                    {
                        return false;
                    }
                    return rawPath.Length > 0;
                }
            }

            return false;
        }

        private static bool TryReadStringValue(string trimmed, string key, out string value)
        {
            value = null;
            var prefix = key + ":";
            if (!trimmed.StartsWith(prefix, System.StringComparison.Ordinal))
            {
                return false;
            }

            return GolemYamlWriter.TryParseScalarToken(trimmed.Substring(prefix.Length), out value) &&
                   value.Length > 0;
        }

        private static bool TryReadIntValue(string trimmed, string key, out int value)
        {
            value = 0;
            var prefix = key + ":";
            if (!trimmed.StartsWith(prefix, System.StringComparison.Ordinal))
            {
                return false;
            }

            if (!GolemYamlWriter.TryParseScalarToken(trimmed.Substring(prefix.Length), out var raw))
            {
                return false;
            }

            return int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out value);
        }
    }
}
