using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.IO;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Exports explicit, saved scene authoring geometry as pure-Go bake input. Never invokes a baker.</summary>
    public static class GolemNavigationExporter
    {
        /// <summary>All source errors prevent any artifact or manifest mutation.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool BytesChanged;
            public string OutputPath;
            /// <summary>Committed source and binary dependency paths/hashes; empty on failure.</summary>
            public IReadOnlyList<GolemScribeArtifactRecord> Artifacts { get; internal set; } = Array.Empty<GolemScribeArtifactRecord>();
        }

        /// <summary>Exports one world/profile; other worlds, including unloaded scenes, retain their manifest entries.</summary>
        public static ExportResult Export(GolemNavigationWorld world, GolemScribeExportOptions options = null)
        {
            var result = new ExportResult();
            if (!GolemScribeContext.TryLoad(GolemUnityEditorSettings.instance.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            if (!TryCollectArtifacts(world, options, out var records, out error)) { result.Errors.Add(error); return result; }
            var record = records[0];
            if (!GolemScribeArtifacts.TryResolveContainedPath(context.ProjectRoot, record.Path, out _, out _, out error))
            { result.Errors.Add(error); return result; }
            var reconcile = GolemScribeArtifacts.ReconcileSource(context.ProjectRoot,
                GolemScribeConstants.ArtifactKindNavigationSource, record.SourceGuid, context.Previous, records);
            result.Errors.AddRange(reconcile.Errors); result.Warnings.AddRange(reconcile.Warnings);
            result.BytesChanged = reconcile.AnyBytesChanged;
            result.OutputPath = result.Errors.Count == 0 ? record.Path : null;
            if (result.Errors.Count == 0) result.Artifacts = records.Select(r => new GolemScribeArtifactRecord
            { Kind = r.Kind, SourceGuid = r.SourceGuid, Entity = r.Entity, Path = r.Path, Hash = r.Hash }).ToArray();
            return result;
        }

        /// <summary>Checks source validity and artifact drift for one saved world, without writing or baking.</summary>
        public static GolemScribeValidator.ValidationResult Validate(GolemNavigationWorld world, GolemScribeExportOptions options = null)
        {
            var result = new GolemScribeValidator.ValidationResult();
            if (!GolemScribeContext.TryLoad(GolemUnityEditorSettings.instance.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            if (!TryCollectArtifacts(world, options, out var records, out error)) { result.Errors.Add(error); return result; }
            var record = records[0];
            GolemScribeValidator.ValidateManifestRecords(context.ProjectRoot, context.Previous, result);
            GolemScribeValidator.CompareDesiredToDisk(context.ProjectRoot, context.Previous, records,
                new[] { GolemScribeConstants.ArtifactKindNavigationSource }, result, record.SourceGuid);
            return result;
        }

        [MenuItem("Tools/Golem/Scribe/Export Selected Navigation World")]
        private static void ExportSelected()
        {
            var world = Selection.activeGameObject == null ? null : Selection.activeGameObject.GetComponentInParent<GolemNavigationWorld>(true);
            var result = Export(world);
            foreach (var error in result.Errors) Debug.LogError("Golem navigation export: " + error);
            foreach (var warning in result.Warnings) Debug.LogWarning("Golem navigation export: " + warning);
            if (result.Errors.Count == 0) Debug.Log("Golem navigation source exported: " + result.OutputPath + ". Run the pure-Go baker explicitly to produce a navigation artifact.");
        }

        [MenuItem("Tools/Golem/Scribe/Validate Selected Navigation World")]
        private static void ValidateSelected()
        {
            var world = Selection.activeGameObject == null ? null : Selection.activeGameObject.GetComponentInParent<GolemNavigationWorld>(true);
            GolemScribeValidator.LogResult(Validate(world));
        }

        // Captures all Unity objects before building the pure immutable artifact plan. Any exception fails
        // the whole selected world, rather than writing an apparently valid but incomplete source file.
        internal static bool TryCollect(GolemNavigationWorld world, out GolemScribeArtifactRecord record, out string error)
        {
            var success = TryCollectArtifacts(world, null, out var records, out error);
            record = success ? records[0] : null; return success;
        }

        internal static bool TryCollectArtifacts(GolemNavigationWorld world, GolemScribeExportOptions options,
            out GolemScribeArtifactRecord[] records, out string error)
        {
            records = null; error = null;
            try
            {
                var identity = new GolemScribeIdentityContext(options);
                string ObjectID(Component value, string explicitID) => identity.Object(value, () => ObjectIdentity(value, explicitID));
                if (world == null) throw new InvalidOperationException("Select an object under a Navigation World marker.");
                if (!world.gameObject.scene.IsValid() || string.IsNullOrEmpty(world.gameObject.scene.path) || world.gameObject.scene.isDirty)
                    throw new InvalidOperationException("Save the scene before export so object identities and source data can be restored reliably.");
                if (EditorApplication.isPlayingOrWillChangePlaymode) throw new InvalidOperationException("Navigation source export requires Edit Mode.");
                if (string.IsNullOrWhiteSpace(world.OutputPath) || !world.OutputPath.EndsWith(".json", StringComparison.OrdinalIgnoreCase))
                    throw new InvalidOperationException("Navigation output must be a project-relative .json path.");

                var assets = new Dictionary<string, NavigationAsset>(StringComparer.Ordinal);
                var instances = new List<NavigationInstance>();
                foreach (var marker in world.GetComponentsInChildren<GolemNavigationSource>(true))
                {
                    if (marker.GetComponentInParent<GolemNavigationWorld>(true) != world) continue;
                    var mesh = marker.GetComponent<MeshFilter>(); var terrain = marker.GetComponent<Terrain>();
                    if ((mesh == null) == (terrain == null)) throw new InvalidOperationException(marker.name + ": source requires exactly one MeshFilter or Terrain on the same object.");
                    if (terrain != null) ValidateTerrainTransform(marker.transform);
                    if (marker.Area < 0 || marker.Area > 63) throw new InvalidOperationException(marker.name + ": area must be 0..63.");
                    var assetObject = mesh != null ? (UnityEngine.Object)mesh.sharedMesh : terrain.terrainData;
                    var assetID = identity.Asset(assetObject, () => AssetIdentity(assetObject)) + "/area/" + marker.Area.ToString(CultureInfo.InvariantCulture);
                    if (!assets.ContainsKey(assetID)) assets.Add(assetID, mesh != null
                        ? CaptureMesh(mesh.sharedMesh, assetID, marker.Area) : new NavigationAsset { ID = assetID, Heightfield = CaptureHeightfield(terrain.terrainData, marker.Area) });
                    instances.Add(new NavigationInstance
                    {
                        ID = ObjectID(marker, marker.StableID), AssetID = assetID, Revision = Revision(marker.Revision),
                        Transform = Matrix(marker.transform.localToWorldMatrix), Disabled = !marker.ExportEnabled || !marker.enabled
                    });
                }
                var scene = new NavigationScene
                {
                    Assets = assets.Values.ToArray(), Instances = instances.ToArray(),
                    Volumes = world.GetComponentsInChildren<GolemNavigationVolume>(true)
                        .Where(m => m.GetComponentInParent<GolemNavigationWorld>(true) == world).Select(m => CaptureVolume(m, ObjectID)).ToArray(),
                    Links = world.GetComponentsInChildren<GolemNavigationLink>(true)
                        .Where(m => m.GetComponentInParent<GolemNavigationWorld>(true) == world).Select(m => CaptureLink(m, ObjectID)).ToArray()
                };
                var bytes = GolemGeometryDocument.BuildNavigation(scene.Assets);
                var binaryPath = Path.ChangeExtension(world.OutputPath, ".golemgeo").Replace('\\', '/');
                var hash = GolemScribeManifest.ComputeBytesHash(bytes);
                var content = GolemNavigationDocument.Build(scene, CaptureConfig(world),
                    new NavigationGeometryReference { Path = Path.GetFileName(binaryPath), SHA256 = hash });
                var sourceID = ObjectID(world, null);
                records = new[]
                {
                    new GolemScribeArtifactRecord { Kind = GolemScribeConstants.ArtifactKindNavigationSource, SourceGuid = sourceID,
                        Entity = world.WorldID + "/" + world.ProfileID, Path = world.OutputPath,
                        Hash = GolemScribeManifest.ComputeContentHash(content), PendingContent = content },
                    new GolemScribeArtifactRecord { Kind = GolemScribeConstants.ArtifactKindNavigationSource, SourceGuid = sourceID,
                        Entity = world.WorldID + "/" + world.ProfileID, Path = binaryPath, Hash = hash, PendingBytes = bytes }
                };
                return true;
            }
            catch (Exception ex) { error = ex.Message; return false; }
        }

        internal static NavigationConfig CaptureConfig(GolemNavigationWorld world) => new NavigationConfig
        {
            WorldID = world.WorldID, Revision = Revision(world.Revision),
            Profile = new NavigationProfile { ID = world.ProfileID, Radius = world.AgentRadius, Height = world.AgentHeight, MaxSlope = world.MaximumSlope, MaxClimb = world.MaximumClimb },
            Settings = new NavigationSettings
            {
                Origin = Vector(world.Origin), Bounds = Bounds(world.WorldBounds), CellSize = world.CellSize, CellHeight = world.CellHeight,
                TileSize = world.TileSize, Partition = world.Partition.ToString().ToLowerInvariant(), BuildLayers = world.BuildHeightfieldLayers,
                MinRegionArea = world.MinimumRegionArea, MergeRegionArea = world.MergeRegionArea, MaxEdgeLength = world.MaximumEdgeLength,
                MaxSimplificationError = world.MaximumSimplificationError, DetailSampleDistance = world.DetailSampleDistance, DetailSampleMaxError = world.DetailSampleMaximumError
            },
            AreaFlags = world.AreaFlags == null || world.AreaFlags.Length == 0 ? new int[64] : (int[])world.AreaFlags.Clone()
        };

        internal static NavigationAsset CaptureMesh(Mesh mesh, string id, int area)
        {
            if (mesh == null || !mesh.isReadable) throw new InvalidOperationException("Navigation meshes must exist and have Read/Write enabled; no asset import settings are changed automatically.");
            for (var i = 0; i < mesh.subMeshCount; i++)
                if (mesh.GetTopology(i) != MeshTopology.Triangles) throw new InvalidOperationException(mesh.name + ": non-triangle submesh is unsupported.");
            if (mesh.vertexCount > 8 * 1024 * 1024) throw new InvalidOperationException(mesh.name + ": mesh vertex limit exceeded.");
            var indices = mesh.triangles;
            if (indices.Length % 3 != 0 || indices.Length / 3 > 8 * 1024 * 1024) throw new InvalidOperationException(mesh.name + ": invalid or oversized triangle array.");
            var triangles = new NavigationTriangle[indices.Length / 3];
            for (var i = 0; i < triangles.Length; i++) triangles[i] = new NavigationTriangle
            { Indices = new[] { checked((uint)indices[i * 3]), checked((uint)indices[i * 3 + 1]), checked((uint)indices[i * 3 + 2]) }, Area = area };
            return new NavigationAsset { ID = id, Vertices = mesh.vertices.Select(Vector).ToArray(), Triangles = triangles };
        }

        internal static NavigationAsset CaptureTerrain(TerrainData data, string id, int area)
        {
            if (data == null) throw new InvalidOperationException("Navigation terrain has no TerrainData asset.");
            var resolution = data.heightmapResolution;
            if (resolution < 2 || (long)resolution * resolution > 8 * 1024 * 1024 || 2L * (resolution - 1) * (resolution - 1) > 8 * 1024 * 1024)
                throw new InvalidOperationException("Terrain exceeds source element limits; split it into smaller TerrainData assets.");
            if (data.holesResolution != resolution - 1) throw new InvalidOperationException("Terrain holes and heightfield resolutions do not match; export refused.");
            var heights = data.GetHeights(0, 0, resolution, resolution);
            var holes = data.GetHoles(0, 0, resolution - 1, resolution - 1);
            var vertices = new NavigationVector[resolution * resolution];
            var size = data.size;
            for (var z = 0; z < resolution; z++) for (var x = 0; x < resolution; x++)
                vertices[z * resolution + x] = new NavigationVector((double)x * size.x / (resolution - 1), (double)heights[z, x] * size.y, (double)z * size.z / (resolution - 1));
            var triangles = new List<NavigationTriangle>();
            for (var z = 0; z < resolution - 1; z++) for (var x = 0; x < resolution - 1; x++)
            {
                if (!holes[z, x]) continue; // Unity true means surface; false means hole.
                var a = (uint)(z * resolution + x); var b = a + 1; var c = a + (uint)resolution; var d = c + 1;
                triangles.Add(new NavigationTriangle { Indices = new[] { a, c, b }, Area = area });
                triangles.Add(new NavigationTriangle { Indices = new[] { b, c, d }, Area = area });
            }
            return new NavigationAsset { ID = id, Vertices = vertices, Triangles = triangles.ToArray() };
        }

        internal static GolemHeightfield CaptureHeightfield(TerrainData data, int area)
        {
            if (!data) throw new InvalidOperationException("Terrain has no TerrainData asset.");
            var resolution = data.heightmapResolution;
            if (resolution < 2 || (long)resolution * resolution > 8 * 1024 * 1024 || 2L * (resolution - 1) * (resolution - 1) > 8 * 1024 * 1024)
                throw new InvalidOperationException("Terrain exceeds source element limits.");
            if (data.holesResolution != resolution - 1) throw new InvalidOperationException("Terrain holes and heightfield resolutions do not match.");
            var heights = data.GetHeights(0, 0, resolution, resolution);
            var holes = data.GetHoles(0, 0, resolution - 1, resolution - 1);
            var cells = checked((resolution - 1) * (resolution - 1));
            var result = new GolemHeightfield { Resolution = resolution, SizeX = data.size.x, SizeY = data.size.y, SizeZ = data.size.z,
                Samples = new float[checked(resolution * resolution)], Surface = new byte[(cells + 7) / 8], Area = checked((byte)area) };
            for (var z = 0; z < resolution; z++) for (var x = 0; x < resolution; x++) result.Samples[z * resolution + x] = heights[z, x];
            for (var z = 0; z < resolution - 1; z++) for (var x = 0; x < resolution - 1; x++)
                if (holes[z, x]) { var cell = z * (resolution - 1) + x; result.Surface[cell >> 3] |= (byte)(1 << (cell & 7)); }
            result.Validate(); return result;
        }

        internal static void ValidateTerrainTransform(Transform transform)
        {
            // Matches Unity's TerrainInspectorUtility.IsValidTransform contract. Terrain does not
            // support scaled/rotated parent transforms; exporting a transformed heightfield would
            // disagree with the authored Terrain. Mesh sources support arbitrary affine transforms.
            for (var current = transform; current != null; current = current.parent)
                if (current.localScale != Vector3.one || current.localRotation != Quaternion.identity)
                    throw new InvalidOperationException(transform.name + ": Terrain and its parents must have identity rotation and scale. Set TerrainData size, or author a MeshFilter for transformed geometry.");
        }

        internal static NavigationVolume CaptureVolume(GolemNavigationVolume marker, Func<Component, string, string> identity = null)
        {
            identity = identity ?? ObjectIdentity;
            var m = marker.transform.localToWorldMatrix;
            // A horizontal prism cannot represent tilt or horizontal/vertical shear faithfully.
            if (Math.Abs(m.m01) > 1e-6 || Math.Abs(m.m21) > 1e-6 || Math.Abs(m.m10) > 1e-6 || Math.Abs(m.m12) > 1e-6 || Math.Abs(m.determinant) < 1e-12)
                throw new InvalidOperationException(marker.name + ": navigation volumes must be nonsingular horizontal prisms (Y rotation only).");
            if (marker.Size.x <= 0 || marker.Size.y <= 0 || marker.Size.z <= 0) throw new InvalidOperationException(marker.name + ": volume size must be positive.");
            var volume = new NavigationVolume
            {
                ID = identity(marker, marker.StableID), Revision = Revision(marker.Revision), Area = marker.Area, Exclude = marker.Exclude,
                Priority = marker.Priority, Disabled = !marker.ExportEnabled || !marker.enabled
            };
            if (marker.Shape == NavigationVolumeShape.Cylinder)
            {
                var xAxis = m.MultiplyVector(Vector3.right); var zAxis = m.MultiplyVector(Vector3.forward);
                if (Math.Abs(xAxis.magnitude - zAxis.magnitude) > 1e-5 * Math.Max(xAxis.magnitude, zAxis.magnitude) || Math.Abs(Vector3.Dot(xAxis, zAxis)) > 1e-5 * xAxis.magnitude * zAxis.magnitude)
                    throw new InvalidOperationException(marker.name + ": cylinders require equal orthogonal X/Z scale; use a box for other shapes.");
                volume.Shape = "cylinder"; volume.Radius = marker.Radius * (double)xAxis.magnitude;
                var center = m.MultiplyPoint3x4(marker.Center); var halfHeight = Math.Abs(m.m11) * (double)marker.Size.y / 2;
                volume.Bounds = new NavigationBounds
                {
                    Min = new NavigationVector(Outward(center.x - volume.Radius, false), center.y - halfHeight, Outward(center.z - volume.Radius, false)),
                    Max = new NavigationVector(Outward(center.x + volume.Radius, true), center.y + halfHeight, Outward(center.z + volume.Radius, true))
                };
            }
            else if (marker.Shape == NavigationVolumeShape.Box)
            {
                volume.Shape = "convex";
                var half = marker.Size * 0.5f;
                var points = new[] { new Vector3(-half.x, -half.y, -half.z), new Vector3(half.x, -half.y, -half.z), new Vector3(half.x, -half.y, half.z), new Vector3(-half.x, -half.y, half.z) }
                    .Select(p => m.MultiplyPoint3x4(marker.Center + p)).ToArray();
                var top = m.MultiplyPoint3x4(marker.Center + new Vector3(0, half.y, 0));
                var minY = Math.Min(points[0].y, top.y); var maxY = Math.Max(points[0].y, top.y);
                volume.Points = points.Select(p => new NavigationVector(p.x, minY, p.z)).ToArray();
                volume.Bounds = new NavigationBounds
                {
                    Min = new NavigationVector(points.Min(p => p.x), minY, points.Min(p => p.z)),
                    Max = new NavigationVector(points.Max(p => p.x), maxY, points.Max(p => p.z))
                };
            }
            else throw new InvalidOperationException("Unknown navigation volume shape.");
            return volume;
        }

        private static NavigationLink CaptureLink(GolemNavigationLink marker, Func<Component, string, string> identity) => new NavigationLink
        {
            ID = identity(marker, marker.StableID), Revision = Revision(marker.Revision),
            Start = Vector(marker.transform.TransformPoint(marker.LocalStart)), End = Vector(marker.transform.TransformPoint(marker.LocalEnd)),
            Radius = marker.Radius, Area = marker.Area, Flags = marker.Flags, Bidirectional = marker.Bidirectional,
            Disabled = !marker.ExportEnabled || !marker.enabled, Profiles = marker.Profiles == null ? Array.Empty<string>() : (string[])marker.Profiles.Clone(), Action = marker.Action ?? ""
        };

        private static string AssetIdentity(UnityEngine.Object asset)
        {
            if (asset == null || !EditorUtility.IsPersistent(asset) || !AssetDatabase.TryGetGUIDAndLocalFileIdentifier(asset, out string guid, out long localID) || string.IsNullOrEmpty(guid))
                throw new InvalidOperationException("Navigation geometry must reference a saved Mesh/TerrainData asset (sharedMesh); transient/procedural geometry must first be saved explicitly.");
            return "unity-asset:" + guid + ":" + localID.ToString(CultureInfo.InvariantCulture);
        }
        private static string ObjectIdentity(Component marker, string explicitID)
        {
            if (!string.IsNullOrWhiteSpace(explicitID)) return explicitID;
            var id = GlobalObjectId.GetGlobalObjectIdSlow(marker);
            if (id.targetObjectId == 0) throw new InvalidOperationException(marker.name + ": object has no saved stable identity.");
            return "unity-object:" + id;
        }
        private static ulong Revision(int revision) { if (revision < 1) throw new InvalidOperationException("Source revision must be positive."); return (ulong)revision; }
        // One representable step outward prevents center +/- radius rounding from producing a box
        // narrower than the exact diameter. BitConverter is available on Unity's .NET Standard floor.
        private static double Outward(double value, bool up)
        {
            if (double.IsNaN(value) || double.IsInfinity(value)) return value;
            if (value == 0) return up ? double.Epsilon : -double.Epsilon;
            var bits = BitConverter.DoubleToInt64Bits(value);
            return BitConverter.Int64BitsToDouble(bits + ((value > 0) == up ? 1 : -1));
        }
        private static NavigationVector Vector(Vector3 p) => new NavigationVector(p.x, p.y, p.z);
        private static NavigationBounds Bounds(Bounds b) => new NavigationBounds { Min = Vector(b.min), Max = Vector(b.max) };
        internal static double[] Matrix(Matrix4x4 m) => new double[]
        { m.m00, m.m01, m.m02, m.m03, m.m10, m.m11, m.m12, m.m13, m.m20, m.m21, m.m22, m.m23, m.m30, m.m31, m.m32, m.m33 };
    }
}
