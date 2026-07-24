using UnityEditor;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Provides the top-level Unity editor menu for Golem project workflows.</summary>
    public static class GolemMenu
    {
        [MenuItem("Golem/Generate Code", priority = 0)]
        public static void GenerateCode()
        {
            GolemCodegenRunner.GenerateCode();
        }

        [MenuItem("Golem/Scribe/Export All", priority = 5)]
        public static void ScribeExportAll()
        {
            GolemScribeScheduler.RequestExportAll();
        }

        [MenuItem("Golem/Run Server", priority = 10)]
        public static void RunServer()
        {
            GolemServerRunner.RunServer();
        }

        [MenuItem("Golem/Validate Setup", priority = 20)]
        public static void ValidateSetup()
        {
            GolemSetupValidator.ValidateSetup();
        }

        [MenuItem("Golem/Configure Current Scene", priority = 40)]
        public static void ConfigureCurrentScene()
        {
            GolemSceneSetup.ConfigureCurrentScene();
        }

        [MenuItem("Golem/Create/Prefab Registry", priority = 60)]
        public static void CreatePrefabRegistry()
        {
            GolemSceneSetup.CreateOrSelectPrefabRegistry();
        }

        [MenuItem("Golem/Create/Entity Spawner", priority = 61)]
        public static void CreateEntitySpawner()
        {
            GolemSceneSetup.CreateOrSelectEntitySpawner();
        }

        [MenuItem("Golem/Create/Client Object", priority = 62)]
        public static void CreateClientObject()
        {
            GolemSceneSetup.CreateOrSelectClientObject();
        }

        [MenuItem("Golem/Settings...", priority = 100)]
        public static void OpenSettings()
        {
            SettingsService.OpenProjectSettings("Project/Golem");
        }
    }
}
