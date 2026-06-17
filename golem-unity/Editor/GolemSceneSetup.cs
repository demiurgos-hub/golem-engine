using System.IO;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Creates and wires common Golem objects in the active Unity scene.</summary>
    public static class GolemSceneSetup
    {
        private const string RegistryAssetName = "GolemPrefabRegistry.asset";
        private const string SpawnerObjectName = "Golem Entity Spawner";
        private const string ClientObjectName = "Golem Client";

        public static void ConfigureCurrentScene()
        {
            var registry = CreateOrSelectPrefabRegistry();
            var spawner = CreateOrSelectEntitySpawner(registry);
            CreateOrSelectClientObject();

            if (spawner != null && registry != null)
            {
                AssignPrefabRegistry(spawner, registry);
            }

            MarkActiveSceneDirty();
            Debug.Log("Configured the current scene for Golem.");
        }

        public static GolemPrefabRegistry CreateOrSelectPrefabRegistry()
        {
            var registry = FindPrefabRegistry();
            if (registry == null)
            {
                var settings = GolemUnityEditorSettings.instance;
                EnsureAssetFolder(settings.AssetFolder);
                var path = AssetDatabase.GenerateUniqueAssetPath(Path.Combine(settings.AssetFolder, RegistryAssetName).Replace('\\', '/'));
                registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
                AssetDatabase.CreateAsset(registry, path);
                AssetDatabase.SaveAssets();
                Debug.Log($"Created Golem prefab registry at {path}.");
            }

            Selection.activeObject = registry;
            EditorGUIUtility.PingObject(registry);
            return registry;
        }

        public static GolemEntitySpawner CreateOrSelectEntitySpawner()
        {
            return CreateOrSelectEntitySpawner(FindPrefabRegistry());
        }

        public static GolemEntitySpawner CreateOrSelectEntitySpawner(GolemPrefabRegistry registry)
        {
            var spawner = UnityEngine.Object.FindObjectOfType<GolemEntitySpawner>();
            if (spawner == null)
            {
                var gameObject = GameObject.Find(SpawnerObjectName);
                if (gameObject == null)
                {
                    gameObject = new GameObject(SpawnerObjectName);
                    Undo.RegisterCreatedObjectUndo(gameObject, "Create Golem Entity Spawner");
                }
                spawner = gameObject.GetComponent<GolemEntitySpawner>();
                if (spawner == null)
                {
                    spawner = Undo.AddComponent<GolemEntitySpawner>(gameObject);
                }
                Debug.Log("Created Golem entity spawner.");
            }

            if (registry != null)
            {
                AssignPrefabRegistry(spawner, registry);
            }

            Selection.activeObject = spawner.gameObject;
            EditorGUIUtility.PingObject(spawner.gameObject);
            MarkActiveSceneDirty();
            return spawner;
        }

        public static GameObject CreateOrSelectClientObject()
        {
            var existingClient = UnityEngine.Object.FindObjectOfType<GolemClientBehaviour>();
            var gameObject = existingClient != null ? existingClient.gameObject : GameObject.Find(ClientObjectName);
            if (gameObject == null)
            {
                gameObject = new GameObject(ClientObjectName);
                Undo.RegisterCreatedObjectUndo(gameObject, "Create Golem Client");
                Debug.Log("Created Golem client object.");
            }

            Selection.activeObject = gameObject;
            EditorGUIUtility.PingObject(gameObject);
            MarkActiveSceneDirty();
            return gameObject;
        }

        public static GolemPrefabRegistry FindPrefabRegistry()
        {
            var guids = AssetDatabase.FindAssets("t:GolemPrefabRegistry");
            foreach (var guid in guids)
            {
                var path = AssetDatabase.GUIDToAssetPath(guid);
                var registry = AssetDatabase.LoadAssetAtPath<GolemPrefabRegistry>(path);
                if (registry != null)
                {
                    return registry;
                }
            }
            return null;
        }

        private static void AssignPrefabRegistry(GolemEntitySpawner spawner, GolemPrefabRegistry registry)
        {
            var serialized = new SerializedObject(spawner);
            serialized.FindProperty("prefabRegistry").objectReferenceValue = registry;
            serialized.ApplyModifiedProperties();
        }

        private static void EnsureAssetFolder(string assetFolder)
        {
            if (AssetDatabase.IsValidFolder(assetFolder))
            {
                return;
            }

            var parts = assetFolder.Split('/');
            var current = parts[0];
            for (var i = 1; i < parts.Length; i++)
            {
                var next = current + "/" + parts[i];
                if (!AssetDatabase.IsValidFolder(next))
                {
                    AssetDatabase.CreateFolder(current, parts[i]);
                }
                current = next;
            }
        }

        private static void MarkActiveSceneDirty()
        {
            var scene = SceneManager.GetActiveScene();
            if (scene.IsValid())
            {
                EditorSceneManager.MarkSceneDirty(scene);
            }
        }
    }
}
