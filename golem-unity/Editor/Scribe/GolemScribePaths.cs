using System;
using System.Collections.Generic;
using System.IO;

namespace GolemEngine.Unity.Editor
{
    internal static class GolemScribePaths
    {
        internal static string CombineRelative(string directory, string fileName)
        {
            var dir = (directory ?? string.Empty).Replace('\\', '/').Trim('/');
            if (string.IsNullOrEmpty(dir))
            {
                return fileName;
            }
            return dir + "/" + fileName;
        }

        internal static bool TryResolveRelative(
            string rootFull,
            string relativePath,
            out string absolutePath,
            out string normalizedRelative,
            out string error)
        {
            absolutePath = null;
            normalizedRelative = null;
            error = null;

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

            var rawParts = raw.Split(new[] { '/' }, StringSplitOptions.RemoveEmptyEntries);
            var parts = new List<string>(rawParts.Length);
            foreach (var part in rawParts)
            {
                if (part == ".")
                {
                    continue;
                }

                if (part == "..")
                {
                    error = $"Artifact path escapes the project root: '{relativePath}'.";
                    return false;
                }

                parts.Add(part);
            }

            if (parts.Count == 0)
            {
                error = $"Artifact path is empty: '{relativePath}'.";
                return false;
            }

            normalizedRelative = string.Join("/", parts);
            try
            {
                var combined = rootFull;
                foreach (var part in parts)
                {
                    combined = Path.Combine(combined, part);
                }
                absolutePath = Path.GetFullPath(combined);
            }
            catch (Exception ex)
            {
                error = $"Invalid artifact path '{relativePath}': {ex.Message}";
                normalizedRelative = null;
                return false;
            }
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

    }
}
