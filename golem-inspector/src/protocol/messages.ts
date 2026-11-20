/**
 * Golem Inspector protocol v1 (golem/inspector/wire/inspector.proto).
 * Shapes mirror the Go wire package's JSON field names so golden fixtures can
 * be compared directly.
 */
import { Reader, Writer, WireError, WIRE_BYTES, WIRE_VARINT, toSafeNumber } from './codec';

export const PROTOCOL_VERSION = 1;

export enum FieldKind {
  Scalar = 0,
  Custom = 1,
  List = 2,
  Map = 3,
}

export enum ObserveState {
  Idle = 0,
  Starting = 1,
  Ready = 2,
}

export interface SchemaField {
  name: string;
  tag: number;
  bit: number;
  sync: string;
  ownerOnly: boolean;
  kind: FieldKind;
  type: string;
  keyType: string;
  role: string;
}

export interface SchemaEntity {
  name: string;
  stateTag: number;
  deltaTag: number;
  global: boolean;
  fields: SchemaField[];
  is3d: boolean;
}

export interface CustomType {
  name: string;
  fields: SchemaField[];
}

export interface Schema {
  fingerprint: string;
  dimensions: number;
  removedTag: number;
  entities: SchemaEntity[];
  types: CustomType[];
}

export interface EntityState {
  id: number;
  typeTag: number;
  state: Uint8Array;
  /** The entity's space; absent or 0 is the default space. */
  space?: number;
}

export interface EntityDelta {
  id: number;
  typeTag: number;
  revision: number;
  mask: bigint;
  delta: Uint8Array;
}

export interface Removal {
  id: number;
  revision: number;
}

/** An entity's new space; spawns carry theirs in EntityState instead. */
export interface SpaceMove {
  id: number;
  space: number;
}

export type Message =
  | { type: 'register'; targetId: string; labels: Record<string, string>; epoch: number; engineVersion: string; schema: Schema | null; tickRate: number }
  | { type: 'observeStatus'; state: ObserveState; entityCount: number; epoch: number }
  | { type: 'snapshotBegin'; targetId: string; epoch: number; seq: number; tick: number; entityCount: number }
  | { type: 'snapshotChunk'; targetId: string; epoch: number; seq: number; entities: EntityState[] }
  | { type: 'snapshotEnd'; targetId: string; epoch: number; seq: number }
  | { type: 'batch'; targetId: string; epoch: number; seq: number; tick: number; spawns: EntityState[]; deltas: EntityDelta[]; removals: Removal[]; moves: SpaceMove[] }
  | { type: 'ping'; clientTime: number }
  | { type: 'pong'; clientTime: number }
  | { type: 'observeStart' }
  | { type: 'observeStop' }
  | { type: 'error'; code: string; message: string; targetId: string }
  | { type: 'subscribe'; targetId: string }
  | { type: 'resync'; targetId: string }
  | { type: 'targetSchema'; targetId: string; schema: Schema | null }
  | { type: 'worldStatus'; targetId: string; epoch: number; entityCount: number; ready: boolean; agentConnected: boolean; observeState: ObserveState; tick: number }
  | { type: 'resyncRequired'; targetId: string; epoch: number; reason: string };

export type MessageType = Message['type'];
export type MessageOf<T extends MessageType> = Extract<Message, { type: T }>;

const BODY_TAGS: Record<number, MessageType> = {
  10: 'register',
  11: 'observeStatus',
  12: 'snapshotBegin',
  13: 'snapshotChunk',
  14: 'snapshotEnd',
  15: 'batch',
  16: 'ping',
  17: 'pong',
  20: 'observeStart',
  21: 'observeStop',
  22: 'error',
  30: 'subscribe',
  31: 'resync',
  40: 'targetSchema',
  41: 'worldStatus',
  42: 'resyncRequired',
};

export class VersionError extends WireError {
  constructor(version: number) {
    super(`unsupported protocol version ${version}`);
    this.name = 'VersionError';
  }
}

// Field readers that validate wire types.
function u(r: Reader, f: number, w: number): number {
  r.expect(f, w, WIRE_VARINT);
  return r.uint();
}
function u64(r: Reader, f: number, w: number): bigint {
  r.expect(f, w, WIRE_VARINT);
  return r.varint64();
}
function i64(r: Reader, f: number, w: number): number {
  r.expect(f, w, WIRE_VARINT);
  const v = toSafeNumber(BigInt.asIntN(64, r.varint64()));
  return typeof v === 'bigint' ? Number(v) : v;
}
function bool(r: Reader, f: number, w: number): boolean {
  r.expect(f, w, WIRE_VARINT);
  return r.varint64() !== 0n;
}
function str(r: Reader, f: number, w: number): string {
  r.expect(f, w, WIRE_BYTES);
  return r.string();
}
function sub(r: Reader, f: number, w: number): Reader {
  r.expect(f, w, WIRE_BYTES);
  return new Reader(r.bytes());
}
function sint32(r: Reader, f: number, w: number): number {
  const v = u(r, f, w);
  return (v >>> 1) ^ -(v & 1);
}

function decodeField(r: Reader): SchemaField {
  const out: SchemaField = { name: '', tag: 0, bit: 0, sync: '', ownerOnly: false, kind: FieldKind.Scalar, type: '', keyType: '', role: '' };
  while (!r.done) {
    const { field: f, wire: w } = r.tag();
    switch (f) {
      case 1: out.name = str(r, f, w); break;
      case 2: out.tag = u(r, f, w); break;
      case 3: out.bit = sint32(r, f, w); break;
      case 4: out.sync = str(r, f, w); break;
      case 5: out.ownerOnly = bool(r, f, w); break;
      case 6: out.kind = u(r, f, w) as FieldKind; break;
      case 7: out.type = str(r, f, w); break;
      case 8: out.keyType = str(r, f, w); break;
      case 9: out.role = str(r, f, w); break;
      default: r.skip(w);
    }
  }
  return out;
}

export function decodeSchema(r: Reader): Schema {
  const s: Schema = { fingerprint: '', dimensions: 0, removedTag: 0, entities: [], types: [] };
  while (!r.done) {
    const { field: f, wire: w } = r.tag();
    switch (f) {
      case 1: s.fingerprint = str(r, f, w); break;
      case 2: s.dimensions = u(r, f, w); break;
      case 3: s.removedTag = u(r, f, w); break;
      case 4: {
        const er = sub(r, f, w);
        const e: SchemaEntity = { name: '', stateTag: 0, deltaTag: 0, global: false, fields: [], is3d: false };
        while (!er.done) {
          const t = er.tag();
          switch (t.field) {
            case 1: e.name = str(er, t.field, t.wire); break;
            case 2: e.stateTag = u(er, t.field, t.wire); break;
            case 3: e.deltaTag = u(er, t.field, t.wire); break;
            case 4: e.global = bool(er, t.field, t.wire); break;
            case 5: e.fields.push(decodeField(sub(er, t.field, t.wire))); break;
            case 6: e.is3d = bool(er, t.field, t.wire); break;
            default: er.skip(t.wire);
          }
        }
        s.entities.push(e);
        break;
      }
      case 5: {
        const tr = sub(r, f, w);
        const ct: CustomType = { name: '', fields: [] };
        while (!tr.done) {
          const t = tr.tag();
          if (t.field === 1) ct.name = str(tr, t.field, t.wire);
          else if (t.field === 2) ct.fields.push(decodeField(sub(tr, t.field, t.wire)));
          else tr.skip(t.wire);
        }
        s.types.push(ct);
        break;
      }
      default:
        r.skip(w);
    }
  }
  return s;
}

function decodeEntityState(r: Reader): EntityState {
  const e: EntityState = { id: 0, typeTag: 0, state: new Uint8Array(), space: 0 };
  while (!r.done) {
    const { field: f, wire: w } = r.tag();
    if (f === 1) e.id = i64(r, f, w);
    else if (f === 2) e.typeTag = u(r, f, w);
    else if (f === 3) { r.expect(f, w, WIRE_BYTES); e.state = r.bytes(); }
    else if (f === 4) e.space = u(r, f, w);
    else r.skip(w);
  }
  return e;
}

function decodeEntityDelta(r: Reader): EntityDelta {
  const e: EntityDelta = { id: 0, typeTag: 0, revision: 0, mask: 0n, delta: new Uint8Array() };
  while (!r.done) {
    const { field: f, wire: w } = r.tag();
    switch (f) {
      case 1: e.id = i64(r, f, w); break;
      case 2: e.typeTag = u(r, f, w); break;
      case 3: e.revision = u(r, f, w); break;
      case 4: e.mask = u64(r, f, w); break;
      case 5: r.expect(f, w, WIRE_BYTES); e.delta = r.bytes(); break;
      default: r.skip(w);
    }
  }
  return e;
}

function decodeBody(type: MessageType, r: Reader): Message {
  switch (type) {
    case 'register': {
      const m: MessageOf<'register'> = { type, targetId: '', labels: {}, epoch: 0, engineVersion: '', schema: null, tickRate: 0 };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        switch (f) {
          case 1: m.targetId = str(r, f, w); break;
          case 2: {
            const er = sub(r, f, w);
            let k = '';
            let v = '';
            while (!er.done) {
              const t = er.tag();
              if (t.field === 1) k = str(er, t.field, t.wire);
              else if (t.field === 2) v = str(er, t.field, t.wire);
              else er.skip(t.wire);
            }
            m.labels[k] = v;
            break;
          }
          case 3: m.epoch = u(r, f, w); break;
          case 4: m.engineVersion = str(r, f, w); break;
          case 5: m.schema = decodeSchema(sub(r, f, w)); break;
          case 6: m.tickRate = u(r, f, w); break;
          default: r.skip(w);
        }
      }
      return m;
    }
    case 'observeStatus': {
      const m: MessageOf<'observeStatus'> = { type, state: ObserveState.Idle, entityCount: 0, epoch: 0 };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) m.state = u(r, f, w);
        else if (f === 2) m.entityCount = u(r, f, w);
        else if (f === 3) m.epoch = u(r, f, w);
        else r.skip(w);
      }
      return m;
    }
    case 'snapshotBegin': {
      const m: MessageOf<'snapshotBegin'> = { type, targetId: '', epoch: 0, seq: 0, tick: 0, entityCount: 0 };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        switch (f) {
          case 1: m.targetId = str(r, f, w); break;
          case 2: m.epoch = u(r, f, w); break;
          case 3: m.seq = u(r, f, w); break;
          case 4: m.tick = u(r, f, w); break;
          case 5: m.entityCount = u(r, f, w); break;
          default: r.skip(w);
        }
      }
      return m;
    }
    case 'snapshotChunk': {
      const m: MessageOf<'snapshotChunk'> = { type, targetId: '', epoch: 0, seq: 0, entities: [] };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        switch (f) {
          case 1: m.targetId = str(r, f, w); break;
          case 2: m.epoch = u(r, f, w); break;
          case 3: m.seq = u(r, f, w); break;
          case 4: m.entities.push(decodeEntityState(sub(r, f, w))); break;
          default: r.skip(w);
        }
      }
      return m;
    }
    case 'snapshotEnd': {
      const m: MessageOf<'snapshotEnd'> = { type, targetId: '', epoch: 0, seq: 0 };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) m.targetId = str(r, f, w);
        else if (f === 2) m.epoch = u(r, f, w);
        else if (f === 3) m.seq = u(r, f, w);
        else r.skip(w);
      }
      return m;
    }
    case 'batch': {
      const m: MessageOf<'batch'> = { type, targetId: '', epoch: 0, seq: 0, tick: 0, spawns: [], deltas: [], removals: [], moves: [] };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        switch (f) {
          case 1: m.targetId = str(r, f, w); break;
          case 2: m.epoch = u(r, f, w); break;
          case 3: m.seq = u(r, f, w); break;
          case 4: m.tick = u(r, f, w); break;
          case 5: m.spawns.push(decodeEntityState(sub(r, f, w))); break;
          case 6: m.deltas.push(decodeEntityDelta(sub(r, f, w))); break;
          case 7: {
            const rr = sub(r, f, w);
            const rm: Removal = { id: 0, revision: 0 };
            while (!rr.done) {
              const t = rr.tag();
              if (t.field === 1) rm.id = i64(rr, t.field, t.wire);
              else if (t.field === 2) rm.revision = u(rr, t.field, t.wire);
              else rr.skip(t.wire);
            }
            m.removals.push(rm);
            break;
          }
          case 8: {
            const mr = sub(r, f, w);
            const mv: SpaceMove = { id: 0, space: 0 };
            while (!mr.done) {
              const t = mr.tag();
              if (t.field === 1) mv.id = i64(mr, t.field, t.wire);
              else if (t.field === 2) mv.space = u(mr, t.field, t.wire);
              else mr.skip(t.wire);
            }
            m.moves.push(mv);
            break;
          }
          default: r.skip(w);
        }
      }
      return m;
    }
    case 'ping':
    case 'pong': {
      let clientTime = 0;
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) clientTime = i64(r, f, w);
        else r.skip(w);
      }
      return { type, clientTime };
    }
    case 'observeStart':
    case 'observeStop':
      return { type };
    case 'error': {
      const m: MessageOf<'error'> = { type, code: '', message: '', targetId: '' };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) m.code = str(r, f, w);
        else if (f === 2) m.message = str(r, f, w);
        else if (f === 3) m.targetId = str(r, f, w);
        else r.skip(w);
      }
      return m;
    }
    case 'subscribe':
    case 'resync': {
      let targetId = '';
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) targetId = str(r, f, w);
        else r.skip(w);
      }
      return { type, targetId };
    }
    case 'targetSchema': {
      const m: MessageOf<'targetSchema'> = { type, targetId: '', schema: null };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) m.targetId = str(r, f, w);
        else if (f === 2) m.schema = decodeSchema(sub(r, f, w));
        else r.skip(w);
      }
      return m;
    }
    case 'worldStatus': {
      const m: MessageOf<'worldStatus'> = { type, targetId: '', epoch: 0, entityCount: 0, ready: false, agentConnected: false, observeState: ObserveState.Idle, tick: 0 };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        switch (f) {
          case 1: m.targetId = str(r, f, w); break;
          case 2: m.epoch = u(r, f, w); break;
          case 3: m.entityCount = u(r, f, w); break;
          case 4: m.ready = bool(r, f, w); break;
          case 5: m.agentConnected = bool(r, f, w); break;
          case 6: m.observeState = u(r, f, w); break;
          case 7: m.tick = u(r, f, w); break;
          default: r.skip(w);
        }
      }
      return m;
    }
    case 'resyncRequired': {
      const m: MessageOf<'resyncRequired'> = { type, targetId: '', epoch: 0, reason: '' };
      while (!r.done) {
        const { field: f, wire: w } = r.tag();
        if (f === 1) m.targetId = str(r, f, w);
        else if (f === 2) m.epoch = u(r, f, w);
        else if (f === 3) m.reason = str(r, f, w);
        else r.skip(w);
      }
      return m;
    }
  }
}

/**
 * Decodes one Envelope. Throws VersionError for other protocol versions and
 * WireError for malformed bytes. Returns null for bodies this client does not
 * know (newer hubs). Byte fields alias buf.
 */
export function decodeMessage(buf: Uint8Array): Message | null {
  if (buf.byteLength === 0) throw new WireError('empty envelope');
  const r = new Reader(buf);
  let version = 0;
  let msg: Message | null = null;
  while (!r.done) {
    const { field, wire } = r.tag();
    if (field === 1) {
      version = u(r, field, wire);
      continue;
    }
    if (wire !== WIRE_BYTES) {
      r.skip(wire);
      continue;
    }
    const body = r.bytes();
    const type = BODY_TAGS[field];
    if (type) msg = decodeBody(type, new Reader(body));
  }
  if (version !== PROTOCOL_VERSION) throw new VersionError(version);
  return msg;
}

function envelope(tag: number, body: Uint8Array): Uint8Array<ArrayBuffer> {
  return new Writer().tag(1, WIRE_VARINT).varint(PROTOCOL_VERSION).tag(tag, WIRE_BYTES).bytes(body).finish();
}

/** Encodes the browser-to-hub messages. */
export function encodeSubscribe(targetId: string): Uint8Array<ArrayBuffer> {
  return envelope(30, new Writer().tag(1, WIRE_BYTES).string(targetId).finish());
}

export function encodeResync(targetId: string): Uint8Array<ArrayBuffer> {
  return envelope(31, new Writer().tag(1, WIRE_BYTES).string(targetId).finish());
}

export function encodePing(clientTime: number): Uint8Array<ArrayBuffer> {
  const body = new Writer();
  if (clientTime !== 0) body.tag(1, WIRE_VARINT).varint(clientTime);
  return envelope(16, body.finish());
}

/** Finds the entity type for a State or Delta oneof tag. */
export function entityByTag(schema: Schema | null, tag: number): SchemaEntity | undefined {
  return schema?.entities.find((e) => e.stateTag === tag || e.deltaTag === tag);
}
