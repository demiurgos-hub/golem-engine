using System;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a MonoBehaviour class as a Golem entity schema source for Golem Scribe.
    /// The explicit name is the YAML entity, generated Synced type suffix, and prefab registry key.
    /// </summary>
    [AttributeUsage(AttributeTargets.Class, Inherited = false, AllowMultiple = false)]
    public sealed class GolemEntityAttribute : Attribute
    {
        /// <summary>Stable PascalCase entity name (for example, "Player").</summary>
        public string Name { get; }

        /// <summary>When true, the entity is always replicated to every client.</summary>
        public bool Global { get; set; }

        /// <summary>When false, the entity type is omitted from world snapshots. Defaults to true.</summary>
        public bool Persistent { get; set; } = true;

        /// <summary>Creates a Golem entity attribute with an explicit stable entity name.</summary>
        public GolemEntityAttribute(string name)
        {
            Name = name;
        }
    }
}
