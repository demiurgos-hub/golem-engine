using System;
using System.Collections.Generic;
using UnityEngine;

namespace GolemEngine.Unity
{
    [Serializable]
    public sealed class GolemDefinitionPrefabEntry
    {
        public string entityType;
        public string definitionId;
        public GameObject prefab;
    }

    /// <summary>Client-only presentation mapping; portable server definitions never contain these prefabs.</summary>
    [CreateAssetMenu(menuName = "Golem/Definition Prefab Registry")]
    public sealed class GolemDefinitionPrefabRegistry : ScriptableObject
    {
        [SerializeField] private List<GolemDefinitionPrefabEntry> entries = new List<GolemDefinitionPrefabEntry>();

        /// <summary>Validates the complete selected presentation registry before entity spawning begins.</summary>
        public void Validate()
        {
            var keys = new HashSet<string>(StringComparer.Ordinal);
            foreach (var entry in entries)
            {
                if (entry == null || string.IsNullOrEmpty(entry.entityType) || string.IsNullOrEmpty(entry.definitionId) || entry.prefab == null)
                    throw new InvalidOperationException("Definition presentation entries require entity type, definition ID and prefab.");
                if (!keys.Add(entry.entityType + "\n" + entry.definitionId))
                    throw new InvalidOperationException("Duplicate definition presentation: " + entry.entityType + " / " + entry.definitionId);
            }
        }

        /// <summary>Finds the exact type/definition pair; missing mappings never fall back to another appearance.</summary>
        public GameObject GetPrefab(string entityType, string definitionId)
        {
            GameObject result = null;
            var found = false;
            foreach (var entry in entries)
            {
                if (entry == null || entry.entityType != entityType || entry.definitionId != definitionId) continue;
                if (found) throw new InvalidOperationException("Duplicate definition presentation: " + entityType + " / " + definitionId);
                found = true; result = entry.prefab;
            }
            if (result == null) throw new InvalidOperationException("Missing definition presentation: " + entityType + " / " + definitionId);
            return result;
        }

        /// <summary>Inserts or replaces one explicit client presentation mapping.</summary>
        public void Upsert(string entityType, string definitionId, GameObject prefab)
        {
            if (string.IsNullOrEmpty(entityType) || string.IsNullOrEmpty(definitionId) || prefab == null)
                throw new ArgumentException("Entity type, definition ID and prefab are required.");
            foreach (var entry in entries)
                if (entry.entityType == entityType && entry.definitionId == definitionId) { entry.prefab = prefab; return; }
            entries.Add(new GolemDefinitionPrefabEntry { entityType = entityType, definitionId = definitionId, prefab = prefab });
        }
    }
}
