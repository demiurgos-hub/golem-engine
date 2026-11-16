using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using NUnit.Framework;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemObjectTemplateExporterTests
    {
        private string folder;
        private Mesh mesh;

        [SetUp]
        public void SetUp()
        {
            folder = "Assets/GolemObjectTemplateTest_" + Guid.NewGuid().ToString("N");
            AssetDatabase.CreateFolder("Assets", folder.Substring(7));
            mesh = new Mesh { vertices = new[] { Vector3.zero, Vector3.forward, Vector3.right }, triangles = new[] { 0, 1, 2 } };
            AssetDatabase.CreateAsset(mesh, folder + "/shared.asset");
        }

        [TearDown]
        public void TearDown() => AssetDatabase.DeleteAsset(folder);

        [Test]
        public void TemplatesAreRootLocalWithHierarchyIdentities()
        {
            var prefab = Prefab("crate", root =>
            {
                root.transform.SetPositionAndRotation(new Vector3(50, 3, -20), Quaternion.Euler(0, 40, 0));
                root.transform.localScale = new Vector3(2, 2, 2);
                var child = Child(root, "box", new Vector3(1, 0.5f, 0));
                child.AddComponent<BoxCollider>().size = new Vector3(2, 1, 1);
            });

            var json = Collect(3, ("crate", prefab)).Single(r => r.PendingContent != null).PendingContent;

            Assert.That(json, Does.StartWith(GolemObjectTemplateDocument.OwnershipPrefix));
            Assert.That(json, Does.Contain("\"CatalogID\": \"templates\""));
            Assert.That(json, Does.Contain("{\"ID\":\"crate\",\"SourceID\":\"" + AssetDatabase.GetAssetPath(prefab) + "\""));
            Assert.That(json, Does.Contain("\"ID\":\"root/child/0/transform/static\""));
            Assert.That(json, Does.Contain("\"Position\":{\"X\":1,\"Y\":0.5,\"Z\":0}"));
            Assert.That(json, Does.Contain("\"ID\":\"root/child/0/collider/0\""));
            Assert.That(json, Does.Contain("\"Size\":{\"X\":2,\"Y\":1,\"Z\":1}"));
            Assert.That(json, Does.Not.Contain("\"Geometry\""));
        }

        [Test]
        public void SharedMeshesAndNavigationLandInOneSidecar()
        {
            GameObject Rock(string name) => Prefab(name, root =>
            {
                root.AddComponent<MeshCollider>().sharedMesh = mesh;
                var visual = Child(root, "visual", Vector3.zero);
                visual.AddComponent<MeshFilter>().sharedMesh = mesh;
                visual.AddComponent<GolemNavigationSource>();
            });

            var records = Collect(3, ("rock-a", Rock("RockA")), ("rock-b", Rock("RockB")));

            Assert.That(records.Length, Is.EqualTo(2));
            var json = records[0].PendingContent;
            var bytes = records[1].PendingBytes;
            Assert.That(records[1].Path, Is.EqualTo("templates.golemgeo"));
            Assert.That(json, Does.Contain("\"Geometry\": {\"Path\":\"templates.golemgeo\",\"SHA256\":\"" + records[1].Hash + "\"}"));
            Assert.That(BitConverter.ToUInt32(bytes, 8), Is.EqualTo(2), "one physics mesh and one navigation mesh");
            Assert.That(json, Does.Contain("\"Navigation\":{\"Instances\":[{\"AssetID\":\""));
            Assert.That(json, Does.Contain("\"ID\":\"root/child/0/transform/component/GolemEngine.Unity.GolemNavigationSource/0\""));
        }

        [Test]
        public void UnsupportedTemplatesReportEveryError()
        {
            var dynamic = Prefab("Dynamic", root => { root.AddComponent<SphereCollider>(); root.AddComponent<Rigidbody>(); });
            var empty = Prefab("Empty", root => Child(root, "nothing", Vector3.zero));
            var flat = Prefab("Flat", root => root.AddComponent<BoxCollider2D>());
            var scene = new GameObject("SceneObject");
            try
            {
                var errors = CollectErrors(3, ("dynamic", dynamic), ("empty", empty), ("flat", flat), ("scene", scene), ("empty", empty), (" padded", dynamic));
                Assert.That(errors, Has.Some.Contain("dynamic Rigidbody"));
                Assert.That(errors, Has.Some.Contain("no colliders or navigation sources"));
                Assert.That(errors, Has.Some.Contain("2D colliders do not belong in a 3D catalog"));
                Assert.That(errors, Has.Some.Contain("saved prefab asset roots"));
                Assert.That(errors, Has.Some.Contain("duplicate template ID 'empty'"));
                Assert.That(errors, Has.Some.Contain("without surrounding whitespace"));
            }
            finally { UnityEngine.Object.DestroyImmediate(scene); }
        }

        [Test]
        public void TwoDimensionalTemplatesUseQuarterTurnShapes()
        {
            var prefab = Prefab("Gate", root =>
            {
                var rotated = Child(root, "rotated", new Vector3(3, 1, 0));
                rotated.transform.localRotation = Quaternion.Euler(0, 0, 90);
                var box = rotated.AddComponent<BoxCollider2D>();
                box.size = new Vector2(4, 1);
                Child(root, "post", Vector3.zero).AddComponent<CircleCollider2D>().radius = 0.25f;
            });

            var records = Collect(2, ("gate", prefab));

            Assert.That(records.Length, Is.EqualTo(1));
            var json = records[0].PendingContent;
            Assert.That(json, Does.Contain("\"Dimensions\": 2"));
            Assert.That(json, Does.Not.Contain("LayerCollisionMasks"));
            Assert.That(json, Does.Contain("{\"ID\":\"root/child/0/collider2d/0\",\"Kind\":\"box\",\"OffsetX\":3,\"OffsetY\":1,\"Width\":1,\"Height\":4,\"Layer\":\"Default\"}"));
            Assert.That(json, Does.Contain("{\"ID\":\"root/child/1/collider2d/0\",\"Kind\":\"circle\",\"OffsetX\":0,\"OffsetY\":0,\"Radius\":0.25,\"Layer\":\"Default\"}"));

            var mixed = Prefab("Mixed", root => { root.AddComponent<BoxCollider2D>(); Child(root, "sphere", Vector3.zero).AddComponent<SphereCollider>(); });
            Assert.That(CollectErrors(2, ("mixed", mixed)), Has.Some.Contain("3D colliders do not belong in a 2D catalog"));
            var tilted = Prefab("Tilted", root => { var child = Child(root, "tilted", Vector3.zero); child.transform.localRotation = Quaternion.Euler(0, 0, 30); child.AddComponent<BoxCollider2D>(); });
            Assert.That(CollectErrors(2, ("tilted", tilted)), Has.Some.Contain("exact quarter turn"));
        }

        [Test]
        public void MarkerCollectionUsesTemplateIDOrPrefabGuidAndRejectsNestedMarkers()
        {
            var named = Prefab("Named", root => { root.AddComponent<BoxCollider>(); root.AddComponent<GolemObjectTemplate>().TemplateID = "named"; });
            var unnamed = Prefab("Unnamed", root => { root.AddComponent<BoxCollider>(); root.AddComponent<GolemObjectTemplate>(); });
            var unmarked = Prefab("Unmarked", root => root.AddComponent<BoxCollider>());
            var projectRoot = Path.GetTempPath();
            var errors = new List<string>();

            GolemObjectTemplateExporter.CollectMarked(3, "templates.json", projectRoot,
                new[] { Source(named), Source(unnamed), Source(unmarked) }, out var records, out var count, errors);

            Assert.That(errors, Is.Empty);
            Assert.That(count, Is.EqualTo(2));
            Assert.That(records[0].SourceGuid, Is.EqualTo(GolemScribeConstants.ObjectTemplateSourceGuid));
            Assert.That(records[0].PendingContent, Does.Contain("{\"ID\":\"named\""));
            Assert.That(records[0].PendingContent, Does.Contain("{\"ID\":\"" + PrefabGuid(unnamed) + "\""));

            var nested = Prefab("Nested", r => { r.AddComponent<BoxCollider>(); Child(r, "inner", Vector3.zero).AddComponent<GolemObjectTemplate>(); });
            GolemObjectTemplateExporter.CollectMarked(3, "templates.json", projectRoot, new[] { Source(named), Source(nested) }, out records, out count, errors);
            Assert.That(errors, Has.Some.Contain("belongs on the prefab root"));
            Assert.That(records, Is.Empty);
        }

        private GolemScribeArtifactRecord[] Collect(int dimensions, params (string id, GameObject prefab)[] templates)
        {
            var errors = new List<string>();
            var ok = GolemObjectTemplateExporter.TryCollect("templates.json", "test", templates.Select(t => new GolemObjectTemplateSource(t.id, t.prefab)).ToList(),
                dimensions, out var records, errors);
            Assert.That(ok, Is.True, string.Join("\n", errors));
            return records;
        }

        private List<string> CollectErrors(int dimensions, params (string id, GameObject prefab)[] templates)
        {
            var errors = new List<string>();
            var ok = GolemObjectTemplateExporter.TryCollect("templates.json", "test", templates.Select(t => new GolemObjectTemplateSource(t.id, t.prefab)).ToList(),
                dimensions, out _, errors);
            Assert.That(ok, Is.False);
            return errors;
        }

        private GameObject Prefab(string name, Action<GameObject> build)
        {
            var root = new GameObject(name);
            try
            {
                build(root);
                return PrefabUtility.SaveAsPrefabAsset(root, folder + "/" + name + ".prefab");
            }
            finally { UnityEngine.Object.DestroyImmediate(root); }
        }

        private static GameObject Child(GameObject parent, string name, Vector3 position)
        {
            var child = new GameObject(name);
            child.transform.SetParent(parent.transform, false);
            child.transform.localPosition = position;
            return child;
        }

        private static string PrefabGuid(GameObject prefab) => AssetDatabase.AssetPathToGUID(AssetDatabase.GetAssetPath(prefab));

        private static ScribePrefabSource Source(GameObject prefab) => new ScribePrefabSource(PrefabGuid(prefab), AssetDatabase.GetAssetPath(prefab), prefab);
    }
}
