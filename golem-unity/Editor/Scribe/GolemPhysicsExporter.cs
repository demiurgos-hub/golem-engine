using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Unity.Collections;
using UnityEditor;
using UnityEngine;
#if UNITY_6000_0_OR_NEWER
using UnityPhysicsMaterial = UnityEngine.PhysicsMaterial;
#else
using UnityPhysicsMaterial = UnityEngine.PhysicMaterial;
#endif

namespace GolemEngine.Unity.Editor
{
    /// <summary>Captures resolved Unity collider hierarchies as portable physics source, without cooking or simulation.</summary>
    public static class GolemPhysicsExporter
    {
        /// <summary>The selected world's transaction result and actionable source diagnostics.</summary>
        public sealed class ExportResult
        {
            public readonly List<string> Errors = new List<string>();
            public readonly List<string> Warnings = new List<string>();
            public bool BytesChanged;
            public string OutputPath;
        }

        /// <summary>Validates and exports one saved world using Scribe's manifest-last rollback transaction.</summary>
        public static ExportResult Export(GolemPhysicsWorld world)
        {
            var result = new ExportResult();
            if (!GolemScribeContext.TryLoad(GolemUnityEditorSettings.instance.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            if (!TryCollect(world, out var record, out error)) { result.Errors.Add(error); return result; }
            var committed = GolemScribeArtifacts.ReconcileSource(context.ProjectRoot, GolemScribeConstants.ArtifactKindPhysicsSource,
                record.SourceGuid, context.Previous, new[] { record });
            result.Errors.AddRange(committed.Errors); result.Warnings.AddRange(committed.Warnings);
            result.BytesChanged = committed.AnyBytesChanged;
            result.OutputPath = result.Errors.Count == 0 ? record.Path : null;
            return result;
        }

        /// <summary>Checks the selected world and recorded source without writing files.</summary>
        public static GolemScribeValidator.ValidationResult Validate(GolemPhysicsWorld world)
        {
            var result = new GolemScribeValidator.ValidationResult();
            if (!GolemScribeContext.TryLoad(GolemUnityEditorSettings.instance.ProjectRoot, out var context, out var error))
            { result.Errors.Add(error); return result; }
            if (!TryCollect(world, out var record, out error)) { result.Errors.Add(error); return result; }
            GolemScribeValidator.ValidateManifestRecords(context.ProjectRoot, context.Previous, result);
            GolemScribeValidator.CompareDesiredToDisk(context.ProjectRoot, context.Previous, new[] { record },
                new[] { GolemScribeConstants.ArtifactKindPhysicsSource }, result, record.SourceGuid);
            return result;
        }

        [MenuItem("Tools/Golem/Scribe/Export Selected Physics World")]
        private static void ExportSelected()
        {
            var world = Selection.activeGameObject == null ? null : Selection.activeGameObject.GetComponentInParent<GolemPhysicsWorld>(true);
            var result = Export(world);
            foreach (var error in result.Errors) Debug.LogError("Golem physics export: " + error);
            foreach (var warning in result.Warnings) Debug.LogWarning("Golem physics export: " + warning);
            if (result.Errors.Count == 0) Debug.Log("Golem physics source exported: " + result.OutputPath);
        }

        [MenuItem("Tools/Golem/Scribe/Validate Selected Physics World")]
        private static void ValidateSelected()
        {
            var world = Selection.activeGameObject == null ? null : Selection.activeGameObject.GetComponentInParent<GolemPhysicsWorld>(true);
            GolemScribeValidator.LogResult(Validate(world));
        }

        internal static bool TryCollect(GolemPhysicsWorld world, out GolemScribeArtifactRecord record, out string error)
        {
            record = null; error = null;
            try
            {
                var source = Capture(world);
                var content = GolemPhysicsDocument.Build(source);
                record = new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindPhysicsSource, SourceGuid = Identity(world), Entity = world.WorldID,
                    Path = world.OutputPath, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content)
                };
                return true;
            }
            catch (Exception ex) { error = ex.Message; return false; }
        }

        internal static PhysicsSource Capture(GolemPhysicsWorld world)
        {
            if (!world) throw new InvalidOperationException("Select a Physics World marker.");
            if (!world.gameObject.scene.IsValid() || string.IsNullOrEmpty(world.gameObject.scene.path) || world.gameObject.scene.isDirty)
                throw new InvalidOperationException("Save the scene before physics export; stable identities and resolved prefab overrides require a saved scene.");
            if (EditorApplication.isPlayingOrWillChangePlaymode) throw new InvalidOperationException("Physics source export requires Edit Mode.");
            if (string.IsNullOrWhiteSpace(world.OutputPath) || !world.OutputPath.EndsWith(".json", StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("Physics output must be a project-relative .json path.");
            if (world.Revision <= 0) throw new InvalidOperationException("Physics world revision must be positive.");
            foreach (var transform in world.GetComponentsInChildren<Transform>(true))
                if (transform.GetComponentInParent<GolemPhysicsWorld>(true) == world && PrefabUtility.GetPrefabInstanceStatus(transform.gameObject) == PrefabInstanceStatus.MissingAsset)
                    throw new InvalidOperationException(Hierarchy(transform) + ": missing prefab source prevents complete physics capture; restore the source or explicitly resolve the authored instance before exporting.");
            foreach (var articulation in world.GetComponentsInChildren<ArticulationBody>(true))
                if (articulation.GetComponentInParent<GolemPhysicsWorld>(true) == world)
                    throw new InvalidOperationException(articulation.name + ": ArticulationBody simulation is unsupported; export cannot reinterpret it as static geometry.");
            var source = new PhysicsSource { WorldID = world.WorldID, Revision = (ulong)world.Revision };
            for (var a = 0; a < 32; a++)
            {
                source.LayerCollisionMasks[a] = 0;
                for (var b = 0; b < 32; b++) if (!UnityEngine.Physics.GetIgnoreLayerCollision(a, b)) source.LayerCollisionMasks[a] |= 1u << b;
            }
            var bodies = new Dictionary<string, PhysicsBody>(StringComparer.Ordinal);
            var materials = new Dictionary<string, PhysicsMaterial>(StringComparer.Ordinal);
            var colliders = new List<PhysicsCollider>(); var assets = new List<PhysicsAsset>();
            foreach (var rigidbody in world.GetComponentsInChildren<Rigidbody>(true))
                if (rigidbody.GetComponentInParent<GolemPhysicsWorld>(true) == world)
                    bodies.Add(Identity(rigidbody), CaptureBody(Identity(rigidbody), rigidbody.transform, rigidbody, world));
            foreach (var collider in world.GetComponentsInChildren<Collider>(true))
            {
                if (collider.GetComponentInParent<GolemPhysicsWorld>(true) != world) continue;
                try
                {
                    // attachedRigidbody may be null for inactive hierarchies; authoring must retain compound membership.
                    var rigidbody = collider is CharacterController ? null : collider.GetComponentInParent<Rigidbody>(true);
                    var bodyTransform = rigidbody ? rigidbody.transform : collider.transform;
                    if (bodyTransform.GetComponentInParent<GolemPhysicsWorld>(true) != world)
                        throw new InvalidOperationException("Attached body is outside the selected physics world.");
                    var bodyID = rigidbody ? Identity(rigidbody) : Identity(bodyTransform) + (collider is CharacterController ? "/character" : "/static");
                    if (!bodies.ContainsKey(bodyID)) bodies.Add(bodyID, CaptureBody(bodyID, bodyTransform, rigidbody, world));
                    if (collider is CharacterController) bodies[bodyID].Kind = "kinematic";
                    var item = CaptureCollider(collider, bodyID, bodyTransform, world, assets);
                    var material = collider.sharedMaterial ? collider.sharedMaterial : DefaultMaterial();
                    item.MaterialID = material ? AssetIdentity(material) : "unity-default-material";
                    if (!materials.ContainsKey(item.MaterialID)) materials.Add(item.MaterialID, CaptureMaterial(item.MaterialID, material));
                    colliders.Add(item);
                }
                catch (Exception ex) { throw new InvalidOperationException(Hierarchy(collider.transform) + " [" + collider.GetType().Name + "]: " + ex.Message, ex); }
            }
            source.Bodies = bodies.Values.ToArray(); source.Materials = materials.Values.ToArray(); source.Colliders = colliders.ToArray(); source.Assets = assets.ToArray();
            source.Joints = world.GetComponentsInChildren<Joint>(true).Where(j => j.GetComponentInParent<GolemPhysicsWorld>(true) == world)
                .Select(j => GolemPhysicsJointCapture.Capture(j, world)).ToArray();
            return GolemPhysicsDocument.Normalize(source);
        }

        private static PhysicsBody CaptureBody(string id, Transform transform, Rigidbody body, GolemPhysicsWorld world)
        {
            var result = new PhysicsBody
            {
                ID = id, OwnerID = Owner(transform, world), Position = Vector(transform.position), Rotation = Rotation(transform.rotation),
                Active = transform.gameObject.activeInHierarchy, Enabled = !body || body.detectCollisions,
                Kind = !body ? "static" : body.isKinematic ? "kinematic" : "dynamic"
            };
            if (!body) return result;
            RejectLayerOverrides(body);
            result.Mass = body.mass; result.UseGravity = body.useGravity; result.Constraints = (int)body.constraints;
            result.CollisionDetection = body.collisionDetectionMode.ToString().ToLowerInvariant();
            var serialized = new SerializedObject(body);
            result.AutomaticCenterOfMass = serialized.FindProperty("m_ImplicitCom")?.boolValue ?? true;
            result.AutomaticInertiaTensor = serialized.FindProperty("m_ImplicitTensor")?.boolValue ?? true;
            result.CenterOfMass = Vector(Quaternion.Inverse(body.rotation) * (body.worldCenterOfMass - body.position));
            result.InertiaTensor = Vector(body.inertiaTensor); result.InertiaRotation = Rotation(body.inertiaTensorRotation);
#if UNITY_6000_0_OR_NEWER
            result.LinearDamping = body.linearDamping; result.AngularDamping = body.angularDamping;
#else
            result.LinearDamping = body.drag; result.AngularDamping = body.angularDrag;
#endif
            return result;
        }

        private static PhysicsCollider CaptureCollider(Collider collider, string bodyID, Transform body, GolemPhysicsWorld world, List<PhysicsAsset> assets)
        {
            var metadata = collider.GetComponent<GolemPhysicsMetadata>();
            RejectLayerOverrides(collider);
            var result = new PhysicsCollider
            {
                ID = Identity(collider), BodyID = bodyID, SourceID = Identity(collider), ParentID = collider.transform.parent ? Identity(collider.transform.parent) : "",
                OwnerID = Owner(collider.transform, world), Enabled = collider.enabled, Active = collider.gameObject.activeInHierarchy,
                Layer = collider.gameObject.layer, LayerName = LayerMask.LayerToName(collider.gameObject.layer),
                Solid = !collider.isTrigger, Sensor = collider.isTrigger, ContactOffset = collider.contactOffset
            };
            if (metadata)
            {
                if (metadata.OverrideRoles) { result.Solid = metadata.Solid; result.Sensor = metadata.Sensor; result.Query = metadata.Query; }
                result.Metadata = (metadata.Properties ?? Array.Empty<GolemPhysicsProperty>()).Select(p => new PhysicsProperty { Key = p.Key, Value = p.Value ?? "" }).ToArray();
            }
            var inverseBody = Matrix4x4.TRS(body.position, body.rotation, Vector3.one).inverse;
            var local = inverseBody * collider.transform.localToWorldMatrix;
            if (collider is MeshCollider mesh)
            {
                result.Shape = mesh.convex ? "convex" : "mesh";
                var assetSource = AssetIdentity(mesh.sharedMesh);
                var captured = CaptureMesh(mesh.sharedMesh, assetSource);
                result.AssetID = result.ID + "/geometry";
                assets.Add(TransformAsset(captured, result.AssetID, assetSource, local));
                return result;
            }
            if (collider is TerrainCollider terrain)
            {
                GolemNavigationExporter.ValidateTerrainTransform(terrain.transform);
                RejectGeneratedTreeColliders(terrain);
                result.Shape = "terrain"; result.AssetID = result.ID + "/geometry";
                var assetSource = AssetIdentity(terrain.terrainData);
                assets.Add(TransformAsset(CaptureTerrain(terrain.terrainData, assetSource), result.AssetID, assetSource, local));
                return result;
            }
            var scale = PrimitiveScale(local, out var rotation);
            result.Rotation = Rotation(rotation);
            if (collider is SphereCollider sphere)
            {
                result.Shape = "sphere"; result.Center = Vector(local.MultiplyPoint3x4(sphere.center));
                result.Radius = sphere.radius * Mathf.Max(scale.x, Mathf.Max(scale.y, scale.z));
            }
            else if (collider is CapsuleCollider capsule)
            {
                result.Shape = "capsule"; result.Center = Vector(local.MultiplyPoint3x4(capsule.center)); result.Axis = capsule.direction;
                result.Radius = capsule.radius * Mathf.Max(scale[(capsule.direction + 1) % 3], scale[(capsule.direction + 2) % 3]);
                result.Height = Mathf.Max(capsule.height * scale[capsule.direction], 2 * result.Radius);
            }
            else if (collider is CharacterController character)
            {
                if ((scale - Vector3.one).sqrMagnitude > 0.00000001f)
                    throw new InvalidOperationException("Scaled CharacterController settings require a declared motor scale policy. Author capsule dimensions directly at unit scale; regular CapsuleCollider scale is supported.");
                result.Shape = "capsule"; result.Center = Vector(local.MultiplyPoint3x4(character.center)); result.Axis = 1;
                result.Radius = character.radius * Mathf.Max(scale.x, scale.z); result.Height = Mathf.Max(character.height * scale.y, 2 * result.Radius);
                result.Character = new PhysicsCharacter
                {
                    StepOffset = character.stepOffset, SlopeLimit = character.slopeLimit, SkinWidth = character.skinWidth,
                    MinimumMoveDistance = character.minMoveDistance, DetectCollisions = character.detectCollisions, EnableOverlapRecovery = character.enableOverlapRecovery
                };
            }
            else if (collider is BoxCollider box)
            {
                result.Shape = "box"; result.Center = Vector(local.MultiplyPoint3x4(box.center)); result.Size = Vector(Vector3.Scale(box.size, scale));
            }
            else throw new InvalidOperationException("Unsupported collider type; no AABB substitution is allowed.");
            return result;
        }

        private static void RejectGeneratedTreeColliders(TerrainCollider terrain)
        {
            var data = terrain.terrainData;
            if (!data || data.treeInstanceCount == 0) return;
            var enabled = new SerializedObject(terrain).FindProperty("m_EnableTreeColliders");
            if (enabled == null) throw new InvalidOperationException("Cannot inspect TerrainCollider tree collision settings.");
            if (!enabled.boolValue) return;
            var prototypes = data.treePrototypes;
            var checkedPrototypes = new HashSet<int>();
            for (var i = 0; i < data.treeInstanceCount; i++)
            {
                var index = data.GetTreeInstance(i).prototypeIndex;
                if (!checkedPrototypes.Add(index)) continue;
                if (index < 0 || index >= prototypes.Length || !prototypes[index].prefab)
                    throw new InvalidOperationException("Terrain tree instance references a missing prototype.");
                if (prototypes[index].prefab.GetComponentsInChildren<Collider>(true).Length != 0)
                    throw new InvalidOperationException("Terrain-generated tree colliders are not yet exported. Resolve the tree colliders as explicit saved scene instances before exporting; heightfield-only export would omit collision geometry.");
            }
        }

        private static NavigationAsset CaptureMesh(Mesh mesh, string id)
        {
            if (mesh == null) throw new InvalidOperationException("Physics mesh is missing.");
            // Editor snapshots can read imported collision geometry without enabling
            // a runtime CPU copy or changing the asset's importer settings.
            using (var snapshot = MeshUtility.AcquireReadOnlyMeshData(mesh))
            {
                var data = snapshot[0];
                if (data.vertexCount > 8 * 1024 * 1024) throw new InvalidOperationException(mesh.name + ": mesh vertex limit exceeded.");
                long indexCount = 0;
                for (var submesh = 0; submesh < data.subMeshCount; submesh++)
                {
                    var section = data.GetSubMesh(submesh);
                    if (section.topology != MeshTopology.Triangles) throw new InvalidOperationException(mesh.name + ": non-triangle submesh is unsupported.");
                    if (section.indexCount % 3 != 0) throw new InvalidOperationException(mesh.name + ": invalid triangle array.");
                    indexCount += section.indexCount;
                    if (indexCount / 3 > 8 * 1024 * 1024) throw new InvalidOperationException(mesh.name + ": mesh triangle limit exceeded.");
                }
                var vertices = new NavigationVector[data.vertexCount];
                using (var nativeVertices = new NativeArray<Vector3>(data.vertexCount, Allocator.TempJob, NativeArrayOptions.UninitializedMemory))
                {
                    data.GetVertices(nativeVertices);
                    for (var i = 0; i < vertices.Length; i++)
                    {
                        var vertex = nativeVertices[i];
                        vertices[i] = new NavigationVector(vertex.x, vertex.y, vertex.z);
                    }
                }
                var triangles = new NavigationTriangle[checked((int)(indexCount / 3))];
                var triangle = 0;
                for (var submesh = 0; submesh < data.subMeshCount; submesh++)
                {
                    using (var indices = new NativeArray<int>(data.GetSubMesh(submesh).indexCount, Allocator.TempJob, NativeArrayOptions.UninitializedMemory))
                    {
                        data.GetIndices(indices, submesh, true);
                        for (var i = 0; i < indices.Length; i += 3)
                        {
                            var face = new uint[3];
                            for (var corner = 0; corner < 3; corner++)
                            {
                                var index = indices[i + corner];
                                if (index < 0 || index >= vertices.Length) throw new InvalidOperationException(mesh.name + ": triangle vertex index is outside the mesh.");
                                face[corner] = (uint)index;
                            }
                            triangles[triangle++] = new NavigationTriangle { Indices = face };
                        }
                    }
                }
                return new NavigationAsset { ID = id, Vertices = vertices, Triangles = triangles };
            }
        }

        private static NavigationAsset CaptureTerrain(TerrainData data, string id)
        {
            var asset = GolemNavigationExporter.CaptureTerrain(data, id, 0);
            // Native Terrain collision splits each retained cell across a-d. Keep the
            // navigation source's historical diagonal separate from physical geometry.
            for (var i = 0; i < asset.Triangles.Length; i += 2)
            {
                var first = asset.Triangles[i].Indices; var second = asset.Triangles[i + 1].Indices;
                var a = first[0]; var b = first[2]; var c = first[1]; var d = second[2];
                asset.Triangles[i].Indices = new[] { a, c, d };
                asset.Triangles[i + 1].Indices = new[] { a, d, b };
            }
            return asset;
        }

        internal static Vector3 PrimitiveScale(Matrix4x4 matrix, out Quaternion rotation)
        {
            var x = (Vector3)matrix.GetColumn(0); var y = (Vector3)matrix.GetColumn(1); var z = (Vector3)matrix.GetColumn(2);
            var scale = new Vector3(x.magnitude, y.magnitude, z.magnitude);
            if (scale.x <= 0 || scale.y <= 0 || scale.z <= 0) throw new InvalidOperationException("Singular primitive transform.");
            x /= scale.x; y /= scale.y; z /= scale.z;
            if (Mathf.Abs(Vector3.Dot(x, y)) > 0.00001f || Mathf.Abs(Vector3.Dot(x, z)) > 0.00001f || Mathf.Abs(Vector3.Dot(y, z)) > 0.00001f)
                throw new InvalidOperationException("Sheared primitive hierarchy cannot be represented faithfully. Author an explicit mesh/convex collider or remove the shear; no AABB approximation is exported.");
            // A reflected symmetric primitive is unchanged by reversing one local basis axis.
            if (Vector3.Dot(Vector3.Cross(x, y), z) < 0) x = -x;
            rotation = Quaternion.LookRotation(z, y);
            return scale;
        }

        private static PhysicsAsset TransformAsset(NavigationAsset input, string id, string sourceID, Matrix4x4 matrix)
        {
            if (Mathf.Abs(matrix.determinant) <= 1e-12f) throw new InvalidOperationException("Singular mesh transform.");
            var vertices = input.Vertices.Select(v => Vector(matrix.MultiplyPoint3x4(new Vector3((float)v.X, (float)v.Y, (float)v.Z)))).ToArray();
            var indices = input.Triangles.SelectMany(t => t.Indices).ToArray();
            if (matrix.determinant < 0) for (var i = 0; i < indices.Length; i += 3) { var temp = indices[i + 1]; indices[i + 1] = indices[i + 2]; indices[i + 2] = temp; }
            return new PhysicsAsset { ID = id, SourceID = sourceID, Vertices = vertices, Indices = indices };
        }

        private static PhysicsMaterial CaptureMaterial(string id, UnityPhysicsMaterial material) => material ? new PhysicsMaterial
        {
            ID = id, StaticFriction = material.staticFriction, DynamicFriction = material.dynamicFriction,
            Restitution = material.bounciness, FrictionCombine = material.frictionCombine.ToString().ToLowerInvariant(), BounceCombine = material.bounceCombine.ToString().ToLowerInvariant()
        } : new PhysicsMaterial { ID = id };

        private static UnityPhysicsMaterial DefaultMaterial()
        {
            var settings = AssetDatabase.LoadAllAssetsAtPath("ProjectSettings/DynamicsManager.asset");
            if (settings.Length == 0) throw new InvalidOperationException("Cannot inspect the project physics default material.");
            var property = new SerializedObject(settings[0]).FindProperty("m_DefaultMaterial");
            if (property == null) throw new InvalidOperationException("Cannot read the project physics default material setting.");
            return property.objectReferenceValue as UnityPhysicsMaterial;
        }

        private static string Owner(Transform transform, GolemPhysicsWorld world)
        {
            for (var current = transform; current; current = current.parent)
            {
                var metadata = current.GetComponent<GolemPhysicsMetadata>();
                if (metadata && !string.IsNullOrWhiteSpace(metadata.OwnerID)) return metadata.OwnerID;
                if (current == world.transform) break;
            }
            return "";
        }
        private static string AssetIdentity(UnityEngine.Object asset)
        {
            if (!asset || !EditorUtility.IsPersistent(asset) || !AssetDatabase.TryGetGUIDAndLocalFileIdentifier(asset, out string guid, out long localID) || string.IsNullOrEmpty(guid))
                throw new InvalidOperationException("Geometry and material references must be saved assets; transient geometry must be saved explicitly.");
            return "unity-asset:" + guid + ":" + localID.ToString(CultureInfo.InvariantCulture);
        }
        internal static string Identity(Component component)
        {
            var id = GlobalObjectId.GetGlobalObjectIdSlow(component);
            if (id.targetObjectId == 0) throw new InvalidOperationException("Object lacks a stable saved identity: " + component.name);
            return "unity-object:" + id;
        }
        private static string Hierarchy(Transform transform) => transform.parent ? Hierarchy(transform.parent) + "/" + transform.name : transform.name;
        internal static PhysicsVector Vector(Vector3 v) => new PhysicsVector(v.x, v.y, v.z);
        internal static PhysicsRotation Rotation(Quaternion q) => new PhysicsRotation { X = q.x, Y = q.y, Z = q.z, W = q.w };

        private static void RejectLayerOverrides(Component component)
        {
            var serialized = new SerializedObject(component);
            foreach (var name in new[] { "m_IncludeLayers", "m_ExcludeLayers" })
            {
                var property = serialized.FindProperty(name);
                if (property == null) continue;
                var bits = property.FindPropertyRelative("m_Bits") ?? property;
                if (bits.intValue != 0) throw new InvalidOperationException(component.name + ": per-collider/body layer overrides are not yet supported; use the exported layer matrix or explicit server query filters.");
            }
        }
    }
}
