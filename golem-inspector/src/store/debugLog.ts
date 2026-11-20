import type { Message } from '../protocol/messages';

export interface DebugEntry {
  id: number;
  at: number;
  direction: 'in' | 'out';
  type: string;
  bytes: number;
  summary: string;
}

export interface DebugLog {
  getState(): { enabled: boolean; entries: readonly DebugEntry[] };
  subscribe(listener: () => void): () => void;
  setEnabled(enabled: boolean): void;
  record(direction: 'in' | 'out', bytes: number, msg: Message | null, note?: string): void;
  clear(): void;
}

const MAX_ENTRIES = 300;

/** Summarizes a decoded message for the debug panel. */
export function summarize(msg: Message): string {
  switch (msg.type) {
    case 'snapshotBegin':
      return `epoch ${msg.epoch} seq ${msg.seq} tick ${msg.tick} entities ${msg.entityCount}`;
    case 'snapshotChunk':
      return `epoch ${msg.epoch} seq ${msg.seq} entities ${msg.entities.length}`;
    case 'snapshotEnd':
      return `epoch ${msg.epoch} seq ${msg.seq}`;
    case 'batch':
      return `epoch ${msg.epoch} seq ${msg.seq} tick ${msg.tick} +${msg.spawns.length} ~${msg.deltas.length} -${msg.removals.length}`;
    case 'worldStatus':
      return `ready=${msg.ready} agent=${msg.agentConnected} observe=${msg.observeState} entities=${msg.entityCount} tick=${msg.tick}`;
    case 'resyncRequired':
      return `epoch ${msg.epoch} reason ${msg.reason}`;
    case 'targetSchema':
      return `${msg.schema?.entities.length ?? 0} entity types, fingerprint ${msg.schema?.fingerprint.slice(0, 12) ?? '—'}`;
    case 'error':
      return `${msg.code}${msg.message ? `: ${msg.message}` : ''}`;
    case 'subscribe':
    case 'resync':
      return msg.targetId;
    case 'ping':
    case 'pong':
      return String(msg.clientTime);
    default:
      return '';
  }
}

/** A bounded ring of decoded frames, recorded only while enabled. */
export function createDebugLog(): DebugLog {
  let enabled = false;
  let entries: DebugEntry[] = [];
  let nextId = 1;
  let state = { enabled, entries: entries as readonly DebugEntry[] };
  const listeners = new Set<() => void>();
  let scheduled = false;

  const publish = () => {
    state = { enabled, entries };
    for (const l of listeners) l();
  };
  // Frames can arrive far faster than React should re-render.
  const schedule = () => {
    if (scheduled) return;
    scheduled = true;
    setTimeout(() => {
      scheduled = false;
      publish();
    }, 250);
  };

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    setEnabled(next) {
      enabled = next;
      publish();
    },
    record(direction, bytes, msg, note) {
      if (!enabled) return;
      const entry: DebugEntry = {
        id: nextId++,
        at: Date.now(),
        direction,
        type: msg?.type ?? 'unknown',
        bytes,
        summary: note ?? (msg ? summarize(msg) : ''),
      };
      entries = entries.length >= MAX_ENTRIES ? [...entries.slice(entries.length - MAX_ENTRIES + 1), entry] : [...entries, entry];
      schedule();
    },
    clear() {
      entries = [];
      publish();
    },
  };
}
