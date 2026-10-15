using System;
using System.Collections;
using System.Reflection;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;
using Object = UnityEngine.Object;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemEntitySpawnerTests
    {
        private GameObject _root;
        private GameObject _prefab;
        private GolemEntitySpawner _spawner;
        private GolemPrefabRegistry _registry;
        private SpawnProbeManager _manager;

        [SetUp]
        public void SetUp()
        {
            _root = new GameObject("test root");
            _prefab = new GameObject("prefab");
            _prefab.transform.SetParent(_root.transform);
            var host = new GameObject("spawner");
            host.transform.SetParent(_root.transform);
            _spawner = host.AddComponent<GolemEntitySpawner>();
            _registry = ScriptableObject.CreateInstance<GolemPrefabRegistry>();
            _registry.Upsert("LifecycleProbe", _prefab);
            _registry.Upsert("PlaneProbe", _prefab);
            typeof(GolemEntitySpawner).GetField("prefabRegistry", BindingFlags.NonPublic | BindingFlags.Instance).SetValue(_spawner, _registry);
            _manager = new SpawnProbeManager();
            _spawner.Bind(_manager);
            SpawnProbeLog.Events.Clear();
            SpawnProbeLog.Bound = null;
            SpawnProbeLog.Spawned = null;
        }

        [TearDown]
        public void TearDown()
        {
            SpawnProbeLog.Bound = null;
            SpawnProbeLog.Spawned = null;
            Object.DestroyImmediate(_root);
            Object.DestroyImmediate(_registry);
        }

        private GameObject Spawn(object entity)
        {
            _manager.Spawn(entity);
            return _spawner.transform.GetChild(_spawner.transform.childCount - 1).gameObject;
        }

        [Test]
        public void SerializedPrefabPreservesGeneratedViewsAndSharedChildReference()
        {
            var prefab = Resources.Load<GameObject>("GolemSpawnLifecycle");
            Assert.That(prefab, Is.Not.Null);
            _registry.Upsert("LifecycleProbe", prefab);
            SpawnProbeLog.Spawned = (view, bound) =>
            {
                Assert.That(bound.PositionReads, Is.EqualTo(2));
                Assert.That(view.transform.position, Is.EqualTo(new Vector3(3, 4, 5)));
                Assert.That(view.transform.GetChild(0).position, Is.EqualTo(new Vector3(3, 4, 5)));
            };
            var entity = new SyncedLifecycleProbe();
            var instance = Spawn(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(2));
            Assert.That(instance.GetComponents<RecordingProbeView>().Length, Is.EqualTo(2));
            foreach (var view in instance.GetComponents<RecordingProbeView>())
                Assert.That(view.SpawnCalls, Is.EqualTo(1));
        }

        [TestCase(false)]
        [TestCase(true)]
        public void GeneratedViewsInitializeRootOnceInEitherComponentOrder(bool bindingFirst)
        {
            if (bindingFirst) _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<RecordingProbeView>();
            _prefab.AddComponent<RecordingProbeView>();
            if (!bindingFirst) _prefab.AddComponent<GolemTransformBinding>();
            SpawnProbeLog.Spawned = (view, bound) =>
            {
                Assert.That(bound.PositionReads, Is.EqualTo(1));
                Assert.That(view.transform.position, Is.EqualTo(new Vector3(3, 4, 5)));
            };
            var entity = new SyncedLifecycleProbe();
            var instance = Spawn(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(1));
            foreach (var view in instance.GetComponents<RecordingProbeView>())
            {
                Assert.That(view.SpawnCalls, Is.EqualTo(1));
                Assert.That(view.Entity, Is.SameAs(entity));
            }
        }

        [Test]
        public void PrefabWithoutAViewStillSnapsOnce()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            var entity = new SyncedLifecycleProbe();
            Assert.That(Spawn(entity).transform.position, Is.EqualTo(new Vector3(3, 4, 5)));
            Assert.That(entity.PositionReads, Is.EqualTo(1));
        }

        [Test]
        public void CustomCallbacksKeepOrderAndAreNotResnappedByGeneratedViews()
        {
            _prefab.AddComponent<CustomProbeView>();
            _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<RecordingProbeView>();
            SpawnProbeLog.Events.Clear();
            SpawnProbeLog.Bound = (view, value) =>
            {
                var entity = (SyncedLifecycleProbe)value;
                Assert.That(view.transform.position, Is.EqualTo(new Vector3(3, 4, 5)));
                entity.X = 9;
                view.transform.position = new Vector3(20, 21, 22);
                view.GetComponent<GolemTransformBinding>().Bind(entity);
            };
            SpawnProbeLog.Spawned = (view, entity) =>
            {
                Assert.That(view.transform.position, Is.EqualTo(new Vector3(20, 21, 22)));
                Assert.That(entity.PositionReads, Is.EqualTo(1));
            };
            var entity = new SyncedLifecycleProbe();
            var instance = Spawn(entity);
            Assert.That(SpawnProbeLog.Events, Is.EqualTo(new[] { "awake", "enabled", "custom", "spawned" }));
            Assert.That(instance.GetComponent<CustomProbeView>().BindCalls, Is.EqualTo(1));
            instance.GetComponent<GolemTransformBinding>().Bind(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(2));
            Assert.That(instance.transform.position.x, Is.EqualTo(9));
        }

        [TestCase(false)]
        [TestCase(true)]
        public void SerializedTargetsCanBeSharedChildrenOrExternalObjects(bool external)
        {
            _prefab.AddComponent<GolemTransformBinding>();
            var target = new GameObject("binding target");
            target.transform.SetParent(external ? _root.transform : _prefab.transform);
            var binding = target.AddComponent<GolemTransformBinding>();
            for (var i = 0; i < 2; i++)
            {
                var view = _prefab.AddComponent<RecordingProbeView>();
                typeof(LifecycleProbeView).GetField("transformBinding", BindingFlags.NonPublic | BindingFlags.Instance).SetValue(view, binding);
            }
            var entity = new SyncedLifecycleProbe();
            var instance = Spawn(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(2), "root and selected target each initialize once");
            var boundTarget = external ? target.transform : instance.transform.GetChild(0);
            Assert.That(boundTarget.position, Is.EqualTo(new Vector3(3, 4, 5)));
        }

        [Test]
        public void AdditionalRootBindingsRetainTheirTraversalPosition()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<CustomProbeView>();
            _prefab.AddComponent<GolemTransformBinding>();
            SpawnProbeLog.Bound = (_, value) => Assert.That(((SyncedLifecycleProbe)value).PositionReads, Is.EqualTo(1));
            var entity = new SyncedLifecycleProbe();
            Spawn(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(2));
        }

        [Test]
        public void StandaloneViewsAndExplicitRebindsAlwaysSnap()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            var view = _prefab.AddComponent<RecordingProbeView>();
            var first = new SyncedLifecycleProbe();
            view.Bind(first);
            first.X = 8;
            view.Bind(first);
            var second = new SyncedLifecycleProbe { X = 12 };
            view.Bind(second);
            Assert.That(first.PositionReads, Is.EqualTo(2));
            Assert.That(second.PositionReads, Is.EqualTo(1));
            Assert.That(view.SpawnCalls, Is.EqualTo(3));
            Assert.That(view.transform.position.x, Is.EqualTo(12));
        }

        [Test]
        public void NestedSpawnsRestoreTheirParentScope()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<RecordingProbeView>();
            var nested = new SyncedLifecycleProbe { EntityId = 2 };
            SpawnProbeLog.Spawned = (view, entity) =>
            {
                if (entity.EntityId != 1) return;
                _manager.Spawn(nested);
                view.GetComponent<GolemTransformBinding>().Bind(entity);
            };
            var outer = new SyncedLifecycleProbe();
            Spawn(outer);
            Assert.That(outer.PositionReads, Is.EqualTo(1));
            Assert.That(nested.PositionReads, Is.EqualTo(1));
            Assert.That(_spawner.transform.childCount, Is.EqualTo(2));
        }

        [Test]
        public void RebindingAnotherEntityAndRetryingFailedInitializationAreNotSuppressed()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<RecordingProbeView>();
            var other = new SyncedLifecycleProbe { EntityId = 2 };
            SpawnProbeLog.Spawned = (view, entity) =>
            {
                var binding = view.GetComponent<GolemTransformBinding>();
                binding.Bind(other);
                entity.ThrowOnPosition = true;
                Assert.Throws<TargetInvocationException>(() => binding.Bind(entity));
                entity.ThrowOnPosition = false;
                binding.Bind(entity);
                binding.Bind(entity);
            };
            var entity = new SyncedLifecycleProbe();
            Spawn(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(3), "initial, failed return, successful return");
            Assert.That(other.PositionReads, Is.EqualTo(1));
        }

        [Test]
        public void CallbackFailureEndsTheSpawnScope()
        {
            _prefab.AddComponent<GolemTransformBinding>();
            _prefab.AddComponent<RecordingProbeView>();
            SpawnProbeLog.Spawned = (_, __) => throw new InvalidOperationException("callback");
            var entity = new SyncedLifecycleProbe();
            Assert.Throws<TargetInvocationException>(() => Spawn(entity));
            var binding = _spawner.transform.GetChild(0).GetComponent<GolemTransformBinding>();
            binding.Bind(entity);
            Assert.That(entity.PositionReads, Is.EqualTo(2));
        }

        [Test]
        public void TwoDimensionalBindingPreservesExistingDepth()
        {
            _prefab.transform.position = new Vector3(0, 0, 17);
            _prefab.AddComponent<GolemTransformBinding>();
            Assert.That(Spawn(new SyncedPlaneProbe()).transform.position, Is.EqualTo(new Vector3(3, 4, 17)));
        }

        [UnityTest]
        public IEnumerator SmoothingContinuesOnLaterFrames()
        {
            var binding = _prefab.AddComponent<GolemTransformBinding>();
            typeof(GolemTransformBinding).GetField("lerpSpeed", BindingFlags.NonPublic | BindingFlags.Instance).SetValue(binding, 1f);
            var entity = new SyncedLifecycleProbe();
            var instance = Spawn(entity);
            Assert.That(instance.transform.position.x, Is.EqualTo(3));
            entity.X = 100;
            yield return null;
            Assert.That(instance.transform.position.x, Is.GreaterThan(3).And.LessThan(100));
        }
    }
}
