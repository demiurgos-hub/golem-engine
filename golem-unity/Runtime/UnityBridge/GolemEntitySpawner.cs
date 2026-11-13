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
        private readonly Dictionary<long, string> _definitionIds = new Dictionary<long, string>();
        private readonly List<DefinitionSelector> _definitionSelectors = new List<DefinitionSelector>();

        private sealed class DefinitionSelector
        {
            internal Type Type;
            internal string EntityType;
            internal Func<object, string> Select;
            internal GolemDefinitionPrefabRegistry Registry;
        }

        /// <summary>
        /// Selects client presentation from an explicitly authored definition-ID field. Register before Bind;
        /// initial state is available at spawn, and the selected ID must remain stable for that instance.
        /// </summary>
        public void UseDefinitions<T>(string entityType, Func<T, string> definitionId, GolemDefinitionPrefabRegistry registry) where T : class
        {
            if (string.IsNullOrEmpty(entityType)) throw new ArgumentException("Entity type is required.", nameof(entityType));
            if (definitionId == null) throw new ArgumentNullException(nameof(definitionId));
            if (registry == null) throw new ArgumentNullException(nameof(registry));
            if (_instances.Count != 0) throw new InvalidOperationException("Register definition presentation before spawning entities.");
            registry.Validate();
            foreach (var selector in _definitionSelectors)
                if (selector.Type.IsAssignableFrom(typeof(T)) || typeof(T).IsAssignableFrom(selector.Type))
                    throw new InvalidOperationException("Overlapping definition presentation selectors for " + typeof(T).Name);
            _definitionSelectors.Add(new DefinitionSelector { Type = typeof(T), EntityType = entityType, Select = entity => definitionId((T)entity), Registry = registry });
        }

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
            GameObject prefab;
            string definitionId = null;
            try
            {
                var selector = Selector(entity);
                if (selector == null) prefab = prefabRegistry != null ? prefabRegistry.GetPrefab(EntityTypeName(entity)) : null;
                else
                {
                    definitionId = selector.Select(entity);
                    if (string.IsNullOrEmpty(definitionId)) throw new InvalidOperationException("Missing definition ID for " + selector.EntityType + " instance " + id);
                    prefab = selector.Registry.GetPrefab(selector.EntityType, definitionId);
                }
            }
            catch (Exception ex) { Debug.LogError("Golem presentation: " + ex.Message); return; }
            if (prefab == null)
            {
                return;
            }

            var instance = Instantiate(prefab, transform);
            _instances[id] = instance;
            if (definitionId != null) _definitionIds[id] = definitionId;
            using (GolemTransformBinding.BeginSpawnBindingScope(entity))
            {
                var binding = instance.GetComponent<GolemTransformBinding>();
                binding?.Bind(entity);
                foreach (var view in instance.GetComponents<MonoBehaviour>())
                {
                    if (view is GolemTransformBinding transformBinding)
                    {
                        transformBinding.Bind(entity);
                        continue;
                    }
                    var bind = FindCompatibleMethod(view.GetType(), "Bind", entity.GetType());
                    bind?.Invoke(view, new[] { entity });
                }
            }
        }

        private void UpdateEntity(object entity)
        {
            var id = GetEntityId(entity);
            if (!_instances.TryGetValue(id, out var instance))
            {
                return;
            }
            if (_definitionIds.TryGetValue(id, out var expected))
            {
                var current = Selector(entity).Select(entity);
                if (!string.Equals(current, expected, StringComparison.Ordinal))
                {
                    Debug.LogError("Golem presentation: definition ID changed for live instance " + id + "; recreate the instance explicitly.");
                    return;
                }
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
                _definitionIds.Remove(entityId);
                Destroy(instance);
            }
        }

        private DefinitionSelector Selector(object entity)
        {
            foreach (var selector in _definitionSelectors)
                if (selector.Type.IsInstanceOfType(entity)) return selector;
            return null;
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
