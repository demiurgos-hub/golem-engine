using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    [Serializable] internal struct GoDebugVector { public float X, Y, Z; internal Vector3 Vector => new Vector3(X, Y, Z); }
    [Serializable] internal struct GoDebugRotation { public float X, Y, Z, W; internal Quaternion Rotation => new Quaternion(X, Y, Z, W); }
    [Serializable] internal struct GoDebugPose { public GoDebugVector Position; public GoDebugRotation Rotation; internal Matrix4x4 Matrix => Matrix4x4.TRS(Position.Vector, Rotation.Rotation, Vector3.one); }
    [Serializable] internal struct GoDebugHandle
    {
        public ulong World; public uint Index, Generation;
        internal string Key => World + "/" + Index + "/" + Generation;
        internal bool Valid => World != 0 && Index != 0 && Generation != 0;
    }
    [Serializable] internal sealed class GoDebugSupport { public bool Grounded; public GoDebugHandle Body, Collider; public GoDebugVector Point, LocalPoint, Normal; }
    [Serializable] internal sealed class GoDebugState
    {
        public GoDebugHandle Handle; public string ID; public long Owner; public int Mode;
        public GoDebugPose Pose, Anchor; public GoDebugVector LinearVelocity, AngularVelocity, CenterOfMass;
        public float Mass; public bool Enabled, Awake; public GoDebugSupport Support;
    }
    [Serializable] internal sealed class GoDebugBody { public GoDebugState State, Uncommitted; public bool HasUncommitted; }
    [Serializable] internal sealed class GoDebugRoles { public bool Solid, Sensor, Query; }
    [Serializable] internal sealed class GoDebugCollider
    {
        public GoDebugHandle Handle, Body, Shape; public string ID, ShapeID; public long Owner;
        public bool Enabled, GeometryOmitted; public GoDebugRoles Roles;
    }
    [Serializable] internal sealed class GoDebugGeometry
    {
        public GoDebugHandle Collider; public uint Child, BackendShapeIndex; public int Kind;
        public float Radius; public GoDebugVector PointA, PointB;
        public GoDebugVector[] Vertices; public uint[] Indices;
    }
    [Serializable] internal sealed class GoDebugPath { public GoDebugHandle Body; public GoDebugVector[] Positions; public GoDebugPose[] Poses; public float[] Fractions; }
    [Serializable] internal sealed class GoDebugPair { public GoDebugHandle A, B; public int Kind; }
    [Serializable] internal sealed class GoDebugContact { public GoDebugPair Pair; public int Phase, Reason; public GoDebugVector Point, Normal, Force; public float Depth, Fraction; public ulong Frame, Revision; }
    [Serializable] internal sealed class GoDebugShape { public int Kind; public float Radius; public GoDebugVector PointA, PointB, HalfExtents; public GoDebugVector[] Vertices; public uint[] Indices; public GoDebugShapeChild[] Children; }
    [Serializable] internal sealed class GoDebugShapeChild { public string ID; public GoDebugPose Pose; public GoDebugShape Shape; }
    [Serializable] internal sealed class GoDebugQueryInput { public int Kind, Mode; public GoDebugShape Shape; public GoDebugPose Pose; public GoDebugVector Translation; }
    [Serializable] internal sealed class GoDebugHit { public GoDebugHandle Body, Collider; public string Content; public GoDebugVector Point, Normal; public float Distance, Fraction, Depth; public bool Initial, Touching, Penetrating, Sensor, Blocking; }
    [Serializable] internal sealed class GoDebugQueryResult { public int Count, Total; public bool Incomplete, Blocked; }
    [Serializable] internal sealed class GoDebugQuery { public string Label, Error; public GoDebugQueryInput Query; public GoDebugQueryResult Result; public GoDebugHit[] Hits; }
    [Serializable] internal sealed class GoDebugTarget { public GoDebugHandle Body; public GoDebugVector From, Target; public string Label; }
    [Serializable] internal sealed class GoDebugOmissions { public int Bodies, Colliders, Geometry, Vertices, Triangles, Records; internal bool Any => Bodies != 0 || Colliders != 0 || Geometry != 0 || Vertices != 0 || Triangles != 0 || Records != 0; }
    [Serializable] internal sealed class GoDebugConfig { public GoDebugVector Gravity; public int Substeps, TickRate, Workers; public bool AllowSleep, Continuous; public int SolverVelocityIterations, SolverRelaxIterations; }
    [Serializable] internal sealed class GoPhysicsDebugCapture
    {
        public int Version; public string Backend, SourceSnapshot, Status, Fault;
        public GoDebugConfig Config;
        public ulong Revision, AcceptedFrame, Frame; public bool Incomplete, FrameAccepted;
        public int CCDCandidatePairs, CCDPairTests, CCDPredictiveContacts;
        public GoDebugOmissions Omitted; public GoDebugBody[] Bodies; public GoDebugCollider[] Colliders;
        public GoDebugGeometry[] Geometry; public GoDebugPath[] Paths; public GoDebugContact[] Contacts;
        public GoDebugQuery[] Queries; public GoDebugTarget[] Targets;
    }

    /// <summary>Reads bounded server diagnostic documents; never imports them as scene physics or recovery state.</summary>
    internal static class GolemPhysicsDebug
    {
        internal const int Version = 2;
        internal const int MaxBytes = 32 * 1024 * 1024;
        private static readonly HashSet<string> RootFields = new HashSet<string>(StringComparer.Ordinal) { "Version", "Config", "Backend", "SourceSnapshot", "Status", "Fault", "Revision", "AcceptedFrame", "Incomplete", "Omitted", "Bodies", "Colliders", "Geometry", "Frame", "FrameAccepted", "CCDCandidatePairs", "CCDPairTests", "CCDPredictiveContacts", "Paths", "Contacts", "Queries", "Targets" };
        internal static GoPhysicsDebugCapture ReadFile(string path)
        {
            using (var input = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read))
            {
                if (input.Length > MaxBytes) throw Invalid("byte limit");
                using (var reader = new StreamReader(input, new UTF8Encoding(false, true))) return Read(reader.ReadToEnd());
            }
        }
        internal static GoPhysicsDebugCapture Read(string json)
        {
            if (json == null || json.Length > MaxBytes || Encoding.UTF8.GetByteCount(json) > MaxBytes) throw Invalid("byte limit");
            CheckEnvelope(json);
            GoPhysicsDebugCapture c;
            try { c = JsonUtility.FromJson<GoPhysicsDebugCapture>(json); } catch (Exception ex) { throw Invalid("JSON: " + ex.Message); }
            Validate(c); return c;
        }
        private static void CheckEnvelope(string text)
        {
            var s = text.Trim(); if (s.Length < 2 || s[0] != '{' || s[s.Length - 1] != '}') throw Invalid("JSON envelope");
            var depth = 0; var tokens = 0; var roots = new HashSet<string>(StringComparer.Ordinal);
            for (var i = 0; i < s.Length; i++)
            {
                var ch = s[i];
                if (ch == '"')
                {
                    var begin = ++i; var escaped = false;
                    for (; i < s.Length; i++) { if (s[i] == '\\') { escaped = true; i++; } else if (s[i] == '"') break; }
                    if (i >= s.Length) throw Invalid("unterminated string");
                    var next = i + 1; while (next < s.Length && char.IsWhiteSpace(s[next])) next++;
                    if (depth == 1 && next < s.Length && s[next] == ':')
                    {
                        var key = s.Substring(begin, i - begin);
                        if (escaped || !RootFields.Contains(key) || !roots.Add(key)) throw Invalid("unknown or duplicate root field");
                    }
                    tokens++;
                }
                else if (ch == '{' || ch == '[') { if (++depth > 32) throw Invalid("nesting limit"); tokens++; }
                else if (ch == '}' || ch == ']') { if (--depth < 0 || depth == 0 && i != s.Length - 1) throw Invalid("trailing JSON"); }
                else if (ch == ',') tokens++;
                if (tokens > 2500000) throw Invalid("token limit");
            }
            if (depth != 0 || !roots.Contains("Version") || !roots.Contains("Status") || !roots.Contains("Omitted")) throw Invalid("incomplete envelope");
        }
        internal static void Validate(GoPhysicsDebugCapture c)
        {
            if (c == null) throw Invalid("missing capture");
            if (c.Version == 1) throw Invalid("capture version 1 uses retired solver settings; regenerate using version " + Version);
            if (c.Version != Version || (c.Status != "accepted" && c.Status != "pending" && c.Status != "faulted" && c.Status != "unsynchronized")) throw Invalid("version/status");
            if (c.Config == null || c.Config.Substeps < 1 || c.Config.Substeps > 128 || c.Config.TickRate < 1 || c.Config.TickRate > 1000 || c.Config.Workers < 1 || c.Config.Workers > 64) throw Invalid("solver config");
            if (c.Config.SolverVelocityIterations < 1 || c.Config.SolverVelocityIterations > 16 || c.Config.SolverRelaxIterations < 1 || c.Config.SolverRelaxIterations > 16) throw Invalid("solver iteration counts");
            Vector(c.Config.Gravity);
            if (c.Omitted == null || c.Omitted.Bodies < 0 || c.Omitted.Colliders < 0 || c.Omitted.Geometry < 0 || c.Omitted.Vertices < 0 || c.Omitted.Triangles < 0 || c.Omitted.Records < 0 || c.Incomplete != c.Omitted.Any) throw Invalid("omissions");
            if (c.FrameAccepted && c.Frame != c.AcceptedFrame) throw Invalid("accepted frame identity");
            if (c.CCDCandidatePairs < 0 || c.CCDPairTests < 0 || c.CCDPredictiveContacts < 0) throw Invalid("native CCD work counts");
            c.Bodies = c.Bodies ?? Array.Empty<GoDebugBody>(); c.Colliders = c.Colliders ?? Array.Empty<GoDebugCollider>(); c.Geometry = c.Geometry ?? Array.Empty<GoDebugGeometry>();
            c.Paths = c.Paths ?? Array.Empty<GoDebugPath>(); c.Contacts = c.Contacts ?? Array.Empty<GoDebugContact>(); c.Queries = c.Queries ?? Array.Empty<GoDebugQuery>(); c.Targets = c.Targets ?? Array.Empty<GoDebugTarget>();
            if (c.Bodies.Length > 4096 || c.Colliders.Length > 8192 || c.Geometry.Length > 8192) throw Invalid("object limit");
            var bodies = new HashSet<string>(); var colliders = new HashSet<string>();
            foreach (var b in c.Bodies)
            {
                if (b == null || b.State == null || !b.State.Handle.Valid || !bodies.Add(b.State.Handle.Key)) throw Invalid("body identity");
                State(b.State);
                // JsonUtility constructs empty objects for JSON null reference fields.
                if (!b.HasUncommitted) { if (b.Uncommitted != null && b.Uncommitted.Handle.Valid) throw Invalid("unflagged candidate pose"); b.Uncommitted = null; }
                else if (b.Uncommitted == null || !b.Uncommitted.Handle.Valid) throw Invalid("missing candidate pose");
                if (b.Uncommitted != null) { if (c.Status == "accepted" || b.Uncommitted.Handle.Key != b.State.Handle.Key) throw Invalid("candidate pose mislabeled"); State(b.Uncommitted); }
            }
            foreach (var v in c.Colliders) { if (v == null || !v.Handle.Valid || !colliders.Add(v.Handle.Key) || !bodies.Contains(v.Body.Key)) throw Invalid("collider identity"); Text(v.ID, 4096); }
            long vertices = 0, triangles = 0, records = c.Paths.Length + c.Contacts.Length + c.Queries.Length + c.Targets.Length;
            foreach (var g in c.Geometry)
            {
                if (g == null || !colliders.Contains(g.Collider.Key)) throw Invalid("geometry collider");
                Vector(g.PointA); Vector(g.PointB); Number(g.Radius);
                g.Vertices = g.Vertices ?? Array.Empty<GoDebugVector>(); g.Indices = g.Indices ?? Array.Empty<uint>();
                if (g.Kind == 1 || g.Kind == 2) { if (g.Radius <= 0 || g.Vertices.Length != 0 || g.Indices.Length != 0) throw Invalid("round geometry"); }
                else if (g.Kind != 4 && g.Kind != 5 || g.Vertices.Length < 3 || g.Indices.Length < 3) throw Invalid("geometry kind");
                Mesh(g.Vertices, g.Indices); vertices += g.Vertices.Length; triangles += g.Indices.Length / 3;
            }
            foreach (var path in c.Paths)
            {
                if (path == null) throw Invalid("path"); path.Positions = path.Positions ?? Array.Empty<GoDebugVector>(); path.Poses = path.Poses ?? Array.Empty<GoDebugPose>(); path.Fractions = path.Fractions ?? Array.Empty<float>();
                if (path.Poses.Length != 0 && path.Poses.Length != path.Positions.Length || path.Fractions.Length != 0 && path.Fractions.Length != path.Positions.Length) throw Invalid("path sizes");
                foreach (var v in path.Positions) Vector(v); foreach (var p in path.Poses) Pose(p); foreach (var f in path.Fractions) { Number(f); if (f < 0 || f > 1) throw Invalid("path fraction"); } records += path.Positions.Length;
            }
            foreach (var v in c.Contacts) { if (v == null || v.Pair == null) throw Invalid("contact"); Vector(v.Point); Vector(v.Normal); Vector(v.Force); Number(v.Depth); Number(v.Fraction); }
            foreach (var q in c.Queries)
            {
                if (q == null || q.Query == null || q.Result == null || q.Query.Kind < 1 || q.Query.Kind > 3) throw Invalid("query");
                Pose(q.Query.Pose); Vector(q.Query.Translation); Text(q.Label, 4096); Text(q.Error, 65536);
                q.Hits = q.Hits ?? Array.Empty<GoDebugHit>(); if (q.Result.Count < 0 || q.Result.Total < q.Result.Count || q.Hits.Length > q.Result.Count) throw Invalid("query counts");
                if (q.Query.Kind != 1)
                {
                    var shape = q.Query.Shape; if (shape == null || shape.Kind < 1 || shape.Kind > 4 || shape.Children != null && shape.Children.Length != 0) throw Invalid("query shape");
                    Vector(shape.PointA); Vector(shape.PointB); Vector(shape.HalfExtents); Number(shape.Radius);
                    if ((shape.Kind == 1 || shape.Kind == 2) && shape.Radius <= 0 || shape.Kind == 3 && (shape.HalfExtents.X <= 0 || shape.HalfExtents.Y <= 0 || shape.HalfExtents.Z <= 0)) throw Invalid("query dimensions");
                    shape.Vertices = shape.Vertices ?? Array.Empty<GoDebugVector>(); shape.Indices = shape.Indices ?? Array.Empty<uint>(); Mesh(shape.Vertices, shape.Indices); vertices += shape.Vertices.Length; triangles += shape.Indices.Length / 3;
                }
                foreach (var h in q.Hits) { if (h == null) throw Invalid("hit"); Vector(h.Point); Vector(h.Normal); Number(h.Distance); Number(h.Fraction); Number(h.Depth); } records += q.Hits.Length;
            }
            foreach (var v in c.Targets) { if (v == null) throw Invalid("target"); Vector(v.From); Vector(v.Target); Text(v.Label, 4096); }
            if (vertices > 200000 || triangles > 200000 || records > 65536) throw Invalid("geometry/record limit");
            Text(c.SourceSnapshot, 4096); Text(c.Backend, 4096); Text(c.Fault, 65536);
        }
        private static void State(GoDebugState s) { Pose(s.Pose); Pose(s.Anchor); Vector(s.LinearVelocity); Vector(s.AngularVelocity); Vector(s.CenterOfMass); Number(s.Mass); Text(s.ID, 4096); if (s.Support != null) { Vector(s.Support.Point); Vector(s.Support.Normal); } }
        private static void Mesh(GoDebugVector[] vertices, uint[] indices) { if (indices.Length % 3 != 0) throw Invalid("triangle count"); foreach (var v in vertices) Vector(v); foreach (var i in indices) if (i >= vertices.Length) throw Invalid("triangle index"); }
        private static void Pose(GoDebugPose p) { Vector(p.Position); var q = p.Rotation; Number(q.X); Number(q.Y); Number(q.Z); Number(q.W); if (Math.Abs(q.X*q.X+q.Y*q.Y+q.Z*q.Z+q.W*q.W-1) >= .00001f) throw Invalid("nonunit rotation"); }
        private static void Vector(GoDebugVector v) { Number(v.X); Number(v.Y); Number(v.Z); }
        private static void Number(float v) { if (float.IsNaN(v) || float.IsInfinity(v)) throw Invalid("nonfinite number"); }
        private static void Text(string s, int limit) { if (s != null && s.Length > limit) throw Invalid("text limit"); }
        private static InvalidDataException Invalid(string reason) => new InvalidDataException("Invalid Golem physics debug capture: " + reason);
    }
}
