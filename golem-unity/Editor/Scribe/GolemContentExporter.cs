using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Exports only a selected content set through Scribe's existing manifest-last transaction.</summary>
    public static class GolemContentExporter
    {
        public sealed class Result
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool Changed;
            public GolemContentPrepared Prepared;
        }

        /// <summary>Captures and validates selected authored data without filesystem writes or dependency discovery.</summary>
        public static GolemContentPrepared Capture(GolemContentExportSet set)
        {
            if (set == null) throw new ArgumentNullException(nameof(set));
            if (!System.Text.RegularExpressions.Regex.IsMatch(set.Namespace ?? "", "^[A-Za-z0-9_][A-Za-z0-9_.-]*$")) throw new InvalidOperationException("Content namespace must be a stable nonempty key.");
            if (string.IsNullOrWhiteSpace(set.Output)) throw new InvalidOperationException("Content output stem is required.");
            if (set.Roots == null || set.Roots.Count > GolemContentDocument.MaximumDefinitions) throw new InvalidOperationException("Invalid root count.");
            var definitions = new List<GolemContentDefinition>();
            var types = new List<GolemContentType>();
            var seen = new HashSet<string>(StringComparer.Ordinal);
            foreach (var root in set.Roots)
            {
                if (root == null || root.Source == null) throw new InvalidOperationException("Every selected content root needs a saved source.");
                var source = GolemContentBuilder.DescribeSource(root.Source);
                if (!seen.Add(source.Key)) throw new InvalidOperationException(source.Path + ": duplicate selected root " + source.Key);
                try
                {
                    var builder = new GolemContentBuilder(set.Namespace, source);
                    GolemContentAdapters.Find(root.Adapter).Capture(root.Source, builder);
                    if (builder.Captured == null) throw new InvalidOperationException("Adapter must capture exactly one definition.");
                    types.AddRange(builder.Types); definitions.Add(builder.Captured);
                }
                catch (Exception ex) { throw new InvalidOperationException(source.Path + " [" + root.Adapter + "]: " + ex.Message, ex); }
            }
            var imports = new List<GolemContentImport>();
            if (set.Imports != null && set.Imports.Count > 64) throw new InvalidOperationException("Content import count exceeds 64.");
            foreach (var import in set.Imports ?? new List<GolemContentImportedBundle>())
            {
                if (import == null || import.Bundle == null) throw new InvalidOperationException("Every content import needs an explicitly supplied bundle asset.");
                imports.Add(new GolemContentImport { Hash = import.Hash, BundleJson = import.Bundle.text });
            }
            return GolemContentDocument.Prepare(types, definitions, imports);
        }

        /// <summary>Validates selected source and committed artifacts; no export, bake or asset refresh is performed.</summary>
        public static Result Validate(GolemContentExportSet set, string projectRoot)
        { return Execute(set, projectRoot, false); }

        /// <summary>Validates the complete set, then transactionally reconciles only this set's content artifacts.</summary>
        public static Result Export(GolemContentExportSet set, string projectRoot)
        { return Execute(set, projectRoot, true); }

        private static Result Execute(GolemContentExportSet set, string projectRoot, bool write)
        {
            var result = new Result();
            try
            {
                if (!GolemScribeContext.TryLoad(projectRoot, out var context, out var error)) throw new InvalidOperationException(error);
                var owner = GolemContentBuilder.DescribeSource(set);
                result.Prepared = Capture(set);
                ValidateEntityBindings(set, result.Prepared.EntityTypes, context.ProjectRoot);
                var records = new List<GolemScribeArtifactRecord>
                {
                    Record(owner.Key, set.Output + ".schema.json", result.Prepared.SchemaJson),
                    Record(owner.Key, set.Output + ".bundle.json", result.Prepared.BundleJson)
                };
                if (write)
                {
                    var reconcile = GolemScribeArtifacts.ReconcileSource(context.ProjectRoot, GolemScribeConstants.ArtifactKindContentSource,
                        owner.Key, context.Previous, records);
                    result.Errors.AddRange(reconcile.Errors); result.Warnings.AddRange(reconcile.Warnings); result.Changed = reconcile.AnyBytesChanged;
                }
                else
                {
                    var input = GolemScribeArtifactPlan.Prepare(context.ProjectRoot, new[] { GolemScribeConstants.ArtifactKindContentSource }, context.Previous, records, owner.Key);
                    result.Errors.AddRange(input.Issues.Select(i => i.WriterError).Where(e => e != null));
                    var plan = GolemScribeArtifactPlan.Build(input, GolemScribeArtifactReader.Capture(input));
                    foreach (var entry in plan.Entries)
                    {
                        if (entry.HasReadError) result.Errors.Add(entry.Disk.Error);
                        else if (entry.Desired != null && (entry.Disk.Status != ScribeArtifactStatus.Owned || entry.Disk.Content != entry.Desired.Content))
                            result.Errors.Add(entry.Path + ": missing, unowned or stale selected content artifact; export this set explicitly.");
                        else if (entry.Desired == null && entry.Disk.Status == ScribeArtifactStatus.Owned)
                            result.Errors.Add(entry.Path + ": prior selected content output remains after a path change.");
                    }
                    foreach (var record in records)
                        if (!context.Previous.Any(p => p.Kind == record.Kind && p.SourceGuid == record.SourceGuid && p.Path == record.Path && p.Hash == record.Hash))
                            result.Errors.Add(record.Path + ": selected content manifest is missing or stale.");
                }
            }
            catch (Exception ex) { result.Errors.Add(ex.Message); }
            return result;
        }

        private static GolemScribeArtifactRecord Record(string owner, string path, string content) => new GolemScribeArtifactRecord
        {
            Kind = GolemScribeConstants.ArtifactKindContentSource, SourceGuid = owner,
            Entity = "", Path = path, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content)
        };

        private static void ValidateEntityBindings(GolemContentExportSet set, IReadOnlyList<string> entities, string projectRoot)
        {
            var bindings = new Dictionary<string, string>(StringComparer.Ordinal);
            foreach (var binding in set.EntitySchemas ?? new List<GolemContentEntityBinding>())
            {
                if (binding == null || string.IsNullOrEmpty(binding.Entity) || bindings.ContainsKey(binding.Entity)) throw new InvalidOperationException("Duplicate or invalid content entity binding.");
                bindings.Add(binding.Entity, binding.SchemaFile);
            }
            foreach (var entity in entities)
            {
                if (!bindings.TryGetValue(entity, out var path)) throw new InvalidOperationException(entity + ": provide an explicit existing entity schema file in the export set.");
                if (!GolemScribeArtifacts.TryResolveContainedPath(projectRoot, path, out var absolute, out _, out var error)) throw new InvalidOperationException(error);
                var file = new FileInfo(absolute);
                if (!file.Exists || file.Length > GolemContentDocument.MaximumDocumentBytes) throw new InvalidOperationException(entity + ": missing or oversized entity schema " + path);
                var declarations = File.ReadAllLines(absolute).Where(l => l.StartsWith("entity:", StringComparison.Ordinal)).ToArray();
                if (declarations.Length != 1 || !GolemYamlWriter.TryParseScalarToken(declarations[0].Substring(7).Trim(), out var actual) || actual != entity)
                    throw new InvalidOperationException(path + ": expected existing top-level entity: " + entity + ". Full entity validation remains owned by golem-bake.");
            }
        }

        [MenuItem("Tools/Golem/Scribe/Export Selected Content Set")]
        private static void ExportSelected() => Log(Export(Selected(), GolemUnityEditorSettings.instance.ProjectRoot));

        [MenuItem("Tools/Golem/Scribe/Validate Selected Content Set")]
        private static void ValidateSelected() => Log(Validate(Selected(), GolemUnityEditorSettings.instance.ProjectRoot));

        private static GolemContentExportSet Selected()
        {
            var set = Selection.activeObject as GolemContentExportSet;
            if (set == null) Debug.LogError("Select one Golem Content Export Set asset; content export never scans all assets.");
            return set;
        }

        internal static void Log(Result result)
        {
            foreach (var warning in result.Warnings) Debug.LogWarning("Golem Scribe content: " + warning);
            foreach (var error in result.Errors) Debug.LogError("Golem Scribe content: " + error);
            if (result.Errors.Count == 0) Debug.Log("Golem Scribe content: selected set passed.");
        }
    }

    /// <summary>Explicit selected-set batch entry points; neither method invokes Export All or golem-bake.</summary>
    public static class GolemContentCI
    {
        /// <summary>Validates the exact -golemContentSet asset path or GUID; missing selection fails.</summary>
        public static void Validate() => Run(false);

        /// <summary>Exports the exact -golemContentSet asset path or GUID, then validates the committed result.</summary>
        public static void Export() => Run(true);

        private static void Run(bool export)
        {
            var exit = 1;
            try
            {
                var args = Environment.GetCommandLineArgs();
                var index = Array.IndexOf(args, "-golemContentSet");
                if (index < 0 || index + 1 >= args.Length || args[index + 1].StartsWith("-", StringComparison.Ordinal)) throw new InvalidOperationException("Required: -golemContentSet <asset-path-or-guid>.");
                var path = args[index + 1];
                if (!path.Contains("/")) path = AssetDatabase.GUIDToAssetPath(path);
                var set = AssetDatabase.LoadAssetAtPath<GolemContentExportSet>(path);
                if (set == null) throw new InvalidOperationException("No content export set at selected path " + path);
                var root = GolemUnityEditorSettings.instance.ProjectRoot;
                var result = export ? GolemContentExporter.Export(set, root) : GolemContentExporter.Validate(set, root);
                GolemContentExporter.Log(result);
                if (result.Errors.Count == 0 && export) { result = GolemContentExporter.Validate(set, root); GolemContentExporter.Log(result); }
                exit = result.Errors.Count == 0 ? 0 : 1;
            }
            catch (Exception ex) { Debug.LogError("Golem Scribe content CI: " + ex.Message); }
            finally { if (Application.isBatchMode) EditorApplication.Exit(exit); }
        }
    }
}
