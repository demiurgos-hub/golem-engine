using System;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a <see cref="UnityEngine.ScriptableObject"/> class as a Golem catalog source for Golem Scribe.
    /// Scribe exports a custom type schema, a catalog world schema, and a catalog data file.
    /// <para>
    /// Server boundary: <c>golem-bake</c> generates <c>Load{Name}Data</c> for the world type.
    /// Application code must still call that loader and store/publish the resulting world data
    /// (for example via <c>world.Store.Set</c>); Scribe does not push catalogs to clients.
    /// </para>
    /// <para>
    /// Class-level managed artifacts use a deterministic synthetic source ID derived from the type
    /// full name (not a Unity asset GUID), because many assets share one catalog class.
    /// </para>
    /// </summary>
    [AttributeUsage(AttributeTargets.Class, Inherited = false, AllowMultiple = false)]
    public sealed class GolemCatalogAttribute : Attribute
    {
        /// <summary>C# field name used as the catalog map key (for example, "Id").</summary>
        public string Key { get; }

        /// <summary>Creates a catalog attribute naming the C# key field.</summary>
        public GolemCatalogAttribute(string key)
        {
            Key = key;
        }
    }
}
