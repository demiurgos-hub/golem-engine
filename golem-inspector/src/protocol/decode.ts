/**
 * Schema-driven decoding of generated entity messages into display values.
 * Encoding rules follow golem's generated codecs: list elements are one
 * wire-type-2 entry each holding the bare scalar (or custom message), map
 * entries are {1: key, 2: value} messages, and a zero-length entry marks a
 * cleared collection.
 */
import { Reader, WireError, WIRE_BYTES, WIRE_FIXED32, WIRE_FIXED64, WIRE_VARINT, toSafeNumber } from './codec';
import { FieldKind, type CustomType, type Schema, type SchemaEntity, type SchemaField } from './messages';
import type { EntityFields } from './fields';

export type ScalarValue = number | bigint | boolean | string | Uint8Array;

export type Value =
  | { kind: 'scalar'; type: string; value: ScalarValue }
  | { kind: 'list'; type: string; items: Value[] }
  | { kind: 'map'; keyType: string; type: string; entries: { key: Value; value: Value }[] }
  | { kind: 'message'; type: string; fields: DecodedField[] }
  | { kind: 'error'; message: string };

export interface DecodedField {
  field: SchemaField;
  value: Value;
  /** False when the field is absent from the state (default value shown). */
  present: boolean;
}

export interface UnknownField {
  tag: number;
  bytes: number;
}

export interface DecodedEntity {
  fields: DecodedField[];
  unknown: UnknownField[];
}

const SCALAR_WIRE: Record<string, number> = {
  double: WIRE_FIXED64,
  float: WIRE_FIXED32,
  int32: WIRE_VARINT,
  int64: WIRE_VARINT,
  uint32: WIRE_VARINT,
  uint64: WIRE_VARINT,
  sint32: WIRE_VARINT,
  sint64: WIRE_VARINT,
  bool: WIRE_VARINT,
  string: WIRE_BYTES,
  bytes: WIRE_BYTES,
};

export function isScalarType(type: string): boolean {
  return type in SCALAR_WIRE;
}

function zigzag(v: bigint): bigint {
  return (v >> 1n) ^ -(v & 1n);
}

/** Reads one bare scalar value (no key) of the given proto type. */
export function readScalar(r: Reader, type: string): ScalarValue {
  switch (type) {
    case 'double':
      return r.double();
    case 'float':
      return r.float();
    case 'int32':
      return Number(BigInt.asIntN(32, r.varint64()));
    case 'int64':
      return toSafeNumber(BigInt.asIntN(64, r.varint64()));
    case 'uint32':
      return Number(BigInt.asUintN(32, r.varint64()));
    case 'uint64':
      return toSafeNumber(r.varint64());
    case 'sint32':
      return Number(BigInt.asIntN(32, zigzag(r.varint64())));
    case 'sint64':
      return toSafeNumber(BigInt.asIntN(64, zigzag(r.varint64())));
    case 'bool':
      return r.varint64() !== 0n;
    case 'string':
      return r.string();
    case 'bytes':
      return r.bytes().slice();
    default:
      throw new WireError(`unknown scalar type ${type}`);
  }
}

export function defaultScalar(type: string): ScalarValue {
  switch (type) {
    case 'bool':
      return false;
    case 'string':
      return '';
    case 'bytes':
      return new Uint8Array();
    default:
      return 0;
  }
}

class Decoder {
  private readonly types: Map<string, CustomType>;

  constructor(schema: Schema) {
    this.types = new Map(schema.types.map((t) => [t.name, t]));
  }

  defaultValue(field: SchemaField): Value {
    switch (field.kind) {
      case FieldKind.List:
        return { kind: 'list', type: field.type, items: [] };
      case FieldKind.Map:
        return { kind: 'map', keyType: field.keyType, type: field.type, entries: [] };
      case FieldKind.Custom:
        return this.message(field.type, new Uint8Array(), 0);
      default:
        return { kind: 'scalar', type: field.type, value: defaultScalar(field.type) };
    }
  }

  /** Decodes a value that follows an entry key of the given wire type. */
  private element(type: string, r: Reader, depth: number): Value {
    if (this.types.has(type)) return this.message(type, r.bytes(), depth + 1);
    return { kind: 'scalar', type, value: readScalar(r, type) };
  }

  message(type: string, body: Uint8Array, depth: number): Value {
    const ct = this.types.get(type);
    if (!ct || depth > 16) return { kind: 'error', message: `unknown type ${type}` };
    const entries = new Map<number, Uint8Array[]>();
    const r = new Reader(body);
    while (!r.done) {
      const start = r.pos;
      const { field, wire } = r.tag();
      r.skip(wire);
      const list = entries.get(field) ?? [];
      list.push(body.subarray(start, r.pos));
      entries.set(field, list);
    }
    return { kind: 'message', type, fields: ct.fields.map((f) => this.field(f, entries.get(f.tag), depth)) };
  }

  field(field: SchemaField, entries: readonly Uint8Array[] | undefined, depth = 0): DecodedField {
    if (!entries || entries.length === 0) {
      return { field, value: this.defaultValue(field), present: false };
    }
    try {
      return { field, value: this.decodeEntries(field, entries, depth), present: true };
    } catch (err) {
      return { field, value: { kind: 'error', message: err instanceof Error ? err.message : String(err) }, present: true };
    }
  }

  private decodeEntries(field: SchemaField, entries: readonly Uint8Array[], depth: number): Value {
    switch (field.kind) {
      case FieldKind.List: {
        const items: Value[] = [];
        for (const raw of entries) {
          const r = new Reader(raw);
          const key = r.tag();
          r.expect(key.field, key.wire, WIRE_BYTES);
          const inner = r.bytes();
          if (inner.byteLength === 0) continue; // cleared-collection marker
          if (this.types.has(field.type)) {
            items.push(this.message(field.type, inner, depth + 1));
          } else {
            items.push({ kind: 'scalar', type: field.type, value: readScalar(new Reader(inner), field.type) });
          }
        }
        return { kind: 'list', type: field.type, items };
      }
      case FieldKind.Map: {
        const out: { key: Value; value: Value }[] = [];
        for (const raw of entries) {
          const r = new Reader(raw);
          const key = r.tag();
          r.expect(key.field, key.wire, WIRE_BYTES);
          const inner = r.bytes();
          if (inner.byteLength === 0) continue;
          const er = new Reader(inner);
          let k: Value = { kind: 'scalar', type: field.keyType, value: defaultScalar(field.keyType) };
          let v: Value = this.defaultValue({ ...field, kind: this.types.has(field.type) ? FieldKind.Custom : FieldKind.Scalar });
          while (!er.done) {
            const t = er.tag();
            if (t.field === 1) k = { kind: 'scalar', type: field.keyType, value: readScalar(er, field.keyType) };
            else if (t.field === 2) v = this.element(field.type, er, depth);
            else er.skip(t.wire);
          }
          out.push({ key: k, value: v });
        }
        return { kind: 'map', keyType: field.keyType, type: field.type, entries: out };
      }
      case FieldKind.Custom: {
        const r = new Reader(entries[entries.length - 1]!);
        const key = r.tag();
        r.expect(key.field, key.wire, WIRE_BYTES);
        return this.message(field.type, r.bytes(), depth + 1);
      }
      default: {
        const r = new Reader(entries[entries.length - 1]!);
        const key = r.tag();
        const want = SCALAR_WIRE[field.type];
        if (want === undefined) throw new WireError(`unknown scalar type ${field.type}`);
        r.expect(key.field, key.wire, want);
        return { kind: 'scalar', type: field.type, value: readScalar(r, field.type) };
      }
    }
  }
}

const decoders = new WeakMap<Schema, Decoder>();

function decoderFor(schema: Schema): Decoder {
  let d = decoders.get(schema);
  if (!d) {
    d = new Decoder(schema);
    decoders.set(schema, d);
  }
  return d;
}

/** Decodes every schema field of an entity, plus a list of unknown tags. */
export function decodeEntity(schema: Schema, entity: SchemaEntity, fields: EntityFields): DecodedEntity {
  const d = decoderFor(schema);
  const known = new Set(entity.fields.map((f) => f.tag));
  const unknown: UnknownField[] = [];
  for (const [tag, list] of fields.entries) {
    if (!known.has(tag)) unknown.push({ tag, bytes: list.reduce((n, e) => n + e.byteLength, 0) });
  }
  return { fields: entity.fields.map((f) => d.field(f, fields.entries.get(f.tag))), unknown };
}

export interface Position {
  x: number;
  y: number;
  z: number;
}

/** Reads pos_x/pos_y/pos_z (role "position") without decoding other fields. */
export function readPosition(entity: SchemaEntity | undefined, fields: EntityFields): Position {
  const pos: Position = { x: 0, y: 0, z: 0 };
  if (!entity) return pos;
  for (const f of entity.fields) {
    if (f.role !== 'position') continue;
    const list = fields.entries.get(f.tag);
    if (!list || list.length === 0) continue;
    try {
      const r = new Reader(list[list.length - 1]!);
      const key = r.tag();
      const v = key.wire === WIRE_FIXED32 ? r.float() : key.wire === WIRE_FIXED64 ? r.double() : Number(r.varint64());
      if (f.name === 'pos_x') pos.x = v;
      else if (f.name === 'pos_y') pos.y = v;
      else if (f.name === 'pos_z') pos.z = v;
    } catch {
      // A malformed position leaves the default.
    }
  }
  return pos;
}

/** Reads an unsigned varint field (revision, entity_id) or 0. */
export function readVarintField(fields: EntityFields, tag: number): bigint {
  const list = fields.entries.get(tag);
  if (!list || list.length === 0) return 0n;
  try {
    const r = new Reader(list[list.length - 1]!);
    const key = r.tag();
    return key.wire === WIRE_VARINT ? r.varint64() : 0n;
  } catch {
    return 0n;
  }
}

/** Formats a decoded scalar for display. */
export function formatScalar(type: string, value: ScalarValue): string {
  if (value instanceof Uint8Array) {
    const hex = [...value.slice(0, 32)].map((b) => b.toString(16).padStart(2, '0')).join(' ');
    return value.byteLength > 32 ? `${hex} … (${value.byteLength} bytes)` : hex || '(empty)';
  }
  if (typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'number' && (type === 'float' || type === 'double')) {
    return Number.isInteger(value) ? value.toFixed(1) : String(Math.round(value * 1e4) / 1e4);
  }
  return String(value);
}
