using System;
using System.Collections;
using System.IO;
using System.Linq;
using System.Text;
using GolemEngine.Unity;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.Networking;
using UnityEngine.TestTools;

namespace Golem.Integration.Fixture.Tests
{
    public sealed class LoopbackIntegrationTests
    {
        [Serializable]
        public sealed class EntityState
        {
            public long id;
            public string definition_id;
            public int health;
            public float x, y, z;
            public Vector3 Position => new Vector3(x, y, z);
        }

        [Serializable]
        public sealed class ServerState
        {
            public string mode, phase, error, checkpoint_path, restored_checkpoint_id;
            public EntityState[] entities, initial_entities, checkpoint_entities;
            public bool motion_passed, query_passed, pose_equality_passed, checkpoint_ready;
        }

        [Serializable]
        public sealed class ViewState
        {
            public long id;
            public string definition_id, presentation, mesh;
            public int health, bind_calls;
            public bool renderer_enabled;
            public Vector3 position, view_position, initial_view_position;
        }

        [Serializable]
        public sealed class ClientEvidence
        {
            public string unity_version, mode, transport;
            public ServerState initial_server, final_server;
            public ViewState[] initial_views, final_views;
            public bool real_generated_client, definitions_matched, initial_transforms_matched, motion_observed;
            public bool deletion_observed, checkpoint_state_matched, continuation_observed, moving_transforms_matched, visible_presentations_matched;
        }

        private string endpoint;
        private ServerState state;
        private GameObject host;
        private GameClient client;
        private EntityManager entities;

        [UnityTest]
        public IEnumerator AuthoredDefinitionsCrossRealSocketAndSurviveCheckpoint()
        {
            endpoint = RequiredEnvironment("GOLEM_INTEGRATION_URL").TrimEnd('/');
            var uri = new Uri(endpoint);
            Assert.That(uri.IsLoopback && uri.Scheme == "http", Is.True, "Fixture requires loopback HTTP.");
            var mode = RequiredEnvironment("GOLEM_INTEGRATION_MODE");
            Assert.That(mode, Is.EqualTo("fresh").Or.EqualTo("resume"));
            var evidenceDirectory = Path.GetFullPath(RequiredEnvironment("GOLEM_INTEGRATION_EVIDENCE"));
            var evidence = new ClientEvidence { unity_version = Application.unityVersion, mode = mode };
            var registry = ScriptableObject.CreateInstance<GolemDefinitionPrefabRegistry>();
            try
            {
                yield return FetchState();
                Assert.That(state.mode, Is.EqualTo(mode));
                evidence.initial_server = state;
                AssertInitialIdentity(state.initial_entities, mode);
                if (mode == "resume")
                {
                    var fresh = JsonUtility.FromJson<ClientEvidence>(File.ReadAllText(Path.Combine(evidenceDirectory, "unity-fresh-client.json")));
                    Assert.That(fresh.final_server.checkpoint_ready, Is.True, "Fresh process must have completed paired checkpoint capture.");
                    AssertSameSavedState(fresh.final_server.checkpoint_entities, state.initial_entities);
                    Assert.That(state.restored_checkpoint_id, Is.Not.Null.And.Not.Empty);
                    evidence.checkpoint_state_matched = true;
                }

                var scout = Resources.Load<GameObject>("Integration/Scout");
                var guard = Resources.Load<GameObject>("Integration/Guard");
                Assert.That(scout, Is.Not.Null, "Run the selected content export before the integration test.");
                Assert.That(guard, Is.Not.Null);
                Assert.That(scout.GetComponent<MeshFilter>(), Is.Not.Null);
                Assert.That(guard.GetComponent<MeshFilter>(), Is.Not.Null);
                Assert.That(scout.GetComponent<MeshFilter>().sharedMesh, Is.Not.Null);
                Assert.That(guard.GetComponent<MeshFilter>().sharedMesh, Is.Not.Null.And.Not.SameAs(scout.GetComponent<MeshFilter>().sharedMesh),
                    "Scout and Guard need distinct visible prefab meshes.");
                registry.Upsert("Monster", "demo/Monster/scout", scout);
                registry.Upsert("Monster", "demo/Monster/guard", guard);
                registry.Validate();
                host = new GameObject("Integration views");
                var spawner = host.AddComponent<GolemEntitySpawner>();
                spawner.UseDefinitions<SyncedMonster>("Monster", entity => entity.DefinitionId, registry);
                client = ClientFactory.CreateClient();
                entities = (EntityManager)client.Entities;
                spawner.Bind(entities);
                GolemMainThreadDispatcher.Ensure();
                var socket = new UriBuilder(uri) { Scheme = "ws", Path = "/ws" }.Uri.AbsoluteUri;
                client.Connect(new GolemConnectOptions(socket, GolemConnectOptions.TransportWebSocket));
                yield return WaitForReplica(state.initial_entities, 15);
                Assert.That(client.ConnectedTransport, Is.EqualTo(GolemConnectOptions.TransportWebSocket));
                AssertReplica(state.initial_entities, true);
                evidence.real_generated_client = true;
                evidence.transport = client.ConnectedTransport;
                evidence.definitions_matched = true;
                evidence.initial_transforms_matched = true;
                evidence.visible_presentations_matched = true;
                evidence.initial_views = CaptureViews();

                yield return Post("/integration/start");
                var deadline = Time.realtimeSinceStartup + 30;
                do
                {
                    yield return FetchState();
                    if (state.motion_passed) break;
                    Assert.That(Time.realtimeSinceStartup, Is.LessThan(deadline), "Server did not complete its bounded navigation/collision movement checks.");
                    yield return new WaitForSecondsRealtime(0.1f);
                } while (true);

                var guardStart = evidence.initial_server.initial_entities.Single(x => x.id == 3).Position;
                yield return WaitForViewMovement(3, guardStart, 10);
                if (mode == "fresh")
                {
                    var scoutStart = evidence.initial_server.initial_entities.Single(x => x.id == 1).Position;
                    yield return WaitForViewMovement(1, scoutStart, 10);
                    evidence.motion_observed = true;
                    yield return Post("/integration/checkpoint");
                    deadline = Time.realtimeSinceStartup + 15;
                    do
                    {
                        yield return FetchState();
                        if (state.checkpoint_ready) break;
                        Assert.That(Time.realtimeSinceStartup, Is.LessThan(deadline), "Paired checkpoint did not complete.");
                        yield return new WaitForSecondsRealtime(0.1f);
                    } while (true);
                    AssertInitialIdentity(state.checkpoint_entities, "resume");
                    Assert.That(state.checkpoint_path, Is.Not.Null.And.Not.Empty);
                    yield return WaitForReplica(state.checkpoint_entities, 10);
                    AssertReplica(state.checkpoint_entities, false);
                    Assert.That(entities.Get(2), Is.Null, "Deleted entity was still present in the generated manager.");
                    Assert.That(Views().Any(x => ((SyncedMonster)x.Entity).EntityId == 2), Is.False, "Deleted entity view remained alive.");
                    evidence.deletion_observed = true;
                }
                else
                {
                    Assert.That(((SyncedMonster)entities.Get(1)).Health, Is.EqualTo(13), "Restoration must not reapply archetype health defaults.");
                    Assert.That(entities.Get(2), Is.Null, "Restoration must not instantiate the deleted authored instance.");
                    AssertReplica(evidence.initial_server.initial_entities, false);
                    evidence.continuation_observed = true;
                }

                yield return FetchState();
                Assert.That(state.query_passed, Is.True, "Server must verify the blocker query through its existing physics API.");
                Assert.That(state.pose_equality_passed, Is.True, "Accepted physics, entity and replicated poses must agree on the server.");
                yield return WaitForViewAlignment(5);
                evidence.moving_transforms_matched = true;
                evidence.final_server = state;
                evidence.final_views = CaptureViews();
                Directory.CreateDirectory(evidenceDirectory);
                using (var stream = new FileStream(Path.Combine(evidenceDirectory, "unity-" + mode + "-client.json"), FileMode.CreateNew))
                using (var writer = new StreamWriter(stream, new UTF8Encoding(false))) writer.Write(JsonUtility.ToJson(evidence, true));
            }
            finally
            {
                client?.Disconnect();
                entities?.Clear();
                if (host != null) UnityEngine.Object.DestroyImmediate(host);
                UnityEngine.Object.DestroyImmediate(registry);
            }
        }

        private IEnumerator FetchState()
        {
            using (var request = UnityWebRequest.Get(endpoint + "/integration/state"))
            {
                request.timeout = 5;
                yield return request.SendWebRequest();
                Assert.That(request.result, Is.EqualTo(UnityWebRequest.Result.Success), request.error);
                state = JsonUtility.FromJson<ServerState>(request.downloadHandler.text);
                Assert.That(state, Is.Not.Null);
                Assert.That(state.error, Is.Null.Or.Empty, "Server fixture failed: " + state.error);
            }
        }

        private IEnumerator Post(string path)
        {
            using (var request = new UnityWebRequest(endpoint + path, UnityWebRequest.kHttpVerbPOST))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes("{}"));
                request.downloadHandler = new DownloadHandlerBuffer();
                request.SetRequestHeader("Content-Type", "application/json");
                request.timeout = 10;
                yield return request.SendWebRequest();
                Assert.That(request.result, Is.EqualTo(UnityWebRequest.Result.Success), request.error + ": " + request.downloadHandler.text);
            }
        }

        private IEnumerator WaitForReplica(EntityState[] expected, float seconds)
        {
            var deadline = Time.realtimeSinceStartup + seconds;
            while (!ReplicaMatches(expected))
            {
                Assert.That(Time.realtimeSinceStartup, Is.LessThan(deadline), "Generated client/view state did not converge to the expected entity IDs, definitions and health.");
                yield return null;
            }
            // Entity removal destroys a Unity view at the end of its frame.
            yield return null;
        }

        private bool ReplicaMatches(EntityState[] expected)
        {
            if (!client.Connected || entities.GetAll().Count != expected.Length || Views().Length != expected.Length) return false;
            foreach (var item in expected)
            {
                var entity = entities.Get(item.id) as SyncedMonster;
                if (entity == null || entity.DefinitionId != item.definition_id || entity.Health != item.health) return false;
            }
            return true;
        }

        private void AssertReplica(EntityState[] expected, bool initial)
        {
            Assert.That(ReplicaMatches(expected), Is.True);
            var views = Views();
            Assert.That(views.Select(x => ((SyncedMonster)x.Entity).EntityId).Distinct().Count(), Is.EqualTo(expected.Length));
            foreach (var item in expected)
            {
                var entity = (SyncedMonster)entities.Get(item.id);
                var view = views.Single(x => ReferenceEquals(x.Entity, entity));
                Assert.That(view.Presentation, Is.EqualTo(item.definition_id.EndsWith("/guard", StringComparison.Ordinal) ? "guard" : "scout"));
                Assert.That(view.BindCalls, Is.EqualTo(1));
                Assert.That(view.GetComponent<GolemTransformBinding>(), Is.Not.Null);
                var prefab = Resources.Load<GameObject>("Integration/" + (view.Presentation == "guard" ? "Guard" : "Scout"));
                var mesh = view.GetComponent<MeshFilter>();
                var renderer = view.GetComponent<MeshRenderer>();
                Assert.That(mesh, Is.Not.Null);
                Assert.That(renderer, Is.Not.Null);
                Assert.That(mesh.sharedMesh, Is.SameAs(prefab.GetComponent<MeshFilter>().sharedMesh), "Spawned visible geometry must come from the selected definition prefab.");
                Assert.That(renderer.enabled && view.gameObject.activeInHierarchy, Is.True);
                Assert.That(renderer.sharedMaterial, Is.SameAs(prefab.GetComponent<MeshRenderer>().sharedMaterial));
                if (initial)
                {
                    Assert.That(Vector3.Distance(new Vector3(entity.PosX, entity.PosY, entity.PosZ), item.Position), Is.LessThan(0.0001f));
                    Assert.That(Vector3.Distance(view.InitialPosition, item.Position), Is.LessThan(0.0001f), "Initial transform must be bound before the view callback.");
                    Assert.That(Vector3.Distance(view.transform.position, item.Position), Is.LessThan(0.0001f));
                }
            }
        }

        private IEnumerator WaitForViewMovement(long id, Vector3 initial, float seconds)
        {
            var deadline = Time.realtimeSinceStartup + seconds;
            while (true)
            {
                var entity = entities.Get(id) as SyncedMonster;
                var view = entity == null ? null : Views().SingleOrDefault(x => ReferenceEquals(x.Entity, entity));
                if (view != null && Vector3.Distance(view.transform.position, initial) > 0.05f &&
                    Vector3.Distance(new Vector3(entity.PosX, entity.PosY, entity.PosZ), initial) > 0.05f) break;
                Assert.That(Time.realtimeSinceStartup, Is.LessThan(deadline), "Accepted server movement was not observed through the real generated client and transform binding for entity " + id);
                yield return null;
            }
        }

        private IEnumerator WaitForViewAlignment(float seconds)
        {
            var deadline = Time.realtimeSinceStartup + seconds;
            while (true)
            {
                var views = Views();
                if (views.Length == entities.GetAll().Count && views.All(view =>
                {
                    var entity = (SyncedMonster)view.Entity;
                    var position = view.transform.position;
                    return position.x == entity.PosX && position.y == entity.PosY && position.z == entity.PosZ;
                })) yield break;
                Assert.That(Time.realtimeSinceStartup, Is.LessThan(deadline), "Moving views did not match the current generated entity positions with fixture smoothing disabled.");
                // The dispatcher and transform binding both update on the main thread. Observe
                // a subsequent frame instead of using WaitForEndOfFrame, which stalls in batch mode.
                yield return null;
            }
        }

        private ObservedDefinitionView[] Views() => host.GetComponentsInChildren<ObservedDefinitionView>();

        private ViewState[] CaptureViews() => Views().Select(view =>
        {
            var entity = (SyncedMonster)view.Entity;
            return new ViewState
            {
                id = entity.EntityId, definition_id = entity.DefinitionId, health = entity.Health,
                presentation = view.Presentation, bind_calls = view.BindCalls,
                mesh = view.GetComponent<MeshFilter>().sharedMesh.name, renderer_enabled = view.GetComponent<MeshRenderer>().enabled,
                position = new Vector3(entity.PosX, entity.PosY, entity.PosZ), view_position = view.transform.position,
                initial_view_position = view.InitialPosition
            };
        }).OrderBy(x => x.id).ToArray();

        private static void AssertInitialIdentity(EntityState[] values, string mode)
        {
            Assert.That(values, Is.Not.Null);
            CollectionAssert.AreEquivalent(mode == "fresh" ? new long[] { 1, 2, 3 } : new long[] { 1, 3 }, values.Select(x => x.id).ToArray());
            foreach (var item in values)
            {
                Assert.That(item.definition_id, Is.EqualTo(item.id == 3 ? "demo/Monster/guard" : "demo/Monster/scout"));
                Assert.That(item.health, Is.EqualTo(item.id == 3 ? 160 : mode == "fresh" ? 80 : 13));
            }
        }

        private static void AssertSameSavedState(EntityState[] saved, EntityState[] restored)
        {
            Assert.That(saved, Is.Not.Null);
            Assert.That(restored.Length, Is.EqualTo(saved.Length));
            foreach (var item in saved)
            {
                var actual = restored.Single(x => x.id == item.id);
                Assert.That(actual.definition_id, Is.EqualTo(item.definition_id));
                Assert.That(actual.health, Is.EqualTo(item.health));
                Assert.That(actual.x, Is.EqualTo(item.x));
                Assert.That(actual.y, Is.EqualTo(item.y));
                Assert.That(actual.z, Is.EqualTo(item.z));
            }
        }

        private static string RequiredEnvironment(string name)
        {
            var value = Environment.GetEnvironmentVariable(name);
            Assert.That(value, Is.Not.Null.And.Not.Empty, name + " is required.");
            return value;
        }
    }
}
