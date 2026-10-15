using UnityEngine;

namespace GolemEngine.Unity.Tests
{
    public sealed class CustomProbeView : MonoBehaviour
    {
        public int BindCalls;
        private void Awake() { SpawnProbeLog.Events.Add("awake"); }
        private void OnEnable() { SpawnProbeLog.Events.Add("enabled"); }
        public void Bind(object entity)
        {
            BindCalls++;
            SpawnProbeLog.Events.Add("custom");
            SpawnProbeLog.Bound?.Invoke(this, entity);
        }
    }
}
