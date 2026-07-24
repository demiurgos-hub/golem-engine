namespace GolemEngine.Unity
{
    /// <summary>Replication cadence for a <see cref="GolemVarAttribute"/> field.</summary>
    public enum GolemSync
    {
        /// <summary>Replicated every tick while dirty.</summary>
        Tick = 0,

        /// <summary>Replicated once when the entity spawns or the value is first set.</summary>
        Once = 1
    }
}
