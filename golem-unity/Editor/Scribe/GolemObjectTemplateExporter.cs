using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One prefab, or one hierarchy in a scene, exported as an Object Template under a server-facing ID.</summary>
    public sealed class GolemObjectTemplateSource
    {
        public readonly string ID;
        public readonly GameObject Prefab;
        /// <summary>The template's diagnostic <c>SourceID</c>; null uses the prefab's asset path.</summary>
        public readonly string SourceID;

        /// <param name="prefab">A saved prefab asset root, or an active scene object whose parent, if any, sits at the world origin
        /// with no rotation or scale. A scene object is captured as it is, including unsaved changes.</param>
        public GolemObjectTemplateSource(string id, GameObject prefab, string sourceID = null)
        {
            ID = id;
            Prefab = prefab;
            SourceID = sourceID;
        }
    }

    /// <summary>
    /// Exports prefabs as an Object Template catalog: one JSON file plus, for 3D, one binary geometry sidecar.
    /// Each template holds the prefab's colliders and navigation sources in root-local space, with the root at
    /// the identity transform; a server placement transform replaces the root transform.
    /// </summary>
    public static class GolemObjectTemplateExporter
    {
        /// <summary>Outcome of one catalog export. A failed export leaves every file unchanged.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool BytesChanged;
            public int TemplateCount;
            public string OutputPath;
            /// <summary>Committed catalog and sidecar paths/hashes; empty on failure.</summary>
            public IReadOnlyList<GolemScribeArtifactRecord> Artifacts { get; internal set; } = Array.Empty<GolemScribeArtifactRecord>();
        }

        /// <summary>
        /// Exports <paramref name="templates"/> to a project-relative <c>.json</c> catalog path. Each source is a saved
        /// prefab asset root or an active scene object. Other catalogs, including the marker-driven one, keep their manifest entries.
        /// </summary>
        public static ExportResult Export(string catalogPath, IEnumerable<GolemObjectTemplateSource> templates, int dimensions = 3)
        {
            var result = new ExportResult();
            if (!GolemScribeContext.TryLoad(GolemUnityEditorSettings.instance.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            if (!GolemScribeArtifacts.TryResolveContainedPath(context.ProjectRoot, catalogPath, out _, out var normalized, out error))
            { result.Errors.Add(error); return result; }
            var sources = (templates ?? Array.Empty<GolemObjectTemplateSource>()).ToList();
            if (sources.Count == 0) { result.Errors.Add("An Object Template catalog needs at least one template."); return result; }
            var sourceGuid = GolemScribeConstants.ObjectTemplateSourceGuid + ":" + normalized;
            if (!TryCollect(normalized, sourceGuid, sources, dimensions, out var records, result.Errors)) return result;
            Commit(context, sourceGuid, records, result);
            result.TemplateCount = sources.Count;
            return result;
        }

        /// <summary>
        /// Exports every prefab whose root carries <see cref="GolemObjectTemplate"/> to the configured catalog path,
        /// using the project's simulation dimensions. With no marked prefabs, the catalog is removed.
        /// </summary>
        public static ExportResult ExportAll()
        {
            var result = new ExportResult();
            var settings = GolemUnityEditorSettings.instance;
            if (!GolemScribeContext.TryLoad(settings.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            CollectMarked(context.Schema.Dimensions, settings.ObjectTemplatesPath, context.ProjectRoot,
                out var records, out var count, result.Errors);
            if (result.Errors.Count > 0) return result;
            Commit(context, GolemScribeConstants.ObjectTemplateSourceGuid, records, result);
            result.TemplateCount = count;
            return result;
        }

        private static void Commit(GolemScribeContext context, string sourceGuid, GolemScribeArtifactRecord[] records, ExportResult result)
        {
            var committed = GolemScribeArtifacts.ReconcileSource(context.ProjectRoot, GolemScribeConstants.ArtifactKindObjectTemplates,
                sourceGuid, context.Previous, records);
            result.Errors.AddRange(committed.Errors); result.Warnings.AddRange(committed.Warnings);
            if (result.Errors.Count > 0) return;
            result.BytesChanged = committed.AnyBytesChanged;
            result.OutputPath = records.Length == 0 ? null : records[0].Path;
            result.Artifacts = records.Select(r => new GolemScribeArtifactRecord
            { Kind = r.Kind, SourceGuid = r.SourceGuid, Entity = r.Entity, Path = r.Path, Hash = r.Hash }).ToArray();
        }

        internal static void CollectMarked(int dimensions, string relativePath, string projectRoot,
            out GolemScribeArtifactRecord[] records, out int count, List<string> errors) =>
            CollectMarked(dimensions, relativePath, projectRoot, DiscoverPrefabs(), out records, out count, errors);

        /// <summary>Collects the marker-driven catalog. Errors leave <paramref name="records"/> empty.</summary>
        internal static void CollectMarked(int dimensions, string relativePath, string projectRoot, IEnumerable<ScribePrefabSource> prefabs,
            out GolemScribeArtifactRecord[] records, out int count, List<string> errors)
        {
            records = Array.Empty<GolemScribeArtifactRecord>(); count = 0;
            if (!GolemScribeArtifacts.TryResolveContainedPath(projectRoot, relativePath, out _, out var normalized, out var error))
            { errors.Add("Object templates path rejected: " + error); return; }
            var sources = new List<GolemObjectTemplateSource>();
            foreach (var prefab in prefabs.OrderBy(p => p.SourceGuid, StringComparer.Ordinal))
            {
                if (prefab.Prefab == null) continue;
                var nested = prefab.Prefab.GetComponentsInChildren<GolemObjectTemplate>(true).Where(m => m.gameObject != prefab.Prefab).ToArray();
                if (nested.Length > 0)
                {
                    errors.Add(prefab.AssetPath + ": GolemObjectTemplate belongs on the prefab root, not on child objects.");
                    continue;
                }
                var marker = prefab.Prefab.GetComponent<GolemObjectTemplate>();
                if (marker == null) continue;
                var id = string.IsNullOrEmpty(marker.TemplateID) ? prefab.SourceGuid : marker.TemplateID;
                sources.Add(new GolemObjectTemplateSource(id, prefab.Prefab));
            }
            if (errors.Count > 0 || sources.Count == 0) return;
            if (TryCollect(normalized, GolemScribeConstants.ObjectTemplateSourceGuid, sources, dimensions, out var collected, errors))
            { records = collected; count = sources.Count; }
        }

        private static IEnumerable<ScribePrefabSource> DiscoverPrefabs()
        {
            foreach (var guid in AssetDatabase.FindAssets("t:Prefab").OrderBy(g => g, StringComparer.Ordinal))
            {
                var path = AssetDatabase.GUIDToAssetPath(guid);
                if (string.IsNullOrEmpty(path) || !path.EndsWith(".prefab", StringComparison.OrdinalIgnoreCase)) continue;
                yield return new ScribePrefabSource(guid, path, AssetDatabase.LoadAssetAtPath<GameObject>(path));
            }
        }

        /// <summary>Captures every template and builds the catalog records without writing. All errors are reported.</summary>
        internal static bool TryCollect(string catalogPath, string sourceGuid, IReadOnlyList<GolemObjectTemplateSource> templates, int dimensions,
            out GolemScribeArtifactRecord[] records, List<string> errors)
        {
            records = null;
            var before = errors.Count;
            if (dimensions != 2 && dimensions != 3) errors.Add("Object Template catalogs must be 2D or 3D, got " + dimensions + ".");
            if (string.IsNullOrWhiteSpace(catalogPath) || !catalogPath.EndsWith(".json", StringComparison.OrdinalIgnoreCase))
                errors.Add("Object Template catalogs need a project-relative .json path.");
            if (EditorApplication.isPlayingOrWillChangePlaymode) errors.Add("Object Template export requires Edit Mode.");
            if (errors.Count > before) return false;

            var catalogID = Path.GetFileNameWithoutExtension(catalogPath);
            var state = new CatalogState(dimensions);
            var captured = new List<ObjectTemplate>();
            var ids = new HashSet<string>(StringComparer.Ordinal);
            foreach (var template in templates)
            {
                var name = !template?.Prefab ? "<missing prefab>"
                    : EditorUtility.IsPersistent(template.Prefab) ? AssetDatabase.GetAssetPath(template.Prefab)
                    : template.SourceID ?? template.Prefab.name;
                try
                {
                    if (template == null || string.IsNullOrWhiteSpace(template.ID) || template.ID.Trim() != template.ID)
                        throw new InvalidOperationException("template ID must be non-empty without surrounding whitespace.");
                    if (!ids.Add(template.ID)) throw new InvalidOperationException("duplicate template ID '" + template.ID + "'.");
                    captured.Add(Capture(template, state));
                }
                catch (Exception ex) { errors.Add(name + " (template '" + template?.ID + "'): " + ex.Message); }
            }
            if (errors.Count > before) return false;

            try
            {
                var catalog = new ObjectTemplateCatalog
                {
                    CatalogID = catalogID, Dimensions = dimensions, Templates = captured.ToArray(),
                    LayerCollisionMasks = dimensions == 3 ? GolemPhysicsExporter.LayerCollisionMasks() : Array.Empty<uint>(),
                    Materials = state.Materials.Values.ToArray()
                };
                byte[] bytes = null;
                string binaryPath = null;
                if (state.PhysicsAssets.Count > 0 || state.NavigationAssets.Count > 0)
                {
                    bytes = GolemGeometryDocument.Build(state.PhysicsAssets.Values, state.NavigationAssets.Values);
                    binaryPath = Path.ChangeExtension(catalogPath, ".golemgeo").Replace('\\', '/');
                    catalog.Geometry = new PhysicsGeometryReference { Path = Path.GetFileName(binaryPath), SHA256 = GolemScribeManifest.ComputeBytesHash(bytes) };
                }
                var content = GolemObjectTemplateDocument.Build(catalog);
                var list = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord { Kind = GolemScribeConstants.ArtifactKindObjectTemplates, SourceGuid = sourceGuid, Entity = catalogID,
                        Path = catalogPath, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content) }
                };
                if (bytes != null)
                    list.Add(new GolemScribeArtifactRecord { Kind = GolemScribeConstants.ArtifactKindObjectTemplates, SourceGuid = sourceGuid, Entity = catalogID,
                        Path = binaryPath, PendingBytes = bytes, Hash = catalog.Geometry.SHA256 });
                records = list.ToArray();
                return true;
            }
            catch (Exception ex) { errors.Add(catalogPath + ": " + ex.Message); return false; }
        }

        private sealed class CatalogState
        {
            internal readonly int Dimensions;
            internal readonly Dictionary<string, PhysicsAsset> PhysicsAssets = new Dictionary<string, PhysicsAsset>(StringComparer.Ordinal);
            internal readonly Dictionary<string, NavigationAsset> NavigationAssets = new Dictionary<string, NavigationAsset>(StringComparer.Ordinal);
            internal readonly Dictionary<string, PhysicsMaterial> Materials = new Dictionary<string, PhysicsMaterial>(StringComparer.Ordinal);
            internal CatalogState(int dimensions) => Dimensions = dimensions;
        }

        private static ObjectTemplate Capture(GolemObjectTemplateSource template, CatalogState state)
        {
            if (!template.Prefab) throw new InvalidOperationException("templates need a saved prefab asset root or an active scene object.");
            if (!EditorUtility.IsPersistent(template.Prefab)) return CaptureSceneObject(template, state);
            var path = AssetDatabase.GetAssetPath(template.Prefab);
            if (string.IsNullOrEmpty(path) || !path.EndsWith(".prefab", StringComparison.OrdinalIgnoreCase)
                || AssetDatabase.LoadAssetAtPath<GameObject>(path) != template.Prefab)
                throw new InvalidOperationException("asset templates must be saved prefab asset roots.");
            var root = PrefabUtility.LoadPrefabContents(path);
            try
            {
                root.transform.localPosition = Vector3.zero;
                root.transform.localRotation = Quaternion.identity;
                root.transform.localScale = Vector3.one;
                return CaptureRoot(root.transform, template.ID, template.SourceID ?? path, state);
            }
            finally { PrefabUtility.UnloadPrefabContents(root); }
        }

        /// <summary>Captures a scene hierarchy at the identity pose, then gives the caller its pose back.</summary>
        /// <remarks>Collider and body activity is read from <c>activeInHierarchy</c>, so an inactive object would export inactive colliders.</remarks>
        private static ObjectTemplate CaptureSceneObject(GolemObjectTemplateSource template, CatalogState state)
        {
            var root = template.Prefab.transform;
            if (!template.Prefab.activeInHierarchy) throw new InvalidOperationException("scene templates must be active in the hierarchy.");
            if (root.parent && !root.parent.localToWorldMatrix.isIdentity)
                throw new InvalidOperationException("a scene template's parent must sit at the world origin with no rotation or scale.");
            var position = root.localPosition;
            var rotation = root.localRotation;
            var scale = root.localScale;
            try
            {
                root.localPosition = Vector3.zero;
                root.localRotation = Quaternion.identity;
                root.localScale = Vector3.one;
                return CaptureRoot(root, template.ID, template.SourceID, state);
            }
            finally
            {
                root.localPosition = position;
                root.localRotation = rotation;
                root.localScale = scale;
            }
        }

        private static ObjectTemplate CaptureRoot(Transform root, string id, string sourceID, CatalogState state)
        {
            foreach (var transform in root.GetComponentsInChildren<Transform>(true))
                if (PrefabUtility.GetPrefabInstanceStatus(transform.gameObject) == PrefabInstanceStatus.MissingAsset)
                    throw new InvalidOperationException(transform.name + ": missing nested prefab source.");
            var identity = new GolemObjectTemplateIdentity(root);
            var context = new GolemScribeIdentityContext(new GolemScribeExportOptions { StableObjectIdentity = identity.Resolve });
            var result = new ObjectTemplate { ID = id, SourceID = sourceID ?? "" };
            if (state.Dimensions == 2) Capture2D(root, context, result);
            else Capture3D(root, context, state, result);
            return result;
        }

        private static void Capture3D(Transform root, GolemScribeIdentityContext context, CatalogState state, ObjectTemplate template)
        {
            Reject<Collider2D>(root, "2D colliders do not belong in a 3D catalog");
            Reject<Joint>(root, "joints are not supported in Object Templates");
            Reject<ArticulationBody>(root, "ArticulationBody is not supported in Object Templates");
            Reject<CharacterController>(root, "CharacterController is not supported in Object Templates");
            foreach (var body in root.GetComponentsInChildren<Rigidbody>(true))
                if (!body.isKinematic) throw new InvalidOperationException(body.name + ": dynamic Rigidbody is not supported; templates hold static and kinematic bodies only.");

            bool Owned(Component component) => component.transform.IsChildOf(root);
            var capture = new GolemPhysicsExporter.PhysicsCapture(state.PhysicsAssets);
            GolemPhysicsExporter.CaptureHierarchy(root, Owned, context, true, capture);
            var navigation = GolemNavigationExporter.CaptureScene(root, Owned, context, state.NavigationAssets);
            if (capture.Colliders.Count == 0 && navigation.Instances.Length == 0 && navigation.Volumes.Length == 0 && navigation.Links.Length == 0)
                throw new InvalidOperationException("the prefab has no colliders or navigation sources.");

            var source = GolemPhysicsDocument.Normalize(new PhysicsSource
            {
                Version = 2, WorldID = template.ID, LayerCollisionMasks = GolemPhysicsExporter.LayerCollisionMasks(),
                Bodies = capture.Bodies.Values.ToArray(), Colliders = capture.Colliders.ToArray(), Materials = capture.Materials.Values.ToArray(),
                Assets = capture.Colliders.Where(c => !string.IsNullOrEmpty(c.AssetID)).Select(c => c.AssetID).Distinct(StringComparer.Ordinal)
                    .Select(id => state.PhysicsAssets[id]).ToArray()
            });
            foreach (var material in source.Materials)
                if (!state.Materials.ContainsKey(material.ID)) state.Materials.Add(material.ID, material);
            template.Bodies = source.Bodies;
            template.Colliders = source.Colliders;
            template.Instances = navigation.Instances;
            template.Volumes = navigation.Volumes;
            template.Links = navigation.Links;
        }

        private static void Capture2D(Transform root, GolemScribeIdentityContext context, ObjectTemplate template)
        {
            Reject<Collider>(root, "3D colliders do not belong in a 2D catalog");
            Reject<Rigidbody>(root, "3D bodies do not belong in a 2D catalog");
            Reject<GolemNavigationSource>(root, "navigation sources are 3D only");
            Reject<GolemNavigationVolume>(root, "navigation volumes are 3D only");
            Reject<GolemNavigationLink>(root, "navigation links are 3D only");
            foreach (var body in root.GetComponentsInChildren<Rigidbody2D>(true))
                if (body.bodyType == RigidbodyType2D.Dynamic)
                    throw new InvalidOperationException(body.name + ": dynamic Rigidbody2D is not supported; templates hold static and kinematic bodies only.");
            var shapes = new List<ObjectTemplateShape>();
            foreach (var collider in root.GetComponentsInChildren<Collider2D>(true))
            {
                if (!collider.enabled) continue;
                var shape = GolemObjectTemplateShapes2D.Capture(collider);
                shape.ID = context.Object(collider, () => throw new InvalidOperationException("Missing template identity."));
                shapes.Add(shape);
            }
            if (shapes.Count == 0) throw new InvalidOperationException("the prefab has no enabled 2D colliders.");
            template.Shapes = shapes.ToArray();
        }

        private static void Reject<T>(Transform root, string message) where T : Component
        {
            var found = root.GetComponentInChildren<T>(true);
            if (found) throw new InvalidOperationException(found.name + ": " + message + ".");
        }
    }
}
