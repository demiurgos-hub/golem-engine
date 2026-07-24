using System;
using System.Collections.Generic;
using UnityEngine;

namespace GolemEngine.Unity
{
    [Serializable]
    public sealed class GolemPrefabEntry
    {
        public string entityType;
        public GameObject prefab;
    }

    /// <summary>Maps generated entity type names to Unity prefabs.</summary>
    [CreateAssetMenu(menuName = "Golem/Prefab Registry")]
    public sealed class GolemPrefabRegistry : ScriptableObject
    {
        [SerializeField] private List<GolemPrefabEntry> entries = new List<GolemPrefabEntry>();

        public GameObject GetPrefab(string entityType)
        {
            foreach (var entry in entries)
            {
                if (entry.entityType == entityType)
                {
                    return entry.prefab;
                }
            }
            return null;
        }

        /// <summary>Inserts or replaces the prefab mapping for an explicit entity name.</summary>
        public void Upsert(string entityType, GameObject prefab)
        {
            if (string.IsNullOrEmpty(entityType))
            {
                throw new ArgumentException("Entity type is required.", nameof(entityType));
            }

            for (var i = 0; i < entries.Count; i++)
            {
                if (entries[i].entityType == entityType)
                {
                    entries[i].prefab = prefab;
                    return;
                }
            }

            entries.Add(new GolemPrefabEntry
            {
                entityType = entityType,
                prefab = prefab
            });
        }

        /// <summary>Removes the prefab mapping for an entity name. Returns true when an entry was removed.</summary>
        public bool Remove(string entityType)
        {
            if (string.IsNullOrEmpty(entityType))
            {
                return false;
            }

            var removed = false;
            for (var i = entries.Count - 1; i >= 0; i--)
            {
                if (entries[i].entityType == entityType)
                {
                    entries.RemoveAt(i);
                    removed = true;
                }
            }
            return removed;
        }

        public IEnumerable<GolemPrefabEntry> Entries => entries;
    }
}
