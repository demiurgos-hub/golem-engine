using System.IO;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Checks common Golem Unity editor setup requirements.</summary>
    public static class GolemSetupValidator
    {
        public static void ValidateSetup()
        {
            var settings = GolemUnityEditorSettings.instance;
            var issues = 0;
            var warnings = 0;

            CheckProjectRoot(settings, ref issues);
            CheckBakeCommand(settings, ref warnings);
            CheckGeneratedOutput(settings, ref warnings);
            CheckPrefabRegistry(ref warnings);
            CheckEntitySpawner(ref warnings);

            if (issues == 0 && warnings == 0)
            {
                Debug.Log("Golem setup validation passed.");
            }
            else
            {
                Debug.Log($"Golem setup validation completed with {issues} error(s) and {warnings} warning(s).");
            }
        }

        private static void CheckProjectRoot(GolemUnityEditorSettings settings, ref int issues)
        {
            var projectRoot = settings.ProjectRoot;
            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                issues++;
                Debug.LogError($"Golem project root does not exist: {projectRoot}. Configure it in Project Settings > Golem.");
                return;
            }

            var configPath = Path.Combine(projectRoot, "golem.yaml");
            if (!File.Exists(configPath))
            {
                issues++;
                Debug.LogError($"Could not find golem.yaml at {configPath}. Configure the Golem project root in Project Settings > Golem.");
                return;
            }

            Debug.Log($"Golem project root OK: {projectRoot}");
        }

        private static void CheckBakeCommand(GolemUnityEditorSettings settings, ref int warnings)
        {
            if (!TryGetExecutable(settings.BakeCommand, out var executable))
            {
                warnings++;
                Debug.LogWarning("Golem bake command is empty. Configure it in Project Settings > Golem.");
                return;
            }

            if (settings.BakeCommandMode == GolemBakeCommandMode.GoCommand)
            {
                if (!ExecutableExistsOnPath(executable))
                {
                    warnings++;
                    Debug.LogWarning("The Go command is selected for Golem code generation, but `go` was not found on PATH. Install Go or choose another bake command mode in Project Settings > Golem.");
                    return;
                }
                Debug.Log("Golem bake command OK: Go is available on PATH.");
                return;
            }

            if (Path.IsPathRooted(executable) || executable.Contains("/") || executable.Contains("\\"))
            {
                var fullPath = Path.IsPathRooted(executable)
                    ? executable
                    : Path.GetFullPath(Path.Combine(settings.ProjectRoot, executable));
                if (!File.Exists(fullPath))
                {
                    warnings++;
                    Debug.LogWarning($"Golem bake executable was not found: {fullPath}. Update Project Settings > Golem or choose the Go command preset.");
                    return;
                }
                Debug.Log($"Golem bake executable OK: {fullPath}");
                return;
            }

            if (!ExecutableExistsOnPath(executable))
            {
                warnings++;
                Debug.LogWarning($"Golem bake executable `{executable}` was not found on PATH. Install it on PATH, choose the Go command preset, or configure a custom executable.");
                return;
            }

            Debug.Log($"Golem bake executable OK: {executable} is available on PATH.");
        }

        private static void CheckGeneratedOutput(GolemUnityEditorSettings settings, ref int warnings)
        {
            if (!GolemYamlConfig.TryGetCSharpClientAssetOut(settings.ProjectRoot, out var outputFolder))
            {
                warnings++;
                Debug.LogWarning("No integrations.csharp-client.out value was found in golem.yaml. Add the csharp-client integration so Unity can compile generated C# client code.");
                return;
            }

            if (!AssetDatabase.IsValidFolder(outputFolder))
            {
                warnings++;
                Debug.LogWarning($"Generated C# output folder from golem.yaml does not exist in Unity assets: {outputFolder}. Run Golem > Generate Code.");
                return;
            }

            var clientFiles = Directory.GetFiles(outputFolder, "Client.cs", SearchOption.AllDirectories);
            var managerFiles = Directory.GetFiles(outputFolder, "EntityManager.cs", SearchOption.AllDirectories);
            if (clientFiles.Length == 0 || managerFiles.Length == 0)
            {
                warnings++;
                Debug.LogWarning($"Generated C# client files were not found under {outputFolder}. Expected Client.cs and EntityManager.cs from the csharp-client integration.");
                return;
            }

            Debug.Log($"Generated C# client output OK: {outputFolder} (from golem.yaml)");
        }

        private static void CheckPrefabRegistry(ref int warnings)
        {
            var registry = GolemSceneSetup.FindPrefabRegistry();
            if (registry == null)
            {
                warnings++;
                Debug.LogWarning("No GolemPrefabRegistry asset found. Use Golem > Create > Prefab Registry.");
                return;
            }

            Debug.Log($"Golem prefab registry OK: {AssetDatabase.GetAssetPath(registry)}");
        }

        private static void CheckEntitySpawner(ref int warnings)
        {
            var spawner = Object.FindObjectOfType<GolemEntitySpawner>();
            if (spawner == null)
            {
                warnings++;
                Debug.LogWarning("No GolemEntitySpawner found in the current scene. Use Golem > Create > Entity Spawner.");
                return;
            }

            Debug.Log($"Golem entity spawner OK: {spawner.gameObject.name}");
        }

        private static bool TryGetExecutable(string command, out string executable)
        {
            executable = string.Empty;
            command = command?.Trim();
            if (string.IsNullOrEmpty(command))
            {
                return false;
            }

            if (command[0] == '"')
            {
                var closingQuote = command.IndexOf('"', 1);
                if (closingQuote < 0)
                {
                    return false;
                }
                executable = command.Substring(1, closingQuote - 1);
                return !string.IsNullOrEmpty(executable);
            }

            var firstSpace = command.IndexOfAny(new[] { ' ', '\t' });
            executable = firstSpace < 0 ? command : command.Substring(0, firstSpace);
            return !string.IsNullOrEmpty(executable);
        }

        private static bool ExecutableExistsOnPath(string executable)
        {
            var path = System.Environment.GetEnvironmentVariable("PATH");
            if (string.IsNullOrEmpty(path))
            {
                return false;
            }

            foreach (var dir in path.Split(Path.PathSeparator))
            {
                if (string.IsNullOrWhiteSpace(dir))
                {
                    continue;
                }

                if (File.Exists(Path.Combine(dir, executable)) || File.Exists(Path.Combine(dir, executable + ".exe")))
                {
                    return true;
                }
            }
            return false;
        }
    }
}
