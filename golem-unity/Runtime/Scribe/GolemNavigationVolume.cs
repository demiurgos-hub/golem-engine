using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Paints or excludes server standing space. Boxes may rotate around Y; tilted volumes are rejected.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Navigation Volume")]
    public sealed class GolemNavigationVolume : MonoBehaviour
    {
        public string StableID;
        [Min(1)] public int Revision = 1;
        public NavigationVolumeShape Shape;
        public Vector3 Center;
        public Vector3 Size = Vector3.one;
        [Min(0.001f)] public float Radius = 0.5f;
        [Range(0, 63)] public int Area = 1;
        public bool Exclude;
        public int Priority;
        public bool ExportEnabled = true;
    }

    /// <summary>Authoring shapes; rotated boxes export as convex horizontal prisms.</summary>
    public enum NavigationVolumeShape { Box, Cylinder }
}
