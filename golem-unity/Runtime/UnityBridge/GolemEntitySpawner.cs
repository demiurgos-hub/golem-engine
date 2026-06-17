using System;
using System.Collections.Generic;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Creates and removes GameObjects as generated entities spawn and despawn.</summary>
    public sealed class GolemEntitySpawner : MonoBehaviour
    {
        [SerializeField] private GolemPrefabRegistry prefabRegistry;
        private readonly Dictionary<long, GameObject> _instances = new Dictionary<long, GameObject>();

        public void Bind(object entityManager)
        {
            var type = entityManager.GetType();
            type.GetMethod("OnSpawn")?.Invoke(entityManager, new object[] { (Action<object>)Spawn });
            type.GetMethod("OnUpdate")?.Invoke(entityManager, new object[] { (Action<object>)UpdateEntity });
            type.GetMethod("OnRemove")?.Invoke(entityManager, new object[] { (Action<long>)Remove });
        }

        private void Spawn(object entity)
        {
            var id = GetEntityId(entity);
            var prefab = prefabRegistry != null ? prefabRegistry.GetPrefab(EntityTypeName(entity)) : null;
            if (prefab == null)
            {
                return;
            }

            var instance = Instantiate(prefab, transform);
            _instances[id] = instance;
            var binding = instance.GetComponent<GolemTransformBinding>();
            binding?.Bind(entity);
            foreach (var view in instance.GetComponents<MonoBehaviour>())
            {
                var bind = FindCompatibleMethod(view.GetType(), "Bind", entity.GetType());
                bind?.Invoke(view, new[] { entity });
            }
        }

        private void UpdateEntity(object entity)
        {
            var id = GetEntityId(entity);
            if (!_instances.TryGetValue(id, out var instance))
            {
                return;
            }
            foreach (var view in instance.GetComponents<MonoBehaviour>())
            {
                var apply = view.GetType().GetMethod("ApplyUpdate", Type.EmptyTypes);
                apply?.Invoke(view, Array.Empty<object>());
            }
        }

        private void Remove(long entityId)
        {
            if (_instances.TryGetValue(entityId, out var instance))
            {
                _instances.Remove(entityId);
                Destroy(instance);
            }
        }

        private static long GetEntityId(object entity)
        {
            return Convert.ToInt64(entity.GetType().GetProperty("EntityId")?.GetValue(entity));
        }

        private static string EntityTypeName(object entity)
        {
            var name = entity.GetType().Name;
            return name.StartsWith("Synced", StringComparison.Ordinal) ? name.Substring("Synced".Length) : name;
        }

        private static System.Reflection.MethodInfo FindCompatibleMethod(Type type, string name, Type argumentType)
        {
            foreach (var method in type.GetMethods())
            {
                if (method.Name != name)
                {
                    continue;
                }
                var parameters = method.GetParameters();
                if (parameters.Length == 1 && parameters[0].ParameterType.IsAssignableFrom(argumentType))
                {
                    return method;
                }
            }
            return null;
        }
    }
}
