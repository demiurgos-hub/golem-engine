using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using UnityEngine;
using Debug = UnityEngine.Debug;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Opens the configured Golem server command in an external terminal.</summary>
    public static class GolemServerRunner
    {
        public static void RunServer()
        {
            var settings = GolemUnityEditorSettings.instance;
            var projectRoot = settings.ProjectRoot;
            if (string.IsNullOrWhiteSpace(projectRoot) || !Directory.Exists(projectRoot))
            {
                Debug.LogError($"Golem project root does not exist: {projectRoot}");
                return;
            }

            var command = settings.ServerCommand;
            if (string.IsNullOrWhiteSpace(command))
            {
                Debug.LogError("Golem server command is empty. Configure it in Project Settings > Golem.");
                return;
            }

            try
            {
                Process.Start(CreateTerminalStartInfo(projectRoot, command));
                Debug.Log($"Opened Golem server terminal in `{projectRoot}` with `{command}`.");
            }
            catch (Win32Exception ex)
            {
                Debug.LogError($"Failed to open Golem server terminal: {ex.Message}");
            }
            catch (Exception ex)
            {
                Debug.LogError($"Failed to run Golem server command `{command}`: {ex.Message}");
            }
        }

        private static ProcessStartInfo CreateTerminalStartInfo(string projectRoot, string command)
        {
            if (Application.platform == RuntimePlatform.WindowsEditor)
            {
                return new ProcessStartInfo
                {
                    FileName = "powershell.exe",
                    Arguments = $"-NoExit -Command \"Set-Location -LiteralPath '{EscapePowerShellSingleQuoted(projectRoot)}'; {command}\"",
                    UseShellExecute = true,
                    CreateNoWindow = false
                };
            }

            if (Application.platform == RuntimePlatform.OSXEditor)
            {
                return new ProcessStartInfo
                {
                    FileName = "osascript",
                    Arguments = $"-e \"tell application \\\"Terminal\\\" to do script \\\"cd {EscapeShellDoubleQuoted(projectRoot)} && {EscapeAppleScript(command)}\\\"\"",
                    UseShellExecute = false,
                    CreateNoWindow = true
                };
            }

            return new ProcessStartInfo
            {
                FileName = "sh",
                Arguments = $"-c \"cd {EscapeShellDoubleQuoted(projectRoot)} && {command}; exec $SHELL\"",
                UseShellExecute = true,
                CreateNoWindow = false
            };
        }

        private static string EscapePowerShellSingleQuoted(string value)
        {
            return value.Replace("'", "''");
        }

        private static string EscapeShellDoubleQuoted(string value)
        {
            return "\"" + value.Replace("\\", "\\\\").Replace("\"", "\\\"") + "\"";
        }

        private static string EscapeAppleScript(string value)
        {
            return value.Replace("\\", "\\\\").Replace("\"", "\\\"");
        }
    }
}
