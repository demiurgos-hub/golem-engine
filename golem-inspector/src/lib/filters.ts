import type { EntityRecord } from '../store/entityStore';

/** Client-side filters; changing them never re-subscribes to the hub. */
export interface ClientFilters {
  query: string;
  entityId: string;
  typeNames: string[];
  /** Spaces to show; empty shows every space. */
  spaces: number[];
}

export const EMPTY_FILTERS: ClientFilters = { query: '', entityId: '', typeNames: [], spaces: [] };

export function entityLabel(e: EntityRecord): string {
  return `${e.typeName} #${e.id}`;
}

export function matchesFilters(e: EntityRecord, f: ClientFilters): boolean {
  const id = f.entityId.trim();
  if (id && !String(e.id).includes(id)) return false;
  if (f.typeNames.length > 0 && !f.typeNames.includes(e.typeName)) return false;
  if (f.spaces.length > 0 && !f.spaces.includes(e.space)) return false;
  const q = f.query.trim().toLowerCase();
  if (q && !`${e.typeName} ${e.id}`.toLowerCase().includes(q)) return false;
  return true;
}

/** Filters and sorts by type name, then id. */
export function filterEntities(entities: Iterable<EntityRecord>, f: ClientFilters): EntityRecord[] {
  const out: EntityRecord[] = [];
  for (const e of entities) if (matchesFilters(e, f)) out.push(e);
  out.sort((a, b) => a.typeName.localeCompare(b.typeName) || a.id - b.id);
  return out;
}

/**
 * Drops space filter entries whose space has no entity, so a space emptied by
 * deletion (or a selection carried over from earlier) never hides every
 * entity. Returns `f` itself when nothing changes. The inspector applies it
 * when reading, never to the stored filter: a space that comes back (for
 * example after a resync's brief empty entity set) is filtered again.
 */
export function pruneFilterSpaces(f: ClientFilters, spaces: readonly number[]): ClientFilters {
  if (f.spaces.length === 0) return f;
  const kept = f.spaces.filter((s) => spaces.includes(s));
  return kept.length === f.spaces.length ? f : { ...f, spaces: kept };
}

/** Space toggles the filter bar offers: the present spaces, only when there are several. */
export function spaceFilterOptions(spaces: readonly number[]): readonly number[] {
  return spaces.length > 1 ? spaces : [];
}

/**
 * Toggles `space` in the space filter the user sees (`effective`, from
 * pruneFilterSpaces) and stores the result, so absent spaces leave the stored
 * filter once the user edits it.
 */
export function toggleFilterSpace(f: ClientFilters, effective: ClientFilters, space: number): ClientFilters {
  const spaces = effective.spaces.includes(space) ? effective.spaces.filter((s) => s !== space) : [...effective.spaces, space];
  return { ...f, spaces };
}

/** Returns the distinct spaces of entities in ascending order. */
export function spacesOf(entities: Iterable<EntityRecord>): number[] {
  const seen = new Set<number>();
  for (const e of entities) seen.add(e.space);
  return [...seen].sort((a, b) => a - b);
}

/** Map selection: one space, or every space drawn together. */
export type MapSpace = number | 'all';

/**
 * Resolves the map's space selection against the spaces present: a selection
 * that is gone (or none yet) falls back to the lowest space; with a single
 * space everything is shown.
 */
export function effectiveMapSpace(selected: MapSpace | null, spaces: readonly number[]): MapSpace {
  if (spaces.length <= 1) return 'all';
  if (selected === 'all') return 'all';
  if (selected !== null && spaces.includes(selected)) return selected;
  return spaces[0]!;
}

/**
 * Spaces the map may show: every space when the space filter is empty,
 * otherwise only the filtered spaces that are present, so the map never
 * defaults to (or offers) a space the filter excludes.
 */
export function mapSpaces(spaces: readonly number[], filterSpaces: readonly number[]): number[] {
  return filterSpaces.length === 0 ? [...spaces] : spaces.filter((s) => filterSpaces.includes(s));
}

/** Entities the map draws for a resolved map space. */
export function mapEntities(entities: EntityRecord[], mapSpace: MapSpace): EntityRecord[] {
  return mapSpace === 'all' ? entities : entities.filter((e) => e.space === mapSpace);
}
