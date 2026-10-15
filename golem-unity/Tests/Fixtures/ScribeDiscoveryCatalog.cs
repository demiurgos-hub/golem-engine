using UnityEngine;

namespace GolemEngine.Unity.ScribeFixtures
{
    [GolemCatalog("Id")]
    public sealed class ScribeDiscoveryCatalog : ScriptableObject
    {
        [GolemField(1)] public string Id;
        [GolemField(2)] public int Health;
    }
}
