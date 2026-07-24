using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Exports ScriptableObject catalogs marked with <see cref="GolemCatalogAttribute"/>.
    /// Emits three manifest-owned artifacts per catalog class (type schema, world schema, catalog data).
    /// <para>
    /// Server boundary: bake generates <c>Load{Name}Data</c>; application code must load the catalog
    /// and store/publish the resulting world data explicitly. Data-only edits do not require bake.
    /// </para>
    /// <para>
    /// Manifest <c>source_guid</c> values for catalog class artifacts are deterministic synthetic IDs
    /// (see <see cref="GolemCatalogSchemaBuilder.StableSourceGuid"/>), not Unity asset GUIDs.
    /// </para>
    /// </summary>
    public static class GolemCatalogExporter
    {
        private static readonly string[] CatalogArtifactKinds =
        {
            GolemScribeConstants.ArtifactKindTypeSchema,
            GolemScribeConstants.ArtifactKindWorldSchema,
            GolemScribeConstants.ArtifactKindCatalogData
        };

        private static readonly HashSet<string> CatalogKindSet =
            new HashSet<string>(CatalogArtifactKinds, StringComparer.Ordinal);

        /// <summary>Outcome of a full catalog-category reconcile.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool TypeSchemaBytesChanged;
            public bool WorldSchemaBytesChanged;
            public bool CatalogDataBytesChanged;
            public int CatalogCount;

            /// <summary>True when type or world schema bytes changed (triggers auto-bake).</summary>
            public bool SchemaBytesChanged => TypeSchemaBytesChanged || WorldSchemaBytesChanged;
        }

        /// <summary>
        /// Scans catalog ScriptableObject types/assets and reconciles type, world, and catalog data artifacts.
        /// Transient invalid catalog classes preserve previously committed artifacts (no delete, no bake).
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

            CollectCatalogs(schema, previous, projectRoot, out var desiredRecords, out var catalogCount, out var collectErrors);
            result.Errors.AddRange(collectErrors);
            result.CatalogCount = catalogCount;

            var reconcile = GolemScribeArtifacts.ReconcileKinds(
                projectRoot,
                CatalogArtifactKinds,
                previous,
                desiredRecords);
            result.Errors.AddRange(reconcile.Errors);
            result.Warnings.AddRange(reconcile.Warnings);
            if (reconcile.Errors.Count > 0)
            {
                return result;
            }

            result.TypeSchemaBytesChanged = reconcile.TypeSchemaBytesChanged;
            result.WorldSchemaBytesChanged = reconcile.WorldSchemaBytesChanged;
            result.CatalogDataBytesChanged = reconcile.CatalogDataBytesChanged;
            return result;
        }

        /// <summary>
        /// Collects valid catalog exports. Invalid catalogs contribute errors and, when prior Scribe-owned
        /// artifacts exist for that same type name, re-emit those prior records so reconcile preserves them.
        /// <para>
        /// Rename rule: catalog identity is the C# type short name. Renaming a class is a true removal of
        /// the old type (old artifacts orphan-delete) plus creation of a new type — Scribe does not infer
        /// renames. Preserve only for transient invalidity of an identifiable existing type name.
        /// </para>
        /// Output-path / snake-case collisions exclude only the conflicting catalogs (preserve their prior
        /// artifacts when present); valid peers still reconcile.
        /// </summary>
        internal static void CollectCatalogs(
            GolemYamlConfig.ProjectSchemaConfig schema,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            out List<GolemScribeArtifactRecord> desiredRecords,
            out int catalogCount,
            out List<string> errors)
        {
            desiredRecords = new List<GolemScribeArtifactRecord>();
            catalogCount = 0;
            errors = new List<string>();
            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();

            var typeNameToFullName = new Dictionary<string, string>(StringComparer.Ordinal);
            var conflictedTypes = new HashSet<string>(StringComparer.Ordinal);
            var pathToTypeName = new Dictionary<string, string>(StringComparer.Ordinal);
            var catalogTypes = FindCatalogTypes();
            foreach (var catalogType in catalogTypes.OrderBy(t => t.FullName, StringComparer.Ordinal))
            {
                var model = GolemCatalogSchemaBuilder.Build(
                    catalogType, schema.TypesSchema, schema.WorldSchema, projectRoot);
                if (model.Errors.Count > 0)
                {
                    errors.AddRange(model.Errors.Select(e => $"{catalogType.FullName}: {e}"));
                    AppendPreviousCatalogArtifacts(previous, projectRoot, catalogType.Name, desiredRecords, errors);
                    continue;
                }

                if (conflictedTypes.Contains(model.TypeName))
                {
                    errors.Add(
                        $"Catalog type '{model.TypeName}' is defined by multiple classes; skipping '{catalogType.FullName}'.");
                    AppendPreviousCatalogArtifacts(previous, projectRoot, model.TypeName, desiredRecords, errors);
                    continue;
                }

                if (typeNameToFullName.TryGetValue(model.TypeName, out var previousFullName))
                {
                    errors.Add(
                        $"Catalog type '{model.TypeName}' is defined by multiple classes: '{previousFullName}' and '{catalogType.FullName}'.");
                    conflictedTypes.Add(model.TypeName);
                    typeNameToFullName.Remove(model.TypeName);
                    desiredRecords.RemoveAll(r => string.Equals(r.Entity, model.TypeName, StringComparison.Ordinal));
                    UnclaimPathsForType(pathToTypeName, model.TypeName);
                    AppendPreviousCatalogArtifacts(previous, projectRoot, model.TypeName, desiredRecords, errors);
                    continue;
                }

                if (!TryClaimCatalogOutputPaths(
                        model.TypeName,
                        model.TypesRelativePath,
                        model.WorldRelativePath,
                        model.CatalogDataRelativePath,
                        pathToTypeName,
                        conflictedTypes,
                        typeNameToFullName,
                        desiredRecords,
                        previous,
                        projectRoot,
                        errors))
                {
                    continue;
                }

                var assets = FindCatalogAssets(catalogType);
                GolemCatalogSchemaBuilder.BuildRows(model, assets, out var rows, out var rowErrors);
                if (rowErrors.Count > 0)
                {
                    errors.AddRange(rowErrors);
                    UnclaimPathsForType(pathToTypeName, model.TypeName);
                    AppendPreviousCatalogArtifacts(previous, projectRoot, model.TypeName, desiredRecords, errors);
                    continue;
                }

                typeNameToFullName[model.TypeName] = catalogType.FullName;
                catalogCount++;

                var typeYaml = GolemCatalogSchemaBuilder.BuildTypeYaml(model);
                var worldYaml = GolemCatalogSchemaBuilder.BuildWorldYaml(model);
                var dataYaml = GolemCatalogSchemaBuilder.BuildCatalogDataYaml(rows);

                desiredRecords.Add(new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindTypeSchema,
                    SourceGuid = model.SourceGuid,
                    Entity = model.TypeName,
                    Path = model.TypesRelativePath,
                    PendingContent = typeYaml,
                    Hash = GolemScribeManifest.ComputeContentHash(typeYaml)
                });
                desiredRecords.Add(new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindWorldSchema,
                    SourceGuid = model.SourceGuid,
                    Entity = model.TypeName,
                    Path = model.WorldRelativePath,
                    PendingContent = worldYaml,
                    Hash = GolemScribeManifest.ComputeContentHash(worldYaml)
                });
                desiredRecords.Add(new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindCatalogData,
                    SourceGuid = model.SourceGuid,
                    Entity = model.TypeName,
                    Path = model.CatalogDataRelativePath,
                    PendingContent = dataYaml,
                    Hash = GolemScribeManifest.ComputeContentHash(dataYaml)
                });
            }
        }

        /// <summary>
        /// Claims type/world/data output paths for <paramref name="typeName"/>. On collision with another
        /// catalog (or self-collision when schema dirs overlap), excludes conflicting types, restores their
        /// prior artifacts when present, and returns false.
        /// </summary>
        internal static bool TryClaimCatalogOutputPaths(
            string typeName,
            string typesPath,
            string worldPath,
            string dataPath,
            Dictionary<string, string> pathToTypeName,
            HashSet<string> conflictedTypes,
            Dictionary<string, string> typeNameToFullName,
            List<GolemScribeArtifactRecord> desiredRecords,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            List<string> errors)
        {
            pathToTypeName = pathToTypeName ?? new Dictionary<string, string>(StringComparer.Ordinal);
            conflictedTypes = conflictedTypes ?? new HashSet<string>(StringComparer.Ordinal);
            typeNameToFullName = typeNameToFullName ?? new Dictionary<string, string>(StringComparer.Ordinal);
            desiredRecords = desiredRecords ?? new List<GolemScribeArtifactRecord>();
            errors = errors ?? new List<string>();

            var paths = new[] { typesPath, worldPath, dataPath };
            var unique = new HashSet<string>(StringComparer.Ordinal);
            foreach (var path in paths)
            {
                if (string.IsNullOrEmpty(path) || !unique.Add(path))
                {
                    errors.Add(
                        $"Catalog '{typeName}' output paths collide with each other " +
                        $"(types='{typesPath}', world='{worldPath}', data='{dataPath}'). " +
                        "Check that types_schema and world_schema resolve to distinct directories.");
                    conflictedTypes.Add(typeName);
                    AppendPreviousCatalogArtifacts(previous, projectRoot, typeName, desiredRecords, errors);
                    return false;
                }
            }

            var collidedWith = new HashSet<string>(StringComparer.Ordinal);
            foreach (var path in paths)
            {
                if (pathToTypeName.TryGetValue(path, out var other) &&
                    !string.Equals(other, typeName, StringComparison.Ordinal))
                {
                    collidedWith.Add(other);
                }
            }

            if (collidedWith.Count > 0)
            {
                collidedWith.Add(typeName);
                foreach (var conflicted in collidedWith.OrderBy(t => t, StringComparer.Ordinal))
                {
                    conflictedTypes.Add(conflicted);
                    typeNameToFullName.Remove(conflicted);
                    desiredRecords.RemoveAll(r => string.Equals(r.Entity, conflicted, StringComparison.Ordinal));
                    UnclaimPathsForType(pathToTypeName, conflicted);
                    AppendPreviousCatalogArtifacts(previous, projectRoot, conflicted, desiredRecords, errors);
                }

                errors.Add(
                    $"Catalog '{typeName}' output path/snake_case collides with: " +
                    string.Join(", ", collidedWith.Where(t => t != typeName).OrderBy(t => t, StringComparer.Ordinal)) +
                    ".");
                return false;
            }

            foreach (var path in paths)
            {
                pathToTypeName[path] = typeName;
            }

            return true;
        }

        private static void UnclaimPathsForType(Dictionary<string, string> pathToTypeName, string typeName)
        {
            if (pathToTypeName == null || string.IsNullOrEmpty(typeName))
            {
                return;
            }

            var remove = pathToTypeName
                .Where(kv => string.Equals(kv.Value, typeName, StringComparison.Ordinal))
                .Select(kv => kv.Key)
                .ToList();
            foreach (var path in remove)
            {
                pathToTypeName.Remove(path);
            }
        }

        /// <summary>
        /// Re-adds previously committed Scribe-owned catalog artifacts for <paramref name="typeName"/>
        /// so a transient invalid class does not orphan them during reconcile.
        /// Only matches the identifiable type name; renames are not inferred.
        /// </summary>
        internal static void AppendPreviousCatalogArtifacts(
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            string projectRoot,
            string typeName,
            List<GolemScribeArtifactRecord> desiredRecords,
            List<string> errors)
        {
            if (string.IsNullOrEmpty(typeName) || desiredRecords == null)
            {
                return;
            }

            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();
            foreach (var prior in previous)
            {
                if (prior == null ||
                    string.IsNullOrEmpty(prior.Kind) ||
                    !CatalogKindSet.Contains(prior.Kind) ||
                    !string.Equals(prior.Entity, typeName, StringComparison.Ordinal))
                {
                    continue;
                }

                if (desiredRecords.Any(r =>
                        r != null &&
                        string.Equals(r.Kind, prior.Kind, StringComparison.Ordinal) &&
                        string.Equals(r.Path, prior.Path, StringComparison.Ordinal)))
                {
                    continue;
                }

                if (!GolemScribeArtifacts.TryResolveContainedPath(
                        projectRoot, prior.Path, out var absolute, out var normalized, out var pathError))
                {
                    errors?.Add($"Preserved catalog '{typeName}' artifact path rejected: {pathError}");
                    continue;
                }

                if (!File.Exists(absolute))
                {
                    continue;
                }

                var content = File.ReadAllText(absolute);
                if (!GolemYamlWriter.IsScribeOwned(content))
                {
                    // Handwritten occupant: do not claim it; orphan cleanup will leave it in place.
                    continue;
                }

                desiredRecords.Add(new GolemScribeArtifactRecord
                {
                    Kind = prior.Kind,
                    SourceGuid = prior.SourceGuid,
                    Entity = prior.Entity,
                    Path = normalized,
                    PendingContent = content,
                    Hash = GolemScribeManifest.ComputeContentHash(content)
                });
            }
        }

        /// <summary>Applies duplicate catalog-type conflict rules for unit tests without AssetDatabase scanning.</summary>
        internal static void ApplyCatalogCandidate(
            string typeName,
            string sourceFullName,
            IReadOnlyList<GolemScribeArtifactRecord> records,
            Dictionary<string, string> typeNameToFullName,
            HashSet<string> conflictedTypes,
            List<GolemScribeArtifactRecord> desiredRecords,
            List<string> errors)
        {
            if (conflictedTypes.Contains(typeName))
            {
                errors.Add($"Catalog type '{typeName}' is defined by multiple classes; skipping '{sourceFullName}'.");
                return;
            }

            if (typeNameToFullName.TryGetValue(typeName, out var previousFullName))
            {
                errors.Add(
                    $"Catalog type '{typeName}' is defined by multiple classes: '{previousFullName}' and '{sourceFullName}'.");
                conflictedTypes.Add(typeName);
                typeNameToFullName.Remove(typeName);
                desiredRecords.RemoveAll(r => string.Equals(r.Entity, typeName, StringComparison.Ordinal));
                return;
            }

            typeNameToFullName[typeName] = sourceFullName;
            desiredRecords.AddRange(records);
        }

        private static List<Type> FindCatalogTypes()
        {
            var results = new List<Type>();
            foreach (var type in TypeCache.GetTypesWithAttribute<GolemCatalogAttribute>())
            {
                if (type == null || type.IsAbstract || !typeof(ScriptableObject).IsAssignableFrom(type))
                {
                    continue;
                }

                if (IsTestAssembly(type.Assembly))
                {
                    continue;
                }

                if (type.GetCustomAttributes(typeof(GolemCatalogAttribute), inherit: false).Length == 0)
                {
                    continue;
                }

                results.Add(type);
            }

            return results;
        }

        private static bool IsTestAssembly(System.Reflection.Assembly assembly)
        {
            if (assembly == null)
            {
                return true;
            }

            var name = assembly.GetName().Name ?? string.Empty;
            return name.EndsWith(".Tests", StringComparison.OrdinalIgnoreCase) ||
                   name.IndexOf(".Tests.", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        /// <summary>
        /// Finds assets for <paramref name="catalogType"/>.
        /// AssetDatabase.FindAssets only filters by short type name (not namespace-qualified), so results
        /// are post-filtered to the exact runtime type.
        /// </summary>
        private static List<ScriptableObject> FindCatalogAssets(Type catalogType)
        {
            var assets = new List<ScriptableObject>();
            // t:ShortName is the supported filter; full-name filters are not reliable across Unity versions.
            var guids = AssetDatabase.FindAssets("t:" + catalogType.Name);
            foreach (var guid in guids)
            {
                var path = AssetDatabase.GUIDToAssetPath(guid);
                if (string.IsNullOrEmpty(path))
                {
                    continue;
                }

                var asset = AssetDatabase.LoadAssetAtPath(path, catalogType) as ScriptableObject;
                if (asset == null || asset.GetType() != catalogType)
                {
                    continue;
                }

                assets.Add(asset);
            }

            return assets
                .OrderBy(a => AssetDatabase.GetAssetPath(a) ?? string.Empty, StringComparer.Ordinal)
                .ToList();
        }
    }
}
