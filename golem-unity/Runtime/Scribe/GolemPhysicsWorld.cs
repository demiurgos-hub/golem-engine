using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Marks a saved hierarchy for portable server physics export; it performs no simulation.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Physics World")]
    public sealed class GolemPhysicsWorld : MonoBehaviour
    {
        public string WorldID = "world";
        public string OutputPath = "physics/world.source.json";
        [Min(1)] public int Revision = 1;
    }
}
