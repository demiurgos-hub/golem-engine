using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Deterministic footprints.golem.yaml emitter matching the Phase 3 Go golden contract,
    /// with a leading Scribe ownership marker.
    /// </summary>
    public static class GolemFootprintYamlBuilder
    {
        /// <summary>
        /// Builds a Scribe-owned footprints document. Footprints are ordered by GUID;
        /// shapes keep authoring order.
        /// </summary>
        public static string BuildYaml(int dimensions, IEnumerable<GolemFootprintModel> footprints)
        {
            var ordered = (footprints ?? Array.Empty<GolemFootprintModel>())
                .Where(f => f != null && !string.IsNullOrEmpty(f.Guid))
                .OrderBy(f => NormalizeGuid(f.Guid), StringComparer.Ordinal)
                .ToList();

            var lines = new List<string>
            {
                "version: " + GolemYamlWriter.FormatInt(GolemScribeConstants.FootprintFormatVersion),
                "dimensions: " + GolemYamlWriter.FormatInt(dimensions)
            };

            if (ordered.Count == 0)
            {
                // Single-line empty map so build/parse round-trips without a dangling "  {}" block.
                lines.Add("footprints: {}");
            }
            else
            {
                lines.Add("footprints:");
                foreach (var fp in ordered)
                {
                    var guid = NormalizeGuid(fp.Guid);
                    lines.Add("  " + guid + ":");
                    lines.Add("    name: " + GolemYamlWriter.FormatScalar(fp.Name ?? string.Empty));
                    lines.Add("    asset_path: " + GolemYamlWriter.FormatScalar(fp.AssetPath ?? string.Empty));
                    var alias = (fp.Alias ?? string.Empty).Trim();
                    if (alias.Length > 0)
                    {
                        lines.Add("    alias: " + GolemYamlWriter.FormatScalar(alias));
                    }

                    lines.Add("    shapes:");
                    if (fp.Shapes == null || fp.Shapes.Count == 0)
                    {
                        lines.Add("      []");
                        continue;
                    }

                    foreach (var shape in fp.Shapes)
                    {
                        AppendShape(lines, shape, dimensions);
                    }
                }
            }

            return GolemYamlWriter.BuildDocument(lines);
        }

        /// <summary>
        /// Parses a previously emitted Scribe footprints document for invalid-prefab preservation.
        /// Returns false when the text is not a recognizable Scribe footprints file.
        /// </summary>
        public static bool TryParse(string text, out int dimensions, out List<GolemFootprintModel> footprints)
        {
            dimensions = 0;
            footprints = new List<GolemFootprintModel>();
            if (string.IsNullOrEmpty(text) || !GolemYamlWriter.IsScribeOwned(text))
            {
                return false;
            }

            GolemFootprintModel current = null;
            GolemFootprintShapeModel currentShape = null;
            var inFootprints = false;
            var inShapes = false;
            var inOffset = false;
            string pendingGuid = null;

            using (var reader = new StringReader(text))
            {
                string line;
                while ((line = reader.ReadLine()) != null)
                {
                    if (line.Length == 0 || line.TrimStart().StartsWith("#", StringComparison.Ordinal))
                    {
                        continue;
                    }

                    var indent = line.Length - line.TrimStart().Length;
                    var trimmed = line.Trim();

                    if (indent == 0 && trimmed.StartsWith("version:", StringComparison.Ordinal))
                    {
                        continue;
                    }

                    if (indent == 0 && TryReadInt(trimmed, "dimensions", out var dims))
                    {
                        dimensions = dims;
                        continue;
                    }

                    if (indent == 0 && (trimmed == "footprints:" || trimmed == "footprints: {}"))
                    {
                        inFootprints = true;
                        continue;
                    }

                    // Two-line empty map emitted by older writers: footprints:\n  {}
                    if (inFootprints && current == null && indent == 2 && trimmed == "{}")
                    {
                        continue;
                    }

                    if (!inFootprints)
                    {
                        continue;
                    }

                    if (indent == 2 && trimmed.EndsWith(":", StringComparison.Ordinal) && !trimmed.StartsWith("shapes", StringComparison.Ordinal))
                    {
                        FlushShape(ref currentShape, current);
                        pendingGuid = trimmed.Substring(0, trimmed.Length - 1).Trim();
                        current = new GolemFootprintModel { Guid = NormalizeGuid(pendingGuid) };
                        footprints.Add(current);
                        inShapes = false;
                        inOffset = false;
                        continue;
                    }

                    if (current == null)
                    {
                        continue;
                    }

                    if (indent == 4 && TryReadString(trimmed, "name", out var name))
                    {
                        current.Name = name;
                        continue;
                    }

                    if (indent == 4 && TryReadString(trimmed, "asset_path", out var assetPath))
                    {
                        current.AssetPath = assetPath;
                        continue;
                    }

                    if (indent == 4 && TryReadString(trimmed, "alias", out var alias))
                    {
                        current.Alias = alias;
                        continue;
                    }

                    if (indent == 4 && (trimmed == "shapes:" || trimmed == "shapes: []"))
                    {
                        FlushShape(ref currentShape, current);
                        inShapes = trimmed == "shapes:";
                        inOffset = false;
                        continue;
                    }

                    if (!inShapes)
                    {
                        continue;
                    }

                    if (indent == 6 && trimmed.StartsWith("- ", StringComparison.Ordinal))
                    {
                        FlushShape(ref currentShape, current);
                        currentShape = new GolemFootprintShapeModel();
                        var rest = trimmed.Substring(2).Trim();
                        if (TryReadString(rest, "type", out var type))
                        {
                            currentShape.Type = type;
                        }

                        inOffset = false;
                        continue;
                    }

                    if (currentShape == null)
                    {
                        continue;
                    }

                    if (indent == 8 && TryReadString(trimmed, "type", out var shapeType))
                    {
                        currentShape.Type = shapeType;
                        continue;
                    }

                    if (indent == 8 && TryReadFloat(trimmed, "r", out var r))
                    {
                        currentShape.R = r;
                        continue;
                    }

                    if (indent == 8 && TryReadFloat(trimmed, "w", out var w))
                    {
                        currentShape.W = w;
                        continue;
                    }

                    if (indent == 8 && TryReadFloat(trimmed, "h", out var h))
                    {
                        currentShape.H = h;
                        continue;
                    }

                    if (indent == 8 && TryReadFloat(trimmed, "d", out var d))
                    {
                        currentShape.D = d;
                        continue;
                    }

                    if (indent == 8 && trimmed == "offset:")
                    {
                        inOffset = true;
                        continue;
                    }

                    if (inOffset && indent == 10 && TryReadFloat(trimmed, "x", out var ox))
                    {
                        currentShape.OffsetX = ox;
                        continue;
                    }

                    if (inOffset && indent == 10 && TryReadFloat(trimmed, "y", out var oy))
                    {
                        currentShape.OffsetY = oy;
                        continue;
                    }

                    if (inOffset && indent == 10 && TryReadFloat(trimmed, "z", out var oz))
                    {
                        currentShape.OffsetZ = oz;
                        continue;
                    }

                    if (indent == 8 && TryReadBool(trimmed, "trigger", out var trigger))
                    {
                        inOffset = false;
                        currentShape.Trigger = trigger;
                        continue;
                    }

                    if (indent == 8 && TryReadString(trimmed, "layer", out var layer))
                    {
                        inOffset = false;
                        currentShape.Layer = layer;
                    }
                }
            }

            FlushShape(ref currentShape, current);
            return dimensions == 2 || dimensions == 3;
        }

        private static void AppendShape(List<string> lines, GolemFootprintShapeModel shape, int dimensions)
        {
            if (shape == null || string.IsNullOrEmpty(shape.Type))
            {
                return;
            }

            var type = shape.Type.Trim().ToLowerInvariant();
            lines.Add("      - type: " + GolemYamlWriter.FormatScalar(type));
            switch (type)
            {
                case "circle":
                case "sphere":
                    lines.Add("        r: " + GolemYamlWriter.FormatFloat(shape.R));
                    break;
                case "aabb":
                    lines.Add("        w: " + GolemYamlWriter.FormatFloat(shape.W));
                    lines.Add("        h: " + GolemYamlWriter.FormatFloat(shape.H));
                    if (dimensions == 3)
                    {
                        lines.Add("        d: " + GolemYamlWriter.FormatFloat(shape.D));
                    }

                    break;
            }

            lines.Add("        offset:");
            lines.Add("          x: " + GolemYamlWriter.FormatFloat(shape.OffsetX));
            lines.Add("          y: " + GolemYamlWriter.FormatFloat(shape.OffsetY));
            if (dimensions == 3)
            {
                lines.Add("          z: " + GolemYamlWriter.FormatFloat(shape.OffsetZ));
            }

            lines.Add("        trigger: " + GolemYamlWriter.FormatBool(shape.Trigger));
            lines.Add("        layer: " + GolemYamlWriter.FormatScalar(shape.Layer ?? string.Empty));
        }

        private static void FlushShape(ref GolemFootprintShapeModel shape, GolemFootprintModel owner)
        {
            if (shape == null || owner == null)
            {
                shape = null;
                return;
            }

            owner.Shapes.Add(shape);
            shape = null;
        }

        internal static string NormalizeGuid(string guid)
        {
            return (guid ?? string.Empty).Trim().ToLowerInvariant();
        }

        private static bool TryReadInt(string trimmed, string key, out int value)
        {
            value = 0;
            if (!TryReadString(trimmed, key, out var raw))
            {
                return false;
            }

            return int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out value);
        }

        private static bool TryReadFloat(string trimmed, string key, out float value)
        {
            value = 0f;
            if (!TryReadString(trimmed, key, out var raw))
            {
                return false;
            }

            return float.TryParse(raw, NumberStyles.Float, CultureInfo.InvariantCulture, out value);
        }

        private static bool TryReadBool(string trimmed, string key, out bool value)
        {
            value = false;
            if (!TryReadString(trimmed, key, out var raw))
            {
                return false;
            }

            if (raw == "true")
            {
                value = true;
                return true;
            }

            if (raw == "false")
            {
                value = false;
                return true;
            }

            return false;
        }

        private static bool TryReadString(string trimmed, string key, out string value)
        {
            value = null;
            var prefix = key + ":";
            if (!trimmed.StartsWith(prefix, StringComparison.Ordinal))
            {
                return false;
            }

            return GolemYamlWriter.TryParseScalarToken(trimmed.Substring(prefix.Length), out value);
        }
    }
}
