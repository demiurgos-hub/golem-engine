using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Explicit marker that a prefab should export collision footprints via Golem Scribe.
    /// Entity status alone does not export colliders; every footprint prefab needs exactly one
    /// <see cref="GolemFootprint"/> on the prefab root (nested/inactive child markers are rejected).
    /// </summary>
    [DisallowMultipleComponent]
    public sealed class GolemFootprint : MonoBehaviour
    {
        [Tooltip("Optional unique lookup alias for handwritten Go. Prefab name/path need not be unique.")]
        [SerializeField] private string alias;

        [Tooltip("Only enabled colliders on these Unity layers are exported (inactive children included; disabled components skipped). Unsupported colliders inside the mask fail export.")]
        [SerializeField] private LayerMask includedLayers = ~0;

        /// <summary>Optional unique alias; empty when unset.</summary>
        public string Alias
        {
            get => alias ?? string.Empty;
            set => alias = value;
        }

        /// <summary>Unity layers included in footprint export.</summary>
        public LayerMask IncludedLayers
        {
            get => includedLayers;
            set => includedLayers = value;
        }
    }
}
