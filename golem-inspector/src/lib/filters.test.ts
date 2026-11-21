import { describe, expect, it } from 'vitest';
import { EntityFields } from '../protocol/fields';
import type { EntityRecord } from '../store/entityStore';
import {
  effectiveMapSpace,
  EMPTY_FILTERS,
  filterEntities,
  mapEntities,
  mapSpaces,
  pruneFilterSpaces,
  spaceFilterOptions,
  spacesOf,
  toggleFilterSpace,
} from './filters';

function record(id: number, typeName: string, space: number): EntityRecord {
  return { id, typeTag: 1, typeName, space, position: { x: 0, y: 0, z: 0 }, fields: EntityFields.parse(1, new Uint8Array()) };
}

const entities = [record(3, 'Crate', 2), record(1, 'Actor', 0), record(2, 'Actor', 2), record(4, 'Actor', 5)];

describe('filters', () => {
  it('shows every space when no space is selected', () => {
    expect(filterEntities(entities, EMPTY_FILTERS).map((e) => e.id)).toEqual([1, 2, 4, 3]);
  });

  it('filters by space alongside type and id', () => {
    expect(filterEntities(entities, { ...EMPTY_FILTERS, spaces: [2] }).map((e) => e.id)).toEqual([2, 3]);
    expect(filterEntities(entities, { ...EMPTY_FILTERS, spaces: [0, 5] }).map((e) => e.id)).toEqual([1, 4]);
    expect(filterEntities(entities, { ...EMPTY_FILTERS, spaces: [2], typeNames: ['Actor'] }).map((e) => e.id)).toEqual([2]);
    expect(filterEntities(entities, { ...EMPTY_FILTERS, spaces: [9] })).toEqual([]);
  });

  it('lists distinct spaces in ascending order', () => {
    expect(spacesOf(entities)).toEqual([0, 2, 5]);
    expect(spacesOf([])).toEqual([]);
  });

  it('defaults the map to the lowest space and keeps valid selections', () => {
    expect(effectiveMapSpace(null, [2, 5])).toBe(2);
    expect(effectiveMapSpace(5, [2, 5])).toBe(5);
    expect(effectiveMapSpace(7, [2, 5])).toBe(2);
    expect(effectiveMapSpace('all', [2, 5])).toBe('all');
    expect(effectiveMapSpace(null, [0])).toBe('all');
    expect(effectiveMapSpace(3, [])).toBe('all');
  });

  it('keeps the map inside a single-space filter', () => {
    const f = { ...EMPTY_FILTERS, spaces: [2] };
    const listed = filterEntities(entities, f);
    const options = mapSpaces(spacesOf(entities), f.spaces);
    expect(options).toEqual([2]);
    // No user choice, or a choice the filter excludes, still shows the filtered space.
    for (const chosen of [null, 0, 5, 'all'] as const) {
      const space = effectiveMapSpace(chosen, options);
      expect(mapEntities(listed, space)).toEqual(listed);
    }
    expect(mapEntities(listed, effectiveMapSpace(null, options)).map((e) => e.id)).toEqual([2, 3]);
  });

  it('restricts map space options to a multi-space filter', () => {
    const f = { ...EMPTY_FILTERS, spaces: [0, 5, 9] };
    const listed = filterEntities(entities, f);
    const options = mapSpaces(spacesOf(entities), f.spaces);
    expect(options).toEqual([0, 5]);
    expect(effectiveMapSpace(null, options)).toBe(0);
    expect(effectiveMapSpace(2, options)).toBe(0);
    expect(mapEntities(listed, effectiveMapSpace(5, options)).map((e) => e.id)).toEqual([4]);
    expect(mapEntities(listed, effectiveMapSpace('all', options)).map((e) => e.id)).toEqual([1, 4]);
  });

  it('offers every space when the space filter is empty', () => {
    const options = mapSpaces(spacesOf(entities), EMPTY_FILTERS.spaces);
    expect(options).toEqual([0, 2, 5]);
    expect(effectiveMapSpace(null, options)).toBe(0);
    expect(mapEntities(entities, 2).map((e) => e.id)).toEqual([3, 2]);
    expect(mapEntities(entities, 'all')).toEqual(entities);
  });

  it('drops absent spaces from the applied filter without touching the stored one', () => {
    const stored = { ...EMPTY_FILTERS, spaces: [2, 9] };
    expect(pruneFilterSpaces(stored, [0, 2, 5])).toEqual({ ...EMPTY_FILTERS, spaces: [2] });
    expect(stored.spaces).toEqual([2, 9]);
    const unchanged = { ...EMPTY_FILTERS, spaces: [0, 5] };
    expect(pruneFilterSpaces(unchanged, [0, 2, 5])).toBe(unchanged);
    expect(pruneFilterSpaces(EMPTY_FILTERS, [])).toBe(EMPTY_FILTERS);
  });

  it('keeps the list, map and filter bar valid when the selected space empties', () => {
    const stored = { ...EMPTY_FILTERS, spaces: [5] };
    // Entity 4, the only one in space 5, is removed.
    const remaining = entities.filter((e) => e.id !== 4);
    const spaces = spacesOf(remaining);
    const effective = pruneFilterSpaces(stored, spaces);
    expect(effective.spaces).toEqual([]);
    const listed = filterEntities(remaining, effective);
    expect(listed.map((e) => e.id)).toEqual([1, 2, 3]);
    expect(spaceFilterOptions(spaces)).toEqual([0, 2]);
    const options = mapSpaces(spaces, effective.spaces);
    expect(mapEntities(listed, effectiveMapSpace(null, options)).map((e) => e.id)).toEqual([1]);
    expect(mapEntities(listed, effectiveMapSpace('all', options))).toEqual(listed);
    // A single remaining space hides the space toggles altogether.
    const lone = entities.filter((e) => e.space === 2);
    expect(spaceFilterOptions(spacesOf(lone))).toEqual([]);
    expect(filterEntities(lone, pruneFilterSpaces(stored, spacesOf(lone))).map((e) => e.id)).toEqual([2, 3]);
  });

  it('reapplies a stored space filter after a brief empty entity set', () => {
    const stored = { ...EMPTY_FILTERS, spaces: [2] };
    // During a resync the store briefly holds no entities.
    expect(filterEntities([], pruneFilterSpaces(stored, spacesOf([])))).toEqual([]);
    expect(filterEntities(entities, pruneFilterSpaces(stored, spacesOf(entities))).map((e) => e.id)).toEqual([2, 3]);
  });

  it('shows a new target unfiltered after the reset to empty filters', () => {
    // Another target's types and spaces: type names would hide every entity
    // even after pruning, which is why switching targets resets all filters.
    const previous = { ...EMPTY_FILTERS, typeNames: ['Ship'], spaces: [7] };
    const next = [record(10, 'Actor', 0), record(11, 'Crate', 1)];
    expect(filterEntities(next, pruneFilterSpaces(previous, spacesOf(next)))).toEqual([]);
    const listed = filterEntities(next, pruneFilterSpaces(EMPTY_FILTERS, spacesOf(next)));
    expect(listed.map((e) => e.id)).toEqual([10, 11]);
    expect(mapEntities(listed, effectiveMapSpace(null, mapSpaces(spacesOf(next), EMPTY_FILTERS.spaces))).map((e) => e.id)).toEqual([10]);
  });

  it('toggles spaces relative to the applied filter', () => {
    const stored = { ...EMPTY_FILTERS, query: 'a', spaces: [2, 9] };
    const effective = pruneFilterSpaces(stored, [0, 2, 5]);
    expect(toggleFilterSpace(stored, effective, 5)).toEqual({ ...stored, spaces: [2, 5] });
    expect(toggleFilterSpace(stored, effective, 2)).toEqual({ ...stored, spaces: [] });
  });
});
