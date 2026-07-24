using System;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a public or <c>[SerializeField]</c> instance field as a scalar catalog field.
    /// Custom-type tags are direct protobuf field numbers and do not use the entity tag offset.
    /// </summary>
    [AttributeUsage(AttributeTargets.Field, Inherited = false, AllowMultiple = false)]
    public sealed class GolemFieldAttribute : Attribute
    {
        /// <summary>Mandatory protobuf field number (1-based). Proto field equals the tag directly.</summary>
        public int Tag { get; }

        /// <summary>Creates a catalog field attribute with a direct custom-type tag.</summary>
        public GolemFieldAttribute(int tag)
        {
            Tag = tag;
        }
    }
}
