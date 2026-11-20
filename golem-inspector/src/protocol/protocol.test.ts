import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { WireError } from './codec';
import { decodeEntity, readPosition, readVarintField, type Value } from './decode';
import { EntityFields } from './fields';
import { decodeMessage, encodePing, encodeResync, encodeSubscribe, entityByTag, VersionError, type Message, type Schema } from './messages';

// Golden fixtures written by `go test ./golem/inspector/wire -update`.
const fixtureDir = fileURLToPath(new URL('../../../golem/inspector/wire/testdata/', import.meta.url));

interface FixtureIndex {
  messages: { name: string; file: string; type: string; want: unknown }[];
  merge: { typeTag: number; state: string; delta: string; mask: string; merged: string };
}

const index = JSON.parse(readFileSync(fixtureDir + 'fixtures.json', 'utf8')) as FixtureIndex;
const load = (file: string) => new Uint8Array(readFileSync(fixtureDir + file));
const b64 = (s: string) => new Uint8Array(Buffer.from(s, 'base64'));

/** Converts decoded values to Go's JSON conventions and drops empty values. */
function normalize(v: unknown): unknown {
  if (v instanceof Uint8Array) return v.byteLength ? Buffer.from(v).toString('base64') : undefined;
  if (typeof v === 'bigint') return v === 0n ? undefined : Number(v);
  if (Array.isArray(v)) {
    const out = v.map(normalize);
    return out.length ? out : undefined;
  }
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v)) {
      const n = normalize(val);
      if (n !== undefined) out[k] = n;
    }
    return Object.keys(out).length ? out : undefined;
  }
  if (v === 0 || v === '' || v === false || v === null) return undefined;
  return v;
}

function lowerFirst(s: string): string {
  return s.charAt(0).toLowerCase() + s.slice(1);
}

describe('golden fixtures', () => {
  it.each(index.messages.map((m) => [m.name, m] as const))('decodes %s like Go', (_name, fixture) => {
    const msg = decodeMessage(load(fixture.file));
    expect(msg).not.toBeNull();
    const { type, ...body } = msg as Message;
    expect(type).toBe(lowerFirst(fixture.type));
    expect(normalize(body)).toEqual(normalize(fixture.want));
  });

  it('encodes browser messages byte-for-byte like Go', () => {
    expect(encodeSubscribe('cave-7')).toEqual(load('subscribe.bin'));
    expect(encodeResync('cave-7')).toEqual(load('resync.bin'));
    expect(encodePing(1700000000000)).toEqual(load('ping.bin'));
  });

  it('decodes entity spaces and space moves', () => {
    const chunk = decodeMessage(load('snapshot_chunk_spaces.bin'));
    if (chunk?.type !== 'snapshotChunk') throw new Error('chunk fixture');
    expect(chunk.entities.map((e) => [e.id, e.space])).toEqual([
      [42, 0],
      [43, 3],
    ]);
    const batch = decodeMessage(load('batch_spaces.bin'));
    if (batch?.type !== 'batch') throw new Error('batch fixture');
    expect(batch.spawns.map((e) => [e.id, e.space])).toEqual([[44, 2]]);
    expect(batch.moves).toEqual([
      { id: 42, space: 3 },
      { id: 43, space: 0 },
    ]);
    const plain = decodeMessage(load('batch.bin'));
    if (plain?.type !== 'batch') throw new Error('batch fixture');
    expect(plain.moves).toEqual([]);
    expect(plain.spawns[0]?.space).toBe(0);
  });

  it('merges deltas exactly like the Go hub', () => {
    const { merge } = index;
    const reg = decodeMessage(load('register.bin'));
    if (reg?.type !== 'register' || !reg.schema) throw new Error('register fixture');
    const entity = entityByTag(reg.schema, merge.typeTag);
    const merged = EntityFields.parse(merge.typeTag, b64(merge.state)).withDelta(entity, BigInt(merge.mask), b64(merge.delta));
    expect(merged.encode()).toEqual(b64(merge.merged));
  });
});

describe('decode', () => {
  const reg = decodeMessage(load('register.bin'));
  if (reg?.type !== 'register' || !reg.schema) throw new Error('register fixture');
  const schema: Schema = reg.schema;
  const chunk = decodeMessage(load('snapshot_chunk.bin'));
  if (chunk?.type !== 'snapshotChunk') throw new Error('chunk fixture');
  const state = chunk.entities[0]!;
  const actor = entityByTag(schema, state.typeTag)!;
  const fields = EntityFields.parse(state.typeTag, state.state);

  const byName = (decoded: ReturnType<typeof decodeEntity>, name: string): Value => {
    const f = decoded.fields.find((d) => d.field.name === name);
    if (!f) throw new Error(`no field ${name}`);
    return f.value;
  };

  it('decodes every field kind from a snapshot state', () => {
    const decoded = decodeEntity(schema, actor, fields);
    expect(byName(decoded, 'name')).toEqual({ kind: 'scalar', type: 'string', value: 'Ada' });
    expect(byName(decoded, 'health')).toEqual({ kind: 'scalar', type: 'int32', value: -7 });
    expect(byName(decoded, 'gold')).toEqual({ kind: 'scalar', type: 'uint32', value: 99 });
    expect(byName(decoded, 'tags')).toEqual({
      kind: 'list',
      type: 'string',
      items: [
        { kind: 'scalar', type: 'string', value: 'red' },
        { kind: 'scalar', type: 'string', value: '' },
      ],
    });
    expect(byName(decoded, 'stats')).toEqual({
      kind: 'map',
      keyType: 'string',
      type: 'int32',
      entries: [{ key: { kind: 'scalar', type: 'string', value: 'str' }, value: { kind: 'scalar', type: 'int32', value: 12 } }],
    });
    const held = byName(decoded, 'held');
    expect(held.kind).toBe('message');
    if (held.kind === 'message') {
      expect(held.fields.map((f) => [f.field.name, f.value])).toEqual([
        ['name', { kind: 'scalar', type: 'string', value: 'sword' }],
        ['qty', { kind: 'scalar', type: 'int32', value: 1 }],
      ]);
    }
    expect(readPosition(actor, fields)).toEqual({ x: 1.5, y: -2, z: 10.25 });
    expect(readVarintField(fields, 1000)).toBe(3n);
    expect(decoded.unknown).toEqual([]);
  });

  it('shows cleared collections and explicit zeros after a merge', () => {
    const { merge } = index;
    const merged = fields.withDelta(actor, BigInt(merge.mask), b64(merge.delta));
    const decoded = decodeEntity(schema, actor, merged);
    expect(byName(decoded, 'health')).toEqual({ kind: 'scalar', type: 'int32', value: 0 });
    expect(byName(decoded, 'tags')).toEqual({ kind: 'list', type: 'string', items: [] });
    const stats = decoded.fields.find((d) => d.field.name === 'stats')!;
    expect(stats.present).toBe(false);
    expect(readPosition(actor, merged).x).toBe(4);
  });
});

describe('errors', () => {
  it('rejects malformed and foreign-version envelopes', () => {
    expect(() => decodeMessage(new Uint8Array())).toThrow(WireError);
    expect(() => decodeMessage(Uint8Array.of(0x08, 0x01, 0x62, 0x7f))).toThrow(WireError);
    expect(() => decodeMessage(Uint8Array.of(0x08, 0x02, 0x82, 0x01, 0x00))).toThrow(VersionError);
  });

  it('ignores unknown bodies from newer hubs', () => {
    expect(decodeMessage(Uint8Array.of(0x08, 0x01, 0xfa, 0x06, 0x00))).toBeNull();
  });
});
