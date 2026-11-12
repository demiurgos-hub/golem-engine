#if UNITY_6000_0_OR_NEWER
using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using UnityEditor;
using UnityEditor.SceneManagement;
using UnityEngine;
using UnityEngine.SceneManagement;

namespace GolemEngine.Unity.Editor.Tests
{
    /// <summary>Records actual fixed-step Unity dynamics from the same saved source consumed by Go.</summary>
    public static class GolemPhysicsDynamicsFixture
    {
        [Serializable] private sealed class Document
        {
            public string Version = "unity-physics-dynamics-v1", UnityVersion, SourceSHA256;
            public float FrameSeconds = 0.05f;
            public int Substeps = 4, Frames = 120, UnityPositionIterations = 6, UnityVelocityIterations = 1;
            public Vector3 Gravity = new Vector3(0, -9.81f, 0);
            public Command[] Commands;
            public Sample[] Samples;
        }
        [Serializable] private sealed class Command
        {
            public string Owner, Kind;
            public int FirstFrame, FrameCount = 1;
            public Vector3 Value;
        }
        [Serializable] private sealed class Sample
        {
            public string Owner;
            public int Frame;
            public Vector3 Position, LinearVelocity, AngularVelocity;
            public Quaternion Rotation;
            public bool Sleeping;
        }

        /// <summary>Exports initial geometry and immutable observations; never executes game scripts or edits Fractured.</summary>
        public static void Generate()
        {
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            if (string.IsNullOrWhiteSpace(output)) throw new InvalidOperationException("Set GOLEM_PHYSICS_FIXTURE_OUTPUT.");
            Directory.CreateDirectory(output);
            const string assets = "Assets/GolemPhysicsDynamics";
            if (!AssetDatabase.IsValidFolder(assets)) AssetDatabase.CreateFolder("Assets", "GolemPhysicsDynamics");
            var oldGravity = Physics.gravity;
            var oldMode = Physics.simulationMode;
            var scene = EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            var document = new Document { UnityVersion = Application.unityVersion };
            var bodies = new SortedDictionary<string, Rigidbody>(StringComparer.Ordinal);
            try
            {
                SceneManager.SetActiveScene(scene);
                Physics.gravity = document.Gravity;
                Physics.simulationMode = SimulationMode.Script;
                var world = new GameObject("Dynamics reference").AddComponent<GolemPhysicsWorld>();
                world.WorldID = "unity-dynamics-reference";
                var plain = Material(assets, "inelastic", 0, 0);
                var bounce = Material(assets, "bounce", 0, 0.5f);
                var rough = Material(assets, "rough", 0.6f, 0);
                Sphere(world, bodies, "freefall", new Vector3(0, 100, 0), 2, true, plain);
                Sphere(world, bodies, "impulse", new Vector3(0, 2, 20), 2, false, plain);
                Sphere(world, bodies, "force", new Vector3(0, 2, 40), 2, false, plain);
                var torque = Sphere(world, bodies, "torque", new Vector3(0, 2, 60), 2, false, plain);
                torque.inertiaTensor = new Vector3(0.2f, 0.2f, 0.2f);
                torque.inertiaTensorRotation = Quaternion.identity;
                Sphere(world, bodies, "bounce", new Vector3(0, 3, 80), 1, true, bounce);
                Box(world, "bounce-floor", new Vector3(0, -0.5f, 80), new Vector3(10, 1, 10), bounce);
                Box(world, "stack-floor", new Vector3(0, -0.5f, 100), new Vector3(10, 1, 10), rough);
                for (var i = 0; i < 5; i++)
                {
                    var collider = Box(world, "stack-" + i, new Vector3(0, 0.51f + 1.01f * i, 100), Vector3.one, rough);
                    Body(collider.gameObject, bodies, "stack-" + i, 1, true);
                }
                Sphere(world, bodies, "push-light", new Vector3(-1, 2, 120), 1, false, plain);
                Sphere(world, bodies, "push-heavy", new Vector3(1, 2, 120), 3, false, plain);
                var pendulumRotation = Quaternion.AngleAxis(20, Vector3.forward);
                var pivot = new Vector3(0, 5, 140);
                var pendulumCollider = Box(world, "hinge", pivot - pendulumRotation * new Vector3(0, 0.75f, 0), new Vector3(0.5f, 1.5f, 0.5f), plain);
                pendulumCollider.transform.rotation = pendulumRotation;
                var pendulum = Body(pendulumCollider.gameObject, bodies, "hinge", 2, true);
                var hinge = pendulum.gameObject.AddComponent<HingeJoint>();
                hinge.axis = Vector3.forward; hinge.anchor = new Vector3(0, 0.75f, 0);
                hinge.autoConfigureConnectedAnchor = false; hinge.connectedAnchor = pivot;
                hinge.useLimits = true; hinge.limits = new JointLimits { min = -30, max = 30 };
                Physics.SyncTransforms();
                if (!EditorSceneManager.SaveScene(scene, assets + "/dynamics.unity")) throw new InvalidOperationException("Cannot save dynamics fixture.");
                var source = GolemPhysicsDocument.Build(GolemPhysicsExporter.Capture(world));
                var sourceBytes = new UTF8Encoding(false).GetBytes(source);
                WriteNew(Path.Combine(output, "dynamics.source.json"), sourceBytes);
                using (var hash = SHA256.Create()) document.SourceSHA256 = BitConverter.ToString(hash.ComputeHash(sourceBytes)).Replace("-", "").ToLowerInvariant();
                document.Commands = new[]
                {
                    new Command { Owner = "impulse", Kind = "impulse", Value = new Vector3(4, 0, 0) },
                    new Command { Owner = "force", Kind = "force", FrameCount = 20, Value = new Vector3(6, 0, 0) },
                    new Command { Owner = "torque", Kind = "torque", FrameCount = 20, Value = new Vector3(0, 0, 0.4f) },
                    new Command { Owner = "push-light", Kind = "velocity", Value = new Vector3(4, 0, 0) }
                };
                var observations = new List<Sample>();
                Observe(bodies, -1, observations);
                var physicsScene = scene.GetPhysicsScene();
                for (var frame = 0; frame < document.Frames; frame++)
                {
                    for (var substep = 0; substep < document.Substeps; substep++)
                    {
                        foreach (var command in document.Commands)
                        {
                            if (frame < command.FirstFrame || frame >= command.FirstFrame + command.FrameCount) continue;
                            var body = bodies[command.Owner];
                            switch (command.Kind)
                            {
                                case "force": body.AddForce(command.Value, ForceMode.Force); break;
                                case "torque": body.AddTorque(command.Value, ForceMode.Force); break;
                                case "impulse": if (substep == 0) body.AddForce(command.Value, ForceMode.Impulse); break;
                                case "velocity": if (substep == 0) body.linearVelocity = command.Value; break;
                                default: throw new InvalidOperationException("Unknown fixture command.");
                            }
                        }
                        physicsScene.Simulate(document.FrameSeconds / document.Substeps);
                    }
                    Observe(bodies, frame, observations);
                }
                document.Samples = observations.ToArray();
                WriteNew(Path.Combine(output, "dynamics.reference.json"), new UTF8Encoding(false).GetBytes(JsonUtility.ToJson(document, true)));
                Debug.Log("Recorded " + observations.Count + " Unity dynamics states in " + output);
            }
            finally
            {
                Physics.gravity = oldGravity;
                Physics.simulationMode = oldMode;
                EditorSceneManager.NewScene(NewSceneSetup.EmptyScene, NewSceneMode.Single);
            }
        }

        private static UnityEngine.PhysicsMaterial Material(string assets, string name, float friction, float restitution)
        {
            var material = new UnityEngine.PhysicsMaterial(name) { staticFriction = friction, dynamicFriction = friction, bounciness = restitution,
                frictionCombine = PhysicsMaterialCombine.Average, bounceCombine = PhysicsMaterialCombine.Average };
            // Replace only generated fixture assets; source content is never modified.
            var path = assets + "/" + name + ".physicMaterial";
            if (AssetDatabase.LoadAssetAtPath<UnityEngine.PhysicsMaterial>(path)) AssetDatabase.DeleteAsset(path);
            AssetDatabase.CreateAsset(material, path);
            return material;
        }

        private static Rigidbody Sphere(GolemPhysicsWorld world, IDictionary<string, Rigidbody> bodies, string name, Vector3 position, float mass, bool gravity, UnityEngine.PhysicsMaterial material)
        {
            var item = new GameObject(name); item.transform.SetParent(world.transform); item.transform.position = position;
            var collider = item.AddComponent<SphereCollider>(); collider.radius = 0.5f; collider.sharedMaterial = material;
            return Body(item, bodies, name, mass, gravity);
        }

        private static BoxCollider Box(GolemPhysicsWorld world, string name, Vector3 position, Vector3 size, UnityEngine.PhysicsMaterial material)
        {
            var item = new GameObject(name); item.transform.SetParent(world.transform); item.transform.position = position;
            var collider = item.AddComponent<BoxCollider>(); collider.size = size; collider.sharedMaterial = material;
            return collider;
        }

        private static Rigidbody Body(GameObject item, IDictionary<string, Rigidbody> bodies, string name, float mass, bool gravity)
        {
            item.AddComponent<GolemPhysicsMetadata>().OwnerID = name;
            var body = item.AddComponent<Rigidbody>(); body.mass = mass; body.useGravity = gravity;
            body.linearDamping = 0; body.angularDamping = 0; body.maxAngularVelocity = 100;
            body.solverIterations = 6; body.solverVelocityIterations = 1;
            bodies.Add(name, body);
            return body;
        }

        private static void Observe(SortedDictionary<string, Rigidbody> bodies, int frame, ICollection<Sample> samples)
        {
            foreach (var pair in bodies)
            {
                var body = pair.Value;
                samples.Add(new Sample { Owner = pair.Key, Frame = frame, Position = body.position, Rotation = body.rotation,
                    LinearVelocity = body.linearVelocity, AngularVelocity = body.angularVelocity, Sleeping = body.IsSleeping() });
            }
        }

        private static void WriteNew(string path, byte[] bytes)
        {
            using (var stream = new FileStream(path, FileMode.CreateNew)) stream.Write(bytes, 0, bytes.Length);
        }
    }
}
#endif
