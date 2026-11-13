using System;
using System.Collections.Generic;
using System.Globalization;
using UnityEditor;
using UnityEngine;
using Object = UnityEngine.Object;

namespace GolemEngine.Unity.Editor
{
    [Serializable]
    public sealed class GolemContentRoot
    {
        public Object Source;
        public string Adapter;
    }

    [Serializable]
    public sealed class GolemContentImportedBundle
    {
        public TextAsset Bundle;
        [Tooltip("Expected semantic hash reported by Golem's content loader, not the file-byte hash.")]
        public string Hash;
    }

    [Serializable]
    public sealed class GolemContentEntityBinding
    {
        public string Entity;
        [Tooltip("Existing YAML file relative to the Golem project root. This export never writes it.")]
        public string SchemaFile;
    }

    /// <summary>Selects the complete bounded roots of one Scribe content export; never included by Export All.</summary>
    [CreateAssetMenu(menuName = "Golem/Scribe Content Export Set")]
    public sealed class GolemContentExportSet : ScriptableObject
    {
        public string Namespace = "game";
        [Tooltip("Project-relative output stem; .schema.json and .bundle.json are appended.")]
        public string Output = "content/game";
        public List<GolemContentRoot> Roots = new List<GolemContentRoot>();
        public List<GolemContentImportedBundle> Imports = new List<GolemContentImportedBundle>();
        public List<GolemContentEntityBinding> EntitySchemas = new List<GolemContentEntityBinding>();
    }

    /// <summary>Explicit adapter from a saved Unity root into portable selected fields. It must not mutate assets.</summary>
    public interface IGolemContentAdapter
    {
        void Capture(Object source, GolemContentBuilder builder);
    }

    /// <summary>Explicit Editor adapter registration; no asset discovery or reflection serialization is implied.</summary>
    public static class GolemContentAdapters
    {
        private static readonly Dictionary<string, IGolemContentAdapter> Adapters = new Dictionary<string, IGolemContentAdapter>(StringComparer.Ordinal);

        /// <summary>Registers an adapter under a stable authored key; duplicate registrations fail.</summary>
        public static void Register(string key, IGolemContentAdapter adapter)
        {
            if (string.IsNullOrWhiteSpace(key)) throw new ArgumentException("Adapter key is required.", nameof(key));
            if (adapter == null) throw new ArgumentNullException(nameof(adapter));
            if (Adapters.ContainsKey(key)) throw new InvalidOperationException("Content adapter already registered: " + key);
            Adapters.Add(key, adapter);
        }

        /// <summary>Removes an explicit registration, useful when an Editor extension is unloaded.</summary>
        public static bool Unregister(string key) => key != null && Adapters.Remove(key);

        internal static IGolemContentAdapter Find(string key)
        {
            if (key == null || !Adapters.TryGetValue(key, out var adapter)) throw new InvalidOperationException("Content adapter is not registered: " + key);
            return adapter;
        }
    }

    /// <summary>Collects an adapter's selected schema/data and deferred references without expanding its roots.</summary>
    public sealed class GolemContentBuilder
    {
        private readonly string space;
        private readonly GolemContentSource source;
        internal readonly List<GolemContentType> Types = new List<GolemContentType>();
        internal GolemContentDefinition Captured;

        internal GolemContentBuilder(string space, GolemContentSource source)
        { this.space = space; this.source = source; }

        /// <summary>Declares a portable type. Identical declarations from different adapters are shared.</summary>
        public void Type(GolemContentType type) => Types.Add(type ?? throw new ArgumentNullException(nameof(type)));

        /// <summary>Captures this root's one definition; the ID is namespace/type/authored-key.</summary>
        public void Definition(string type, string key, IDictionary<string, object> values, string factory = null)
        {
            if (Captured != null) throw new InvalidOperationException("Each content root must produce exactly one definition.");
            Captured = new GolemContentDefinition { Id = space + "/" + type + "/" + key, Type = type, Factory = factory, Source = source, Values = values };
        }

        /// <summary>Records a typed reference to another explicitly selected root or supplied import.</summary>
        public GolemContentReference Reference(Object target, string expectedType)
        {
            if (target == null) return null;
            return new GolemContentReference(DescribeSource(target).Key, expectedType);
        }

        internal static GolemContentSource DescribeSource(Object source)
        {
            if (source == null || !EditorUtility.IsPersistent(source) || !AssetDatabase.TryGetGUIDAndLocalFileIdentifier(source, out string guid, out long localId))
                throw new InvalidOperationException("Content roots and references must be saved project assets, including explicitly selected subassets.");
            return new GolemContentSource { Guid = guid.ToLowerInvariant(), LocalId = localId.ToString(CultureInfo.InvariantCulture), Path = AssetDatabase.GetAssetPath(source) };
        }
    }
}
