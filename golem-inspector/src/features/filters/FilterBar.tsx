import type { CSSProperties } from 'react';
import { useEntityStore, useInspector } from '../../hooks/useInspector';
import { legend } from '../../lib/entityTypes';
import { EMPTY_FILTERS, type ClientFilters } from '../../lib/filters';

export function FilterBar() {
  const { filters, setFilters, client, spaces } = useInspector();
  const { schema } = useEntityStore();
  const types = legend(schema);
  // Space toggles appear only when the world uses several spaces (or a space
  // filter is still active), so single-space games see no extra controls.
  const spaceOptions = [...new Set([...spaces, ...filters.spaces])].sort((a, b) => a - b);

  const patch = (partial: Partial<ClientFilters>) => setFilters({ ...filters, ...partial });
  const toggleType = (name: string) =>
    patch({ typeNames: filters.typeNames.includes(name) ? filters.typeNames.filter((t) => t !== name) : [...filters.typeNames, name] });
  const toggleSpace = (space: number) =>
    patch({ spaces: filters.spaces.includes(space) ? filters.spaces.filter((s) => s !== space) : [...filters.spaces, space] });

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
      {spaceOptions.length > 1 || filters.spaces.length > 0 ? (
        <div className="filter-types" role="group" aria-label="Spaces">
          {spaceOptions.map((space) => {
            const selected = filters.spaces.includes(space);
            const active = filters.spaces.length === 0 || selected;
            return (
              <button
                key={space}
                type="button"
                className={`type-toggle${selected ? ' type-toggle--on' : ''}`}
                style={{ opacity: active ? 1 : 0.35 }}
                aria-pressed={selected}
                onClick={() => toggleSpace(space)}
              >
                Space {space}
              </button>
            );
          })}
        </div>
      ) : null}
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
