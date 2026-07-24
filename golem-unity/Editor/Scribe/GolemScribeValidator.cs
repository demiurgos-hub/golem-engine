using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Side-effect-free Scribe validation: compares desired exporter output to committed
    /// artifacts and the managed-artifact manifest without writing files or mutating the registry.
    /// </summary>
    public static class GolemScribeValidator
    {
        /// <summary>Known Scribe artifact kinds accepted in <c>scribe.golem.yaml</c>.</summary>
        internal static readonly HashSet<string> KnownKinds = new HashSet<string>(StringComparer.Ordinal)
        {
            GolemScribeConstants.ArtifactKindEntitySchema,
            GolemScribeConstants.ArtifactKindTypeSchema,
            GolemScribeConstants.ArtifactKindWorldSchema,
            GolemScribeConstants.ArtifactKindCatalogData,
            GolemScribeConstants.ArtifactKindFootprint
        };

        private static readonly string[] AllKinds = KnownKinds.ToArray();

        /// <summary>Outcome of a dry-run Scribe validation.</summary>
        public sealed class ValidationResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public readonly List<string> Missing = new List<string>();
            public readonly List<string> Stale = new List<string>();
            public readonly List<string> Orphaned = new List<string>();
            public readonly List<string> ManuallyModified = new List<string>();

            /// <summary>True when exporters reported validation errors (names, aliases, GUIDs, geometry, etc.).</summary>
            public bool HasExporterErrors => Errors.Count > 0;

            /// <summary>True when committed artifacts diverge from desired output (missing/stale/orphaned/manual).</summary>
            public bool HasDrift =>
                Missing.Count > 0 ||
                Stale.Count > 0 ||
                Orphaned.Count > 0 ||
                ManuallyModified.Count > 0;

            /// <summary>True when validation found no exporter errors and no artifact drift.</summary>
            public bool IsClean => !HasExporterErrors && !HasDrift;

            /// <summary>True when CI / Validate should treat the project as failed.</summary>
            public bool HasFailures => !IsClean;
        }

        /// <summary>
        /// Dry-run validation against <see cref="GolemUnityEditorSettings.ProjectRoot"/>.
        /// Never writes artifacts, never updates the prefab registry, and never invokes bake.
        /// </summary>
        public static ValidationResult Validate()
        {
            return Validate(GolemUnityEditorSettings.instance.ProjectRoot);
        }

        /// <summary>
        /// Dry-run validation for <paramref name="projectRoot"/>.
        /// Never writes artifacts, never updates the prefab registry, and never invokes bake.
        /// </summary>
        public static ValidationResult Validate(string projectRoot)
        {
            var result = new ValidationResult();
            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                result.Errors.Add($"Golem project root does not exist: {projectRoot}");
                return result;
            }

            if (!GolemYamlConfig.TryGetProjectSchema(projectRoot, out var schema, out var schemaError))
            {
                result.Errors.Add(schemaError);
                return result;
            }

            List<GolemScribeArtifactRecord> previous;
            try
            {
                previous = GolemScribeManifest.Load(projectRoot);
            }
            catch (Exception ex)
            {
                result.Errors.Add(ex.Message);
                return result;
            }

            ValidateManifestRecords(projectRoot, previous, result);

            var desired = new List<GolemScribeArtifactRecord>();
            Dictionary<string, GameObject> registryMap;

            GolemEntityExporter.CollectEntities(schema, out var entityRecords, out registryMap, out var entityErrors);
            result.Errors.AddRange(entityErrors);
            desired.AddRange(entityRecords);

            GolemCatalogExporter.CollectCatalogs(
                schema, previous, projectRoot, out var catalogRecords, out _, out var catalogErrors);
            result.Errors.AddRange(catalogErrors);
            desired.AddRange(catalogRecords);

            var footprintsPath = GolemUnityEditorSettings.instance.FootprintsPath;
            GolemFootprintExporter.CollectFootprints(
                schema.Dimensions,
                footprintsPath,
                previous,
                projectRoot,
                out var footprintRecords,
                out _,
                out var footprintErrors);
            result.Errors.AddRange(footprintErrors);
            desired.AddRange(footprintRecords);

            CompareDesiredToDisk(projectRoot, previous, desired, AllKinds, result);
            ValidateFootprintDocument(projectRoot, schema.Dimensions, footprintsPath, result);
            ValidatePrefabRegistryParity(registryMap, result);

            return result;
        }

        /// <summary>
        /// Compares desired artifact content to disk and the prior manifest without mutating anything.
        /// Exposed for focused editor tests.
        /// </summary>
        internal static void CompareDesiredToDisk(
            string projectRoot,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            IReadOnlyList<GolemScribeArtifactRecord> desired,
            IReadOnlyCollection<string> kinds,
            ValidationResult result)
        {
            if (result == null)
            {
                throw new ArgumentNullException(nameof(result));
            }

            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();
            desired = desired ?? Array.Empty<GolemScribeArtifactRecord>();
            var kindSet = new HashSet<string>(
                (kinds ?? Array.Empty<string>()).Where(k => !string.IsNullOrEmpty(k)),
                StringComparer.Ordinal);

            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                result.Errors.Add($"Golem project root does not exist: {projectRoot}");
                return;
            }

            var previousByPath = new Dictionary<string, GolemScribeArtifactRecord>(StringComparer.Ordinal);
            foreach (var prior in previous)
            {
                if (prior == null || string.IsNullOrEmpty(prior.Path) ||
                    string.IsNullOrEmpty(prior.Kind) || !kindSet.Contains(prior.Kind))
                {
                    continue;
                }

                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, prior.Path, out _, out var normalized, out var pathError))
                {
                    result.Errors.Add("Manifest ownership path rejected: " + pathError);
                    continue;
                }

                previousByPath[normalized] = prior;
            }

            var desiredByPath = new Dictionary<string, GolemScribeArtifactRecord>(StringComparer.Ordinal);
            foreach (var record in desired)
            {
                if (record == null || string.IsNullOrEmpty(record.Path))
                {
                    result.Errors.Add("Desired Scribe artifact is missing a relative path.");
                    continue;
                }

                if (string.IsNullOrEmpty(record.Kind) || !kindSet.Contains(record.Kind))
                {
                    result.Errors.Add(
                        $"Desired Scribe artifact '{record.Path}' has unexpected kind '{record.Kind}'.");
                    continue;
                }

                if (record.PendingContent == null)
                {
                    result.Errors.Add($"Desired Scribe artifact '{record.Path}' is missing generated content.");
                    continue;
                }

                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, record.Path, out var absolute, out var normalized, out var pathError))
                {
                    result.Errors.Add(pathError);
                    continue;
                }

                if (desiredByPath.ContainsKey(normalized))
                {
                    result.Errors.Add($"Duplicate desired Scribe output path '{normalized}'.");
                    continue;
                }

                desiredByPath[normalized] = record;
                ClassifyDesiredPath(normalized, absolute, record, previousByPath, result);
            }

            foreach (var prior in previousByPath.Values.OrderBy(r => r.Path, StringComparer.Ordinal))
            {
                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, prior.Path, out var absolute, out var normalized, out _))
                {
                    continue;
                }

                if (desiredByPath.ContainsKey(normalized))
                {
                    continue;
                }

                // Manifest still owns this path but it is not in the current desired set.
                if (!File.Exists(absolute))
                {
                    result.Missing.Add(normalized);
                    continue;
                }

                var existing = File.ReadAllText(absolute);
                if (!GolemYamlWriter.IsScribeOwned(existing))
                {
                    result.Warnings.Add(
                        $"Left handwritten file '{normalized}' in place; not reported as an orphan.");
                    continue;
                }

                result.Orphaned.Add(normalized);
            }
        }

        /// <summary>Logs a validation result via <see cref="Debug"/> (editor / batch logs).</summary>
        public static void LogResult(ValidationResult result)
        {
            if (result == null)
            {
                return;
            }

            foreach (var warning in result.Warnings)
            {
                Debug.LogWarning("Golem Scribe validate: " + warning);
            }

            foreach (var path in result.Missing)
            {
                Debug.LogError("Golem Scribe validate: missing artifact: " + path);
            }

            foreach (var path in result.Stale)
            {
                Debug.LogError("Golem Scribe validate: stale artifact: " + path);
            }

            foreach (var path in result.Orphaned)
            {
                Debug.LogError("Golem Scribe validate: orphaned artifact: " + path);
            }

            foreach (var path in result.ManuallyModified)
            {
                Debug.LogError("Golem Scribe validate: manually modified artifact: " + path);
            }

            foreach (var error in result.Errors)
            {
                Debug.LogError("Golem Scribe validate: " + error);
            }

            if (result.IsClean)
            {
                Debug.Log("Golem Scribe validation passed.");
            }
            else
            {
                Debug.Log(
                    $"Golem Scribe validation failed: {result.Errors.Count} error(s), " +
                    $"{result.Missing.Count} missing, {result.Stale.Count} stale, " +
                    $"{result.Orphaned.Count} orphaned, {result.ManuallyModified.Count} manually modified.");
            }
        }

        private static void ClassifyDesiredPath(
            string normalized,
            string absolute,
            GolemScribeArtifactRecord desired,
            IReadOnlyDictionary<string, GolemScribeArtifactRecord> previousByPath,
            ValidationResult result)
        {
            if (!File.Exists(absolute))
            {
                result.Missing.Add(normalized);
                return;
            }

            var existing = File.ReadAllText(absolute);
            if (!GolemYamlWriter.IsScribeOwned(existing))
            {
                result.ManuallyModified.Add(normalized);
                result.Errors.Add(
                    $"Path '{normalized}' is occupied by a handwritten file; Scribe will not overwrite it.");
                return;
            }

            var diskHash = GolemScribeManifest.ComputeContentHash(existing);
            previousByPath.TryGetValue(normalized, out var prior);

            if (prior != null &&
                !string.IsNullOrEmpty(prior.Hash) &&
                !string.Equals(prior.Hash, diskHash, StringComparison.Ordinal))
            {
                // Marker present but bytes no longer match the committed manifest hash.
                result.ManuallyModified.Add(normalized);
            }

            if (!string.Equals(existing, desired.PendingContent, StringComparison.Ordinal))
            {
                // Desired exporter output differs from disk — stale unless already a manual edit.
                if (!result.ManuallyModified.Contains(normalized))
                {
                    result.Stale.Add(normalized);
                }
            }
            else if (prior == null)
            {
                // Disk already matches desired but the path is absent from the manifest.
                result.Stale.Add(normalized + " (missing from manifest)");
            }
            // When disk matches desired, diskHash == desiredHash, so a prior.Hash mismatch
            // is already classified as ManuallyModified above — no separate manifest-hash stale case.
        }

        /// <summary>Validates manifest structure, known kinds, and path containment. Exposed for tests.</summary>
        internal static void ValidateManifestRecords(
            string projectRoot,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            ValidationResult result)
        {
            var manifestPath = GolemScribeManifest.ManifestPath(projectRoot);
            if (File.Exists(manifestPath))
            {
                var text = File.ReadAllText(manifestPath);
                if (!GolemYamlWriter.IsScribeOwned(text))
                {
                    result.Errors.Add(
                        $"Refusing to trust {GolemScribeConstants.ManifestFileName}: file exists but is not Scribe-owned.");
                    return;
                }
            }

            var seenPaths = new HashSet<string>(StringComparer.Ordinal);
            foreach (var record in previous ?? Array.Empty<GolemScribeArtifactRecord>())
            {
                if (record == null)
                {
                    result.Errors.Add("Manifest contains a null artifact record.");
                    continue;
                }

                if (string.IsNullOrEmpty(record.Kind))
                {
                    result.Errors.Add("Manifest artifact is missing kind.");
                    continue;
                }

                if (!KnownKinds.Contains(record.Kind))
                {
                    result.Errors.Add(
                        $"Manifest artifact '{record.Path}' has unknown kind '{record.Kind}'. " +
                        "Known kinds: " + string.Join(", ", KnownKinds.OrderBy(k => k, StringComparer.Ordinal)) + ".");
                    continue;
                }

                if (string.IsNullOrEmpty(record.SourceGuid))
                {
                    result.Errors.Add($"Manifest artifact '{record.Path}' is missing source_guid.");
                }

                if (string.IsNullOrEmpty(record.Path))
                {
                    result.Errors.Add("Manifest artifact is missing path.");
                    continue;
                }

                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, record.Path, out _, out var normalized, out var pathError))
                {
                    result.Errors.Add("Manifest ownership path rejected: " + pathError);
                    continue;
                }

                if (!seenPaths.Add(normalized))
                {
                    result.Errors.Add($"Manifest lists duplicate path '{normalized}'.");
                }
            }
        }

        /// <summary>Validates footprints document version/dimensions when the file exists. Exposed for tests.</summary>
        internal static void ValidateFootprintDocument(
            string projectRoot,
            int expectedDimensions,
            string footprintsRelativePath,
            ValidationResult result)
        {
            if (string.IsNullOrWhiteSpace(footprintsRelativePath))
            {
                return;
            }

            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, footprintsRelativePath, out var absolute, out var normalized, out var pathError))
            {
                result.Errors.Add("Footprints path rejected: " + pathError);
                return;
            }

            if (!File.Exists(absolute))
            {
                return;
            }

            var text = File.ReadAllText(absolute);
            if (!GolemYamlWriter.IsScribeOwned(text))
            {
                return;
            }

            if (!TryReadFootprintHeader(text, out var version, out var dimensions))
            {
                result.Errors.Add($"Footprints artifact '{normalized}' is not a recognizable versioned footprints document.");
                return;
            }

            if (version != GolemScribeConstants.FootprintFormatVersion)
            {
                result.Errors.Add(
                    $"Footprints artifact '{normalized}' has version {version}; expected {GolemScribeConstants.FootprintFormatVersion}.");
            }

            if (dimensions != expectedDimensions)
            {
                result.Errors.Add(
                    $"Footprints artifact '{normalized}' has dimensions {dimensions}; golem.yaml simulation.dimensions is {expectedDimensions}.");
            }
        }

        private static bool TryReadFootprintHeader(string text, out int version, out int dimensions)
        {
            version = 0;
            dimensions = 0;
            var sawVersion = false;
            var sawDimensions = false;
            using (var reader = new StringReader(text ?? string.Empty))
            {
                string line;
                while ((line = reader.ReadLine()) != null)
                {
                    var trimmed = line.Trim();
                    if (trimmed.Length == 0 || trimmed.StartsWith("#", StringComparison.Ordinal))
                    {
                        continue;
                    }

                    var indent = line.Length - line.TrimStart().Length;
                    if (indent != 0)
                    {
                        continue;
                    }

                    if (trimmed.StartsWith("version:", StringComparison.Ordinal))
                    {
                        if (int.TryParse(trimmed.Substring("version:".Length).Trim(), out version))
                        {
                            sawVersion = true;
                        }
                    }
                    else if (trimmed.StartsWith("dimensions:", StringComparison.Ordinal))
                    {
                        if (int.TryParse(trimmed.Substring("dimensions:".Length).Trim(), out dimensions))
                        {
                            sawDimensions = true;
                        }
                    }
                }
            }

            return sawVersion && sawDimensions;
        }

        /// <summary>
        /// One-way prefab registry parity: every Scribe-desired entity must map to the expected prefab.
        /// Handwritten / extra registry entries are allowed and are not reported as errors.
        /// </summary>
        internal static void ValidatePrefabRegistryParity(
            IReadOnlyDictionary<string, GameObject> desiredRegistry,
            ValidationResult result,
            GolemPrefabRegistry registryOverride = null)
        {
            desiredRegistry = desiredRegistry ?? new Dictionary<string, GameObject>(StringComparer.Ordinal);
            GolemPrefabRegistry registry = registryOverride;
            if (registry == null)
            {
                if (!GolemPrefabRegistryUtil.TryGetUniqueRegistry(out registry, out var registryError))
                {
                    if (desiredRegistry.Count > 0)
                    {
                        result.Errors.Add(registryError);
                    }
                    else
                    {
                        result.Warnings.Add(registryError);
                    }
                    return;
                }
            }

            foreach (var pair in desiredRegistry.OrderBy(p => p.Key, StringComparer.Ordinal))
            {
                var mapped = registry.GetPrefab(pair.Key);
                if (mapped == null)
                {
                    result.Errors.Add(
                        $"Prefab registry is missing entity '{pair.Key}' (expected prefab '{DescribePrefab(pair.Value)}').");
                    continue;
                }

                if (mapped != pair.Value)
                {
                    result.Errors.Add(
                        $"Prefab registry entity '{pair.Key}' maps to '{DescribePrefab(mapped)}' " +
                        $"but Scribe sources require '{DescribePrefab(pair.Value)}'.");
                }
            }
        }

        private static string DescribePrefab(GameObject prefab)
        {
            if (prefab == null)
            {
                return "(null)";
            }

            var path = AssetDatabase.GetAssetPath(prefab);
            return string.IsNullOrEmpty(path) ? prefab.name : path;
        }
    }
}
