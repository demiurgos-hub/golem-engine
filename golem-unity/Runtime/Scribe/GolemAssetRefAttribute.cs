using System;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a Unity asset/prefab object reference field as a catalog string that serializes
    /// the asset's 32-hex GUID. The server treats the value as an opaque identifier.
    /// </summary>
    [AttributeUsage(AttributeTargets.Field, Inherited = false, AllowMultiple = false)]
    public sealed class GolemAssetRefAttribute : Attribute
    {
        /// <summary>Mandatory protobuf field number (1-based). Proto field equals the tag directly.</summary>
        public int Tag { get; }

        /// <summary>Creates an asset-reference catalog field with a direct custom-type tag.</summary>
        public GolemAssetRefAttribute(int tag)
        {
            Tag = tag;
        }
    }
}
