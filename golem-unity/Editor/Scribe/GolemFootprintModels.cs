using System.Collections.Generic;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One root-local collision primitive ready for footprints.golem.yaml emission.</summary>
    public sealed class GolemFootprintShapeModel
    {
        public string Type;
        public float R;
        public float W;
        public float H;
        public float D;
        public float OffsetX;
        public float OffsetY;
        public float OffsetZ;
        public bool Trigger;
        public string Layer;
    }

    /// <summary>One prefab footprint keyed by Unity asset GUID.</summary>
    public sealed class GolemFootprintModel
    {
        public string Guid;
        public string Name;
        public string AssetPath;
        public string Alias;
        public readonly List<GolemFootprintShapeModel> Shapes = new List<GolemFootprintShapeModel>();
    }
}
