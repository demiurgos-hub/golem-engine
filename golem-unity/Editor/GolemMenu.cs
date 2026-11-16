using UnityEditor;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Provides Unity editor commands under Tools > Golem.</summary>
    public static class GolemMenu
    {
        [MenuItem("Tools/Golem/Generate Code", priority = 0)]
        public static void GenerateCode()
        {
            GolemCodegenRunner.GenerateCode();
        }

        [MenuItem("Tools/Golem/Scribe/Export All", priority = 5)]
        public static void ScribeExportAll()
        {
            GolemScribeScheduler.RequestExportAll();
        }

        [MenuItem("Tools/Golem/Scribe/Validate", priority = 6)]
        public static void ScribeValidate()
        {
            GolemSetupValidator.ValidateScribe();
        }

        [MenuItem("Tools/Golem/Run Server", priority = 10)]
        public static void RunServer()
        {
            GolemServerRunner.RunServer();
        }

        [MenuItem("Tools/Golem/Validate Setup", priority = 20)]
        public static void ValidateSetup()
        {
            GolemSetupValidator.ValidateSetup();
        }

        [MenuItem("Tools/Golem/Configure Current Scene", priority = 40)]
        public static void ConfigureCurrentScene()
        {
            GolemSceneSetup.ConfigureCurrentScene();
        }

        [MenuItem("Tools/Golem/Create/Prefab Registry", priority = 60)]
        public static void CreatePrefabRegistry()
        {
            GolemSceneSetup.CreateOrSelectPrefabRegistry();
        }

        [MenuItem("Tools/Golem/Create/Entity Spawner", priority = 61)]
        public static void CreateEntitySpawner()
        {
            GolemSceneSetup.CreateOrSelectEntitySpawner();
        }

        [MenuItem("Tools/Golem/Create/Client Object", priority = 62)]
        public static void CreateClientObject()
        {
            GolemSceneSetup.CreateOrSelectClientObject();
        }

        [MenuItem("Tools/Golem/Settings...", priority = 100)]
        public static void OpenSettings()
        {
            SettingsService.OpenProjectSettings("Project/Golem");
        }
    }
}
