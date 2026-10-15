using System;
using GolemEngine.Unity;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemEntityExportPlannerTests
    {
        [GolemEntity("Player")]
        private sealed class PlayerSource : MonoBehaviour { [GolemVar(1)] public int Health; }
        [GolemEntity("Peer")]
        private sealed class PeerSource : MonoBehaviour { [GolemVar(1)] public int Health; }
        [GolemEntity("Invalid")]
        private sealed class InvalidSource : MonoBehaviour { [GolemVar(1)] public int[] Unsupported; }

        [Test]
        public void ProductionCollectorDropsAllDuplicateNamesButKeepsValidPeer()
        {
            var objects = new[] { new GameObject("A"), new GameObject("B"), new GameObject("C"), new GameObject("Peer"), new GameObject("Invalid") };
            try
            {
                for (var i = 0; i < 3; i++) objects[i].AddComponent<PlayerSource>();
                objects[3].AddComponent<PeerSource>();
                objects[4].AddComponent<InvalidSource>();
                var sources = new ScribePrefabSource[objects.Length];
                for (var i = 0; i < objects.Length; i++)
                    sources[i] = new ScribePrefabSource(i.ToString(), "Assets/" + i + ".prefab", objects[i]);
                GolemEntityExporter.CollectEntities(new GolemYamlConfig.ProjectSchemaConfig(), sources,
                    out var desired, out var registry, out var errors);
                Assert.That(desired, Has.Count.EqualTo(1));
                Assert.That(desired[0].Entity, Is.EqualTo("Peer"));
                Assert.That(registry.Keys, Is.EquivalentTo(new[] { "Peer" }));
                Assert.That(errors, Has.Some.Contain("multiple prefabs"));
                Assert.That(errors, Has.Some.Contain("skipping"));
                Assert.That(errors, Has.Some.Contain("unsupported type"));
            }
            finally
            {
                foreach (var obj in objects) UnityEngine.Object.DestroyImmediate(obj);
            }
        }
    }
}
