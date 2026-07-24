using System;
using System.Collections.Generic;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Coalesces Scribe export work onto <see cref="EditorApplication.delayCall"/> with a reentrancy guard.
    /// Never runs export, bake, or <see cref="AssetDatabase.Refresh"/> synchronously from an AssetPostprocessor.
    /// </summary>
    public static class GolemScribeScheduler
    {
        private const string PendingKey = "GolemScribe.PendingReconcile";
        private const string SuppressKey = "GolemScribe.SuppressAutoExport";

        private static bool _scheduled;
        private static bool _running;
        private static readonly HashSet<string> DirtyPaths = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        private static readonly HashSet<string> DeletedPaths = new HashSet<string>(StringComparer.OrdinalIgnoreCase);

        /// <summary>
        /// True while Scribe is suppressing recursive postprocessor-driven exports caused by its own writes/refresh.
        /// External asset notifications are still accepted while an export is running.
        /// </summary>
        public static bool IsSuppressed =>
            SessionState.GetBool(SuppressKey, false) || GolemScribeSession.SuppressCount > 0;

        /// <summary>Queues a full Scribe reconcile (entities + catalogs + footprints; same work as Export All).</summary>
        public static void RequestExportAll()
        {
            SessionState.SetBool(PendingKey, true);
            Schedule();
        }

        /// <summary>Outcome of a synchronous full Scribe export (entities + catalogs + footprints).</summary>
        public sealed class ImmediateExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool EntitySchemaBytesChanged;
            public bool CatalogSchemaBytesChanged;
            public bool FootprintBytesChanged;
            public bool AnyBytesChanged;
            public int EntityCount;
            public int CatalogCount;
            public int FootprintCount;
            public bool EntityHasErrors;
            public bool CatalogHasErrors;
            public bool FootprintHasErrors;

            /// <summary>True when any exporter reported errors.</summary>
            public bool HasErrors => Errors.Count > 0;
        }

        /// <summary>
        /// Runs Export All synchronously (no <see cref="EditorApplication.delayCall"/>).
        /// Safe for batch-mode CI. When <paramref name="runBake"/> is true and auto-bake is enabled,
        /// queues bake only for successful schema byte changes (footprints never bake).
        /// </summary>
        public static ImmediateExportResult ExportAllImmediate(bool runBake = true)
        {
            return ExportAllImmediate(runBake, assumeRunning: false);
        }

        /// <summary>
        /// Runs Export All synchronously. When <paramref name="assumeRunning"/> is true, the caller
        /// already owns <c>_running</c> (used by the deferred scheduler tick).
        /// </summary>
        internal static ImmediateExportResult ExportAllImmediate(bool runBake, bool assumeRunning)
        {
            var result = new ImmediateExportResult();
            if (!assumeRunning)
            {
                if (_running)
                {
                    result.Errors.Add("Golem Scribe export is already running; cannot start a synchronous Export All.");
                    return result;
                }

                _running = true;
            }

            try
            {
                var entityExport = GolemEntityExporter.ExportAll();
                var catalogExport = GolemCatalogExporter.ExportAll();
                var footprintExport = GolemFootprintExporter.ExportAll();

                result.Warnings.AddRange(entityExport.Warnings);
                result.Warnings.AddRange(catalogExport.Warnings);
                result.Warnings.AddRange(footprintExport.Warnings);
                result.Errors.AddRange(entityExport.Errors);
                result.Errors.AddRange(catalogExport.Errors);
                result.Errors.AddRange(footprintExport.Errors);

                result.EntityCount = entityExport.EntityCount;
                result.CatalogCount = catalogExport.CatalogCount;
                result.FootprintCount = footprintExport.FootprintCount;
                result.EntityHasErrors = entityExport.Errors.Count > 0;
                result.CatalogHasErrors = catalogExport.Errors.Count > 0;
                result.FootprintHasErrors = footprintExport.Errors.Count > 0;
                result.EntitySchemaBytesChanged = entityExport.EntitySchemaBytesChanged;
                result.CatalogSchemaBytesChanged = catalogExport.SchemaBytesChanged;
                result.FootprintBytesChanged = footprintExport.FootprintBytesChanged;
                result.AnyBytesChanged =
                    entityExport.EntitySchemaBytesChanged ||
                    catalogExport.SchemaBytesChanged ||
                    catalogExport.CatalogDataBytesChanged ||
                    footprintExport.FootprintBytesChanged;

                if (runBake)
                {
                    var shouldBake = ShouldAutoBake(
                        entityExport.Errors.Count > 0,
                        entityExport.EntitySchemaBytesChanged,
                        catalogExport.Errors.Count > 0,
                        catalogExport.SchemaBytesChanged);
                    if (shouldBake && GolemUnityEditorSettings.instance.AutoBakeOnExport)
                    {
                        RunSuppressed(() => GolemCodegenRunner.GenerateCode());
                    }
                }
            }
            catch (Exception ex)
            {
                result.Errors.Add(ex.Message);
            }
            finally
            {
                if (!assumeRunning)
                {
                    _running = false;
                }
            }

            return result;
        }

        /// <summary>Records imported/moved asset paths and schedules a coalesced reconcile.</summary>
        public static void NotifyImportedOrMoved(IEnumerable<string> paths)
        {
            if (IsSuppressed || !GolemUnityEditorSettings.instance.AutoExportOnAssetChange)
            {
                return;
            }

            var relevant = false;
            foreach (var path in paths)
            {
                if (!IsRelevantAssetPath(path))
                {
                    continue;
                }

                DirtyPaths.Add(path);
                relevant = true;
            }

            if (!relevant)
            {
                return;
            }

            SessionState.SetBool(PendingKey, true);
            if (!_running)
            {
                Schedule();
            }
        }

        /// <summary>Records deleted asset paths and schedules a coalesced reconcile.</summary>
        public static void NotifyDeleted(IEnumerable<string> paths)
        {
            if (IsSuppressed || !GolemUnityEditorSettings.instance.AutoExportOnAssetChange)
            {
                return;
            }

            var relevant = false;
            foreach (var path in paths)
            {
                if (!IsRelevantAssetPath(path))
                {
                    continue;
                }

                DeletedPaths.Add(path);
                relevant = true;
            }

            if (!relevant)
            {
                return;
            }

            SessionState.SetBool(PendingKey, true);
            if (!_running)
            {
                Schedule();
            }
        }

        /// <summary>
        /// Called after script reload to resume a pending coalesce across domain reloads.
        /// Catalog/entity/footprint source changes are reflected only after reload completes — never during
        /// the import that triggered compilation.
        /// </summary>
        public static void HandleScriptsReloaded()
        {
            _scheduled = false;
            _running = false;
            // Static suppress count is lost on domain reload; clear any stuck SessionState flag.
            GolemScribeSession.SuppressCount = 0;
            SessionState.SetBool(SuppressKey, false);
            if (SessionState.GetBool(PendingKey, false))
            {
                Schedule();
            }
        }

        /// <summary>Runs <paramref name="action"/> while auto-export is suppressed.</summary>
        public static void RunSuppressed(Action action)
        {
            GolemScribeSession.SuppressCount++;
            SessionState.SetBool(SuppressKey, true);
            try
            {
                action?.Invoke();
            }
            finally
            {
                GolemScribeSession.SuppressCount = Math.Max(0, GolemScribeSession.SuppressCount - 1);
                if (GolemScribeSession.SuppressCount == 0)
                {
                    SessionState.SetBool(SuppressKey, false);
                }
            }
        }

        /// <summary>Exposed for editor tests: clears in-memory coalesce state.</summary>
        internal static void ResetForTests()
        {
            _scheduled = false;
            _running = false;
            DirtyPaths.Clear();
            DeletedPaths.Clear();
            SessionState.SetBool(PendingKey, false);
            SessionState.SetBool(SuppressKey, false);
            GolemScribeSession.SuppressCount = 0;
            GolemEntityExporter.ClearPendingRegistryRemovalsForTests();
        }

        /// <summary>Exposed for editor tests: marks an export as running without performing work.</summary>
        internal static void SetRunningForTests(bool running)
        {
            _running = running;
        }

        /// <summary>Exposed for editor tests: whether a delayCall is currently queued.</summary>
        internal static bool IsScheduledForTests => _scheduled;

        /// <summary>Exposed for editor tests: whether reconcile is executing.</summary>
        internal static bool IsRunningForTests => _running;

        /// <summary>Exposed for editor tests: pending dirty path count.</summary>
        internal static int DirtyPathCountForTests => DirtyPaths.Count + DeletedPaths.Count;

        /// <summary>
        /// Decoupled auto-bake gate: bake when a successful exporter changed schema bytes.
        /// An exporter with errors never contributes its schema-change flag.
        /// </summary>
        internal static bool ShouldAutoBake(
            bool entityHasErrors,
            bool entitySchemaBytesChanged,
            bool catalogHasErrors,
            bool catalogSchemaBytesChanged)
        {
            var entityNeedsBake = !entityHasErrors && entitySchemaBytesChanged;
            var catalogNeedsBake = !catalogHasErrors && catalogSchemaBytesChanged;
            return entityNeedsBake || catalogNeedsBake;
        }

        private static void Schedule()
        {
            if (_scheduled)
            {
                return;
            }

            _scheduled = true;
            EditorApplication.delayCall += RunDeferred;
        }

        private static void RunDeferred()
        {
            _scheduled = false;
            if (_running)
            {
                Schedule();
                return;
            }

            if (!SessionState.GetBool(PendingKey, false) && DirtyPaths.Count == 0 && DeletedPaths.Count == 0)
            {
                return;
            }

            // Hold the running flag across the whole deferred tick so overlapping delayCalls
            // reschedule instead of nesting; ExportAllImmediate is invoked with ownership borrowed.
            _running = true;
            SessionState.SetBool(PendingKey, false);
            DirtyPaths.Clear();
            DeletedPaths.Clear();

            try
            {
                var export = ExportAllImmediate(runBake: true, assumeRunning: true);

                foreach (var warning in export.Warnings)
                {
                    Debug.LogWarning("Golem Scribe: " + warning);
                }

                foreach (var error in export.Errors)
                {
                    Debug.LogError("Golem Scribe: " + error);
                }

                // Per-exporter partial success: a failed category must not hide successful peers.
                if (!export.EntityHasErrors)
                {
                    Debug.Log($"Golem Scribe: exported {export.EntityCount} entity schema(s).");
                }

                if (!export.CatalogHasErrors)
                {
                    Debug.Log($"Golem Scribe: exported {export.CatalogCount} catalog(s).");
                }

                if (!export.FootprintHasErrors)
                {
                    Debug.Log($"Golem Scribe: exported {export.FootprintCount} footprint prefab(s).");
                }
            }
            catch (Exception ex)
            {
                Debug.LogError("Golem Scribe: export failed: " + ex.Message);
            }
            finally
            {
                _running = false;
                if (SessionState.GetBool(PendingKey, false) || DirtyPaths.Count > 0 || DeletedPaths.Count > 0)
                {
                    Schedule();
                }
            }
        }

        private static bool IsRelevantAssetPath(string path)
        {
            if (string.IsNullOrEmpty(path))
            {
                return false;
            }

            if (path.EndsWith(".prefab", StringComparison.OrdinalIgnoreCase) ||
                path.EndsWith(".asset", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }

            // Script changes may alter entity/catalog attributes; survive domain reload via SessionState.
            return path.EndsWith(".cs", StringComparison.OrdinalIgnoreCase);
        }
    }

    /// <summary>Domain-reload-safe suppress counter for Scribe auto-export.</summary>
    internal static class GolemScribeSession
    {
        public static int SuppressCount;
    }

    /// <summary>Resumes coalesced Scribe work after script compilation.</summary>
    public static class GolemScribeScriptReload
    {
        [UnityEditor.Callbacks.DidReloadScripts]
        private static void OnScriptsReloaded()
        {
            GolemScribeScheduler.HandleScriptsReloaded();
        }
    }
}
