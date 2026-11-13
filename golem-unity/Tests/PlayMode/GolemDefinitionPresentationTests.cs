using System;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;

namespace GolemEngine.Unity.Tests
{
    public sealed class SyncedContentProbe
    {
        public long EntityId { get; set; }
        public string DefinitionId { get; set; }
        public float PosX { get; set; }
        public float PosY => 0;
        public float PosZ => 0;
    }

    public sealed class ContentProbeManager
    {
        private Action<object> spawn;
        private Action<object> update;
        private Action<long> remove;
        public void OnSpawn(Action<object> callback) => spawn = callback;
        public void OnUpdate(Action<object> callback) => update = callback;
        public void OnRemove(Action<long> callback) => remove = callback;
        public void Spawn(SyncedContentProbe entity) => spawn(entity);
        public void Update(SyncedContentProbe entity) => update(entity);
        public void Remove(long id) => remove(id);
    }

    public sealed class GolemDefinitionPresentationTests
    {
        [Test]
        public void OneEntityTypeSelectsTwoPresentationsAndIndependentInstances()
        {
            var host = new GameObject("content-host");
            var scout = new GameObject("Scout"); var guard = new GameObject("Guard");
            var registry = ScriptableObject.CreateInstance<GolemDefinitionPrefabRegistry>();
            try
            {
                scout.AddComponent<GolemTransformBinding>(); guard.AddComponent<GolemTransformBinding>();
                registry.Upsert("Monster", "demo/Monster/scout", scout);
                registry.Upsert("Monster", "demo/Monster/guard", guard);
                var spawner = host.AddComponent<GolemEntitySpawner>();
                spawner.UseDefinitions<SyncedContentProbe>("Monster", entity => entity.DefinitionId, registry);
                var manager = new ContentProbeManager(); spawner.Bind(manager);
                manager.Spawn(new SyncedContentProbe { EntityId = 1, DefinitionId = "demo/Monster/scout", PosX = 1 });
                manager.Spawn(new SyncedContentProbe { EntityId = 2, DefinitionId = "demo/Monster/guard", PosX = 2 });
                manager.Spawn(new SyncedContentProbe { EntityId = 3, DefinitionId = "demo/Monster/scout", PosX = 3 });
                Assert.That(host.transform.childCount, Is.EqualTo(3));
                Assert.That(host.transform.GetChild(0).name, Does.StartWith("Scout"));
                Assert.That(host.transform.GetChild(1).name, Does.StartWith("Guard"));
                Assert.That(host.transform.GetChild(2).name, Does.StartWith("Scout"));
                for (var i = 0; i < 3; i++) Assert.That(host.transform.GetChild(i).position.x, Is.EqualTo(i + 1));
                LogAssert.Expect(LogType.Error, "Golem presentation: Missing definition presentation: Monster / demo/Monster/missing");
                manager.Spawn(new SyncedContentProbe { EntityId = 4, DefinitionId = "demo/Monster/missing" });
                Assert.That(host.transform.childCount, Is.EqualTo(3));
                LogAssert.Expect(LogType.Error, "Golem presentation: definition ID changed for live instance 1; recreate the instance explicitly.");
                manager.Update(new SyncedContentProbe { EntityId = 1, DefinitionId = "demo/Monster/guard" });
                Assert.That(host.transform.GetChild(0).name, Does.StartWith("Scout"));
            }
            finally
            {
                UnityEngine.Object.DestroyImmediate(host); UnityEngine.Object.DestroyImmediate(scout);
                UnityEngine.Object.DestroyImmediate(guard); UnityEngine.Object.DestroyImmediate(registry);
            }
        }

        [Test]
        public void DefinitionRegistryRequiresCompleteEntriesAndExactKeys()
        {
            var prefab = new GameObject("view"); var registry = ScriptableObject.CreateInstance<GolemDefinitionPrefabRegistry>();
            try
            {
                registry.Upsert("Monster", "demo/Monster/scout", prefab);
                registry.Validate();
                Assert.That(registry.GetPrefab("Monster", "demo/Monster/scout"), Is.SameAs(prefab));
                Assert.Throws<InvalidOperationException>(() => registry.GetPrefab("Building", "demo/Monster/scout"));
                Assert.Throws<ArgumentException>(() => registry.Upsert("Monster", "", prefab));
            }
            finally { UnityEngine.Object.DestroyImmediate(prefab); UnityEngine.Object.DestroyImmediate(registry); }
        }
    }
}
