using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Opts selected mesh, terrain or explicit collider geometry into server navigation export.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Navigation Source")]
    public sealed class GolemNavigationSource : MonoBehaviour
    {
        [Tooltip("Stable server object identity. Empty derives an identity from the saved scene object, distinct from its shared asset.")]
        public string StableID;
        /// <summary>Selects collider-local geometry with its full affine hierarchy; round surfaces use 24 azimuth segments and eight steps per hemisphere.</summary>
        /// <remarks>This navigation approximation is separate from native physics primitive scaling. Physical enabled/trigger state does not override explicit navigation selection.</remarks>
        [Tooltip("Optional explicit Box, Sphere, Capsule or MeshCollider source in the same navigation world. Empty uses this object's MeshFilter or Terrain. Round sources use 24 segments/eight hemisphere steps and the full affine hierarchy; this is separate from native physics primitive scaling.")]
        public Collider SourceCollider;
        [Min(1)] public int Revision = 1;
        [Tooltip("0 is nonwalkable solid. Areas 1..63 are walkable candidates subject to slope and clearance tests.")]
        [Range(0, 63)] public int Area = 1;
        public bool ExportEnabled = true;
    }
}
