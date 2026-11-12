using System;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Authored physics owner binding and collider-local game metadata, without runtime policy.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Physics Metadata")]
    public sealed class GolemPhysicsMetadata : MonoBehaviour
    {
        [Tooltip("Optional stable content owner, inherited by descendants. The server binds this to its entity identity.")]
        public string OwnerID = "";
        [Tooltip("Overrides the roles of colliders on this GameObject only. Metadata never makes a parent collider a child blocker.")]
        public bool OverrideRoles;
        public bool Solid = true;
        public bool Sensor;
        public bool Query = true;
        public GolemPhysicsProperty[] Properties = Array.Empty<GolemPhysicsProperty>();
    }

    /// <summary>An opaque collider-local property interpreted by the consumer.</summary>
    [Serializable]
    public struct GolemPhysicsProperty
    {
        public string Key;
        public string Value;
    }
}
