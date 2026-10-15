using System;
using System.Collections.Generic;
using System.IO;

namespace GolemEngine.Unity.Editor
{
    internal sealed class GolemScribeContext
    {
        internal string ProjectRoot { get; }
        internal GolemYamlConfig.ProjectSchemaConfig Schema { get; }
        internal IReadOnlyList<GolemScribeArtifactRecord> Previous { get; }

        private GolemScribeContext(string root, GolemYamlConfig.ProjectSchemaConfig schema,
            List<GolemScribeArtifactRecord> previous)
        {
            ProjectRoot = root;
            Schema = schema;
            Previous = previous.AsReadOnly();
        }

        internal static bool TryLoad(string projectRoot, out GolemScribeContext context, out string error)
        {
            context = null;
            error = null;
            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                error = $"Golem project root does not exist: {projectRoot}";
                return false;
            }

            var source = Path.Combine(projectRoot, "golem.yaml");
            try
            {
                if (!GolemYamlConfig.TryGetProjectSchema(projectRoot, out var schema, out error))
                {
                    return false;
                }

                source = GolemScribeManifest.ManifestPath(projectRoot);
                var previous = GolemScribeManifest.Load(projectRoot);
                context = new GolemScribeContext(Path.GetFullPath(projectRoot), schema, previous);
                return true;
            }
            catch (Exception ex)
            {
                error = $"Failed to load Scribe input '{source}': {ex.Message}";
                return false;
            }
        }
    }
}
