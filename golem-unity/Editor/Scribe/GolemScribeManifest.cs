using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One Scribe-managed generated artifact recorded in the committed manifest.</summary>
    public sealed class GolemScribeArtifactRecord
    {
        public string Kind;
        public string SourceGuid;
        public string Entity;
        public string Path;
        public string Hash;

        /// <summary>Generated file text awaiting a safe write. Not persisted in the manifest.</summary>
        public string PendingContent;
    }

    /// <summary>Load/save helpers for <c>scribe.golem.yaml</c>.</summary>
    public static class GolemScribeManifest
    {
        private static readonly Regex ArtifactStart = new Regex(@"^\s*-\s+kind:\s*(.+)\s*$", RegexOptions.Compiled);
        private static readonly Regex FieldLine = new Regex(@"^\s*([A-Za-z0-9_]+):\s*(.*)$", RegexOptions.Compiled);

        /// <summary>Absolute path to the managed-artifact manifest under the consumer project root.</summary>
        public static string ManifestPath(string projectRoot)
        {
            return Path.Combine(projectRoot, GolemScribeConstants.ManifestFileName);
        }

        /// <summary>Computes a lowercase hex SHA-256 hash of UTF-8 content.</summary>
        public static string ComputeContentHash(string content)
        {
            var bytes = Encoding.UTF8.GetBytes(content ?? string.Empty);
            using (var sha = SHA256.Create())
            {
                var hash = sha.ComputeHash(bytes);
                var builder = new StringBuilder(hash.Length * 2);
                foreach (var b in hash)
                {
                    builder.Append(b.ToString("x2", CultureInfo.InvariantCulture));
                }
                return builder.ToString();
            }
        }

        /// <summary>Loads manifest records. Missing files yield an empty list.</summary>
        public static List<GolemScribeArtifactRecord> Load(string projectRoot)
        {
            var path = ManifestPath(projectRoot);
            if (!File.Exists(path))
            {
                return new List<GolemScribeArtifactRecord>();
            }

            var text = File.ReadAllText(path);
            if (!GolemYamlWriter.IsScribeOwned(text))
            {
                throw new InvalidOperationException(
                    $"Refusing to read {GolemScribeConstants.ManifestFileName}: file exists but is not Scribe-owned. Move or rename the handwritten file.");
            }

            return Parse(text);
        }

        /// <summary>Parses manifest body text into artifact records with structural validation.</summary>
        public static List<GolemScribeArtifactRecord> Parse(string text)
        {
            var records = new List<GolemScribeArtifactRecord>();
            GolemScribeArtifactRecord current = null;
            using (var reader = new StringReader(text ?? string.Empty))
            {
                string line;
                while ((line = reader.ReadLine()) != null)
                {
                    if (line.Length == 0 || line.TrimStart().StartsWith("#", StringComparison.Ordinal))
                    {
                        continue;
                    }

                    var trimmed = line.Trim();
                    if (trimmed == "artifacts:" || trimmed.StartsWith("version:", StringComparison.Ordinal) || trimmed == "[]" || trimmed == "artifacts: []")
                    {
                        continue;
                    }

                    var start = ArtifactStart.Match(line);
                    if (start.Success)
                    {
                        if (current != null)
                        {
                            ValidateRecord(current);
                            records.Add(current);
                        }

                        if (!GolemYamlWriter.TryParseScalarToken(start.Groups[1].Value, out var kind) ||
                            string.IsNullOrEmpty(kind))
                        {
                            throw new InvalidOperationException("Manifest artifact is missing a valid kind.");
                        }

                        current = new GolemScribeArtifactRecord { Kind = kind };
                        continue;
                    }

                    if (current == null)
                    {
                        continue;
                    }

                    var field = FieldLine.Match(line);
                    if (!field.Success)
                    {
                        continue;
                    }

                    var key = field.Groups[1].Value;
                    if (!GolemYamlWriter.TryParseScalarToken(field.Groups[2].Value, out var value))
                    {
                        throw new InvalidOperationException($"Manifest field '{key}' has an invalid scalar value.");
                    }

                    switch (key)
                    {
                        case "kind":
                            current.Kind = value;
                            break;
                        case "source_guid":
                            current.SourceGuid = value;
                            break;
                        case "entity":
                            current.Entity = value;
                            break;
                        case "path":
                            current.Path = value.Replace('\\', '/');
                            break;
                        case "hash":
                            current.Hash = value;
                            break;
                        default:
                            // Ignore unknown fields for forward compatibility.
                            break;
                    }
                }
            }

            if (current != null)
            {
                ValidateRecord(current);
                records.Add(current);
            }

            return records;
        }

        /// <summary>Builds deterministic manifest document text for the given records.</summary>
        public static string BuildDocument(IEnumerable<GolemScribeArtifactRecord> records)
        {
            var ordered = records
                .OrderBy(r => r.Path ?? string.Empty, StringComparer.Ordinal)
                .ThenBy(r => r.Kind ?? string.Empty, StringComparer.Ordinal)
                .ThenBy(r => r.SourceGuid ?? string.Empty, StringComparer.Ordinal)
                .ToList();

            var lines = new List<string>
            {
                "version: " + GolemYamlWriter.FormatInt(GolemScribeConstants.ManifestVersion),
                "artifacts:"
            };

            if (ordered.Count == 0)
            {
                lines.Add("  []");
            }
            else
            {
                foreach (var record in ordered)
                {
                    lines.Add("  - kind: " + GolemYamlWriter.FormatScalar(record.Kind ?? string.Empty));
                    if (!string.IsNullOrEmpty(record.Entity))
                    {
                        lines.Add("    entity: " + GolemYamlWriter.FormatScalar(record.Entity));
                    }
                    lines.Add("    source_guid: " + GolemYamlWriter.FormatScalar(record.SourceGuid ?? string.Empty));
                    lines.Add("    path: " + GolemYamlWriter.FormatScalar(record.Path ?? string.Empty));
                    lines.Add("    hash: " + GolemYamlWriter.FormatScalar(record.Hash ?? string.Empty));
                }
            }

            return GolemYamlWriter.BuildDocument(lines);
        }

        /// <summary>Writes the manifest atomically when content changed.</summary>
        public static bool Write(string projectRoot, IEnumerable<GolemScribeArtifactRecord> records)
        {
            var path = ManifestPath(projectRoot);
            var content = BuildDocument(records);
            return GolemScribeArtifacts.WriteManagedFile(path, content);
        }

        private static void ValidateRecord(GolemScribeArtifactRecord record)
        {
            if (string.IsNullOrEmpty(record.Kind))
            {
                throw new InvalidOperationException("Manifest artifact is missing kind.");
            }

            if (string.IsNullOrEmpty(record.SourceGuid))
            {
                throw new InvalidOperationException($"Manifest artifact '{record.Kind}' is missing source_guid.");
            }

            if (string.IsNullOrEmpty(record.Path))
            {
                throw new InvalidOperationException($"Manifest artifact '{record.Kind}' is missing path.");
            }

            if (string.IsNullOrEmpty(record.Hash))
            {
                throw new InvalidOperationException($"Manifest artifact '{record.Path}' is missing hash.");
            }
        }
    }
}
