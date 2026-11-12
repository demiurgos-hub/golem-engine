using UnityEditor;
using UnityEngine;
using GolemEngine.Unity;
using GolemEngine.Unity.Editor;

public static class ScribeHostSetup
{
    [InitializeOnLoadMethod]
    private static void Queue() { EditorApplication.delayCall += Ensure; }

    private static void Ensure()
    {
        GolemUnityEditorSettings.instance.AutoExportOnAssetChange = false;
        GolemUnityEditorSettings.instance.AutoBakeOnExport = false;
        if (AssetDatabase.FindAssets("t:GolemPrefabRegistry").Length != 0) return;
        AssetDatabase.CreateAsset(ScriptableObject.CreateInstance<GolemPrefabRegistry>(), "Assets/TestRegistry.asset");
        AssetDatabase.SaveAssets();
    }
}
