using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using UnityEditor;
using UnityEngine;
using Debug = UnityEngine.Debug;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Runs the configured Golem code generator from the Unity editor.</summary>
    public static class GolemCodegenRunner
    {
        public static void GenerateCode()
        {
            var settings = GolemUnityEditorSettings.instance;
            var projectRoot = settings.ProjectRoot;
            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                Debug.LogError($"Golem project root does not exist: {projectRoot}");
                return;
            }

            var configPath = Path.Combine(projectRoot, "golem.yaml");
            if (!File.Exists(configPath))
            {
                Debug.LogError($"Could not find golem.yaml at {configPath}. Configure the Golem project root in Project Settings > Golem.");
                return;
            }

            if (!TrySplitCommand(settings.BakeCommand, out var fileName, out var arguments))
            {
                Debug.LogError("Golem bake command is empty. Configure it in Project Settings > Golem.");
                return;
            }

            var resolvedFileName = ResolveExecutablePath(projectRoot, fileName);
            var startInfo = new ProcessStartInfo
            {
                FileName = resolvedFileName,
                Arguments = arguments,
                WorkingDirectory = projectRoot,
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true
            };

            try
            {
                Debug.Log($"Running Golem bake command `{settings.BakeCommand}` in `{projectRoot}`.");
                using (var process = Process.Start(startInfo))
                {
                    if (process == null)
                    {
                        Debug.LogError($"Failed to start Golem bake command: {settings.BakeCommand}");
                        return;
                    }

                    var stdout = process.StandardOutput.ReadToEnd();
                    var stderr = process.StandardError.ReadToEnd();
                    process.WaitForExit();

                    LogProcessOutput(stdout, stderr);
                    if (process.ExitCode != 0)
                    {
                        Debug.LogError($"Golem code generation failed with exit code {process.ExitCode}.");
                        return;
                    }
                }
            }
            catch (Win32Exception ex)
            {
                Debug.LogError(BuildMissingCommandMessage(settings, projectRoot, fileName, resolvedFileName, ex));
                return;
            }
            catch (Exception ex)
            {
                Debug.LogError($"Failed to run Golem bake command `{settings.BakeCommand}`: {ex.Message}");
                return;
            }

            AssetDatabase.Refresh();
            Debug.Log("Golem code generation completed.");
        }

        private static string ResolveExecutablePath(string projectRoot, string fileName)
        {
            if (Path.IsPathRooted(fileName) || !LooksLikePath(fileName))
            {
                return fileName;
            }

            var candidate = Path.GetFullPath(Path.Combine(projectRoot, fileName));
            return File.Exists(candidate) ? candidate : fileName;
        }

        private static bool LooksLikePath(string fileName)
        {
            return fileName.Contains("/") || fileName.Contains("\\");
        }

        private static string BuildMissingCommandMessage(
            GolemUnityEditorSettings settings,
            string projectRoot,
            string fileName,
            string resolvedFileName,
            Win32Exception ex)
        {
            return "Failed to start Golem bake command.\n"
                + $"Command: {settings.BakeCommand}\n"
                + $"Executable: {fileName}\n"
                + $"Resolved executable: {resolvedFileName}\n"
                + $"Project Root: {projectRoot}\n"
                + $"Error: {ex.Message}\n\n"
                + "Fixes:\n"
                + "- Use Project Settings > Golem > Bake Command Mode > Go Command if Go is installed.\n"
                + "- Install golem-bake on PATH and choose Path Executable.\n"
                + "- Choose Custom Command and point at a project-local binary such as Tools/golem-bake.exe.";
        }

        private static void LogProcessOutput(string stdout, string stderr)
        {
            if (!string.IsNullOrWhiteSpace(stdout))
            {
                Debug.Log(stdout.TrimEnd());
            }
            if (!string.IsNullOrWhiteSpace(stderr))
            {
                Debug.LogError(stderr.TrimEnd());
            }
        }

        private static bool TrySplitCommand(string command, out string fileName, out string arguments)
        {
            fileName = string.Empty;
            arguments = string.Empty;
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
                fileName = command.Substring(1, closingQuote - 1);
                arguments = command.Substring(closingQuote + 1).TrimStart();
                return !string.IsNullOrEmpty(fileName);
            }

            var firstSpace = command.IndexOfAny(new[] { ' ', '\t' });
            if (firstSpace < 0)
            {
                fileName = command;
                return true;
            }

            fileName = command.Substring(0, firstSpace);
            arguments = command.Substring(firstSpace + 1).TrimStart();
            return !string.IsNullOrEmpty(fileName);
        }
    }
}
