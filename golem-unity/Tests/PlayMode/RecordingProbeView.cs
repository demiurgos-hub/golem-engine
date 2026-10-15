namespace GolemEngine.Unity.Tests
{
    public sealed class RecordingProbeView : LifecycleProbeView
    {
        public int SpawnCalls;
        protected override void OnSpawned(SyncedLifecycleProbe entity)
        {
            SpawnCalls++;
            SpawnProbeLog.Events.Add("spawned");
            SpawnProbeLog.Spawned?.Invoke(this, entity);
        }
    }
}
