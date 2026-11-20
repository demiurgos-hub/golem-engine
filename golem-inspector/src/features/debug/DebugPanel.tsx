import { useEffect } from 'react';
import { useDebugLog, useInspector } from '../../hooks/useInspector';

function formatTime(at: number): string {
  const d = new Date(at);
  return `${d.toTimeString().slice(0, 8)}.${String(d.getMilliseconds()).padStart(3, '0')}`;
}

/** Decoded frame log for troubleshooting the binary protocol. */
export function DebugPanel() {
  const { debugLog } = useInspector();
  const { entries } = useDebugLog();

  useEffect(() => {
    debugLog.setEnabled(true);
    return () => debugLog.setEnabled(false);
  }, [debugLog]);

  return (
    <section className="debug-panel" aria-label="Protocol log">
      <header className="debug-header">
        <h2>Protocol log</h2>
        <span className="muted">last {entries.length} frames</span>
        <button type="button" className="btn btn--small" onClick={() => debugLog.clear()}>
          Clear
        </button>
      </header>
      <div className="debug-scroll">
        <table className="debug-table">
          <tbody>
            {[...entries].reverse().map((e) => (
              <tr key={e.id} className={e.direction === 'out' ? 'debug-row--out' : undefined}>
                <td className="mono muted">{formatTime(e.at)}</td>
                <td className="mono">{e.direction === 'in' ? '←' : '→'}</td>
                <td className="mono">{e.type}</td>
                <td className="mono muted">{e.bytes} B</td>
                <td className="mono">{e.summary}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
