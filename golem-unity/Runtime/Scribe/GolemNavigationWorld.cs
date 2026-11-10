using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Explicit server navigation export settings. This component has no runtime navigation behavior.</summary>
    [DisallowMultipleComponent, AddComponentMenu("Golem/Scribe/Navigation World")]
    public sealed class GolemNavigationWorld : MonoBehaviour
    {
        public string WorldID = "world";
        public string OutputPath = "navigation/world.source.json";
        public string ProfileID = "human";
        [Min(1)] public int Revision = 1;
        [Min(0)] public float AgentRadius = 0.4f;
        [Min(0.001f)] public float AgentHeight = 1.8f;
        [Range(0, 89.9f)] public float MaximumSlope = 45;
        [Min(0)] public float MaximumClimb = 0.4f;
        [Tooltip("World-space region origin; geometry remains in world coordinates in the source file.")]
        public Vector3 Origin;
        [Tooltip("World-space bake bounds in meters, independent of this object's transform.")]
        public Bounds WorldBounds = new Bounds(Vector3.zero, new Vector3(64, 32, 64));
        [Min(0.001f)] public float CellSize = 0.2f;
        [Min(0.001f)] public float CellHeight = 0.1f;
        [Min(1)] public int TileSize = 64;
        public NavigationPartition Partition = NavigationPartition.Layers;
        public bool BuildHeightfieldLayers;
        [Min(0)] public int MinimumRegionArea;
        [Min(0)] public int MergeRegionArea = 400;
        [Min(0)] public int MaximumEdgeLength = 60;
        [Min(0)] public float MaximumSimplificationError = 1.3f;
        [Min(0)] public float DetailSampleDistance = 1.2f;
        [Min(0)] public float DetailSampleMaximumError = 0.1f;
        [Tooltip("Optional 64 area flag values. Empty uses flag 1 for each walkable area.")]
        public int[] AreaFlags = new int[0];
    }

    /// <summary>Region partition algorithms supported by the pure-Go baker.</summary>
    public enum NavigationPartition { Watershed, Monotone, Layers }
}
