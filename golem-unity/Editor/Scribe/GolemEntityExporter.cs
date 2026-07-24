using System;
using System.Collections.Generic;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Exports entity schemas from prefabs marked with <see cref="GolemEntityAttribute"/>.</summary>
    public static class GolemEntityExporter
    {
        private const string PendingRegistryRemovalsKey = "GolemScribe.PendingRegistryRemovals";

        /// <summary>Outcome of a full entity-category reconcile.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool EntitySchemaBytesChanged;
            public int EntityCount;
        }

        /// <summary>Scans all prefabs, reconciles entity YAML + manifest, and updates the prefab registry.</summary>
        public static ExportResult ExportAll()
        {
            var result = new ExportResult();
            var settings = GolemUnityEditorSettings.instance;
            var projectRoot = settings.ProjectRoot;
            if (string.IsNullOrWhiteSpace(projectRoot) || !System.IO.Directory.Exists(projectRoot))
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

            CollectEntities(schema, out var desiredRecords, out var registryMap, out var collectErrors);
            result.Errors.AddRange(collectErrors);
            result.EntityCount = registryMap.Count;

            // Resolve registry before mutating artifacts so registry ambiguity cannot strand deletions.
            if (!GolemPrefabRegistryUtil.TryGetUniqueRegistry(out var registry, out var registryError))
            {
                result.Errors.Add(registryError);
                return result;
            }

            var previousEntityNames = previous
                .Where(r => r.Kind == GolemScribeConstants.ArtifactKindEntitySchema)
                .Select(r => r.Entity)
                .Where(name => !string.IsNullOrEmpty(name))
                .Concat(LoadPendingRegistryRemovals())
                .Distinct(StringComparer.Ordinal)
                .ToList();

            var reconcile = GolemScribeArtifacts.ReconcileKind(
                projectRoot,
                GolemScribeConstants.ArtifactKindEntitySchema,
                previous,
                desiredRecords);
            result.Errors.AddRange(reconcile.Errors);
            result.Warnings.AddRange(reconcile.Warnings);
            if (reconcile.Errors.Count > 0)
            {
                return result;
            }

            result.EntitySchemaBytesChanged = reconcile.EntitySchemaBytesChanged;

            var removedNames = previousEntityNames
                .Where(name => !registryMap.ContainsKey(name))
                .Distinct(StringComparer.Ordinal)
                .ToList();

            try
            {
                GolemScribeScheduler.RunSuppressed(() =>
                    GolemPrefabRegistryUtil.ApplyEntityMappings(registry, registryMap, previousEntityNames));
                ClearPendingRegistryRemovals();
            }
            catch (Exception ex)
            {
                StorePendingRegistryRemovals(removedNames);
                result.Errors.Add($"Failed to update GolemPrefabRegistry: {ex.Message}");
            }

            return result;
        }

        /// <summary>Collects valid entity exports; invalid prefabs contribute errors without blocking valid peers.</summary>
        internal static void CollectEntities(
            GolemYamlConfig.ProjectSchemaConfig schema,
            out List<GolemScribeArtifactRecord> desiredRecords,
            out Dictionary<string, GameObject> registryMap,
            out List<string> errors)
        {
            desiredRecords = new List<GolemScribeArtifactRecord>();
            registryMap = new Dictionary<string, GameObject>(StringComparer.Ordinal);
            errors = new List<string>();

            var entityNameToSource = new Dictionary<string, string>(StringComparer.Ordinal);
            var conflictedEntities = new HashSet<string>(StringComparer.Ordinal);
            var prefabGuids = AssetDatabase.FindAssets("t:Prefab");
            foreach (var prefabGuid in prefabGuids)
            {
                var assetPath = AssetDatabase.GUIDToAssetPath(prefabGuid);
                if (string.IsNullOrEmpty(assetPath) || !assetPath.EndsWith(".prefab", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }

                var prefab = AssetDatabase.LoadAssetAtPath<GameObject>(assetPath);
                if (prefab == null)
                {
                    continue;
                }

                if (!TryGetEntityComponent(prefab, assetPath, out var behaviour, out var componentError))
                {
                    if (!string.IsNullOrEmpty(componentError))
                    {
                        errors.Add(componentError);
                    }
                    continue;
                }

                if (behaviour == null)
                {
                    continue;
                }

                var model = GolemEntitySchemaBuilder.Build(behaviour.GetType(), schema.Dimensions, schema.EntitySchema);
                if (model.Errors.Count > 0)
                {
                    errors.AddRange(model.Errors.Select(e => $"{assetPath}: {e}"));
                    continue;
                }

                if (conflictedEntities.Contains(model.EntityName))
                {
                    errors.Add(
                        $"Entity '{model.EntityName}' is defined by multiple prefabs; skipping '{assetPath}'.");
                    continue;
                }

                if (entityNameToSource.TryGetValue(model.EntityName, out var previousPath))
                {
                    errors.Add(
                        $"Entity '{model.EntityName}' is defined by multiple prefabs: '{previousPath}' and '{assetPath}'.");
                    conflictedEntities.Add(model.EntityName);
                    entityNameToSource.Remove(model.EntityName);
                    desiredRecords.RemoveAll(r => string.Equals(r.Entity, model.EntityName, StringComparison.Ordinal));
                    registryMap.Remove(model.EntityName);
                    continue;
                }

                entityNameToSource[model.EntityName] = assetPath;
                var yaml = GolemEntitySchemaBuilder.BuildYaml(model);
                desiredRecords.Add(new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                    SourceGuid = prefabGuid,
                    Entity = model.EntityName,
                    Path = model.RelativePath,
                    PendingContent = yaml,
                    Hash = GolemScribeManifest.ComputeContentHash(yaml)
                });
                registryMap[model.EntityName] = prefab;
            }
        }

        /// <summary>Applies duplicate-entity conflict rules for unit tests without AssetDatabase scanning.</summary>
        internal static void ApplyEntityCandidate(
            string entityName,
            string sourcePath,
            GolemScribeArtifactRecord record,
            GameObject prefab,
            Dictionary<string, string> entityNameToSource,
            HashSet<string> conflictedEntities,
            List<GolemScribeArtifactRecord> desiredRecords,
            Dictionary<string, GameObject> registryMap,
            List<string> errors)
        {
            if (conflictedEntities.Contains(entityName))
            {
                errors.Add($"Entity '{entityName}' is defined by multiple prefabs; skipping '{sourcePath}'.");
                return;
            }

            if (entityNameToSource.TryGetValue(entityName, out var previousPath))
            {
                errors.Add(
                    $"Entity '{entityName}' is defined by multiple prefabs: '{previousPath}' and '{sourcePath}'.");
                conflictedEntities.Add(entityName);
                entityNameToSource.Remove(entityName);
                desiredRecords.RemoveAll(r => string.Equals(r.Entity, entityName, StringComparison.Ordinal));
                registryMap.Remove(entityName);
                return;
            }

            entityNameToSource[entityName] = sourcePath;
            desiredRecords.Add(record);
            registryMap[entityName] = prefab;
        }

        internal static void ClearPendingRegistryRemovalsForTests()
        {
            ClearPendingRegistryRemovals();
        }

        internal static IReadOnlyList<string> LoadPendingRegistryRemovalsForTests()
        {
            return LoadPendingRegistryRemovals();
        }

        internal static void StorePendingRegistryRemovalsForTests(IEnumerable<string> names)
        {
            StorePendingRegistryRemovals(names);
        }

        private static bool TryGetEntityComponent(
            GameObject prefab,
            string assetPath,
            out MonoBehaviour behaviour,
            out string error)
        {
            behaviour = null;
            error = null;
            var matches = new List<MonoBehaviour>();
            foreach (var component in prefab.GetComponentsInChildren<MonoBehaviour>(true))
            {
                if (component == null)
                {
                    continue;
                }

                var attr = component.GetType().GetCustomAttributes(typeof(GolemEntityAttribute), inherit: false);
                if (attr != null && attr.Length > 0)
                {
                    matches.Add(component);
                }
            }

            if (matches.Count == 0)
            {
                return true;
            }

            if (matches.Count > 1)
            {
                error =
                    $"Prefab '{assetPath}' has {matches.Count} components with [GolemEntity]; exactly one is required.";
                return false;
            }

            behaviour = matches[0];
            return true;
        }

        private static List<string> LoadPendingRegistryRemovals()
        {
            var raw = SessionState.GetString(PendingRegistryRemovalsKey, string.Empty);
            if (string.IsNullOrEmpty(raw))
            {
                return new List<string>();
            }

            return raw.Split(new[] { '\n' }, StringSplitOptions.RemoveEmptyEntries)
                .Select(v => v.Trim())
                .Where(v => v.Length > 0)
                .Distinct(StringComparer.Ordinal)
                .ToList();
        }

        private static void StorePendingRegistryRemovals(IEnumerable<string> names)
        {
            var ordered = (names ?? Array.Empty<string>())
                .Where(n => !string.IsNullOrEmpty(n))
                .Distinct(StringComparer.Ordinal)
                .OrderBy(n => n, StringComparer.Ordinal)
                .ToArray();
            SessionState.SetString(PendingRegistryRemovalsKey, string.Join("\n", ordered));
        }

        private static void ClearPendingRegistryRemovals()
        {
            SessionState.EraseString(PendingRegistryRemovalsKey);
        }
    }
}
