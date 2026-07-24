using System.Collections.Generic;
using GolemEngine.Unity;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemPrefabRegistryTests
    {
        [Test]
        public void Upsert_InsertsAndReplacesByEntityName()
        {
            var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var first = new GameObject("first");
            var second = new GameObject("second");
            try
            {
                registry.Upsert("Player", first);
                Assert.That(registry.GetPrefab("Player"), Is.SameAs(first));

                registry.Upsert("Player", second);
                Assert.That(registry.GetPrefab("Player"), Is.SameAs(second));
            }
            finally
            {
                Object.DestroyImmediate(first);
                Object.DestroyImmediate(second);
                Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void Remove_DeletesEntityMapping()
        {
            var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var prefab = new GameObject("player");
            try
            {
                registry.Upsert("Player", prefab);
                Assert.That(registry.Remove("Player"), Is.True);
                Assert.That(registry.GetPrefab("Player"), Is.Null);
                Assert.That(registry.Remove("Player"), Is.False);
            }
            finally
            {
                Object.DestroyImmediate(prefab);
                Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void TrySelectUniqueRegistry_RejectsZeroAndMultiple()
        {
            Assert.That(
                GolemPrefabRegistryUtil.TrySelectUniqueRegistry(
                    new List<(string, GolemPrefabRegistry)>(),
                    out _,
                    out var noneError),
                Is.False);
            Assert.That(noneError, Does.Contain("No GolemPrefabRegistry"));

            var a = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var b = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            try
            {
                Assert.That(
                    GolemPrefabRegistryUtil.TrySelectUniqueRegistry(
                        new List<(string, GolemPrefabRegistry)>
                        {
                            ("Assets/A.asset", a),
                            ("Assets/B.asset", b)
                        },
                        out _,
                        out var multiError),
                    Is.False);
                Assert.That(multiError, Does.Contain("Multiple"));

                Assert.That(
                    GolemPrefabRegistryUtil.TrySelectUniqueRegistry(
                        new List<(string, GolemPrefabRegistry)> { ("Assets/A.asset", a) },
                        out var selected,
                        out _),
                    Is.True);
                Assert.That(selected, Is.SameAs(a));
            }
            finally
            {
                Object.DestroyImmediate(a);
                Object.DestroyImmediate(b);
            }
        }

        [Test]
        public void ApplyEntityMappings_RemovesMissingPreviousEntities()
        {
            var registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            var player = new GameObject("player");
            var monster = new GameObject("monster");
            try
            {
                registry.Upsert("Player", player);
                registry.Upsert("Monster", monster);

                GolemPrefabRegistryUtil.ApplyEntityMappings(
                    registry,
                    new Dictionary<string, GameObject> { { "Player", player } },
                    new[] { "Player", "Monster" });

                Assert.That(registry.GetPrefab("Player"), Is.SameAs(player));
                Assert.That(registry.GetPrefab("Monster"), Is.Null);
            }
            finally
            {
                Object.DestroyImmediate(player);
                Object.DestroyImmediate(monster);
                Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void PendingRegistryRemovals_SurviveFailedApply()
        {
            GolemEntityExporter.ClearPendingRegistryRemovalsForTests();
            try
            {
                GolemEntityExporter.StorePendingRegistryRemovalsForTests(new[] { "Monster", "Player" });
                var pending = GolemEntityExporter.LoadPendingRegistryRemovalsForTests();
                Assert.That(pending, Is.EquivalentTo(new[] { "Monster", "Player" }));
            }
            finally
            {
                GolemEntityExporter.ClearPendingRegistryRemovalsForTests();
            }
        }
    }
}
