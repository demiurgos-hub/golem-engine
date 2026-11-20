import type { CSSProperties } from 'react';
import { useEntityStore, useInspector } from '../../hooks/useInspector';
import { legend } from '../../lib/entityTypes';
import { EMPTY_FILTERS, type ClientFilters } from '../../lib/filters';

export function FilterBar() {
  const { filters, setFilters, client } = useInspector();
  const { schema } = useEntityStore();
  const types = legend(schema);

  const patch = (partial: Partial<ClientFilters>) => setFilters({ ...filters, ...partial });
  const toggleType = (name: string) =>
    patch({ typeNames: filters.typeNames.includes(name) ? filters.typeNames.filter((t) => t !== name) : [...filters.typeNames, name] });

  return (
    <div className="filter-bar">
      <label className="filter-field">
        <span>ID</span>
        <input type="search" inputMode="numeric" placeholder="Entity ID…" value={filters.entityId} onChange={(e) => patch({ entityId: e.target.value })} />
      </label>
      <label className="filter-field filter-field--grow">
        <span>Search</span>
        <input type="search" placeholder="Type / id…" value={filters.query} onChange={(e) => patch({ query: e.target.value })} />
      </label>
      <div className="filter-types" role="group" aria-label="Entity types">
        {types.map((t) => {
          const selected = filters.typeNames.includes(t.name);
          const active = filters.typeNames.length === 0 || selected;
          return (
            <button
              key={t.name}
              type="button"
              className={`type-toggle${selected ? ' type-toggle--on' : ''}`}
              style={{ '--type-color': t.color, opacity: active ? 1 : 0.35 } as CSSProperties}
              aria-pressed={selected}
              onClick={() => toggleType(t.name)}
            >
              <span className="type-swatch" />
              {t.name}
            </button>
          );
        })}
      </div>
      <div className="filter-actions">
        <button type="button" className="btn" onClick={() => setFilters(EMPTY_FILTERS)}>
          Clear
        </button>
        <button type="button" className="btn btn--accent" onClick={() => client.resync()}>
          Resync
        </button>
      </div>
    </div>
  );
}
