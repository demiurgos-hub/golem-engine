using System.Collections.Generic;
using System.Text;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Editor helpers for unambiguous <see cref="GolemPrefabRegistry"/> resolution and mutation.</summary>
    public static class GolemPrefabRegistryUtil
    {
        /// <summary>
        /// Finds the single prefab registry asset. Fails when zero or multiple registries exist.
        /// </summary>
        public static bool TryGetUniqueRegistry(out GolemPrefabRegistry registry, out string error)
        {
            registry = null;
            var guids = AssetDatabase.FindAssets("t:GolemPrefabRegistry");
            var found = new List<(string path, GolemPrefabRegistry asset)>();
            foreach (var guid in guids)
            {
                var path = AssetDatabase.GUIDToAssetPath(guid);
                var asset = AssetDatabase.LoadAssetAtPath<GolemPrefabRegistry>(path);
                if (asset != null)
                {
                    found.Add((path, asset));
                }
            }

            return TrySelectUniqueRegistry(found, out registry, out error);
        }

        /// <summary>Selects a unique registry from candidate assets. Exposed for focused editor tests.</summary>
        internal static bool TrySelectUniqueRegistry(
            IReadOnlyList<(string path, GolemPrefabRegistry asset)> found,
            out GolemPrefabRegistry registry,
            out string error)
        {
            registry = null;
            if (found == null || found.Count == 0)
            {
                error = "No GolemPrefabRegistry asset found. Use Golem > Create > Prefab Registry, then re-run Scribe export.";
                return false;
            }

            if (found.Count > 1)
            {
                var builder = new StringBuilder();
                builder.Append("Multiple GolemPrefabRegistry assets found; Scribe requires exactly one. Candidates:");
                foreach (var item in found)
                {
                    builder.Append("\n- ").Append(item.path);
                }
                builder.Append("\nKeep a single registry asset and re-run Scribe export.");
                error = builder.ToString();
                return false;
            }

            registry = found[0].asset;
            error = null;
            return true;
        }

        /// <summary>Upserts entity prefab mappings and removes Scribe entity names that disappeared.</summary>
        public static void ApplyEntityMappings(
            GolemPrefabRegistry registry,
            IReadOnlyDictionary<string, GameObject> desiredByEntity,
            IEnumerable<string> previousScribeEntityNames)
        {
            if (registry == null)
            {
                throw new System.ArgumentNullException(nameof(registry));
            }

            foreach (var pair in desiredByEntity)
            {
                registry.Upsert(pair.Key, pair.Value);
            }

            if (previousScribeEntityNames != null)
            {
                foreach (var entityName in previousScribeEntityNames)
                {
                    if (string.IsNullOrEmpty(entityName))
                    {
                        continue;
                    }

                    if (!desiredByEntity.ContainsKey(entityName))
                    {
                        registry.Remove(entityName);
                    }
                }
            }

            EditorUtility.SetDirty(registry);
            AssetDatabase.SaveAssets();
        }
    }
}
