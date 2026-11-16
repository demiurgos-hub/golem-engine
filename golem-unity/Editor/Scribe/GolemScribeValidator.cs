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
            GolemScribeConstants.ArtifactKindObjectTemplates,
            GolemScribeConstants.ArtifactKindNavigationSource,
            GolemScribeConstants.ArtifactKindPhysicsSource,
            GolemScribeConstants.ArtifactKindContentSource
        };

        // Explicitly exported sources are validated by their own exporters, and programmatic template catalogs
        // only by their source; this prefab scan must not classify them as orphaned.
        private static readonly HashSet<string> SourceKinds = new HashSet<string>(StringComparer.Ordinal)
        {
            GolemScribeConstants.ArtifactKindObjectTemplates,
            GolemScribeConstants.ArtifactKindNavigationSource,
            GolemScribeConstants.ArtifactKindPhysicsSource,
            GolemScribeConstants.ArtifactKindContentSource
        };
        private static readonly string[] AllKinds = KnownKinds.Where(k => !SourceKinds.Contains(k)).ToArray();

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
            if (!GolemScribeContext.TryLoad(projectRoot, out var context, out var contextError))
            {
                result.Errors.Add(contextError);
                return result;
            }
            projectRoot = context.ProjectRoot;
            var schema = context.Schema;
            var previous = context.Previous;

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

            CompareDesiredToDisk(projectRoot, previous, desired, AllKinds, result);

            var templateErrors = new List<string>();
            GolemObjectTemplateExporter.CollectMarked(schema.Dimensions, GolemUnityEditorSettings.instance.ObjectTemplatesPath,
                projectRoot, out var templateRecords, out _, templateErrors);
            result.Errors.AddRange(templateErrors);
            if (templateErrors.Count == 0)
                CompareDesiredToDisk(projectRoot, previous, templateRecords, new[] { GolemScribeConstants.ArtifactKindObjectTemplates },
                    result, GolemScribeConstants.ObjectTemplateSourceGuid);
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
            ValidationResult result,
            string sourceGuid = null)
        {
            if (result == null)
            {
                throw new ArgumentNullException(nameof(result));
            }

            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                result.Errors.Add($"Golem project root does not exist: {projectRoot}");
                return;
            }

            var prepared = GolemScribeArtifactPlan.Prepare(Path.GetFullPath(projectRoot), kinds, previous, desired, sourceGuid);
            result.Errors.AddRange(prepared.Issues.Select(i => i.ValidationError).Where(e => e != null));
            var plan = GolemScribeArtifactPlan.Build(prepared, GolemScribeArtifactReader.Capture(prepared));
            foreach (var entry in plan.Entries)
            {
                if (entry.HasReadError)
                {
                    result.Errors.Add(entry.Disk.Error);
                    continue;
                }
                if (entry.Missing) result.Missing.Add(entry.Path);
                if (entry.ManuallyModified) result.ManuallyModified.Add(entry.Path);
                if (entry.Stale) result.Stale.Add(entry.Path + (entry.MissingManifest ? " (missing from manifest)" : string.Empty));
                if (entry.Orphaned) result.Orphaned.Add(entry.Path);
                if (entry.Disk.Status == ScribeArtifactStatus.Handwritten)
                {
                    if (entry.Desired != null)
                        result.Errors.Add($"Path '{entry.Path}' is occupied by a handwritten file; Scribe will not overwrite it.");
                    else
                        result.Warnings.Add($"Left handwritten file '{entry.Path}' in place; not reported as an orphan.");
                }
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

        /// <summary>Validates manifest structure, known kinds, and path containment. Exposed for tests.</summary>
        internal static void ValidateManifestRecords(
            string projectRoot,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            ValidationResult result)
        {
            var manifestPath = GolemScribeManifest.ManifestPath(projectRoot);
            var manifest = GolemScribeArtifactReader.ReadAbsolute(manifestPath, GolemScribeConstants.ManifestFileName);
            if (manifest.Status == ScribeArtifactStatus.ReadFailure)
            {
                result.Errors.Add(manifest.Error);
                return;
            }
            if (manifest.Status == ScribeArtifactStatus.Handwritten)
            {
                result.Errors.Add($"Refusing to trust {GolemScribeConstants.ManifestFileName}: file exists but is not Scribe-owned.");
                return;
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
                if (SourceKinds.Contains(record.Kind))
                {
                    var snapshot = GolemScribeArtifactReader.Read(projectRoot, normalized);
                    if (snapshot.Status == ScribeArtifactStatus.Missing) result.Missing.Add(normalized);
                    else if (snapshot.Status == ScribeArtifactStatus.ReadFailure || snapshot.Status == ScribeArtifactStatus.InvalidPath)
                        result.Errors.Add(snapshot.Error);
                    else if (!(record.Kind == GolemScribeConstants.ArtifactKindContentSource ? GolemContentDocument.IsOwned(snapshot.Content)
                        : snapshot.Bytes != null ? GolemGeometryDocument.IsOwned(snapshot.Bytes)
                        : record.Kind == GolemScribeConstants.ArtifactKindObjectTemplates ? GolemObjectTemplateDocument.IsOwned(snapshot.Content)
                        : record.Kind == GolemScribeConstants.ArtifactKindNavigationSource ? GolemNavigationDocument.IsOwned(snapshot.Content)
                        : GolemPhysicsDocument.IsOwned(snapshot.Content)) || !string.Equals(snapshot.Hash, record.Hash, StringComparison.Ordinal))
                        result.ManuallyModified.Add(normalized);
                }
            }
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
