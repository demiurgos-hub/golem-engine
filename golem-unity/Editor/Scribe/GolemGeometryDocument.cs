using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    /// <summary>A compact source payload reference resolved beside its owning JSON document.</summary>
    internal sealed class PhysicsGeometryReference
    {
        public string Path, SHA256;
    }

    internal sealed class GolemHeightfield
    {
        internal int Resolution;
        internal float SizeX, SizeY, SizeZ;
        internal float[] Samples;
        internal byte[] Surface;
        internal byte Area;

        internal void Validate()
        {
            if (Resolution < 2 || (long)Resolution * Resolution > 8 * 1024 * 1024
                || Samples == null || Samples.Length != (long)Resolution * Resolution
                || Surface == null || Surface.Length != ((long)(Resolution - 1) * (Resolution - 1) + 7) / 8
                || !Positive(SizeX) || !Positive(SizeY) || !Positive(SizeZ) || Area > 63)
                throw new InvalidOperationException("Invalid compact heightfield dimensions or buffers.");
            foreach (var value in Samples)
                if (float.IsNaN(value) || float.IsInfinity(value) || value < 0 || value > 1)
                    throw new InvalidOperationException("Heightfield samples must be finite normalized float32 values.");
            var bits = (Resolution - 1) * (Resolution - 1);
            if ((bits & 7) != 0 && (Surface[Surface.Length - 1] >> (bits & 7)) != 0)
                throw new InvalidOperationException("Heightfield surface padding must be zero.");
        }

        private static bool Positive(float value) => !float.IsNaN(value) && !float.IsInfinity(value) && value > 0;
    }

    /// <summary>The deterministic binary source codec for shared mesh and sampled terrain geometry.</summary>
    internal static class GolemGeometryDocument
    {
        internal static readonly byte[] Magic = Encoding.ASCII.GetBytes("GOLEMGE1");
        private static readonly UTF8Encoding Utf8 = new UTF8Encoding(false, true);

        internal static bool IsOwned(byte[] bytes) => bytes != null && bytes.Length >= Magic.Length
            && Magic.Select((value, index) => bytes[index] == value).All(value => value);

        internal static byte[] BuildPhysics(IEnumerable<PhysicsAsset> assets) => Build(assets, Array.Empty<NavigationAsset>());

        internal static byte[] BuildNavigation(IEnumerable<NavigationAsset> assets) => Build(Array.Empty<PhysicsAsset>(), assets);

        /// <summary>Writes physics (float32) and navigation (float64) assets into one sidecar ordered by ID.</summary>
        internal static byte[] Build(IEnumerable<PhysicsAsset> physics, IEnumerable<NavigationAsset> navigation)
        {
            var ordered = physics.Select(asset => (asset.ID, Physics: asset, Navigation: (NavigationAsset)null))
                .Concat(navigation.Select(asset => (asset.ID, Physics: (PhysicsAsset)null, Navigation: asset)))
                .OrderBy(entry => entry.ID, StringComparer.Ordinal).ToArray();
            for (var i = 1; i < ordered.Length; i++)
                if (string.Equals(ordered[i - 1].ID, ordered[i].ID, StringComparison.Ordinal))
                    throw new InvalidOperationException("Duplicate geometry identity: " + ordered[i].ID);
            CheckBudget(ordered.Length, ordered.Select(entry => entry.Physics != null
                ? AssetSize(entry.ID, entry.Physics.SourceID ?? "", entry.Physics.Heightfield, entry.Physics.Vertices?.Length ?? 0, entry.Physics.Indices?.Length / 3 ?? 0, 12)
                : AssetSize(entry.ID, entry.ID, entry.Navigation.Heightfield, entry.Navigation.Vertices?.Length ?? 0, entry.Navigation.Triangles?.Length ?? 0, 24)));
            using (var stream = new MemoryStream())
            using (var writer = new BinaryWriter(stream, Utf8, true))
            {
                writer.Write(Magic); writer.Write(checked((uint)ordered.Length));
                foreach (var entry in ordered)
                {
                    if (entry.Physics != null) WritePhysics(writer, entry.Physics);
                    else WriteNavigation(writer, entry.Navigation);
                }
                return Finish(stream);
            }
        }

        private static void WritePhysics(BinaryWriter writer, PhysicsAsset asset)
        {
            WriteString(writer, asset.ID); WriteString(writer, asset.SourceID ?? "");
            if (asset.Heightfield != null) { WriteHeightfield(writer, asset.Heightfield); return; }
            writer.Write((byte)1);
            writer.Write(checked((uint)asset.Vertices.Length)); writer.Write(checked((uint)(asset.Indices.Length / 3)));
            foreach (var vertex in asset.Vertices) { writer.Write(vertex.X); writer.Write(vertex.Y); writer.Write(vertex.Z); }
            for (var i = 0; i < asset.Indices.Length; i += 3)
            { writer.Write(asset.Indices[i]); writer.Write(asset.Indices[i + 1]); writer.Write(asset.Indices[i + 2]); writer.Write((byte)0); }
        }

        private static void WriteNavigation(BinaryWriter writer, NavigationAsset asset)
        {
            WriteString(writer, asset.ID); WriteString(writer, asset.ID);
            if (asset.Heightfield != null) { WriteHeightfield(writer, asset.Heightfield); return; }
            writer.Write((byte)3);
            writer.Write(checked((uint)asset.Vertices.Length)); writer.Write(checked((uint)asset.Triangles.Length));
            foreach (var vertex in asset.Vertices) { writer.Write(vertex.X); writer.Write(vertex.Y); writer.Write(vertex.Z); }
            foreach (var triangle in asset.Triangles)
            { writer.Write(triangle.Indices[0]); writer.Write(triangle.Indices[1]); writer.Write(triangle.Indices[2]); writer.Write(checked((byte)triangle.Area)); }
        }

        private static void WriteHeightfield(BinaryWriter writer, GolemHeightfield heightfield)
        {
            heightfield.Validate();
            writer.Write((byte)2); writer.Write(checked((uint)heightfield.Resolution));
            writer.Write(heightfield.SizeX); writer.Write(heightfield.SizeY); writer.Write(heightfield.SizeZ);
            writer.Write(checked((uint)heightfield.Samples.Length));
            foreach (var value in heightfield.Samples) writer.Write(value);
            writer.Write(checked((uint)heightfield.Surface.Length)); writer.Write(heightfield.Surface); writer.Write(heightfield.Area);
        }

        private static long AssetSize(string id, string sourceID, GolemHeightfield heightfield, int vertices, int triangles, int width)
        {
            if (string.IsNullOrWhiteSpace(id) || sourceID == null) throw new InvalidOperationException("Missing geometry identity.");
            var idBytes = Utf8.GetByteCount(id); var sourceBytes = Utf8.GetByteCount(sourceID);
            if (idBytes > 4096 || sourceBytes > 4096) throw new InvalidOperationException("Geometry identity exceeds 4096 UTF-8 bytes.");
            if (heightfield != null)
            {
                heightfield.Validate();
                return 9L + idBytes + sourceBytes + 25 + (long)heightfield.Samples.Length * 4 + heightfield.Surface.Length;
            }
            if (vertices < 3 || triangles < 1 || vertices > 8 * 1024 * 1024 || triangles > 8 * 1024 * 1024)
                throw new InvalidOperationException("Invalid geometry mesh counts.");
            return 9L + idBytes + sourceBytes + 8 + (long)vertices * width + (long)triangles * 13;
        }

        private static void CheckBudget(int count, IEnumerable<long> sizes)
        {
            if (count > 100000) throw new InvalidOperationException("Geometry source asset limit exceeded.");
            long bytes = 12;
            foreach (var size in sizes)
            {
                bytes += size;
                if (bytes > 256L * 1024 * 1024) throw new InvalidOperationException("Binary geometry source exceeds 256 MiB.");
            }
        }

        private static void WriteString(BinaryWriter writer, string value)
        {
            if (value == null) throw new InvalidOperationException("Missing geometry identity.");
            var bytes = Utf8.GetBytes(value);
            if (bytes.Length > 4096) throw new InvalidOperationException("Geometry identity exceeds 4096 UTF-8 bytes.");
            writer.Write(checked((uint)bytes.Length)); writer.Write(bytes);
        }

        private static byte[] Finish(MemoryStream stream)
        {
            if (stream.Length > 256L * 1024 * 1024) throw new InvalidOperationException("Binary geometry source exceeds 256 MiB.");
            return stream.ToArray();
        }
    }
}
