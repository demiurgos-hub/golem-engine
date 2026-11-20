import { describe, expect, it } from 'vitest';
import { EntityFields } from '../protocol/fields';
import type { EntityRecord } from '../store/entityStore';
import { effectiveMapSpace, EMPTY_FILTERS, filterEntities, spacesOf } from './filters';

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
});
