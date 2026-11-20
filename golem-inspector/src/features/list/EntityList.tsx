import { useEffect, useRef } from 'react';
import { useInspector } from '../../hooks/useInspector';
import { typeColor } from '../../lib/entityTypes';

// Rendering thousands of rows on every batch is the list's main cost; cap it
// and let filters narrow the rest.
const MAX_ROWS = 1000;

export function EntityList() {
  const { filteredEntities, selectedId, selectEntity, plane } = useInspector();
  const selectedRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: 'nearest' });
  }, [selectedId]);

  const rows = filteredEntities.length > MAX_ROWS ? filteredEntities.slice(0, MAX_ROWS) : filteredEntities;

  return (
    <aside className="panel list-panel">
      <header className="panel-header">
        <h2>Entities</h2>
        <span className="badge">{filteredEntities.length}</span>
      </header>
      <div className="panel-body list-scroll">
        {filteredEntities.length === 0 ? (
          <p className="muted empty-state">No entities match the filters.</p>
        ) : (
          <ul className="entity-list">
            {rows.map((e) => {
              const selected = e.id === selectedId;
              const b = plane === 'xz' ? e.position.z : e.position.y;
              return (
                <li key={e.id}>
                  <button
                    type="button"
                    ref={selected ? selectedRef : undefined}
                    className={`entity-row${selected ? ' entity-row--selected' : ''}`}
                    onClick={() => selectEntity(e.id)}
                  >
                    <span className="entity-dot" style={{ background: typeColor(e.typeName) }} aria-hidden />
                    <span className="entity-row-text">
                      <span className="entity-label">
                        {e.typeName} <span className="mono muted">#{e.id}</span>
                      </span>
                      <span className="entity-meta">
                        <span className="mono">
                          {e.position.x.toFixed(1)}, {b.toFixed(1)}
                        </span>
                      </span>
                    </span>
                  </button>
                </li>
              );
            })}
            {rows.length < filteredEntities.length ? (
              <li className="muted empty-state">
                Showing {rows.length} of {filteredEntities.length}; narrow the filters to see the rest.
              </li>
            ) : null}
          </ul>
        )}
      </div>
    </aside>
  );
}
