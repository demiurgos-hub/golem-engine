import { useAuth } from '../../hooks/useAuth';
import { useConnectionStore, useEntityStore, useInspector } from '../../hooks/useInspector';
import { ObserveState } from '../../protocol/messages';

const OBSERVE_NAMES: Record<number, string> = { [ObserveState.Idle]: 'idle', [ObserveState.Starting]: 'starting', [ObserveState.Ready]: 'ready' };

function statusLabel(conn: string, entity: ReturnType<typeof useEntityStore>): { text: string; tone: string } {
  if (conn === 'protocol_error') return { text: 'Protocol error', tone: 'danger' };
  if (conn === 'disconnected') return { text: 'Disconnected', tone: 'danger' };
  if (conn === 'connecting') return { text: 'Connecting…', tone: 'warn' };
  if (conn === 'reconnecting') return { text: 'Reconnecting…', tone: 'warn' };
  if (entity.needsResync) return { text: 'Resync required', tone: 'warn' };
  if (entity.snapshotProgress) return { text: 'Snapshot…', tone: 'warn' };
  if (entity.targetId && !entity.agentConnected && entity.statusEntityCount !== null) return { text: 'Server offline', tone: 'warn' };
  if (!entity.ready) return { text: 'Waiting for world', tone: 'muted' };
  return { text: 'Live', tone: 'ok' };
}

export function StatusBar({ debugOpen, onToggleDebug }: { debugOpen: boolean; onToggleDebug: () => void }) {
  const conn = useConnectionStore();
  const entity = useEntityStore();
  const { filteredEntities, selectedTargetId, selectedTarget } = useInspector();
  const { session, logout } = useAuth();
  const label = statusLabel(conn.status, entity);
  const snap = entity.snapshotProgress;

  return (
    <footer className="status-bar">
      <div className="status-group">
        <span className={`status-pill status-pill--${label.tone}`}>{label.text}</span>
        {conn.status === 'reconnecting' ? <span className="muted">attempt {conn.reconnectAttempt}</span> : null}
      </div>
      <div className="status-group">
        <span className="status-item">
          <span className="status-key">Entities</span>
          <span className="mono">
            {filteredEntities.length} / {entity.statusEntityCount ?? entity.entities.size}
          </span>
        </span>
        <span className="status-item">
          <span className="status-key">Rate</span>
          <span className="mono">{conn.updateRate}/s</span>
        </span>
        <span className="status-item">
          <span className="status-key">Target</span>
          <span className="mono">{selectedTargetId ?? '—'}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Observe</span>
          <span className="mono">{OBSERVE_NAMES[entity.observeState] ?? selectedTarget?.observeState ?? '—'}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Viewers</span>
          <span className="mono">{selectedTarget?.viewerCount ?? 0}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Tick</span>
          <span className="mono">{entity.tick || '—'}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Epoch</span>
          <span className="mono">{entity.epoch}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Seq</span>
          <span className="mono">{entity.hasSeq ? entity.seq : '—'}</span>
        </span>
        {snap ? (
          <span className="status-item">
            <span className="status-key">Snapshot</span>
            <span className="mono">
              {snap.receivedCount}/{snap.expectedCount}
            </span>
          </span>
        ) : null}
      </div>
      <div className="status-group">
        <span className="status-item">
          <span className="status-key">Hub</span>
          <span className={conn.status === 'connected' ? 'ok' : 'danger'}>{conn.status === 'connected' ? 'up' : 'down'}</span>
        </span>
        <span className="status-item">
          <span className="status-key">Server</span>
          <span className={entity.agentConnected ? 'ok' : 'warn'}>{entity.agentConnected ? 'up' : 'down'}</span>
        </span>
        {selectedTarget?.engineVersion ? (
          <span className="status-item">
            <span className="status-key">Engine</span>
            <span className="mono">{selectedTarget.engineVersion}</span>
          </span>
        ) : null}
        {conn.latencyMs != null ? (
          <span className="status-item">
            <span className="status-key">RTT</span>
            <span className="mono">{conn.latencyMs}ms</span>
          </span>
        ) : null}
        <button type="button" className={`status-logout${debugOpen ? ' status-logout--on' : ''}`} aria-pressed={debugOpen} onClick={onToggleDebug}>
          Debug
        </button>
        {session && !session.authDisabled ? (
          <button type="button" className="status-logout" onClick={() => void logout()}>
            Log out
          </button>
        ) : null}
      </div>
    </footer>
  );
}
