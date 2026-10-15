using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    // Discovery supplies objects; each collector still owns its own marker and recovery rules.
    internal readonly struct ScribePrefabSource
    {
        internal readonly string SourceGuid;
        internal readonly string AssetPath;
        internal readonly GameObject Prefab;

        internal ScribePrefabSource(string sourceGuid, string assetPath, GameObject prefab)
        {
            SourceGuid = sourceGuid;
            AssetPath = assetPath;
            Prefab = prefab;
        }
    }
}
