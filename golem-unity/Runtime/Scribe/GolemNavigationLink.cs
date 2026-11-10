using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Authored server traversal metadata. Gameplay decides whether and how to execute an action.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Navigation Link")]
    public sealed class GolemNavigationLink : MonoBehaviour
    {
        public string StableID;
        [Min(1)] public int Revision = 1;
        public Vector3 LocalStart;
        public Vector3 LocalEnd = Vector3.forward;
        [Tooltip("Endpoint search radius in world meters, unaffected by the object's scale.")]
        [Min(0.001f)] public float Radius = 0.5f;
        public bool Bidirectional = true;
        [Range(0, 63)] public int Area = 1;
        [Range(1, 65535)] public int Flags = 1;
        public string[] Profiles = new string[0];
        public string Action;
        public bool ExportEnabled = true;
    }
}
