import type { EntityRecord } from '../store/entityStore';

/** Client-side filters; changing them never re-subscribes to the hub. */
export interface ClientFilters {
  query: string;
  entityId: string;
  typeNames: string[];
}

export const EMPTY_FILTERS: ClientFilters = { query: '', entityId: '', typeNames: [] };

export function entityLabel(e: EntityRecord): string {
  return `${e.typeName} #${e.id}`;
}

export function matchesFilters(e: EntityRecord, f: ClientFilters): boolean {
  const id = f.entityId.trim();
  if (id && !String(e.id).includes(id)) return false;
  if (f.typeNames.length > 0 && !f.typeNames.includes(e.typeName)) return false;
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
