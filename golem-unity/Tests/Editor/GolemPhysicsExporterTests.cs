using System;
using System.Linq;
using NUnit.Framework;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemPhysicsExporterTests
    {
        private string folder;
        private Scene scene;
        private SceneSetup[] previousSetup;

        [SetUp]
        public void SetUp()
        {
            folder = "Assets/GolemPhysicsTest_" + Guid.NewGuid().ToString("N");
            AssetDatabase.CreateFolder("Assets", folder.Substring(7));
            previousSetup = EditorSceneManager.GetSceneManagerSetup();
            scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            SceneManager.SetActiveScene(scene);
        }

        [TearDown]
        public void TearDown()
        {
            if (scene.IsValid() && scene.isLoaded) EditorSceneManager.CloseScene(scene, true);
            if (previousSetup != null && previousSetup.Length > 0 && previousSetup.All(s => !string.IsNullOrEmpty(s.path))) EditorSceneManager.RestoreSceneManagerSetup(previousSetup);
            AssetDatabase.DeleteAsset(folder);
        }

        [Test]
        public void CompoundCapturesBodyLocalPoseActivationAndColliderLocalMetadata()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var root = new GameObject("Owner"); root.transform.SetParent(world.transform);
            root.transform.SetPositionAndRotation(new Vector3(10, 3, -4), Quaternion.Euler(0, 90, 0));
            root.AddComponent<Rigidbody>().isKinematic = true;
            var owner = root.AddComponent<GolemPhysicsMetadata>(); owner.OwnerID = "actor";
            owner.Properties = new[] { new GolemPhysicsProperty { Key = "not-inherited", Value = "yes" } };
            var child = new GameObject("Collider"); child.transform.SetParent(root.transform, false); child.transform.localPosition = new Vector3(2, 0, 0);
            var box = child.AddComponent<BoxCollider>(); box.center = new Vector3(0, 1, 0); box.size = new Vector3(2, 4, 6); box.enabled = false;
            var sphere = child.AddComponent<SphereCollider>(); sphere.radius = 0.25f; sphere.isTrigger = true;
            child.SetActive(false); Save();
            var source = GolemPhysicsExporter.Capture(world);
            Assert.That(source.Bodies.Length, Is.EqualTo(1)); Assert.That(source.Colliders.Length, Is.EqualTo(2));
            var exported = source.Colliders.Single(c => c.Shape == "box");
            Assert.That(exported.Center.X, Is.EqualTo(2).Within(0.0001)); Assert.That(exported.Center.Y, Is.EqualTo(1).Within(0.0001));
            Assert.That(exported.OwnerID, Is.EqualTo("actor")); Assert.That(exported.Metadata, Is.Empty);
            Assert.That(exported.Active, Is.False); Assert.That(exported.Enabled, Is.False);
            var sensor = source.Colliders.Single(c => c.Shape == "sphere");
            Assert.That(sensor.Enabled, Is.True); Assert.That(sensor.Active, Is.False); Assert.That(sensor.Sensor, Is.True); Assert.That(sensor.Solid, Is.False);
            Assert.That(source.Colliders.Select(c => c.ID).Distinct().Count(), Is.EqualTo(2));
        }

        [Test]
        public void PrefabVariantAndInstanceOverrideAreResolvedBeforeExport()
        {
            var original = new GameObject("Base"); original.AddComponent<BoxCollider>().size = Vector3.one;
            var prefab = PrefabUtility.SaveAsPrefabAsset(original, folder + "/base.prefab"); UnityEngine.Object.DestroyImmediate(original);
            var variantInstance = (GameObject)PrefabUtility.InstantiatePrefab(prefab);
            variantInstance.GetComponent<BoxCollider>().size = new Vector3(2, 3, 4);
            PrefabUtility.RecordPrefabInstancePropertyModifications(variantInstance.GetComponent<BoxCollider>());
            var variant = PrefabUtility.SaveAsPrefabAsset(variantInstance, folder + "/variant.prefab"); UnityEngine.Object.DestroyImmediate(variantInstance);
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var instance = (GameObject)PrefabUtility.InstantiatePrefab(variant); instance.transform.SetParent(world.transform);
            instance.transform.localScale = new Vector3(-2, 3, 4); instance.transform.rotation = Quaternion.Euler(12, 35, 4);
            instance.GetComponent<BoxCollider>().center = Vector3.up;
            PrefabUtility.RecordPrefabInstancePropertyModifications(instance.GetComponent<BoxCollider>()); Save();
            var shape = GolemPhysicsExporter.Capture(world).Colliders.Single();
            Assert.That(shape.Size.X, Is.EqualTo(4).Within(0.0001)); Assert.That(shape.Size.Y, Is.EqualTo(9).Within(0.0001)); Assert.That(shape.Size.Z, Is.EqualTo(16).Within(0.0001));
            Assert.That(shape.Center.Y, Is.EqualTo(3).Within(0.0001));
        }

        [Test]
        public void PrimitiveShearIsAnActionableFailureWhileReflectedMeshBakesOnce()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var parent = new GameObject("Scale"); parent.transform.SetParent(world.transform); parent.transform.localScale = new Vector3(2, 1, 1);
            var child = new GameObject("Tilt"); child.transform.SetParent(parent.transform, false); child.transform.localRotation = Quaternion.Euler(0, 45, 0);
            var box = child.AddComponent<BoxCollider>(); Save();
            Assert.That(GolemPhysicsExporter.TryCollect(world, out var record, out var error), Is.False); Assert.That(record, Is.Null); Assert.That(error, Does.Contain("Sheared primitive"));
            UnityEngine.Object.DestroyImmediate(box);
            var mesh = new Mesh { vertices = new[] { Vector3.zero, Vector3.forward, Vector3.right }, triangles = new[] { 0, 1, 2 } };
            AssetDatabase.CreateAsset(mesh, folder + "/triangle.asset"); child.AddComponent<MeshCollider>().sharedMesh = mesh;
            parent.transform.localScale = new Vector3(-2, 1, 1); Save();
            var source = GolemPhysicsExporter.Capture(world);
            Assert.That(source.Assets.Single().Indices, Is.EqualTo(new uint[] { 0, 2, 1 }));
            Assert.That(source.Colliders.Single().Center.X, Is.Zero);
            Assert.That(source.Colliders.Single().Rotation.W, Is.EqualTo(1));
        }

        [Test]
        public void TerrainHolesAndCapsuleScalingArePreserved()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var data = new TerrainData { heightmapResolution = 33, size = new Vector3(32, 8, 32) };
            var holes = new bool[32, 32]; for (var z = 0; z < 32; z++) for (var x = 0; x < 32; x++) holes[z, x] = true;
            holes[4, 5] = false; data.SetHoles(0, 0, holes); AssetDatabase.CreateAsset(data, folder + "/terrain.asset");
            var terrain = new GameObject("Terrain"); terrain.transform.SetParent(world.transform); terrain.AddComponent<TerrainCollider>().terrainData = data;
            var child = new GameObject("Capsule"); child.transform.SetParent(world.transform); child.transform.localScale = new Vector3(2, 3, 4);
            var capsule = child.AddComponent<CapsuleCollider>(); capsule.radius = 0.5f; capsule.height = 2; Save();
            var source = GolemPhysicsExporter.Capture(world);
            Assert.That(source.Assets.Single().Indices.Length, Is.EqualTo((32 * 32 * 2 - 2) * 3));
            var shape = source.Colliders.Single(c => c.Shape == "capsule"); Assert.That(shape.Radius, Is.EqualTo(2)); Assert.That(shape.Height, Is.EqualTo(6));
        }

        [Test]
        public void NestedWorldsAreIndependentAndUnsupportedJointsCannotDisappear()
        {
            var world = new GameObject("Outer").AddComponent<GolemPhysicsWorld>(); world.gameObject.AddComponent<BoxCollider>();
            var nested = new GameObject("Inner").AddComponent<GolemPhysicsWorld>(); nested.transform.SetParent(world.transform); nested.gameObject.AddComponent<SphereCollider>(); Save();
            Assert.That(GolemPhysicsExporter.Capture(world).Colliders.Length, Is.EqualTo(1));
            world.gameObject.AddComponent<ConfigurableJoint>().xMotion = ConfigurableJointMotion.Free; Save();
            Assert.That(GolemPhysicsExporter.TryCollect(world, out _, out var error), Is.False); Assert.That(error, Does.Contain("ConfigurableJoint"));
        }

        [Test]
        public void CharacterControllerAndExplicitMassPropertiesRetainTheirAuthoring()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var characterObject = new GameObject("Character"); characterObject.transform.SetParent(world.transform);
            var character = characterObject.AddComponent<CharacterController>(); character.center = Vector3.up; character.radius = 0.4f; character.height = 2;
            character.stepOffset = 0.25f; character.slopeLimit = 38; character.skinWidth = 0.03f; character.minMoveDistance = 0.002f;
            var physical = new GameObject("Physical"); physical.transform.SetParent(world.transform); physical.AddComponent<BoxCollider>();
            var body = physical.AddComponent<Rigidbody>(); body.centerOfMass = new Vector3(0.2f, 0.1f, -0.3f); body.inertiaTensor = new Vector3(2, 3, 4); body.inertiaTensorRotation = Quaternion.Euler(0, 15, 0); Save();
            var source = GolemPhysicsExporter.Capture(world);
            var capsule = source.Colliders.Single(c => c.Character != null);
            Assert.That(capsule.Shape, Is.EqualTo("capsule")); Assert.That(capsule.Center.Y, Is.EqualTo(1)); Assert.That(capsule.Character.StepOffset, Is.EqualTo(0.25f));
            Assert.That(capsule.Character.SlopeLimit, Is.EqualTo(38)); Assert.That(source.Bodies.Single(b => b.ID == capsule.BodyID).Kind, Is.EqualTo("kinematic"));
            var dynamic = source.Bodies.Single(b => b.Kind == "dynamic"); Assert.That(dynamic.AutomaticCenterOfMass, Is.False); Assert.That(dynamic.AutomaticInertiaTensor, Is.False);
            Assert.That(dynamic.CenterOfMass.X, Is.EqualTo(0.2f).Within(0.0001)); Assert.That(dynamic.InertiaTensor.Z, Is.EqualTo(4));
        }

        [Test]
        public void SupportedJointsPreserveFramesLimitsDrivesAndUnboundedBreakThresholds()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            var fixedJoint = Joint<FixedJoint>(world, "Fixed", Vector3.zero);
            var hinge = Joint<HingeJoint>(world, "Hinge", new Vector3(3, 0, 0)); hinge.connectedBody = fixedJoint.GetComponent<Rigidbody>();
            hinge.useLimits = true; hinge.limits = new JointLimits { min = -35, max = 50, bounciness = 0.2f, bounceMinVelocity = 0.5f, contactDistance = 1 };
            hinge.useMotor = true; hinge.motor = new JointMotor { targetVelocity = 90, force = 8, freeSpin = true };
            var spring = Joint<SpringJoint>(world, "Spring", new Vector3(6, 0, 0)); spring.spring = 12; spring.damper = 3; spring.minDistance = 1; spring.maxDistance = 2;
            var character = Joint<CharacterJoint>(world, "Character Joint", new Vector3(9, 0, 0)); character.lowTwistLimit = new SoftJointLimit { limit = -20 }; character.highTwistLimit = new SoftJointLimit { limit = 30 };
            var configurable = Joint<ConfigurableJoint>(world, "Locked configurable", new Vector3(12, 0, 0));
            configurable.xMotion = configurable.yMotion = configurable.zMotion = ConfigurableJointMotion.Locked;
            configurable.angularXMotion = configurable.angularYMotion = configurable.angularZMotion = ConfigurableJointMotion.Locked;
            configurable.targetRotation = Quaternion.identity; Save();
            var source = GolemPhysicsExporter.Capture(world); Assert.That(source.Joints.Length, Is.EqualTo(5));
            var hingeSource = source.Joints.Single(j => j.Kind == "hinge"); Assert.That(hingeSource.LowTwist.Limit, Is.EqualTo(-35)); Assert.That(hingeSource.HighTwist.Limit, Is.EqualTo(50));
            Assert.That(hingeSource.Motor.TargetVelocity, Is.EqualTo(90)); Assert.That(hingeSource.Motor.FreeSpin, Is.True); Assert.That(hingeSource.ConnectedBodyID, Is.Not.Empty);
            Assert.That(source.Joints.All(j => j.BreakForce == null && j.BreakTorque == null), Is.True);
            Assert.That(source.Joints.Single(j => j.Kind == "spring").Spring.Damper, Is.EqualTo(3));
            Assert.That(source.Joints.Single(j => j.SourceKind == nameof(ConfigurableJoint)).Kind, Is.EqualTo("fixed"));
        }

#if UNITY_6000_0_OR_NEWER
        [Test]
        public void LayerOverridesCannotBeSilentlyDiscarded()
        {
            var world = new GameObject("Physics").AddComponent<GolemPhysicsWorld>();
            world.gameObject.AddComponent<BoxCollider>().includeLayers = 1 << 5; Save();
            Assert.That(GolemPhysicsExporter.TryCollect(world, out _, out var error), Is.False); Assert.That(error, Does.Contain("layer overrides"));
        }
#endif

        private static T Joint<T>(GolemPhysicsWorld world, string name, Vector3 position) where T : Joint
        {
            var obj = new GameObject(name); obj.transform.SetParent(world.transform); obj.transform.position = position;
            obj.AddComponent<SphereCollider>(); obj.AddComponent<Rigidbody>(); return obj.AddComponent<T>();
        }

        private void Save() => Assert.That(EditorSceneManager.SaveScene(scene, folder + "/world.unity"), Is.True);
    }
}
