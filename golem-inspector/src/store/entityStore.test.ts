import { describe, expect, it } from 'vitest';
import { Writer } from '../protocol/codec';
import { ObserveState, type Message, type Schema } from '../protocol/messages';
import { createEntityStore } from './entityStore';

const schema: Schema = {
  fingerprint: 'f',
  dimensions: 2,
  removedTag: 3,
  types: [],
  entities: [
    {
      name: 'Actor',
      stateTag: 1,
      deltaTag: 2,
      global: false,
      is3d: false,
      fields: [
        { name: 'entity_id', tag: 1, bit: -1, sync: '', ownerOnly: false, kind: 0, type: 'int64', keyType: '', role: 'identity' },
        { name: 'pos_x', tag: 2, bit: 0, sync: '', ownerOnly: false, kind: 0, type: 'float', keyType: '', role: 'position' },
        { name: 'pos_y', tag: 3, bit: 1, sync: '', ownerOnly: false, kind: 0, type: 'float', keyType: '', role: 'position' },
        { name: 'hp', tag: 4, bit: 2, sync: 'tick', ownerOnly: false, kind: 0, type: 'int32', keyType: '', role: 'variable' },
      ],
    },
  ],
};

function f32(v: number): Uint8Array {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setFloat32(0, v, true);
  return b;
}

function state(id: number, x: number, hp: number): Uint8Array {
  const w = new Writer().tag(1, 0).varint(id).tag(2, 5);
  const bytes = [...w.finish(), ...f32(x), ...new Writer().tag(4, 0).varint(hp).finish()];
  return Uint8Array.from(bytes);
}

function xDelta(id: number, x: number): Uint8Array {
  return Uint8Array.from([...new Writer().tag(1, 0).varint(id).tag(2, 5).finish(), ...f32(x)]);
}

const T = 'cave-1';

function snapshot(epoch: number, beginSeq: number, ids: number[]): Message[] {
  return [
    { type: 'targetSchema', targetId: T, schema },
    { type: 'snapshotBegin', targetId: T, epoch, seq: beginSeq, tick: 10, entityCount: ids.length },
    { type: 'snapshotChunk', targetId: T, epoch, seq: beginSeq + 1, entities: ids.map((id) => ({ id, typeTag: 1, state: state(id, id, 5) })) },
    { type: 'snapshotEnd', targetId: T, epoch, seq: beginSeq + 2 },
  ];
}

function storeFor(target = T) {
  const store = createEntityStore();
  store.setTarget(target);
  return store;
}

describe('entityStore', () => {
  it('commits snapshots atomically', () => {
    const store = storeFor();
    const [schemaMsg, begin, chunk, end] = snapshot(1, 1, [1, 2]);
    store.apply(schemaMsg!);
    store.apply(begin!);
    store.apply(chunk!);
    expect(store.getState().entities.size).toBe(0);
    expect(store.getState().snapshotProgress).toEqual({ expectedCount: 2, receivedCount: 2 });
    store.apply(end!);
    const s = store.getState();
    expect(s.ready).toBe(true);
    expect(s.entities.get(2)?.position).toEqual({ x: 2, y: 0, z: 0 });
    expect(s.entities.get(1)?.typeName).toBe('Actor');
  });

  it('ignores frames for other targets', () => {
    const store = storeFor();
    snapshot(1, 1, [1]).forEach((m) => store.apply({ ...m, targetId: 'other' } as Message));
    expect(store.getState().ready).toBe(false);
    expect(store.getState().schema).toBeNull();
  });

  it('applies batches and replaces records immutably', () => {
    const store = storeFor();
    snapshot(1, 1, [1, 2]).forEach(store.apply);
    const before = store.getState().entities.get(1);
    store.apply({
      type: 'batch',
      targetId: T,
      epoch: 1,
      seq: 4,
      tick: 11,
      spawns: [{ id: 3, typeTag: 1, state: state(3, 9, 1) }],
      deltas: [{ id: 1, typeTag: 1, revision: 1, mask: 1n, delta: xDelta(1, 7.5) }],
      removals: [{ id: 2, revision: 2 }],
    });
    const s = store.getState();
    expect([...s.entities.keys()].sort()).toEqual([1, 3]);
    expect(s.entities.get(1)?.position.x).toBe(7.5);
    expect(s.entities.get(1)).not.toBe(before);
    expect(before?.position.x).toBe(1);
    expect(s.tick).toBe(11);
  });

  it('resyncs on a sequence gap, a batch during a snapshot, or an unknown delta', () => {
    let store = storeFor();
    snapshot(1, 1, [1]).forEach(store.apply);
    store.apply({ type: 'batch', targetId: T, epoch: 1, seq: 9, tick: 0, spawns: [], deltas: [], removals: [] });
    expect(store.getState().needsResync).toBe(true);
    expect(store.getState().resyncReason).toMatch(/seq_gap expected=4 got=9/);
    expect(store.getState().entities.size).toBe(0);

    store = storeFor();
    const [s0, begin] = snapshot(1, 1, [1]);
    store.apply(s0!);
    store.apply(begin!);
    store.apply({ type: 'batch', targetId: T, epoch: 1, seq: 2, tick: 0, spawns: [], deltas: [], removals: [] });
    expect(store.getState().resyncReason).toBe('batch_during_snapshot');

    store = storeFor();
    snapshot(1, 1, [1]).forEach(store.apply);
    store.apply({ type: 'batch', targetId: T, epoch: 1, seq: 4, tick: 0, spawns: [], deltas: [{ id: 77, typeTag: 1, revision: 0, mask: 1n, delta: xDelta(77, 1) }], removals: [] });
    expect(store.getState().resyncReason).toMatch(/unknown_entity 77/);
  });

  it('treats a new snapshot as a fresh baseline even when seq rewinds', () => {
    const store = storeFor();
    snapshot(1, 10, [1]).forEach(store.apply);
    snapshot(2, 3, [5]).forEach(store.apply);
    const s = store.getState();
    expect(s.needsResync).toBe(false);
    expect([...s.entities.keys()]).toEqual([5]);
    expect(s.epoch).toBe(2);
  });

  it('clears on resyncRequired and a hub snapshot clears the flag', () => {
    const store = storeFor();
    snapshot(1, 1, [1]).forEach(store.apply);
    store.apply({ type: 'resyncRequired', targetId: T, epoch: 1, reason: 'agent_replaced' });
    expect(store.getState().needsResync).toBe(true);
    expect(store.getState().entities.size).toBe(0);
    snapshot(2, 4, [1]).forEach(store.apply);
    expect(store.getState().needsResync).toBe(false);
  });

  it('does not latch a resync across a disconnect', () => {
    const store = storeFor();
    snapshot(1, 1, [1]).forEach(store.apply);
    store.clear('disconnected');
    expect(store.getState().needsResync).toBe(false);
    expect(store.getState().ready).toBe(false);
  });

  it('tracks world status and clears stale target errors', () => {
    const store = storeFor();
    store.apply({ type: 'error', code: 'target_not_found', message: '', targetId: T });
    expect(store.getState().lastError?.code).toBe('target_not_found');
    store.apply({ type: 'worldStatus', targetId: T, epoch: 3, entityCount: 4, ready: false, agentConnected: true, observeState: ObserveState.Starting, tick: 99 });
    const s = store.getState();
    expect(s.lastError).toBeNull();
    expect(s.agentConnected).toBe(true);
    expect(s.observeState).toBe(ObserveState.Starting);
    expect(s.statusEntityCount).toBe(4);
  });

  it('resets everything when the target changes', () => {
    const store = storeFor();
    snapshot(1, 1, [1]).forEach(store.apply);
    store.setTarget('cave-2');
    const s = store.getState();
    expect(s.targetId).toBe('cave-2');
    expect(s.entities.size).toBe(0);
    expect(s.schema).toBeNull();
    expect(s.hasSeq).toBe(false);
  });
});
