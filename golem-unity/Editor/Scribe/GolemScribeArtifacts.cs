using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Safe filesystem helpers for Scribe-owned generated artifacts.</summary>
    public static class GolemScribeArtifacts
    {
        /// <summary>Optional test hook: invoked after each successful artifact write; throw to force rollback.</summary>
        internal static Action<string> AfterWriteHookForTests;

        /// <summary>Result of reconciling desired artifacts against disk and the prior manifest.</summary>
        public sealed class ReconcileResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public readonly List<GolemScribeArtifactRecord> ManifestRecords = new List<GolemScribeArtifactRecord>();
            public bool EntitySchemaBytesChanged;
            public bool AnyBytesChanged;
        }

        private sealed class PathMutation
        {
            public string AbsolutePath;
            public string PreviousContent;
            public bool Existed;
            public bool Deleted;
        }

        /// <summary>
        /// Reconciles one artifact kind: writes/updates desired managed files, deletes orphaned
        /// Scribe-owned files of that kind, and rebuilds the manifest while preserving other kinds.
        /// Mutations are applied transactionally with rollback on failure.
        /// Never deletes or overwrites handwritten (non-Scribe-marked) files.
        /// </summary>
        public static ReconcileResult ReconcileKind(
            string projectRoot,
            string kind,
            IReadOnlyList<GolemScribeArtifactRecord> previous,
            IReadOnlyList<GolemScribeArtifactRecord> desiredForKind)
        {
            var result = new ReconcileResult();
            previous = previous ?? Array.Empty<GolemScribeArtifactRecord>();
            desiredForKind = desiredForKind ?? Array.Empty<GolemScribeArtifactRecord>();

            if (!TryNormalizeProjectRoot(projectRoot, out var rootFull, out var rootError))
            {
                result.Errors.Add(rootError);
                return result;
            }

            var desiredByPath = new Dictionary<string, GolemScribeArtifactRecord>(StringComparer.Ordinal);
            foreach (var record in desiredForKind)
            {
                if (record == null || string.IsNullOrEmpty(record.Path))
                {
                    result.Errors.Add("Scribe artifact is missing a relative path.");
                    continue;
                }

                if (!string.Equals(record.Kind, kind, StringComparison.Ordinal))
                {
                    result.Errors.Add($"Scribe artifact '{record.Path}' has kind '{record.Kind}', expected '{kind}'.");
                    continue;
                }

                if (record.PendingContent == null)
                {
                    result.Errors.Add($"Scribe artifact '{record.Path}' is missing generated content.");
                    continue;
                }

                if (!TryResolveContainedPath(rootFull, record.Path, out _, out var normalized, out var pathError))
                {
                    result.Errors.Add(pathError);
                    continue;
                }

                if (desiredByPath.ContainsKey(normalized))
                {
                    result.Errors.Add($"Duplicate Scribe output path '{normalized}'.");
                    continue;
                }

                desiredByPath[normalized] = new GolemScribeArtifactRecord
                {
                    Kind = record.Kind,
                    SourceGuid = record.SourceGuid,
                    Entity = record.Entity,
                    Path = normalized,
                    PendingContent = record.PendingContent,
                    Hash = GolemScribeManifest.ComputeContentHash(record.PendingContent)
                };
            }

            if (result.Errors.Count > 0)
            {
                return result;
            }

            foreach (var record in desiredByPath.Values)
            {
                if (!TryResolveContainedPath(rootFull, record.Path, out var absolute, out _, out var pathError))
                {
                    result.Errors.Add(pathError);
                    continue;
                }

                if (!CanWriteManagedPath(absolute, out _))
                {
                    result.Errors.Add(
                        $"Refusing to overwrite handwritten file '{record.Path}'. Move it or remove the conflicting Scribe source.");
                }
            }

            var previousOfKind = previous
                .Where(r => r != null && string.Equals(r.Kind, kind, StringComparison.Ordinal))
                .ToList();
            var desiredPaths = new HashSet<string>(desiredByPath.Keys, StringComparer.Ordinal);
            var orphans = new List<(string relative, string absolute)>();
            foreach (var prior in previousOfKind)
            {
                if (string.IsNullOrEmpty(prior.Path))
                {
                    result.Errors.Add("Prior Scribe manifest record is missing a path.");
                    continue;
                }

                if (!TryResolveContainedPath(rootFull, prior.Path, out var absolute, out var relative, out var pathError))
                {
                    result.Errors.Add("Hostile or corrupt manifest path rejected: " + pathError);
                    continue;
                }

                if (desiredPaths.Contains(relative))
                {
                    continue;
                }

                if (!File.Exists(absolute))
                {
                    continue;
                }

                var existing = File.ReadAllText(absolute);
                if (!GolemYamlWriter.IsScribeOwned(existing))
                {
                    result.Warnings.Add(
                        $"Left handwritten file '{relative}' in place during Scribe orphan cleanup.");
                    continue;
                }

                orphans.Add((relative, absolute));
            }

            if (result.Errors.Count > 0)
            {
                return result;
            }

            var preserved = new List<GolemScribeArtifactRecord>();
            foreach (var prior in previous)
            {
                if (prior == null || string.Equals(prior.Kind, kind, StringComparison.Ordinal))
                {
                    continue;
                }

                if (string.IsNullOrEmpty(prior.Path))
                {
                    result.Errors.Add("Preserved manifest record has hostile or corrupt path: missing path");
                    continue;
                }

                if (!TryResolveContainedPath(rootFull, prior.Path, out _, out var normalized, out var pathError))
                {
                    result.Errors.Add("Preserved manifest record has hostile or corrupt path: " + pathError);
                    continue;
                }

                preserved.Add(new GolemScribeArtifactRecord
                {
                    Kind = prior.Kind,
                    SourceGuid = prior.SourceGuid,
                    Entity = prior.Entity,
                    Path = normalized,
                    Hash = prior.Hash
                });
            }

            if (result.Errors.Count > 0)
            {
                return result;
            }

            var mutations = new List<PathMutation>();
            string previousManifestText = null;
            var manifestPath = GolemScribeManifest.ManifestPath(rootFull);
            if (File.Exists(manifestPath))
            {
                previousManifestText = File.ReadAllText(manifestPath);
            }

            try
            {
                foreach (var record in desiredByPath.Values.OrderBy(r => r.Path, StringComparer.Ordinal))
                {
                    TryResolveContainedPath(rootFull, record.Path, out var absolute, out _, out _);
                    var content = record.PendingContent;
                    if (File.Exists(absolute))
                    {
                        var existing = File.ReadAllText(absolute);
                        if (string.Equals(existing, content, StringComparison.Ordinal))
                        {
                            result.ManifestRecords.Add(StripPending(record));
                            continue;
                        }

                        mutations.Add(new PathMutation
                        {
                            AbsolutePath = absolute,
                            PreviousContent = existing,
                            Existed = true
                        });
                    }
                    else
                    {
                        mutations.Add(new PathMutation
                        {
                            AbsolutePath = absolute,
                            PreviousContent = null,
                            Existed = false
                        });
                    }

                    if (!TryWriteFile(absolute, content, out var writeError))
                    {
                        throw new IOException(writeError);
                    }

                    AfterWriteHookForTests?.Invoke(absolute);

                    result.AnyBytesChanged = true;
                    if (kind == GolemScribeConstants.ArtifactKindEntitySchema)
                    {
                        result.EntitySchemaBytesChanged = true;
                    }
                    result.ManifestRecords.Add(StripPending(record));
                }

                foreach (var orphan in orphans.OrderBy(o => o.relative, StringComparer.Ordinal))
                {
                    var existing = File.ReadAllText(orphan.absolute);
                    mutations.Add(new PathMutation
                    {
                        AbsolutePath = orphan.absolute,
                        PreviousContent = existing,
                        Existed = true,
                        Deleted = true
                    });
                    File.Delete(orphan.absolute);
                    result.AnyBytesChanged = true;
                    if (kind == GolemScribeConstants.ArtifactKindEntitySchema)
                    {
                        result.EntitySchemaBytesChanged = true;
                    }
                }

                var manifestRecords = preserved.Concat(result.ManifestRecords)
                    .OrderBy(r => r.Path ?? string.Empty, StringComparer.Ordinal)
                    .ToList();

                if (GolemScribeManifest.Write(rootFull, manifestRecords))
                {
                    result.AnyBytesChanged = true;
                }

                result.ManifestRecords.Clear();
                result.ManifestRecords.AddRange(manifestRecords);
                return result;
            }
            catch (Exception ex)
            {
                Rollback(mutations, manifestPath, previousManifestText);
                result.Errors.Add(ex.Message);
                result.ManifestRecords.Clear();
                result.EntitySchemaBytesChanged = false;
                result.AnyBytesChanged = false;
                return result;
            }
        }

        /// <summary>Writes a managed file when content differs. Returns true when bytes changed.</summary>
        public static bool WriteManagedFile(string absolutePath, string content)
        {
            if (File.Exists(absolutePath))
            {
                var existing = File.ReadAllText(absolutePath);
                if (!GolemYamlWriter.IsScribeOwned(existing))
                {
                    throw new InvalidOperationException(
                        $"Refusing to overwrite handwritten file '{absolutePath}'.");
                }

                if (string.Equals(existing, content, StringComparison.Ordinal))
                {
                    return false;
                }
            }

            if (!TryWriteFile(absolutePath, content, out var error))
            {
                throw new InvalidOperationException(error);
            }

            return true;
        }

        /// <summary>Returns true when an existing file is Scribe-owned or the path is free.</summary>
        public static bool CanWriteManagedPath(string absolutePath, out string error)
        {
            error = null;
            if (!File.Exists(absolutePath))
            {
                return true;
            }

            var existing = File.ReadAllText(absolutePath);
            if (GolemYamlWriter.IsScribeOwned(existing))
            {
                return true;
            }

            error = $"Path is occupied by a handwritten file: {absolutePath}";
            return false;
        }

        /// <summary>
        /// Resolves a project-relative path that is strictly contained under <paramref name="projectRoot"/>.
        /// Rejects absolute paths, empty paths, and <c>..</c> escapes.
        /// </summary>
        public static bool TryResolveContainedPath(
            string projectRoot,
            string relativePath,
            out string absolutePath,
            out string normalizedRelative,
            out string error)
        {
            absolutePath = null;
            normalizedRelative = null;
            error = null;

            if (!TryNormalizeProjectRoot(projectRoot, out var rootFull, out error))
            {
                return false;
            }

            if (string.IsNullOrWhiteSpace(relativePath))
            {
                error = "Artifact path is empty.";
                return false;
            }

            var raw = relativePath.Replace('\\', '/').Trim();
            if (raw.Length == 0)
            {
                error = "Artifact path is empty.";
                return false;
            }

            if (raw.StartsWith("/", StringComparison.Ordinal) ||
                raw.StartsWith("\\", StringComparison.Ordinal) ||
                Path.IsPathRooted(relativePath) ||
                (raw.Length >= 2 && char.IsLetter(raw[0]) && raw[1] == ':'))
            {
                error = $"Artifact path must be project-relative, not absolute: '{relativePath}'.";
                return false;
            }

            var parts = raw.Split(new[] { '/' }, StringSplitOptions.RemoveEmptyEntries);
            if (parts.Length == 0)
            {
                error = $"Artifact path is empty: '{relativePath}'.";
                return false;
            }

            foreach (var part in parts)
            {
                if (part == "." || part == "..")
                {
                    error = $"Artifact path escapes the project root: '{relativePath}'.";
                    return false;
                }
            }

            normalizedRelative = string.Join("/", parts);
            var combined = rootFull;
            foreach (var part in parts)
            {
                combined = Path.Combine(combined, part);
            }

            absolutePath = Path.GetFullPath(combined);
            var rootPrefix = rootFull.EndsWith(Path.DirectorySeparatorChar.ToString(), StringComparison.Ordinal)
                ? rootFull
                : rootFull + Path.DirectorySeparatorChar;
            var absoluteWithSep = absolutePath.EndsWith(Path.DirectorySeparatorChar.ToString(), StringComparison.Ordinal)
                ? absolutePath
                : absolutePath + Path.DirectorySeparatorChar;

            if (!absolutePath.Equals(rootFull, StringComparison.OrdinalIgnoreCase) &&
                !absoluteWithSep.StartsWith(rootPrefix, StringComparison.OrdinalIgnoreCase))
            {
                error = $"Artifact path escapes the project root: '{relativePath}'.";
                absolutePath = null;
                normalizedRelative = null;
                return false;
            }

            return true;
        }

        /// <summary>Converts a project-relative path to an absolute filesystem path under the project root.</summary>
        public static string ToAbsolutePath(string projectRoot, string relativePath)
        {
            if (!TryResolveContainedPath(projectRoot, relativePath, out var absolute, out _, out var error))
            {
                throw new InvalidOperationException(error);
            }
            return absolute;
        }

        /// <summary>Normalizes a relative artifact path to forward slashes without validating containment.</summary>
        public static string NormalizeRelativePath(string relativePath)
        {
            return (relativePath ?? string.Empty).Replace('\\', '/').Trim();
        }

        private static bool TryNormalizeProjectRoot(string projectRoot, out string rootFull, out string error)
        {
            rootFull = null;
            error = null;
            if (string.IsNullOrWhiteSpace(projectRoot))
            {
                error = "Golem project root is required.";
                return false;
            }

            try
            {
                rootFull = Path.GetFullPath(projectRoot);
            }
            catch (Exception ex)
            {
                error = "Invalid Golem project root: " + ex.Message;
                return false;
            }

            if (!Directory.Exists(rootFull))
            {
                error = $"Golem project root does not exist: {rootFull}";
                return false;
            }

            return true;
        }

        private static void Rollback(List<PathMutation> mutations, string manifestPath, string previousManifestText)
        {
            for (var i = mutations.Count - 1; i >= 0; i--)
            {
                var mutation = mutations[i];
                try
                {
                    if (mutation.Deleted || mutation.Existed)
                    {
                        var directory = Path.GetDirectoryName(mutation.AbsolutePath);
                        if (!string.IsNullOrEmpty(directory))
                        {
                            Directory.CreateDirectory(directory);
                        }
                        File.WriteAllText(mutation.AbsolutePath, mutation.PreviousContent ?? string.Empty, Utf8NoBom);
                    }
                    else if (File.Exists(mutation.AbsolutePath))
                    {
                        File.Delete(mutation.AbsolutePath);
                    }
                }
                catch
                {
                    // Best-effort rollback; original error is reported to the caller.
                }
            }

            try
            {
                if (previousManifestText != null)
                {
                    File.WriteAllText(manifestPath, previousManifestText, Utf8NoBom);
                }
                else if (File.Exists(manifestPath))
                {
                    File.Delete(manifestPath);
                }
            }
            catch
            {
                // Best-effort rollback.
            }
        }

        private static GolemScribeArtifactRecord StripPending(GolemScribeArtifactRecord source)
        {
            return new GolemScribeArtifactRecord
            {
                Kind = source.Kind,
                SourceGuid = source.SourceGuid,
                Entity = source.Entity,
                Path = source.Path,
                Hash = source.Hash ?? (source.PendingContent != null
                    ? GolemScribeManifest.ComputeContentHash(source.PendingContent)
                    : null)
            };
        }

        private static readonly UTF8Encoding Utf8NoBom = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false);

        private static bool TryWriteFile(string absolutePath, string content, out string error)
        {
            error = null;
            try
            {
                var directory = Path.GetDirectoryName(absolutePath);
                if (!string.IsNullOrEmpty(directory))
                {
                    Directory.CreateDirectory(directory);
                }

                var tempPath = absolutePath + ".tmp";
                File.WriteAllText(tempPath, content ?? string.Empty, Utf8NoBom);
                if (File.Exists(absolutePath))
                {
                    File.Delete(absolutePath);
                }
                File.Move(tempPath, absolutePath);
                return true;
            }
            catch (Exception ex)
            {
                error = $"Failed to write '{absolutePath}': {ex.Message}";
                return false;
            }
        }
    }
}
