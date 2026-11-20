/**
 * Game-agnostic entity state: raw protobuf entries grouped by field number.
 * Mirrors wire.EntityFields in Go so the browser merges deltas exactly like
 * the hub does (verified by the shared merge fixture).
 */
import { Reader } from './codec';
import type { SchemaEntity } from './messages';

export class EntityFields {
  readonly typeTag: number;
  readonly entries: ReadonlyMap<number, readonly Uint8Array[]>;

  private constructor(typeTag: number, entries: Map<number, Uint8Array[]>) {
    this.typeTag = typeTag;
    this.entries = entries;
  }

  static parse(typeTag: number, state: Uint8Array): EntityFields {
    return new EntityFields(typeTag, splitFields(state));
  }

  /**
   * Returns a new EntityFields with delta merged: every field present in
   * delta replaces the stored entries for its tag, and every field whose bit
   * is set in mask but is absent from delta is reset.
   */
  withDelta(entity: SchemaEntity | undefined, mask: bigint, delta: Uint8Array): EntityFields {
    const incoming = splitFields(delta);
    const next = new Map(this.entries as Map<number, Uint8Array[]>);
    if (entity) {
      for (const f of entity.fields) {
        if (f.bit >= 0 && (mask & (1n << BigInt(f.bit))) !== 0n && !incoming.has(f.tag)) {
          next.delete(f.tag);
        }
      }
    }
    for (const [tag, list] of incoming) next.set(tag, list);
    return new EntityFields(this.typeTag, next);
  }

  /** Re-encodes the entries as a State message in field-number order. */
  encode(): Uint8Array {
    const tags = [...this.entries.keys()].sort((a, b) => a - b);
    let size = 0;
    for (const t of tags) for (const e of this.entries.get(t)!) size += e.byteLength;
    const out = new Uint8Array(size);
    let pos = 0;
    for (const t of tags) {
      for (const e of this.entries.get(t)!) {
        out.set(e, pos);
        pos += e.byteLength;
      }
    }
    return out;
  }
}

/** Groups a message's raw entries by field number, copying them. */
export function splitFields(buf: Uint8Array): Map<number, Uint8Array[]> {
  const r = new Reader(buf);
  const out = new Map<number, Uint8Array[]>();
  while (!r.done) {
    const start = r.pos;
    const { field, wire } = r.tag();
    r.skip(wire);
    const entry = buf.slice(start, r.pos);
    const list = out.get(field);
    if (list) list.push(entry);
    else out.set(field, [entry]);
  }
  return out;
}
