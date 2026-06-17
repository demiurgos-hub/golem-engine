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

        public IEnumerable<GolemPrefabEntry> Entries => entries;
    }
}
