using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Safe filesystem helpers for Scribe-owned generated artifacts.</summary>
    public static class GolemScribeArtifacts
    {
        /// <summary>Optional test hook: invoked after each successful artifact write; throw to force rollback.</summary>
        internal static Action<string> AfterWriteHookForTests;

        /// <summary>Result of reconciling desired artifacts against disk and the prior manifest.</summary>
        public sealed class ReconcileResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public readonly List<GolemScribeArtifactRecord> ManifestRecords = new List<GolemScribeArtifactRecord>();
            public bool EntitySchemaBytesChanged;
            public bool TypeSchemaBytesChanged;
            public bool WorldSchemaBytesChanged;
            public bool CatalogDataBytesChanged;
            public bool FootprintBytesChanged;
            public bool AnyBytesChanged;

            /// <summary>True when type or world schema bytes changed (catalog data alone does not count).</summary>
            public bool CatalogSchemaBytesChanged => TypeSchemaBytesChanged || WorldSchemaBytesChanged;
        }

        /// <summary>
        /// Reconciles one artifact kind: writes/updates desired managed files, deletes orphaned
        /// Scribe-owned files of that kind, and rebuilds the manifest while preserving other kinds.
        /// Files are replaced atomically with rollback on failure. Rollback failures are included in Errors;
        /// a failed result does not guarantee that the original filesystem state was fully restored.
        /// Never deletes or overwrites handwritten (non-Scribe-marked) files.
        /// </summary>
        public static ReconcileResult ReconcileKind(
            string projectRoot,
            string kind,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            IReadOnlyList<GolemScribeArtifactRecord> desiredForKind)
        {
            return ReconcileKinds(projectRoot, new[] { kind }, previous, desiredForKind);
        }

        /// <summary>
        /// Reconciles one or more artifact kinds in a single transactional pass, preserving
        /// manifest records whose kind is outside <paramref name="kinds"/>.
        /// </summary>
        public static ReconcileResult ReconcileKinds(
            string projectRoot,
            IReadOnlyCollection<string> kinds,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            IReadOnlyList<GolemScribeArtifactRecord> desiredForKinds)
        {
            var result = new ReconcileResult();
            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();
            desiredForKinds = desiredForKinds ?? Array.Empty<GolemScribeArtifactRecord>();
            var kindSet = new HashSet<string>(
                (kinds ?? Array.Empty<string>()).Where(k => !string.IsNullOrEmpty(k)),
                StringComparer.Ordinal);
            if (kindSet.Count == 0)
            {
                result.Errors.Add("Scribe reconcile requires at least one artifact kind.");
                return result;
            }

            if (!TryNormalizeProjectRoot(projectRoot, out var rootFull, out var rootError))
            {
                result.Errors.Add(rootError);
                return result;
            }

            var prepared = GolemScribeArtifactPlan.Prepare(rootFull, kinds, previous, desiredForKinds);
            result.Errors.AddRange(prepared.Issues.Select(i => i.WriterError).Where(e => e != null));
            if (result.Errors.Count > 0) return result;
            var plan = GolemScribeArtifactPlan.Build(prepared, GolemScribeArtifactReader.Capture(prepared));
            foreach (var entry in plan.Entries)
            {
                if (entry.HasReadError)
                {
                    result.Errors.Add(entry.Disk.Error);
                }
                else if (entry.Disk.Status == ScribeArtifactStatus.Handwritten)
                {
                    if (entry.Desired != null)
                        result.Errors.Add($"Refusing to overwrite handwritten file '{entry.Path}'. Move it or remove the conflicting Scribe source.");
                    else
                        result.Warnings.Add($"Left handwritten file '{entry.Path}' in place during Scribe orphan cleanup.");
                }
            }
            var manifestPath = GolemScribeManifest.ManifestPath(rootFull);
            if (!CanWriteManagedPath(manifestPath, out var manifestError)) result.Errors.Add(manifestError);
            if (result.Errors.Count > 0) return result;

            var transaction = new GolemScribeFileTransaction();
            try
            {
                foreach (var entry in plan.Writes)
                {
                    // Recheck even planned no-ops: the snapshot is an observation, not authorization.
                    if (transaction.WriteManaged(entry.Disk.AbsolutePath, entry.Desired.Content))
                    {
                        AfterWriteHookForTests?.Invoke(entry.Disk.AbsolutePath);
                        result.AnyBytesChanged = true;
                        MarkSchemaBytesChanged(result, entry.Kind);
                    }
                }
                foreach (var entry in plan.Deletions)
                {
                    transaction.DeleteManaged(entry.Disk.AbsolutePath);
                    result.AnyBytesChanged = true;
                    MarkSchemaBytesChanged(result, entry.Kind);
                }
                var manifestRecords = plan.ManifestRecords.Select(r => r.ToManifestRecord()).ToList();
                if (transaction.WriteManaged(manifestPath, GolemScribeManifest.BuildDocument(manifestRecords)))
                    result.AnyBytesChanged = true;
                result.ManifestRecords.AddRange(manifestRecords);
                return result;
            }
            catch (Exception ex)
            {
                result.Errors.Add(ex.Message);
                result.Errors.AddRange(transaction.Rollback());
                result.ManifestRecords.Clear();
                result.EntitySchemaBytesChanged = false;
                result.TypeSchemaBytesChanged = false;
                result.WorldSchemaBytesChanged = false;
                result.CatalogDataBytesChanged = false;
                result.FootprintBytesChanged = false;
                result.AnyBytesChanged = false;
                return result;
            }
        }

        private static void MarkSchemaBytesChanged(ReconcileResult result, string kind)
        {
            if (kind == GolemScribeConstants.ArtifactKindEntitySchema)
            {
                result.EntitySchemaBytesChanged = true;
            }
            else if (kind == GolemScribeConstants.ArtifactKindTypeSchema)
            {
                result.TypeSchemaBytesChanged = true;
            }
            else if (kind == GolemScribeConstants.ArtifactKindWorldSchema)
            {
                result.WorldSchemaBytesChanged = true;
            }
            else if (kind == GolemScribeConstants.ArtifactKindCatalogData)
            {
                result.CatalogDataBytesChanged = true;
            }
            else if (kind == GolemScribeConstants.ArtifactKindFootprint)
            {
                result.FootprintBytesChanged = true;
            }
        }

        /// <summary>Atomically replaces a managed file when text differs; throws with rollback diagnostics on failure.</summary>
        public static bool WriteManagedFile(string absolutePath, string content)
        {
            var transaction = new GolemScribeFileTransaction();
            try
            {
                return transaction.WriteManaged(absolutePath, content);
            }
            catch (Exception ex)
            {
                var errors = new List<string> { ex.Message };
                errors.AddRange(transaction.Rollback());
                throw new InvalidOperationException(string.Join("\n", errors), ex);
            }
        }

        /// <summary>Returns true when an existing file is Scribe-owned or the path is free.</summary>
        public static bool CanWriteManagedPath(string absolutePath, out string error)
        {
            var snapshot = GolemScribeArtifactReader.ReadAbsolute(absolutePath, absolutePath);
            error = snapshot.Status == ScribeArtifactStatus.Handwritten
                ? $"Path is occupied by a handwritten file: {absolutePath}" : snapshot.Error;
            return snapshot.Status == ScribeArtifactStatus.Missing || snapshot.Status == ScribeArtifactStatus.Owned;
        }

        /// <summary>
        /// Resolves a project-relative path that is strictly contained under <paramref name="projectRoot"/>.
        /// Collapses <c>.</c> segments and repeated separators; rejects absolute paths, empty paths, and <c>..</c> escapes.
        /// </summary>
        public static bool TryResolveContainedPath(
            string projectRoot,
            string relativePath,
            out string absolutePath,
            out string normalizedRelative,
            out string error)
        {
            absolutePath = null;
            normalizedRelative = null;
            error = null;

            if (!TryNormalizeProjectRoot(projectRoot, out var rootFull, out error))
            {
                return false;
            }

            return GolemScribePaths.TryResolveRelative(rootFull, relativePath, out absolutePath, out normalizedRelative, out error);
        }

        /// <summary>Converts a project-relative path to an absolute filesystem path under the project root.</summary>
        public static string ToAbsolutePath(string projectRoot, string relativePath)
        {
            if (!TryResolveContainedPath(projectRoot, relativePath, out var absolute, out _, out var error))
            {
                throw new InvalidOperationException(error);
            }
            return absolute;
        }

        /// <summary>Normalizes a relative artifact path to forward slashes without validating containment.</summary>
        public static string NormalizeRelativePath(string relativePath)
        {
            return (relativePath ?? string.Empty).Replace('\\', '/').Trim();
        }

        private static bool TryNormalizeProjectRoot(string projectRoot, out string rootFull, out string error)
        {
            rootFull = null;
            error = null;
            if (string.IsNullOrWhiteSpace(projectRoot))
            {
                error = "Golem project root is required.";
                return false;
            }

            try
            {
                rootFull = Path.GetFullPath(projectRoot);
            }
            catch (Exception ex)
            {
                error = "Invalid Golem project root: " + ex.Message;
                return false;
            }

            if (!Directory.Exists(rootFull))
            {
                error = $"Golem project root does not exist: {rootFull}";
                return false;
            }

            return true;
        }
    }
}
