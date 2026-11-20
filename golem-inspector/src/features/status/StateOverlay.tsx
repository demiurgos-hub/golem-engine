import { useConnectionStore, useEntityStore, useInspector } from '../../hooks/useInspector';

export function StateOverlay() {
  const conn = useConnectionStore();
  const entity = useEntityStore();
  const { client, entityStore, selectedTargetId, selectedTarget, targets, targetsLoading } = useInspector();

  if (conn.status === 'protocol_error') {
    return (
      <div className="overlay overlay--danger">
        <h3>Protocol error</h3>
        <p>{conn.protocolErrorDetail ?? 'The hub sent a frame this UI cannot read.'}</p>
        <button type="button" className="btn btn--accent" onClick={() => client.start()}>
          Reconnect
        </button>
      </div>
    );
  }
  if (conn.status !== 'connected') {
    return (
      <div className="overlay">
        <h3>{conn.status === 'disconnected' ? 'Disconnected from the hub' : conn.status === 'reconnecting' ? 'Reconnecting…' : 'Connecting…'}</h3>
        <p className="muted mono">{conn.url}</p>
        {conn.status === 'disconnected' ? (
          <button type="button" className="btn btn--accent" onClick={() => client.start()}>
            Connect
          </button>
        ) : null}
      </div>
    );
  }
  if (!selectedTargetId) {
    return (
      <div className="overlay overlay--soft">
        <h3>Select a server</h3>
        <p className="muted">
          {targetsLoading
            ? 'Loading the server list…'
            : targets.length > 0
              ? 'Choose a game server from the target picker.'
              : 'No game servers are connected. Start a server with GOLEM_INSPECTOR_HUB_URL and GOLEM_INSPECTOR_TOKEN set.'}
        </p>
      </div>
    );
  }
  if (selectedTarget && !selectedTarget.connected) {
    return (
      <div className="overlay overlay--warn">
        <h3>Server offline</h3>
        <p>The agent disconnected. The target is removed if it does not come back.</p>
      </div>
    );
  }
  if (entity.needsResync) {
    return (
      <div className="overlay overlay--warn">
        <h3>Resyncing…</h3>
        <p>{entity.resyncReason ?? 'Sequence gap or new epoch.'}</p>
        <button type="button" className="btn btn--accent" onClick={() => client.resync()}>
          Request resync
        </button>
      </div>
    );
  }
  if (entity.snapshotProgress) {
    const p = entity.snapshotProgress;
    const pct = p.expectedCount > 0 ? Math.min(100, Math.round((p.receivedCount / p.expectedCount) * 100)) : 0;
    return (
      <div className="overlay overlay--soft">
        <h3>Loading snapshot</h3>
        <p>
          {p.receivedCount} / {p.expectedCount} entities ({pct}%)
        </p>
        <div className="progress">
          <div className="progress-bar" style={{ width: `${pct}%` }} />
        </div>
      </div>
    );
  }
  if (!entity.ready) {
    return (
      <div className="overlay overlay--soft">
        <h3>Starting observation…</h3>
        <p>The server is preparing a full snapshot.</p>
      </div>
    );
  }
  if (entity.entities.size === 0) {
    return (
      <div className="overlay overlay--soft">
        <h3>Empty world</h3>
        <p className="muted">The server is observed but has no entities.</p>
      </div>
    );
  }
  if (entity.lastError) {
    return (
      <div className="overlay-toast">
        <strong>{entity.lastError.code}</strong>
        <span>{entity.lastError.message}</span>
        <button type="button" className="btn" onClick={() => entityStore.clearError()}>
          Dismiss
        </button>
      </div>
    );
  }
  return null;
}
