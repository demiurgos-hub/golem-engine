using System;
using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using NUnit.Framework;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemNavigationExporterTests
    {
        private string folder;
        private Scene scene;
        private SceneSetup[] previousSetup;

        [SetUp]
        public void SetUp()
        {
            folder = "Assets/GolemNavigationTest_" + Guid.NewGuid().ToString("N");
            AssetDatabase.CreateFolder("Assets", folder.Substring("Assets/".Length));
            previousSetup = EditorSceneManager.GetSceneManagerSetup();
            // Unity's test runner saves/restores its scene setup. Single mode also works from the
            // fresh untitled startup scene where additive scene creation is forbidden.
            scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            SceneManager.SetActiveScene(scene);
        }

        [TearDown]
        public void TearDown()
        {
            if (scene.IsValid() && scene.isLoaded) EditorSceneManager.CloseScene(scene, true);
            if (previousSetup != null && previousSetup.Length > 0 && previousSetup.All(s => !string.IsNullOrEmpty(s.path)))
                EditorSceneManager.RestoreSceneManagerSetup(previousSetup);
            AssetDatabase.DeleteAsset(folder);
        }

        [Test]
        public void CapturedConfigSelectsAuthoredFloat32Clearance()
        {
            var world = new GameObject("World").AddComponent<GolemNavigationWorld>();
            world.MaximumClimb = 0.5f; world.CellHeight = 0.1f;
            var config = GolemNavigationExporter.CaptureConfig(world);
            Assert.That(config.Settings.ClearancePrecision, Is.EqualTo(GolemNavigationDocument.Binary32Clearance));
            Assert.That(config.Settings.CellHeight, Is.EqualTo((double)world.CellHeight));
            Assert.That(GolemNavigationDocument.QuantizeClearance(config.Profile, config.Settings).Climb, Is.EqualTo(5));
        }

        [Test]
        public void SavedInstancesShareGeometryButHaveDistinctStableIdentities()
        {
            var world = new GameObject("World").AddComponent<GolemNavigationWorld>();
            var mesh = new Mesh
            {
                vertices = new[] { Vector3.zero, new Vector3(0, 0, 4), new Vector3(4, 0, 0) },
                triangles = new[] { 0, 1, 2 }
            };
            AssetDatabase.CreateAsset(mesh, folder + "/ground.asset");
            for (var i = 0; i < 2; i++)
            {
                var obj = new GameObject("Ground"); obj.transform.SetParent(world.transform);
                obj.AddComponent<MeshFilter>().sharedMesh = mesh; obj.AddComponent<GolemNavigationSource>();
                if (i == 1) { obj.transform.position = new Vector3(-8, 2, 0); obj.transform.localScale = new Vector3(-1, 2, 3); }
            }
            Assert.That(EditorSceneManager.SaveScene(scene, folder + "/world.unity"), Is.True);
            Assert.That(GolemNavigationExporter.TryCollect(world, out var record, out var error), Is.True, error);
            Assert.That(Regex.Matches(record.PendingContent, "\"AssetID\"").Count, Is.EqualTo(2));
            Assert.That(Regex.Matches(record.PendingContent, "\"Triangles\"").Count, Is.Zero);
            Assert.That(GolemNavigationExporter.TryCollectArtifacts(world, null, out var compact, out error), Is.True, error);
            Assert.That(compact.Length, Is.EqualTo(2));
            Assert.That(BitConverter.ToUInt32(compact[1].PendingBytes, 8), Is.EqualTo(1));
            Assert.That(record.PendingContent, Does.Contain("\"Transform\":[-1,0,0,-8,0,2,0,2,0,0,3,0,0,0,0,1]"));
            var original = record.PendingContent; var source = record.SourceGuid;
            Assert.That(AssetDatabase.RenameAsset(folder + "/ground.asset", "renamed-ground"), Is.Empty);
            Assert.That(GolemNavigationExporter.TryCollect(world, out record, out error), Is.True, error);
            Assert.That(record.PendingContent, Is.EqualTo(original)); Assert.That(record.SourceGuid, Is.EqualTo(source));
            // Invalid marked geometry aborts the entire world before returning any desired artifact.
            var invalid = new GameObject("Missing Mesh"); invalid.transform.SetParent(world.transform); invalid.AddComponent<GolemNavigationSource>();
            EditorSceneManager.SaveScene(scene);
            Assert.That(GolemNavigationExporter.TryCollect(world, out record, out error), Is.False);
            Assert.That(record, Is.Null); Assert.That(error, Does.Contain("exactly one MeshFilter or Terrain"));
        }

        [Test]
        public void ImportedNonReadableNavigationMeshPreservesBytesWithoutChangingImporter()
        {
            var path = folder + "/navigation.obj";
            File.WriteAllText(path, "o Navigation\nv 0 0 0\nv 0 0 2\nv 3 0 0\nf 1 2 3\n");
            AssetDatabase.ImportAsset(path, ImportAssetOptions.ForceSynchronousImport);
            var importer = (ModelImporter)AssetImporter.GetAtPath(path);
            importer.isReadable = true; importer.SaveAndReimport();
            var world = new GameObject("World").AddComponent<GolemNavigationWorld>();
            var filter = world.gameObject.AddComponent<MeshFilter>();
            filter.sharedMesh = AssetDatabase.LoadAllAssetsAtPath(path).OfType<Mesh>().Single();
            world.gameObject.AddComponent<GolemNavigationSource>().Area = 3;
            Assert.That(EditorSceneManager.SaveScene(scene, folder + "/world.unity"), Is.True);
            Assert.That(GolemNavigationExporter.TryCollectArtifacts(world, null, out var readable, out var error), Is.True, error);
            importer.isReadable = false; importer.SaveAndReimport();
            filter.sharedMesh = AssetDatabase.LoadAllAssetsAtPath(path).OfType<Mesh>().Single();
            EditorSceneManager.SaveScene(scene);
            var metadata = File.ReadAllBytes(path + ".meta");
            Assert.That(GolemNavigationExporter.TryCollectArtifacts(world, null, out var readOnly, out error), Is.True, error);
            Assert.That(readOnly[0].PendingContent, Is.EqualTo(readable[0].PendingContent));
            Assert.That(readOnly[1].PendingBytes, Is.EqualTo(readable[1].PendingBytes));
            Assert.That(filter.sharedMesh.isReadable, Is.False);
            Assert.That(((ModelImporter)AssetImporter.GetAtPath(path)).isReadable, Is.False);
            Assert.That(File.ReadAllBytes(path + ".meta"), Is.EqualTo(metadata));
        }

        [Test]
        public void MeshSnapshotPreservesSubmeshOrderBaseVerticesAndArea()
        {
            var mesh = new Mesh { vertices = new[] { Vector3.zero, Vector3.forward, Vector3.right, Vector3.up, Vector3.up + Vector3.forward, Vector3.up + Vector3.right } };
            try
            {
                mesh.subMeshCount = 2;
                mesh.SetIndices(new[] { 0, 1, 2 }, MeshTopology.Triangles, 0, false, 3);
                mesh.SetIndices(new[] { 0, 2, 1 }, MeshTopology.Triangles, 1, false, 0);
                var captured = GolemNavigationExporter.CaptureMesh(mesh, "mesh", 7);
                Assert.That(captured.Triangles[0].Indices, Is.EqualTo(new uint[] { 3, 4, 5 }));
                Assert.That(captured.Triangles[1].Indices, Is.EqualTo(new uint[] { 0, 2, 1 }));
                Assert.That(captured.Triangles.All(face => face.Area == 7), Is.True);
                Assert.That(captured.Vertices.Select(point => new Vector3((float)point.X, (float)point.Y, (float)point.Z)), Is.EqualTo(mesh.vertices));
                mesh.SetIndices(new[] { 0, 1 }, MeshTopology.Lines, 1);
                Assert.Throws<InvalidOperationException>(() => GolemNavigationExporter.CaptureMesh(mesh, "mesh", 7));
            }
            finally { UnityEngine.Object.DestroyImmediate(mesh); }
        }

        [Test]
        public void ExplicitColliderSourcesShareCanonicalBuffersAndKeepMarkerState()
        {
            var world = new GameObject("World").AddComponent<GolemNavigationWorld>();
            for (var index = 0; index < 2; index++)
            {
                var owner = new GameObject("Collider" + index); owner.transform.SetParent(world.transform, false);
                owner.transform.localPosition = new Vector3(index * 5, 2, 1);
                owner.transform.localScale = new Vector3(index == 0 ? 1 : -2, 3, 4);
                var box = owner.AddComponent<BoxCollider>(); box.center = new Vector3(1, 2, 3); box.size = new Vector3(2, 4, 6);
                box.isTrigger = true; box.enabled = false;
                var marker = owner.AddComponent<GolemNavigationSource>(); marker.SourceCollider = box; marker.Area = 4; marker.ExportEnabled = index == 0;
            }
            EditorSceneManager.SaveScene(scene, folder + "/world.unity");
            Assert.That(GolemNavigationExporter.TryCollectArtifacts(world, null, out var records, out var error), Is.True, error);
            Assert.That(BitConverter.ToUInt32(records[1].PendingBytes, 8), Is.EqualTo(1));
            Assert.That(Regex.Matches(records[0].PendingContent, "\"AssetID\"").Count, Is.EqualTo(2));
            Assert.That(records[0].PendingContent, Does.Contain("\"Disabled\":false").And.Contain("\"Disabled\":true"));
            Assert.That(records[0].PendingContent, Does.Contain("\"Transform\":[-4,0,0,3,0,12,0,8,0,0,24,13,0,0,0,1]"));
            var nested = new GameObject("Nested world").AddComponent<GolemNavigationWorld>(); nested.transform.SetParent(world.transform);
            world.GetComponentInChildren<GolemNavigationSource>().SourceCollider = nested.gameObject.AddComponent<BoxCollider>();
            EditorSceneManager.SaveScene(scene);
            Assert.That(GolemNavigationExporter.TryCollectArtifacts(world, null, out _, out error), Is.False);
            Assert.That(error, Does.Contain("same navigation world"));
        }

        [TestCase("box", 1)]
        [TestCase("sphere", 1)]
        [TestCase("capsule", 0)]
        [TestCase("capsule", 1)]
        [TestCase("capsule", 2)]
        public void PrimitiveSurfacesPreserveAnalyticBoundsAreaAndOutwardWinding(string shape, int axis)
        {
            var owner = new GameObject("Primitive");
            Collider collider;
            Vector3 expectedSize;
            if (shape == "box") { var box = owner.AddComponent<BoxCollider>(); box.size = new Vector3(2, 4, 6); collider = box; expectedSize = box.size; }
            else if (shape == "sphere") { var sphere = owner.AddComponent<SphereCollider>(); sphere.radius = 2; collider = sphere; expectedSize = Vector3.one * 4; }
            else { var capsule = owner.AddComponent<CapsuleCollider>(); capsule.radius = 1; capsule.height = 6; capsule.direction = axis; collider = capsule; expectedSize = Vector3.one * 2; expectedSize[axis] = 6; }
            var id = GolemNavigationPrimitiveGeometry.Identity(collider, 5);
            var asset = GolemNavigationPrimitiveGeometry.Capture(collider, id, 5);
            var transform = GolemNavigationPrimitiveGeometry.Transform(collider);
            var points = asset.Vertices.Select(point => transform.MultiplyPoint3x4(new Vector3((float)point.X, (float)point.Y, (float)point.Z))).ToArray();
            for (var dimension = 0; dimension < 3; dimension++)
                Assert.That(points.Max(point => point[dimension]) - points.Min(point => point[dimension]), Is.EqualTo(expectedSize[dimension]).Within(.00001));
            foreach (var face in asset.Triangles)
            {
                var a = points[face.Indices[0]]; var b = points[face.Indices[1]]; var c = points[face.Indices[2]];
                Assert.That(Vector3.Dot(Vector3.Cross(b - a, c - a), (a + b + c) / 3), Is.GreaterThan(0), "Degenerate or inward primitive triangle");
                Assert.That(face.Area, Is.EqualTo(5));
            }
            var source = GolemNavigationDocumentTests.Scene(); source.Assets = new[] { asset };
            source.Instances = new[] { new NavigationInstance { ID = "primitive", AssetID = id, Revision = 1, Transform = GolemNavigationExporter.Matrix(transform) } };
            Assert.DoesNotThrow(() => GolemNavigationDocument.Build(source, GolemNavigationDocumentTests.Config()));
        }

        [Test]
        public void TerrainHolesAreOmittedAndTrianglesFaceUp()
        {
            var data = new TerrainData { heightmapResolution = 33, size = new Vector3(32, 8, 32) };
            try
            {
                var holes = new bool[32, 32];
                for (var z = 0; z < 32; z++) for (var x = 0; x < 32; x++) holes[z, x] = true;
                holes[4, 5] = false; data.SetHoles(0, 0, holes);
                var asset = GolemNavigationExporter.CaptureTerrain(data, "terrain", 3);
                var compact = GolemNavigationExporter.CaptureHeightfield(data, 3);
                Assert.That(compact.Samples.Length, Is.EqualTo(33 * 33));
                Assert.That(compact.Surface[(4 * 32 + 5) / 8] & (1 << ((4 * 32 + 5) % 8)), Is.Zero);
                Assert.That(compact.Area, Is.EqualTo(3));
                Assert.That(asset.Vertices.Length, Is.EqualTo(33 * 33));
                Assert.That(asset.Triangles.Length, Is.EqualTo(32 * 32 * 2 - 2));
                Assert.That(asset.Triangles.All(t => t.Area == 3), Is.True);
                foreach (var t in asset.Triangles)
                {
                    var a = asset.Vertices[t.Indices[0]]; var b = asset.Vertices[t.Indices[1]]; var c = asset.Vertices[t.Indices[2]];
                    Assert.That((b.Z - a.Z) * (c.X - a.X) - (b.X - a.X) * (c.Z - a.Z), Is.GreaterThan(0));
                    Assert.That(t.Indices, Is.Not.EqualTo(new uint[] { 4 * 33 + 5, 5 * 33 + 5, 4 * 33 + 6 }));
                }
            }
            finally { UnityEngine.Object.DestroyImmediate(data); }
        }

        [Test]
        public void TerrainRejectsUnsupportedParentScaleAndRotation()
        {
            var parent = new GameObject("Parent"); var child = new GameObject("Terrain source"); child.transform.SetParent(parent.transform);
            parent.transform.position = new Vector3(-100, 4, 50);
            Assert.DoesNotThrow(() => GolemNavigationExporter.ValidateTerrainTransform(child.transform));
            parent.transform.localScale = new Vector3(2, 1, 2);
            Assert.Throws<InvalidOperationException>(() => GolemNavigationExporter.ValidateTerrainTransform(child.transform));
            parent.transform.localScale = Vector3.one; parent.transform.rotation = Quaternion.Euler(0, 90, 0);
            Assert.Throws<InvalidOperationException>(() => GolemNavigationExporter.ValidateTerrainTransform(child.transform));
        }

        [Test]
        public void VolumesPreserveRotationAndRejectUnrepresentableTilt()
        {
            var marker = new GameObject("Area").AddComponent<GolemNavigationVolume>(); marker.StableID = "area";
            marker.Size = new Vector3(2, 3, 4); marker.Exclude = true; marker.transform.rotation = Quaternion.Euler(0, 45, 0);
            var volume = GolemNavigationExporter.CaptureVolume(marker);
            Assert.That(volume.Shape, Is.EqualTo("convex")); Assert.That(volume.Points.Length, Is.EqualTo(4));
            Assert.That(volume.Exclude, Is.True); Assert.That(volume.Bounds.Max.Y - volume.Bounds.Min.Y, Is.EqualTo(3).Within(1e-5));
            marker.transform.rotation = Quaternion.Euler(10, 45, 0);
            Assert.Throws<InvalidOperationException>(() => GolemNavigationExporter.CaptureVolume(marker));
            marker.transform.rotation = Quaternion.identity; marker.transform.localScale = new Vector3(2, 1, 1); marker.Shape = NavigationVolumeShape.Cylinder;
            Assert.Throws<InvalidOperationException>(() => GolemNavigationExporter.CaptureVolume(marker));
        }

        [Test]
        public void CylinderBoundsRoundOutwardAtTranslatedScaledPositions()
        {
            var marker = new GameObject("Cylinder").AddComponent<GolemNavigationVolume>(); marker.StableID = "cylinder";
            marker.Shape = NavigationVolumeShape.Cylinder; marker.Radius = 0.1f;
            marker.transform.position = new Vector3(10, 0, -10); marker.transform.localScale = new Vector3(1.1f, 1, 1.1f);
            var volume = GolemNavigationExporter.CaptureVolume(marker);
            Assert.That(volume.Bounds.Max.X - volume.Bounds.Min.X, Is.GreaterThanOrEqualTo(volume.Radius * 2));
            Assert.That(volume.Bounds.Max.Z - volume.Bounds.Min.Z, Is.GreaterThanOrEqualTo(volume.Radius * 2));
            var source = GolemNavigationDocumentTests.Scene(); source.Volumes = new[] { volume };
            Assert.DoesNotThrow(() => GolemNavigationDocument.Build(source, GolemNavigationDocumentTests.Config()));
        }

        [Test]
        public void UnsavedSceneCannotProduceUnstableSourceIdentities()
        {
            var world = new GameObject("World").AddComponent<GolemNavigationWorld>();
            Assert.That(GolemNavigationExporter.TryCollect(world, out var record, out var error), Is.False);
            Assert.That(record, Is.Null); Assert.That(error, Does.Contain("Save the scene"));
        }
    }
}
