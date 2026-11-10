using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Opts this object's MeshFilter or Terrain into server navigation export; colliders are not consulted.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Navigation Source")]
    public sealed class GolemNavigationSource : MonoBehaviour
    {
        [Tooltip("Stable server object identity. Empty derives an identity from the saved scene object, distinct from its shared asset.")]
        public string StableID;
        [Min(1)] public int Revision = 1;
        [Tooltip("0 is nonwalkable solid. Areas 1..63 are walkable candidates subject to slope and clearance tests.")]
        [Range(0, 63)] public int Area = 1;
        public bool ExportEnabled = true;
    }
}
