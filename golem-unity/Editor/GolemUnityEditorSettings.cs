using System.IO;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Selects how the editor runs golem-bake.</summary>
    public enum GolemBakeCommandMode
    {
        GoCommand,
        PathExecutable,
        CustomCommand
    }

    /// <summary>Stores project-scoped editor settings for Golem Unity tooling.</summary>
    [FilePath("ProjectSettings/GolemEngineUnitySettings.asset", FilePathAttribute.Location.ProjectFolder)]
    public sealed class GolemUnityEditorSettings : ScriptableSingleton<GolemUnityEditorSettings>
    {
        public const string GoBakeCommand = "go run github.com/demiurgos-hub/golem-engine/cmd/golem-bake";
        public const string PathBakeCommand = "golem-bake";

        [SerializeField] private string projectRoot;
        [SerializeField] private GolemBakeCommandMode bakeCommandMode = GolemBakeCommandMode.GoCommand;
        [SerializeField] private string bakeCommand = GoBakeCommand;
        [SerializeField] private string serverCommand = "go run ./cmd/server";
        [SerializeField] private string assetFolder = "Assets/Golem";
        [SerializeField] private bool autoBakeOnExport = true;
        [SerializeField] private string footprintsPath = GolemScribeConstants.DefaultFootprintsPath;

        public string ProjectRoot
        {
            get => string.IsNullOrWhiteSpace(projectRoot) ? DefaultProjectRoot : projectRoot;
            set => projectRoot = NormalizePath(value);
        }

        /// <summary>When true, Scribe queues golem-bake after entity or catalog schema bytes change.</summary>
        public bool AutoBakeOnExport
        {
            get => autoBakeOnExport;
            set => autoBakeOnExport = value;
        }

        /// <summary>
        /// Project-root-relative path for the Scribe-managed footprints.golem.yaml artifact.
        /// Footprint changes never trigger golem-bake.
        /// </summary>
        public string FootprintsPath
        {
            get => string.IsNullOrWhiteSpace(footprintsPath)
                ? GolemScribeConstants.DefaultFootprintsPath
                : footprintsPath.Replace('\\', '/').Trim();
            set => footprintsPath = string.IsNullOrWhiteSpace(value)
                ? GolemScribeConstants.DefaultFootprintsPath
                : value.Replace('\\', '/').Trim();
        }

        public GolemBakeCommandMode BakeCommandMode
        {
            get => bakeCommandMode;
            set
            {
                bakeCommandMode = value;
                if (value != GolemBakeCommandMode.CustomCommand)
                {
                    bakeCommand = PresetCommand(value);
                }
            }
        }

        public string BakeCommand
        {
            get
            {
                if (string.IsNullOrWhiteSpace(bakeCommand))
                {
                    return PresetCommand(bakeCommandMode);
                }
                if (bakeCommandMode == GolemBakeCommandMode.GoCommand && bakeCommand == PathBakeCommand)
                {
                    return GoBakeCommand;
                }
                return bakeCommand;
            }
            set => bakeCommand = value?.Trim();
        }

        public string AssetFolder
        {
            get => string.IsNullOrWhiteSpace(assetFolder) ? "Assets/Golem" : assetFolder;
            set => assetFolder = NormalizeAssetPath(value, "Assets/Golem");
        }

        public string ServerCommand
        {
            get => string.IsNullOrWhiteSpace(serverCommand) ? "go run ./cmd/server" : serverCommand;
            set => serverCommand = value?.Trim();
        }

        public static string DefaultProjectRoot
        {
            get
            {
                var parent = Directory.GetParent(Application.dataPath);
                return parent?.FullName ?? Application.dataPath;
            }
        }

        public static void Save()
        {
            instance.Save(true);
        }

        [SettingsProvider]
        public static SettingsProvider CreateSettingsProvider()
        {
            return new SettingsProvider("Project/Golem", SettingsScope.Project)
            {
                label = "Golem",
                guiHandler = _ => instance.DrawSettingsGUI()
            };
        }

        private void DrawSettingsGUI()
        {
            EditorGUI.BeginChangeCheck();

            EditorGUILayout.LabelField("Code Generation", EditorStyles.boldLabel);
            using (new EditorGUILayout.HorizontalScope())
            {
                projectRoot = EditorGUILayout.TextField("Project Root", ProjectRoot);
                if (GUILayout.Button("Browse", GUILayout.Width(72)))
                {
                    var selected = EditorUtility.OpenFolderPanel("Golem Project Root", ProjectRoot, string.Empty);
                    if (!string.IsNullOrEmpty(selected))
                    {
                        projectRoot = selected;
                    }
                }
            }
            EditorGUILayout.HelpBox(
                "Project Root is the folder that contains golem.yaml. Generated paths in golem.yaml are relative to that folder, not necessarily the Unity project folder.",
                MessageType.Info);
            var nextMode = (GolemBakeCommandMode)EditorGUILayout.EnumPopup("Bake Command Mode", BakeCommandMode);
            if (nextMode != bakeCommandMode)
            {
                BakeCommandMode = nextMode;
            }
            bakeCommand = EditorGUILayout.TextField("Bake Command", BakeCommand);
            EditorGUILayout.HelpBox(
                "Use Go Command for the easiest setup when your game is a Go module. Use Path Executable when golem-bake is installed on PATH. Use Custom Command for a team-pinned binary such as Tools/golem-bake.exe.",
                MessageType.None);
            using (new EditorGUI.DisabledScope(true))
            {
                EditorGUILayout.TextField("Generated C# Output", GolemYamlConfig.TryGetCSharpClientAssetOut(ProjectRoot, out var csharpOut) ? csharpOut : "Not found in golem.yaml");
            }
            EditorGUILayout.HelpBox(
                "Generated output paths come from golem.yaml. Configure integrations.csharp-client.out there, then run Golem > Generate Code.",
                MessageType.None);
            serverCommand = EditorGUILayout.TextField("Server Command", ServerCommand);
            EditorGUILayout.HelpBox(
                "Golem > Run Server opens this command in an external terminal at Project Root. Unity does not own, stop, or restart that process.",
                MessageType.None);

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Golem Scribe", EditorStyles.boldLabel);
            var nextAutoBake = EditorGUILayout.Toggle("Auto Bake On Export", AutoBakeOnExport);
            if (nextAutoBake != autoBakeOnExport)
            {
                AutoBakeOnExport = nextAutoBake;
            }
            footprintsPath = EditorGUILayout.TextField("Footprints Path", FootprintsPath);
            EditorGUILayout.HelpBox(
                "When enabled, Golem Scribe runs golem-bake only after entity or catalog type/world schema bytes change. Footprint changes never invoke bake. Generated C# refreshes do not recursively re-export.",
                MessageType.None);
            if (GolemYamlConfig.TryGetProjectSchema(ProjectRoot, out var schema, out _))
            {
                using (new EditorGUI.DisabledScope(true))
                {
                    EditorGUILayout.TextField("Entity Schema", schema.EntitySchema);
                    EditorGUILayout.TextField("Types Schema", schema.TypesSchema);
                    EditorGUILayout.TextField("World Schema", schema.WorldSchema);
                    EditorGUILayout.IntField("Dimensions", schema.Dimensions);
                }
            }
            else
            {
                EditorGUILayout.HelpBox("Could not read schema paths from golem.yaml.", MessageType.Warning);
            }

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Scene Setup", EditorStyles.boldLabel);
            assetFolder = EditorGUILayout.TextField("Asset Folder", AssetFolder);

            if (EditorGUI.EndChangeCheck())
            {
                ProjectRoot = projectRoot;
                BakeCommandMode = bakeCommandMode;
                BakeCommand = bakeCommand;
                ServerCommand = serverCommand;
                AssetFolder = assetFolder;
                FootprintsPath = footprintsPath;
                Save();
            }
        }

        private static string NormalizePath(string path)
        {
            return string.IsNullOrWhiteSpace(path) ? string.Empty : path.Trim().Replace('\\', '/');
        }

        public static string ToUnityAssetPath(string path)
        {
            var normalized = NormalizePath(path);
            const string assetsSegment = "/Assets/";
            var assetsIndex = normalized.IndexOf(assetsSegment, System.StringComparison.Ordinal);
            if (assetsIndex >= 0)
            {
                return normalized.Substring(assetsIndex + 1).TrimEnd('/');
            }

            return normalized.TrimEnd('/');
        }

        private static string NormalizeAssetPath(string path, string fallback)
        {
            var normalized = ToUnityAssetPath(path);
            if (string.IsNullOrEmpty(normalized))
            {
                return fallback;
            }
            return normalized.StartsWith("Assets/", System.StringComparison.Ordinal) || normalized == "Assets" ? normalized : fallback;
        }

        private static string PresetCommand(GolemBakeCommandMode mode)
        {
            return mode == GolemBakeCommandMode.PathExecutable ? PathBakeCommand : GoBakeCommand;
        }
    }
}
