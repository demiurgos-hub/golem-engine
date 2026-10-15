using System;
using System.Collections.Generic;
using System.Reflection;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Copies generated PosX/PosY and optional PosZ properties into a GameObject transform.
    /// In 3D, PosY is Unity's vertical axis and PosZ is depth.
    /// </summary>
    public sealed class GolemTransformBinding : MonoBehaviour
    {
        [SerializeField] private bool smooth = true;
        [SerializeField] private float lerpSpeed = 12f;

        private object _entity;
        private PropertyInfo _posX;
        private PropertyInfo _posY;
        private PropertyInfo _posZ;
        private bool _boundSuccessfully;

        [ThreadStatic] private static SpawnBindingScope _spawnScope;

        /// <summary>
        /// Binds and immediately snaps to an entity. Repeated binding to the current entity
        /// during one spawner dispatch initializes once; explicit binds outside it always snap.
        /// </summary>
        public void Bind(object entity)
        {
            var scope = _spawnScope;
            if (scope != null && ReferenceEquals(scope.Entity, entity) &&
                _boundSuccessfully && ReferenceEquals(_entity, entity) && scope.Contains(this))
            {
                return;
            }
            _boundSuccessfully = false;
            _entity = entity;
            var type = entity.GetType();
            _posX = type.GetProperty("PosX");
            _posY = type.GetProperty("PosY");
            _posZ = type.GetProperty("PosZ");
            ApplyPosition(true);
            _boundSuccessfully = true;
            if (scope != null && ReferenceEquals(scope.Entity, entity))
            {
                scope.Record(this);
            }
        }

        // Only synchronous spawn dispatch owns this scope; never carry it across a yield.
        internal static IDisposable BeginSpawnBindingScope(object entity)
        {
            return new SpawnBindingScope(entity);
        }

        private sealed class SpawnBindingScope : IDisposable
        {
            internal readonly object Entity;
            private readonly SpawnBindingScope _parent;
            private readonly List<GolemTransformBinding> _initialized = new List<GolemTransformBinding>();

            internal SpawnBindingScope(object entity)
            {
                Entity = entity;
                _parent = _spawnScope;
                _spawnScope = this;
            }

            internal bool Contains(GolemTransformBinding binding)
            {
                // Unity Object equality is not managed reference identity.
                foreach (var initialized in _initialized)
                {
                    if (ReferenceEquals(initialized, binding)) return true;
                }
                return false;
            }

            internal void Record(GolemTransformBinding binding)
            {
                if (!Contains(binding)) _initialized.Add(binding);
            }

            public void Dispose()
            {
                _spawnScope = _parent;
            }
        }

        private void Update()
        {
            ApplyPosition(!smooth);
        }

        private void ApplyPosition(bool snap)
        {
            if (_entity == null || _posX == null || _posY == null)
            {
                return;
            }

            var z = _posZ == null
                ? transform.position.z
                : System.Convert.ToSingle(_posZ.GetValue(_entity));
            var target = new Vector3(
                System.Convert.ToSingle(_posX.GetValue(_entity)),
                System.Convert.ToSingle(_posY.GetValue(_entity)),
                z);
            transform.position = snap ? target : Vector3.Lerp(transform.position, target, Time.deltaTime * lerpSpeed);
        }
    }
}
