import { readPosition, type Position } from '../protocol/decode';
import { EntityFields } from '../protocol/fields';
import { entityByTag, ObserveState, type EntityState, type Message, type Schema, type SchemaEntity } from '../protocol/messages';

/** One entity as the UI sees it; replaced (never mutated) on every change. */
export interface EntityRecord {
  id: number;
  typeTag: number;
  typeName: string;
  position: Position;
  fields: EntityFields;
}

export interface SnapshotProgress {
  expectedCount: number;
  receivedCount: number;
}

export interface EntityStoreSnapshot {
  targetId: string | null;
  schema: Schema | null;
  entities: ReadonlyMap<number, EntityRecord>;
  epoch: number;
  seq: number;
  hasSeq: boolean;
  tick: number;
  ready: boolean;
  agentConnected: boolean;
  observeState: ObserveState;
  statusEntityCount: number | null;
  needsResync: boolean;
  resyncReason: string | null;
  snapshotProgress: SnapshotProgress | null;
  lastError: { code: string; message: string } | null;
  updateCount: number;
}

export interface EntityStore {
  getState(): EntityStoreSnapshot;
  subscribe(listener: () => void): () => void;
  apply(msg: Message): void;
  /** Drops entities; a reason other than "disconnected" requests a resync. */
  clear(reason?: string): void;
  setTarget(targetId: string | null): void;
  clearError(): void;
}

interface PendingSnapshot {
  epoch: number;
  lastSeq: number;
  tick: number;
  expectedCount: number;
  entities: Map<number, EntityRecord>;
}

export function makeRecord(schema: Schema | null, e: EntityState): EntityRecord {
  const fields = EntityFields.parse(e.typeTag, e.state);
  const entity = entityByTag(schema, e.typeTag);
  return { id: e.id, typeTag: e.typeTag, typeName: entity?.name ?? `type ${e.typeTag}`, position: readPosition(entity, fields), fields };
}

export function createEntityStore(): EntityStore {
  let targetId: string | null = null;
  let schema: Schema | null = null;
  let entities = new Map<number, EntityRecord>();
  let epoch = 0;
  let seq = 0;
  let hasSeq = false;
  let tick = 0;
  let ready = false;
  let agentConnected = false;
  let observeState = ObserveState.Idle;
  let statusEntityCount: number | null = null;
  let needsResync = false;
  let resyncReason: string | null = null;
  let lastError: { code: string; message: string } | null = null;
  let updateCount = 0;
  let pending: PendingSnapshot | null = null;

  const listeners = new Set<() => void>();
  let cached: EntityStoreSnapshot;

  const rebuild = () => {
    cached = {
      targetId,
      schema,
      entities,
      epoch,
      seq,
      hasSeq,
      tick,
      ready,
      agentConnected,
      observeState,
      statusEntityCount,
      needsResync,
      resyncReason,
      snapshotProgress: pending ? { expectedCount: pending.expectedCount, receivedCount: pending.entities.size } : null,
      lastError,
      updateCount,
    };
  };
  rebuild();

  const bump = () => {
    updateCount += 1;
    rebuild();
    for (const l of listeners) l();
  };

  const clearInternal = (reason?: string) => {
    entities = new Map();
    pending = null;
    hasSeq = false;
    seq = 0;
    ready = false;
    // A transport close must not latch needsResync, or the next open would
    // resync on top of its own subscribe.
    if (reason && reason !== 'disconnected') {
      needsResync = true;
      resyncReason = reason;
    } else {
      needsResync = false;
      resyncReason = null;
    }
  };

  const resync = (reason: string) => {
    clearInternal(reason);
    bump();
  };

  /** Checks a sequenced frame; false means a gap triggered a resync. */
  const nextSeq = (msgSeq: number): boolean => {
    if (!hasSeq || msgSeq !== seq + 1) {
      resync(hasSeq ? `seq_gap expected=${seq + 1} got=${msgSeq}` : 'missing_snapshot');
      return false;
    }
    seq = msgSeq;
    return true;
  };

  const entityFor = (typeTag: number): SchemaEntity | undefined => entityByTag(schema, typeTag);

  const apply = (msg: Message) => {
    if ('targetId' in msg && msg.targetId && msg.targetId !== targetId) return;
    switch (msg.type) {
      case 'targetSchema':
        schema = msg.schema;
        bump();
        return;
      case 'error':
        lastError = { code: msg.code, message: msg.message };
        bump();
        return;
      case 'resyncRequired':
        epoch = msg.epoch;
        resync(msg.reason || 'resync_required');
        return;
      case 'worldStatus':
        epoch = msg.epoch;
        tick = msg.tick;
        agentConnected = msg.agentConnected;
        observeState = msg.observeState;
        statusEntityCount = msg.entityCount;
        if (!msg.ready && ready && !pending) {
          ready = false;
        }
        if (lastError?.code === 'target_not_found') lastError = null;
        bump();
        return;
      case 'snapshotBegin':
        // A snapshot is a fresh baseline; the hub's per-connection seq may
        // continue from earlier frames.
        epoch = msg.epoch;
        seq = msg.seq;
        hasSeq = true;
        pending = { epoch: msg.epoch, lastSeq: msg.seq, tick: msg.tick, expectedCount: msg.entityCount, entities: new Map() };
        if (lastError?.code === 'target_not_found') lastError = null;
        bump();
        return;
      case 'snapshotChunk': {
        if (!pending) return resync('snapshot_chunk_without_begin');
        if (msg.epoch !== pending.epoch) return resync('snapshot_epoch_mismatch');
        if (!nextSeq(msg.seq)) return;
        for (const e of msg.entities) pending.entities.set(e.id, makeRecord(schema, e));
        pending.lastSeq = msg.seq;
        bump();
        return;
      }
      case 'snapshotEnd': {
        if (!pending) return resync('snapshot_end_without_begin');
        if (msg.epoch !== pending.epoch) return resync('snapshot_epoch_mismatch');
        if (!nextSeq(msg.seq)) return;
        entities = pending.entities;
        tick = pending.tick;
        pending = null;
        ready = true;
        needsResync = false;
        resyncReason = null;
        bump();
        return;
      }
      case 'batch': {
        if (pending) return resync('batch_during_snapshot');
        if (msg.epoch !== epoch) return resync('epoch_mismatch');
        if (!nextSeq(msg.seq)) return;
        const next = new Map(entities);
        for (const s of msg.spawns) next.set(s.id, makeRecord(schema, s));
        for (const d of msg.deltas) {
          const existing = next.get(d.id);
          if (!existing) return resync(`delta_for_unknown_entity ${d.id}`);
          const entity = entityFor(d.typeTag);
          const fields = existing.fields.withDelta(entity, d.mask, d.delta);
          next.set(d.id, { ...existing, fields, position: readPosition(entity, fields) });
        }
        for (const r of msg.removals) next.delete(r.id);
        entities = next;
        tick = msg.tick;
        bump();
        return;
      }
      default:
        return;
    }
  };

  return {
    getState: () => cached,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    apply,
    clear(reason) {
      clearInternal(reason);
      bump();
    },
    setTarget(next) {
      targetId = next;
      schema = null;
      epoch = 0;
      tick = 0;
      agentConnected = false;
      observeState = ObserveState.Idle;
      statusEntityCount = null;
      lastError = null;
      clearInternal();
      bump();
    },
    clearError() {
      lastError = null;
      bump();
    },
  };
}
