using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Marks a prefab root for Golem Scribe Object Template export. The template holds the prefab's
    /// colliders and navigation sources in root-local space; the server places copies of it at runtime.
    /// </summary>
    [DisallowMultipleComponent]
    public sealed class GolemObjectTemplate : MonoBehaviour
    {
        [Tooltip("Template ID used by server code. Empty uses the prefab asset GUID.")]
        [SerializeField] private string templateID;

        /// <summary>Explicit template ID; empty when the prefab GUID is used.</summary>
        public string TemplateID
        {
            get => templateID ?? string.Empty;
            set => templateID = value;
        }
    }
}
