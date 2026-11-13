using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Reflection;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    // Wire types intentionally mirror navmesh-go geom.Scene/bake.Config, without Unity or engine runtime types.
    internal sealed class NavigationVector
    {
        public double X, Y, Z;
        internal NavigationVector(double x = 0, double y = 0, double z = 0) { X = x; Y = y; Z = z; }
    }
    internal sealed class NavigationBounds { public NavigationVector Min = new NavigationVector(), Max = new NavigationVector(); }
    internal sealed class NavigationTriangle { public uint[] Indices; public int Area; }
    internal sealed class NavigationAsset { public string ID; public NavigationVector[] Vertices = Array.Empty<NavigationVector>(); public NavigationTriangle[] Triangles = Array.Empty<NavigationTriangle>(); internal GolemHeightfield Heightfield; }
    internal sealed class NavigationGeometryReference { public string Path, SHA256; }
    internal sealed class NavigationInstance { public string ID, AssetID; public ulong Revision; public double[] Transform; public bool Disabled; }
    internal sealed class NavigationVolume
    {
        public string ID, Shape;
        public ulong Revision;
        public NavigationBounds Bounds;
        public NavigationVector[] Points = Array.Empty<NavigationVector>();
        public double Radius;
        public int Area, Priority;
        public bool Exclude, Disabled;
    }
    internal sealed class NavigationLink
    {
        public string ID, Action;
        public ulong Revision;
        public NavigationVector Start, End;
        public double Radius;
        public bool Bidirectional, Disabled;
        public int Area, Flags;
        public string[] Profiles = Array.Empty<string>();
    }
    internal sealed class NavigationScene
    {
        public NavigationAsset[] Assets = Array.Empty<NavigationAsset>();
        public NavigationInstance[] Instances = Array.Empty<NavigationInstance>();
        public NavigationVolume[] Volumes = Array.Empty<NavigationVolume>();
        public NavigationLink[] Links = Array.Empty<NavigationLink>();
    }
    internal sealed class NavigationProfile { public string ID; public double Radius, Height, MaxSlope, MaxClimb; }
    internal sealed class NavigationSettings
    {
        public NavigationVector Origin;
        public NavigationBounds Bounds;
        public double CellSize, CellHeight, MaxSimplificationError, DetailSampleDistance, DetailSampleMaxError;
        public int TileSize, MinRegionArea, MergeRegionArea, MaxEdgeLength;
        public string Partition;
        public bool BuildLayers;
    }
    internal sealed class NavigationConfig
    {
        public string WorldID;
        public ulong Revision;
        public NavigationProfile Profile;
        public NavigationSettings Settings;
        public bool Strict = true;
        public int MaxTiles = 65536, MaxTrianglesPerTile = 1 << 20;
        public int[] AreaFlags = new int[64];
    }

    /// <summary>Pure validation and deterministic JSON planning; never reads Unity objects or writes files.</summary>
    internal static class GolemNavigationDocument
    {
        internal const string Generator = "Golem Scribe navigation source v1";
        internal const string CompactGenerator = "Golem Scribe navigation source v2";
        internal const string CompactOwnershipPrefix = "{\n  \"Generator\": \"" + CompactGenerator + "\",\n";
        internal const string OwnershipPrefix = "{\n  \"Generator\": \"" + Generator + "\",\n";

        internal static bool IsOwned(string content) => content != null && (content.StartsWith(OwnershipPrefix, StringComparison.Ordinal) || content.StartsWith(CompactOwnershipPrefix, StringComparison.Ordinal));

        internal static string Build(NavigationScene scene, NavigationConfig config, NavigationGeometryReference geometry = null)
        {
            Validate(scene, config);
            Require(geometry != null || !scene.Assets.Any(a => a.Heightfield != null), "Compact heightfields require a geometry sidecar.");
            if (geometry != null) Require(!string.IsNullOrWhiteSpace(geometry.Path) && geometry.SHA256?.Length == 64, "Invalid geometry reference.");
            // Sort copies, retaining authoring order for each asset's vertices/triangles and convex points.
            var ordered = new NavigationScene
            {
                Assets = geometry == null ? scene.Assets.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray() : Array.Empty<NavigationAsset>(),
                Instances = scene.Instances.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray(),
                Volumes = scene.Volumes.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray(),
                Links = scene.Links.OrderBy(x => x.ID, StringComparer.Ordinal).Select(x => new NavigationLink
                {
                    ID = x.ID, Action = x.Action ?? "", Revision = x.Revision, Start = x.Start, End = x.End,
                    Radius = x.Radius, Bidirectional = x.Bidirectional, Disabled = x.Disabled, Area = x.Area,
                    Flags = x.Flags, Profiles = x.Profiles.OrderBy(p => p, StringComparer.Ordinal).ToArray()
                }).ToArray()
            };
            var json = new StringBuilder(geometry == null ? OwnershipPrefix : CompactOwnershipPrefix);
            if (geometry != null) { json.Append("  \"Geometry\": "); Write(json, geometry); json.Append(",\n"); }
            json.Append("  \"Scene\": "); Write(json, ordered);
            json.Append(",\n  \"Config\": "); Write(json, config);
            json.Append("\n}\n");
            var content = json.ToString();
            if (Encoding.UTF8.GetByteCount(content) > 256 * 1024 * 1024) throw new InvalidOperationException("Navigation source exceeds the 256 MiB reader limit.");
            return content;
        }

        internal static void Validate(NavigationScene scene, NavigationConfig config)
        {
            Require(scene != null && config != null, "Missing navigation scene or configuration.");
            Require(!string.IsNullOrWhiteSpace(config.WorldID) && config.Revision > 0, "World identity and positive revision are required.");
            var p = config.Profile; var s = config.Settings;
            Require(p != null && s != null, "Missing navigation profile or settings.");
            Require(!string.IsNullOrWhiteSpace(p.ID) && Finite(p.Radius) && p.Radius >= 0 && Finite(p.Height) && p.Height > 0 &&
                Finite(p.MaxSlope) && p.MaxSlope >= 0 && p.MaxSlope < 90 && Finite(p.MaxClimb) && p.MaxClimb >= 0, "Invalid agent profile.");
            Bounds(s.Bounds); Vector(s.Origin);
            Require(s.Bounds.Min.X < s.Bounds.Max.X && s.Bounds.Min.Y < s.Bounds.Max.Y && s.Bounds.Min.Z < s.Bounds.Max.Z, "Bake bounds must have positive extent on every axis.");
            Require(Finite(s.CellSize) && s.CellSize > 0 && Finite(s.CellHeight) && s.CellHeight > 0 && s.TileSize > 0 && s.TileSize <= 4096 &&
                s.MinRegionArea >= 0 && s.MergeRegionArea >= 0 && s.MaxEdgeLength >= 0 &&
                Finite(s.MaxSimplificationError) && s.MaxSimplificationError >= 0 && Finite(s.DetailSampleDistance) && s.DetailSampleDistance >= 0 &&
                Finite(s.DetailSampleMaxError) && s.DetailSampleMaxError >= 0, "Invalid voxel or mesh settings.");
            Require(s.Partition == "watershed" || s.Partition == "monotone" || s.Partition == "layers", "Unknown partition mode.");
            Require(!s.BuildLayers || s.TileSize <= 255, "Heightfield layers require tiles no larger than 255 cells.");
            Require(Math.Ceiling((s.Bounds.Max.Y - s.Bounds.Min.Y) / s.CellHeight) <= 8191 && Math.Ceiling(p.Height / s.CellHeight) <= 255 &&
                Math.Ceiling(p.Radius / s.CellSize) <= 127, "Region/profile exceeds Recast voxel limits.");
            Require(config.AreaFlags != null && config.AreaFlags.Length == 64 && config.AreaFlags.All(f => f >= 0 && f <= 65535), "AreaFlags must contain 64 unsigned 16-bit values.");
            Require(config.MaxTiles > 0 && config.MaxTrianglesPerTile > 0, "Resource limits must be positive.");
            Require(scene.Assets != null && scene.Instances != null && scene.Volumes != null && scene.Links != null, "Source arrays cannot be null.");
            var assets = new HashSet<string>(StringComparer.Ordinal);
            var assetByID = new Dictionary<string, NavigationAsset>(StringComparer.Ordinal);
            foreach (var a in scene.Assets)
            {
                Require(a != null && !string.IsNullOrWhiteSpace(a.ID) && assets.Add(a.ID), "Duplicate or missing asset identity.");
                Require(a.Vertices != null && a.Triangles != null && a.Vertices.Length <= 8 * 1024 * 1024 && a.Triangles.Length <= 8 * 1024 * 1024, "Invalid asset arrays or source element limit exceeded.");
                if (a.Heightfield != null) { a.Heightfield.Validate(); Require(a.Vertices.Length == 0 && a.Triangles.Length == 0, "Heightfield cannot also contain mesh arrays."); }
                foreach (var v in a.Vertices) Vector(v);
                foreach (var t in a.Triangles)
                    Require(t != null && t.Indices != null && t.Indices.Length == 3 && t.Indices.All(i => i < a.Vertices.Length) && t.Area >= 0 && t.Area <= 63, "Invalid triangle indices or area.");
                assetByID.Add(a.ID, a);
            }
            var ids = new HashSet<string>(StringComparer.Ordinal);
            foreach (var i in scene.Instances)
            {
                Require(i != null, "Null geometry instance."); Identity(ids, i.ID, i.Revision);
                Require(assets.Contains(i.AssetID) && i.Transform != null && i.Transform.Length == 16 && i.Transform.All(Finite), "Invalid instance asset or transform.");
                var t = i.Transform;
                Require(t[12] == 0 && t[13] == 0 && t[14] == 0 && t[15] == 1, "Transform must be affine, row-major, multiplying column vectors.");
                var determinant = t[0] * (t[5] * t[10] - t[6] * t[9]) - t[1] * (t[4] * t[10] - t[6] * t[8]) + t[2] * (t[4] * t[9] - t[5] * t[8]);
                Require(Finite(determinant) && Math.Abs(determinant) > 1e-12, "Singular instance transform.");
                if (!i.Disabled && config.Strict)
                {
                    var asset = assetByID[i.AssetID];
                    if (asset.Heightfield != null)
                    {
                        var h = asset.Heightfield;
                        for (var z = 0; z < h.Resolution; z++) for (var x = 0; x < h.Resolution; x++)
                        {
                            var point = Resolve(new NavigationVector((double)x * h.SizeX / (h.Resolution - 1),
                                (double)h.Samples[z * h.Resolution + x] * h.SizeY, (double)z * h.SizeZ / (h.Resolution - 1)), t, s.Origin);
                            Require(Finite(point.X) && Finite(point.Y) && Finite(point.Z), "Overflowing transformed terrain vertex.");
                        }
                    }
                    foreach (var triangle in asset.Triangles)
                    {
                        var a = Resolve(asset.Vertices[triangle.Indices[0]], t, s.Origin);
                        var b = Resolve(asset.Vertices[triangle.Indices[1]], t, s.Origin);
                        var c = Resolve(asset.Vertices[triangle.Indices[2]], t, s.Origin);
                        var ux = b.X - a.X; var uy = b.Y - a.Y; var uz = b.Z - a.Z;
                        var vx = c.X - a.X; var vy = c.Y - a.Y; var vz = c.Z - a.Z;
                        var nx = uy * vz - uz * vy; var ny = uz * vx - ux * vz; var nz = ux * vy - uy * vx;
                        Require(Finite(nx) && Finite(ny) && Finite(nz) && (nx != 0 || ny != 0 || nz != 0), "Strict navigation source contains a degenerate or overflowing transformed triangle: " + i.ID);
                    }
                }
            }
            foreach (var v in scene.Volumes)
            {
                Require(v != null, "Null area volume."); Identity(ids, v.ID, v.Revision); Bounds(v.Bounds);
                Require(v.Area >= 0 && v.Area <= 63 && (v.Shape == "box" || v.Shape == "convex" || v.Shape == "cylinder"), "Invalid volume area or shape.");
                Require(v.Points != null, "Volume points cannot be null.");
                if (v.Shape == "convex") Require(v.Points.Length >= 3 && v.Points.Length <= 256, "Invalid convex volume points.");
                foreach (var point in v.Points) Vector(point);
                Require(Finite(v.Radius) && (v.Shape != "cylinder" || v.Radius > 0), "Invalid cylinder radius.");
                if (v.Shape == "cylinder") Require(v.Bounds.Max.X - v.Bounds.Min.X >= v.Radius * 2 && v.Bounds.Max.Z - v.Bounds.Min.Z >= v.Radius * 2, "Cylinder bounds must contain its radius.");
            }
            foreach (var l in scene.Links)
            {
                Require(l != null, "Null traversal link."); Identity(ids, l.ID, l.Revision); Vector(l.Start); Vector(l.End);
                Require(Finite(l.Radius) && l.Radius > 0 && l.Area >= 0 && l.Area <= 63 && l.Flags > 0 && l.Flags <= 65535, "Invalid traversal link radius, area, or flags.");
                Require(l.Profiles != null && l.Profiles.All(x => !string.IsNullOrWhiteSpace(x)) && l.Profiles.Distinct(StringComparer.Ordinal).Count() == l.Profiles.Length, "Invalid or duplicate traversal profile.");
            }
        }

        private static void Identity(HashSet<string> ids, string id, ulong revision) => Require(!string.IsNullOrWhiteSpace(id) && ids.Add(id) && revision > 0, "Duplicate/missing object identity or invalid revision: " + id);
        private readonly struct ResolvedVertex
        {
            internal readonly double X, Y, Z;
            internal ResolvedVertex(double x, double y, double z) { X = x; Y = y; Z = z; }
        }
        private static ResolvedVertex Resolve(NavigationVector p, double[] t, NavigationVector origin) => new ResolvedVertex(
            t[0] * p.X + t[1] * p.Y + t[2] * p.Z + t[3] - origin.X,
            t[4] * p.X + t[5] * p.Y + t[6] * p.Z + t[7] - origin.Y,
            t[8] * p.X + t[9] * p.Y + t[10] * p.Z + t[11] - origin.Z);
        private static bool Finite(double value) => !double.IsNaN(value) && !double.IsInfinity(value);
        private static void Vector(NavigationVector v) => Require(v != null && Finite(v.X) && Finite(v.Y) && Finite(v.Z), "Non-finite or missing position.");
        private static void Bounds(NavigationBounds b)
        {
            Require(b != null, "Missing bounds."); Vector(b.Min); Vector(b.Max);
            Require(b.Min.X <= b.Max.X && b.Min.Y <= b.Max.Y && b.Min.Z <= b.Max.Z, "Inverted bounds.");
        }
        private static void Require(bool valid, string error) { if (!valid) throw new InvalidOperationException(error); }

        // Restricted to the wire DTOs above. Explicit numeric handling avoids locale-dependent numbers,
        // byte-array base64 conversion, native serializers, and Unity's reference-object serialization.
        private static void Write(StringBuilder output, object value)
        {
            if (value == null) { output.Append("null"); return; }
            if (value is string text) { Quote(output, text); return; }
            if (value is bool boolean) { output.Append(boolean ? "true" : "false"); return; }
            if (value is double number)
            {
                Require(Finite(number), "Cannot encode a non-finite number.");
                output.Append(number.ToString("R", CultureInfo.InvariantCulture)); return;
            }
            if (value is int || value is uint || value is ulong) { output.Append(Convert.ToString(value, CultureInfo.InvariantCulture)); return; }
            if (value is IEnumerable values)
            {
                output.Append('['); var separator = "";
                foreach (var element in values) { output.Append(separator); Write(output, element); separator = ","; }
                output.Append(']'); return;
            }
            var type = value.GetType();
            Require(type.Namespace == typeof(NavigationScene).Namespace && type.Name.StartsWith("Navigation", StringComparison.Ordinal), "Unsupported JSON wire type.");
            output.Append('{'); var comma = "";
            foreach (var field in type.GetFields(BindingFlags.Public | BindingFlags.Instance).OrderBy(f => f.Name, StringComparer.Ordinal))
            {
                output.Append(comma); Quote(output, field.Name); output.Append(':'); Write(output, field.GetValue(value)); comma = ",";
            }
            output.Append('}');
        }

        private static void Quote(StringBuilder output, string value)
        {
            output.Append('"');
            foreach (var c in value)
            {
                if (c == '"' || c == '\\') { output.Append('\\'); output.Append(c); }
                else if (c < 32 || char.IsSurrogate(c)) output.Append("\\u").Append(((int)c).ToString("x4", CultureInfo.InvariantCulture));
                else output.Append(c);
            }
            output.Append('"');
        }
    }
}
