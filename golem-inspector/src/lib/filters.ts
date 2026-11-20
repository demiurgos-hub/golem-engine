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
