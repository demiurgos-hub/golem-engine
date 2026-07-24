using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Exports collision footprints from prefabs marked with exactly one root <see cref="GolemFootprint"/>.
    /// Emits one manifest-owned <c>footprints.golem.yaml</c>; footprint changes never trigger bake.
    /// </summary>
    public static class GolemFootprintExporter
    {
        /// <summary>Outcome of a full footprint-category reconcile.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool FootprintBytesChanged;
            public int FootprintCount;
        }

        /// <summary>
        /// Scans marked prefabs and reconciles the footprints artifact.
        /// Transient invalid marked prefabs preserve prior GUID entries when present;
        /// deleted/unmarked prefabs remove their entries.
        /// </summary>
        public static ExportResult ExportAll()
        {
            var result = new ExportResult();
            var settings = GolemUnityEditorSettings.instance;
            var projectRoot = settings.ProjectRoot;
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

            var relativePath = settings.FootprintsPath;
            CollectFootprints(
                schema.Dimensions,
                relativePath,
                previous,
                projectRoot,
                out var desiredRecords,
                out var footprintCount,
                out var collectErrors);
            result.Errors.AddRange(collectErrors);
            result.FootprintCount = footprintCount;

            var reconcile = GolemScribeArtifacts.ReconcileKind(
                projectRoot,
                GolemScribeConstants.ArtifactKindFootprint,
                previous,
                desiredRecords);
            result.Errors.AddRange(reconcile.Errors);
            result.Warnings.AddRange(reconcile.Warnings);
            if (reconcile.Errors.Count > 0)
            {
                return result;
            }

            result.FootprintBytesChanged = reconcile.FootprintBytesChanged;
            return result;
        }

        /// <summary>
        /// Collects footprint models from marked prefabs. Invalid marked prefabs contribute errors and
        /// re-emit prior GUID entries when the previous Scribe footprints file still contains them.
        /// </summary>
        internal static void CollectFootprints(
            int dimensions,
            string relativePath,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            out List<GolemScribeArtifactRecord> desiredRecords,
            out int footprintCount,
            out List<string> errors)
        {
            desiredRecords = new List<GolemScribeArtifactRecord>();
            footprintCount = 0;
            errors = new List<string>();
            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();

            if (string.IsNullOrWhiteSpace(relativePath))
            {
                errors.Add("Footprints path is empty.");
                return;
            }

            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, relativePath, out _, out var normalizedPath, out var pathError))
            {
                errors.Add(pathError);
                return;
            }

            var models = new List<GolemFootprintModel>();
            var aliasToGuid = new Dictionary<string, string>(StringComparer.Ordinal);
            var conflictedAliases = new HashSet<string>(StringComparer.Ordinal);
            var invalidGuids = new HashSet<string>(StringComparer.Ordinal);
            var seenGuids = new HashSet<string>(StringComparer.Ordinal);

            var prefabGuids = AssetDatabase.FindAssets("t:Prefab");
            foreach (var prefabGuid in prefabGuids.OrderBy(g => g, StringComparer.Ordinal))
            {
                var assetPath = AssetDatabase.GUIDToAssetPath(prefabGuid);
                if (string.IsNullOrEmpty(assetPath) ||
                    !assetPath.EndsWith(".prefab", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }

                var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(assetPath);
                if (prefab == null)
                {
                    continue;
                }

                if (!TryGetFootprintMarker(prefab, assetPath, out var marker, out var markerError))
                {
                    if (!string.IsNullOrEmpty(markerError))
                    {
                        errors.Add(markerError);
                        invalidGuids.Add(GolemFootprintYamlBuilder.NormalizeGuid(prefabGuid));
                    }

                    continue;
                }

                if (marker == null)
                {
                    continue;
                }

                var guid = GolemFootprintYamlBuilder.NormalizeGuid(prefabGuid);
                if (!seenGuids.Add(guid))
                {
                    continue;
                }

                if (!GolemFootprintConverter.TryConvert(
                        prefab.transform,
                        dimensions,
                        marker.IncludedLayers,
                        out var shapes,
                        out var convertErrors))
                {
                    foreach (var convertError in convertErrors)
                    {
                        errors.Add($"{assetPath}: {convertError}");
                    }

                    invalidGuids.Add(guid);
                    continue;
                }

                var aliasRaw = marker.Alias ?? string.Empty;
                var alias = aliasRaw.Trim();
                if (aliasRaw.Length > 0 && alias.Length == 0)
                {
                    errors.Add($"{assetPath}: alias must not be whitespace-only.");
                    invalidGuids.Add(guid);
                    continue;
                }

                if (alias.Length > 0)
                {
                    if (conflictedAliases.Contains(alias))
                    {
                        errors.Add($"{assetPath}: alias '{alias}' conflicts with another footprint; skipping.");
                        invalidGuids.Add(guid);
                        continue;
                    }

                    if (aliasToGuid.TryGetValue(alias, out var previousGuid))
                    {
                        errors.Add(
                            $"Footprint alias '{alias}' is used by multiple prefabs " +
                            $"(GUID {previousGuid} and {guid}).");
                        conflictedAliases.Add(alias);
                        aliasToGuid.Remove(alias);
                        models.RemoveAll(m =>
                            string.Equals(m.Alias, alias, StringComparison.Ordinal) ||
                            string.Equals(m.Guid, previousGuid, StringComparison.Ordinal) ||
                            string.Equals(m.Guid, guid, StringComparison.Ordinal));
                        invalidGuids.Add(previousGuid);
                        invalidGuids.Add(guid);
                        continue;
                    }

                    aliasToGuid[alias] = guid;
                }

                var model = new GolemFootprintModel
                {
                    Guid = guid,
                    Name = prefab.name,
                    AssetPath = assetPath.Replace('\\', '/'),
                    Alias = alias
                };
                model.Shapes.AddRange(shapes);
                models.Add(model);
            }

            // Preserve prior entries for transient invalid marked prefabs (not true deletions).
            if (invalidGuids.Count > 0)
            {
                AppendPreviousFootprints(
                    previous,
                    projectRoot,
                    normalizedPath,
                    invalidGuids,
                    models,
                    aliasToGuid,
                    conflictedAliases,
                    errors);
            }

            // Drop any model still carrying a conflicted alias after preservation.
            if (conflictedAliases.Count > 0)
            {
                models.RemoveAll(m =>
                    !string.IsNullOrEmpty(m.Alias) && conflictedAliases.Contains(m.Alias));
            }

            footprintCount = models.Count;
            if (models.Count == 0 && invalidGuids.Count == 0)
            {
                // No marked prefabs: desired empty so orphan cleanup deletes the managed file.
                return;
            }

            if (models.Count == 0 && invalidGuids.Count > 0)
            {
                // All marked prefabs invalid and nothing preservable: keep prior file if present.
                AppendPreviousWholeArtifact(previous, projectRoot, normalizedPath, desiredRecords, errors);
                return;
            }

            var yaml = GolemFootprintYamlBuilder.BuildYaml(dimensions, models);
            desiredRecords.Add(new GolemScribeArtifactRecord
            {
                Kind = GolemScribeConstants.ArtifactKindFootprint,
                SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                Entity = string.Empty,
                Path = normalizedPath,
                PendingContent = yaml,
                Hash = GolemScribeManifest.ComputeContentHash(yaml)
            });
        }

        /// <summary>Applies alias conflict rules for unit tests without AssetDatabase scanning.</summary>
        internal static void ApplyFootprintCandidate(
            GolemFootprintModel model,
            Dictionary<string, string> aliasToGuid,
            HashSet<string> conflictedAliases,
            List<GolemFootprintModel> models,
            List<string> errors)
        {
            if (model == null || string.IsNullOrEmpty(model.Guid))
            {
                return;
            }

            var alias = (model.Alias ?? string.Empty).Trim();
            model.Alias = alias;
            if (alias.Length == 0)
            {
                models.Add(model);
                return;
            }

            if (conflictedAliases.Contains(alias))
            {
                errors.Add($"alias '{alias}' conflicts with another footprint; skipping GUID {model.Guid}.");
                return;
            }

            if (aliasToGuid.TryGetValue(alias, out var previousGuid))
            {
                errors.Add($"Footprint alias '{alias}' is used by multiple prefabs.");
                conflictedAliases.Add(alias);
                aliasToGuid.Remove(alias);
                models.RemoveAll(m =>
                    string.Equals(m.Alias, alias, StringComparison.Ordinal) ||
                    string.Equals(m.Guid, previousGuid, StringComparison.Ordinal));
                return;
            }

            aliasToGuid[alias] = model.Guid;
            models.Add(model);
        }

        private static void AppendPreviousFootprints(
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            string relativePath,
            HashSet<string> invalidGuids,
            List<GolemFootprintModel> models,
            Dictionary<string, string> aliasToGuid,
            HashSet<string> conflictedAliases,
            List<string> errors)
        {
            if (!TryLoadPreviousFootprints(previous, projectRoot, relativePath, out _, out var prior, out var loadError))
            {
                if (!string.IsNullOrEmpty(loadError))
                {
                    errors.Add(loadError);
                }

                return;
            }

            var existingGuids = new HashSet<string>(
                models.Select(m => m.Guid),
                StringComparer.Ordinal);

            foreach (var fp in prior)
            {
                if (fp == null || string.IsNullOrEmpty(fp.Guid) || !invalidGuids.Contains(fp.Guid))
                {
                    continue;
                }

                if (existingGuids.Contains(fp.Guid))
                {
                    continue;
                }

                var alias = (fp.Alias ?? string.Empty).Trim();
                if (alias.Length > 0)
                {
                    if (conflictedAliases.Contains(alias))
                    {
                        continue;
                    }

                    if (aliasToGuid.TryGetValue(alias, out var other) &&
                        !string.Equals(other, fp.Guid, StringComparison.Ordinal))
                    {
                        errors.Add(
                            $"Preserved footprint GUID {fp.Guid} alias '{alias}' conflicts with GUID {other}; dropping preserved entry.");
                        continue;
                    }

                    aliasToGuid[alias] = fp.Guid;
                }

                models.Add(fp);
                existingGuids.Add(fp.Guid);
            }
        }

        private static void AppendPreviousWholeArtifact(
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            string relativePath,
            List<GolemScribeArtifactRecord> desiredRecords,
            List<string> errors)
        {
            if (!TryFindPriorFootprintRecord(
                    previous, projectRoot, relativePath, out var priorRecord, out var normalized, out var findError))
            {
                if (!string.IsNullOrEmpty(findError))
                {
                    errors.Add(findError);
                }

                return;
            }

            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, priorRecord.Path, out var absolute, out _, out var pathError))
            {
                errors.Add("Preserved footprint artifact path rejected: " + pathError);
                return;
            }

            if (!File.Exists(absolute))
            {
                return;
            }

            var content = File.ReadAllText(absolute);
            if (!GolemYamlWriter.IsScribeOwned(content))
            {
                return;
            }

            desiredRecords.Add(new GolemScribeArtifactRecord
            {
                Kind = GolemScribeConstants.ArtifactKindFootprint,
                SourceGuid = priorRecord.SourceGuid,
                Entity = priorRecord.Entity,
                Path = normalized,
                PendingContent = content,
                Hash = GolemScribeManifest.ComputeContentHash(content)
            });
        }

        private static bool TryLoadPreviousFootprints(
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            string relativePath,
            out int dimensions,
            out List<GolemFootprintModel> footprints,
            out string error)
        {
            dimensions = 0;
            footprints = new List<GolemFootprintModel>();
            error = null;

            if (!TryFindPriorFootprintRecord(
                    previous, projectRoot, relativePath, out var priorRecord, out _, out error))
            {
                return false;
            }

            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, priorRecord.Path, out var absolute, out _, out var pathError))
            {
                error = "Prior footprint artifact path rejected: " + pathError;
                return false;
            }

            if (!File.Exists(absolute))
            {
                return false;
            }

            var content = File.ReadAllText(absolute);
            if (!GolemFootprintYamlBuilder.TryParse(content, out dimensions, out footprints))
            {
                error = "Prior footprints.golem.yaml could not be parsed for preservation.";
                return false;
            }

            return true;
        }

        /// <summary>
        /// Finds a prior footprint manifest record whose path resolves to the same contained
        /// relative path as <paramref name="relativePath"/> (collapses <c>./</c> and repeated separators).
        /// </summary>
        internal static bool TryFindPriorFootprintRecord(
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            string relativePath,
            out GolemScribeArtifactRecord record,
            out string normalizedPath,
            out string error)
        {
            record = null;
            normalizedPath = null;
            error = null;
            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();

            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, relativePath, out _, out var desiredNormalized, out var pathError))
            {
                error = pathError;
                return false;
            }

            normalizedPath = desiredNormalized;
            foreach (var candidate in previous)
            {
                if (candidate == null ||
                    !string.Equals(candidate.Kind, GolemScribeConstants.ArtifactKindFootprint, StringComparison.Ordinal) ||
                    string.IsNullOrEmpty(candidate.Path))
                {
                    continue;
                }

                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, candidate.Path, out _, out var candidateNormalized, out _))
                {
                    continue;
                }

                if (string.Equals(candidateNormalized, desiredNormalized, StringComparison.Ordinal))
                {
                    record = candidate;
                    return true;
                }
            }

            return false;
        }

        /// <summary>
        /// Resolves the footprint marker: exactly one <see cref="GolemFootprint"/> on the prefab root.
        /// Nested or inactive-child markers are errors (they must not silently configure the prefab).
        /// </summary>
        internal static bool TryGetFootprintMarker(
            GameObject prefab,
            string assetPath,
            out GolemFootprint marker,
            out string error)
        {
            marker = null;
            error = null;
            if (prefab == null)
            {
                error = "Prefab is required.";
                return false;
            }

            var rootMarkers = prefab.GetComponents<GolemFootprint>()
                .Where(c => c != null)
                .ToList();
            var nestedMarkers = prefab.GetComponentsInChildren<GolemFootprint>(true)
                .Where(c => c != null && c.gameObject != prefab)
                .ToList();

            if (nestedMarkers.Count > 0)
            {
                error =
                    $"Prefab '{assetPath}' has GolemFootprint on nested child GameObject(s); " +
                    "exactly one marker is required on the prefab root.";
                return false;
            }

            if (rootMarkers.Count == 0)
            {
                return true;
            }

            if (rootMarkers.Count > 1)
            {
                error =
                    $"Prefab '{assetPath}' has {rootMarkers.Count} GolemFootprint components on the root; " +
                    "exactly one is required.";
                return false;
            }

            marker = rootMarkers[0];
            return true;
        }
    }
}
