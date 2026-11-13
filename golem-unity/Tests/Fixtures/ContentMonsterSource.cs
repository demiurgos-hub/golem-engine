using System;
using UnityEngine;

namespace GolemEngine.Unity.ScribeFixtures
{
    [Serializable]
    public sealed class ContentMonsterStats
    {
        public float Speed = 4.5f;
        public int Armor;
    }

    // Deliberately has no GolemEntity marker: prefab variations share one separately authored schema.
    public sealed class ContentMonsterSource : MonoBehaviour
    {
        public string Key = "scout";
        public int Health = 80;
        public ContentMonsterStats Stats = new ContentMonsterStats();
        public ContentMovementProfileSource Movement;
    }
}
