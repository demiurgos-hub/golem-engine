using UnityEngine;

namespace GolemEngine.Unity.ScribeFixtures
{
    [GolemEntity("ScribeDiscoveryPlayer")]
    public sealed class ScribeDiscoveryPlayer : MonoBehaviour
    {
        [GolemVar(1)] public int Health;
    }
}
