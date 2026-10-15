using System;
using System.Collections.Generic;

namespace GolemEngine.Unity.Tests
{
    public sealed class SyncedLifecycleProbe
    {
        public long EntityId { get; set; } = 1;
        public float X = 3;
        public int PositionReads;
        public bool ThrowOnPosition;
        public float PosX
        {
            get
            {
                PositionReads++;
                if (ThrowOnPosition) throw new InvalidOperationException("position");
                return X;
            }
        }
        public float PosY => 4;
        public float PosZ => 5;
    }

    public sealed class SyncedPlaneProbe
    {
        public long EntityId => 1;
        public float PosX => 3;
        public float PosY => 4;
    }

    public static class SpawnProbeLog
    {
        public static readonly List<string> Events = new List<string>();
        public static Action<RecordingProbeView, SyncedLifecycleProbe> Spawned;
        public static Action<CustomProbeView, object> Bound;
    }

    // Uses the generated manager's public callback registration signatures.
    public sealed class SpawnProbeManager
    {
        private Action<object> _spawn;
        public void OnSpawn(Action<object> fn) { _spawn = fn; }
        public void OnUpdate(Action<object> fn) { }
        public void OnRemove(Action<long> fn) { }
        public void Spawn(object entity) { _spawn(entity); }
    }
}
