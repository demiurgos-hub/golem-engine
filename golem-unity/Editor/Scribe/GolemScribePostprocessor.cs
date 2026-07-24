using UnityEditor;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Records prefab/ScriptableObject/script asset changes for deferred Scribe reconciliation.
    /// Performs no export, bake, or AssetDatabase.Refresh work synchronously.
    /// </summary>
    public sealed class GolemScribePostprocessor : AssetPostprocessor
    {
        private static void OnPostprocessAllAssets(
            string[] importedAssets,
            string[] deletedAssets,
            string[] movedAssets,
            string[] movedFromAssetPaths)
        {
            if (GolemScribeScheduler.IsSuppressed)
            {
                return;
            }

            if (importedAssets != null && importedAssets.Length > 0)
            {
                GolemScribeScheduler.NotifyImportedOrMoved(importedAssets);
            }

            if (movedAssets != null && movedAssets.Length > 0)
            {
                GolemScribeScheduler.NotifyImportedOrMoved(movedAssets);
            }

            if (deletedAssets != null && deletedAssets.Length > 0)
            {
                GolemScribeScheduler.NotifyDeleted(deletedAssets);
            }

            if (movedFromAssetPaths != null && movedFromAssetPaths.Length > 0)
            {
                GolemScribeScheduler.NotifyDeleted(movedFromAssetPaths);
            }
        }
    }
}
