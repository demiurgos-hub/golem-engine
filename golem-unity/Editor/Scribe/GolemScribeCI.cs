using System;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Batch-mode CI entry points for Golem Scribe.
    /// Use <c>-executeMethod GolemEngine.Unity.Editor.GolemScribeCI.ExportAllAndValidate</c>.
    /// </summary>
    public static class GolemScribeCI
    {
        /// <summary>
        /// Dry-run validates committed artifacts, runs synchronous Export All (no bake),
        /// then validates again. Exits non-zero when committed artifacts were stale/invalid
        /// or when export/validation fails. Does not rely on <see cref="EditorApplication.delayCall"/>.
        /// </summary>
        public static void ExportAllAndValidate()
        {
            var exitCode = 0;
            try
            {
                Debug.Log("Golem Scribe CI: dry-run validating committed artifacts...");
                var pre = GolemScribeValidator.Validate();
                GolemScribeValidator.LogResult(pre);

                Debug.Log("Golem Scribe CI: exporting all Scribe artifacts (bake suppressed)...");
                var export = GolemScribeScheduler.ExportAllImmediate(runBake: false);
                foreach (var warning in export.Warnings)
                {
                    Debug.LogWarning("Golem Scribe CI: " + warning);
                }

                foreach (var error in export.Errors)
                {
                    Debug.LogError("Golem Scribe CI: " + error);
                }

                Debug.Log("Golem Scribe CI: validating after export...");
                var post = GolemScribeValidator.Validate();
                GolemScribeValidator.LogResult(post);

                exitCode = ComputeExitCode(pre, export, post);
                if (exitCode != 0)
                {
                    if (pre != null && (pre.HasDrift || (export != null && export.AnyBytesChanged)))
                    {
                        Debug.LogError(
                            "Golem Scribe CI: committed Scribe artifacts were stale or drifted from sources. " +
                            "Commit the Export All output (or fix sources) and re-run.");
                    }
                }
                else
                {
                    Debug.Log("Golem Scribe CI: Export All + validation passed.");
                }
            }
            catch (Exception ex)
            {
                exitCode = 1;
                Debug.LogError("Golem Scribe CI: " + ex);
            }
            finally
            {
                // Unity 6000 supports EditorApplication.Exit(int) for batch-mode process codes.
                // Exit-code policy is unit-tested via ComputeExitCode without terminating the editor.
                if (Application.isBatchMode)
                {
                    EditorApplication.Exit(exitCode);
                }
                else if (exitCode != 0)
                {
                    Debug.LogError("Golem Scribe CI finished with exit code " + exitCode + ".");
                }
            }
        }

        /// <summary>
        /// Computes the CI process exit code for Export All + validate.
        /// Returns 1 when the pre-export tree was already failing/drifting, export reported errors,
        /// post-export validation failed, or export changed bytes (committed artifacts were stale).
        /// Auto-fix during Export All does not count as success.
        /// </summary>
        internal static int ComputeExitCode(
            GolemScribeValidator.ValidationResult pre,
            GolemScribeScheduler.ImmediateExportResult export,
            GolemScribeValidator.ValidationResult post)
        {
            if (pre == null || export == null || post == null)
            {
                return 1;
            }

            if (pre.HasFailures || export.HasErrors || post.HasFailures || export.AnyBytesChanged)
            {
                return 1;
            }

            return 0;
        }
    }
}
