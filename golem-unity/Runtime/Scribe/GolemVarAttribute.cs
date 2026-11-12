using System;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a public or <c>[SerializeField]</c> instance field as a Golem synced entity variable.
    /// Serialized C# values are ignored by Scribe v1; only schema metadata is exported.
    /// </summary>
    [AttributeUsage(AttributeTargets.Field, Inherited = false, AllowMultiple = false)]
    public sealed class GolemVarAttribute : Attribute
    {
        /// <summary>Mandatory user-slot tag (1-based). Proto field = tag + dimensions + 1.</summary>
        public int Tag { get; }

        /// <summary>Replication cadence written as lowercase <c>tick</c> or <c>once</c>.</summary>
        public GolemSync Sync { get; }

        /// <summary>Optional physics role: orientation_x/y/z/w, velocity_x/y/z, angular_velocity_x/y/z, or active.</summary>
        public string Physics { get; set; }

        /// <summary>Creates a Golem variable attribute with a user-slot tag and sync mode.</summary>
        public GolemVarAttribute(int tag, GolemSync sync = GolemSync.Tick)
        {
            Tag = tag;
            Sync = sync;
        }
    }
}
