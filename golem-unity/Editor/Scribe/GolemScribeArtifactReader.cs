using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.IO;

namespace GolemEngine.Unity.Editor
{
    internal enum ScribeArtifactStatus { Missing, Handwritten, Owned, InvalidPath, ReadFailure }

    internal sealed class ScribeArtifactSnapshot
    {
        internal string Path { get; }
        internal string AbsolutePath { get; }
        internal ScribeArtifactStatus Status { get; }
        internal string Content { get; }
        internal string Hash { get; }
        internal string Error { get; }

        internal ScribeArtifactSnapshot(string path, string absolutePath, ScribeArtifactStatus status,
            string content = null, string error = null)
        {
            Path = path;
            AbsolutePath = absolutePath;
            Status = status;
            Content = content;
            Hash = content == null ? null : GolemScribeManifest.ComputeContentHash(content);
            Error = error;
        }
    }

    internal static class GolemScribeArtifactReader
    {
        internal static ScribeArtifactSnapshot Read(string projectRoot, string relativePath)
        {
            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, relativePath, out var absolute, out var normalized, out var error))
            {
                return new ScribeArtifactSnapshot(relativePath, null, ScribeArtifactStatus.InvalidPath, error: error);
            }
            return ReadAbsolute(absolute, normalized);
        }

        internal static ScribeArtifactSnapshot ReadAbsolute(string absolutePath, string relativePath)
        {
            try
            {
                // File.Exists also returns false for access/IO errors; only absence is a missing artifact.
                var content = File.ReadAllText(absolutePath);
                return new ScribeArtifactSnapshot(relativePath, absolutePath,
                    GolemYamlWriter.IsScribeOwned(content) ? ScribeArtifactStatus.Owned : ScribeArtifactStatus.Handwritten,
                    content);
            }
            catch (FileNotFoundException)
            {
                return new ScribeArtifactSnapshot(relativePath, absolutePath, ScribeArtifactStatus.Missing);
            }
            catch (DirectoryNotFoundException)
            {
                return new ScribeArtifactSnapshot(relativePath, absolutePath, ScribeArtifactStatus.Missing);
            }
            catch (Exception ex)
            {
                return new ScribeArtifactSnapshot(relativePath, absolutePath, ScribeArtifactStatus.ReadFailure,
                    error: $"Failed to read Scribe artifact '{absolutePath}': {ex.Message}");
            }
        }

        internal static IReadOnlyDictionary<string, ScribeArtifactSnapshot> Capture(GolemScribeArtifactPlan.Inputs inputs)
        {
            var snapshots = new Dictionary<string, ScribeArtifactSnapshot>(StringComparer.Ordinal);
            foreach (var path in inputs.Paths)
            {
                snapshots.Add(path, Read(inputs.Root, path));
            }
            return new ReadOnlyDictionary<string, ScribeArtifactSnapshot>(snapshots);
        }
    }
}
