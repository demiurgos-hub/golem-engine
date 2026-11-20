/**
 * Minimal bounds-checked protobuf codec for the inspector protocol.
 *
 * Unlike golem-js's PbReader this reader returns 64-bit varints as bigint
 * (dirty masks use all 64 bits) and throws WireError on malformed input
 * instead of reading past the buffer.
 */

export class WireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'WireError';
  }
}

export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_BYTES = 2;
export const WIRE_FIXED32 = 5;

const textDecoder = new TextDecoder();
const textEncoder = new TextEncoder();

export class Reader {
  readonly buf: Uint8Array;
  pos = 0;
  private readonly view: DataView;

  constructor(buf: Uint8Array) {
    this.buf = buf;
    this.view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  }

  get done(): boolean {
    return this.pos >= this.buf.byteLength;
  }

  /** Reads a varint as bigint (full 64-bit precision). */
  varint64(): bigint {
    let lo = 0;
    let hi = 0;
    let shift = 0;
    for (let i = 0; i < 10; i++) {
      if (this.pos >= this.buf.byteLength) throw new WireError('truncated varint');
      const b = this.buf[this.pos++]!;
      if (shift < 28) {
        lo |= (b & 0x7f) << shift;
      } else if (shift === 28) {
        lo |= (b & 0x0f) << 28;
        hi |= (b & 0x7f) >> 4;
      } else {
        hi |= (b & 0x7f) << (shift - 32);
      }
      shift += 7;
      if (b < 0x80) {
        return (BigInt(hi >>> 0) << 32n) | BigInt(lo >>> 0);
      }
    }
    throw new WireError('varint overflow');
  }

  /** Reads an unsigned varint that must fit a JS safe integer. */
  uint(): number {
    const v = this.varint64();
    if (v > BigInt(Number.MAX_SAFE_INTEGER)) throw new WireError('integer exceeds 2^53');
    return Number(v);
  }

  tag(): { field: number; wire: number } {
    const key = this.uint();
    const field = Math.floor(key / 8);
    if (field === 0 || field > 0x1fffffff) throw new WireError(`invalid field number ${field}`);
    return { field, wire: key & 7 };
  }

  bytes(): Uint8Array {
    const n = this.uint();
    if (n > this.buf.byteLength - this.pos) throw new WireError('length exceeds buffer');
    const out = this.buf.subarray(this.pos, this.pos + n);
    this.pos += n;
    return out;
  }

  string(): string {
    return textDecoder.decode(this.bytes());
  }

  fixed32(): number {
    if (this.buf.byteLength - this.pos < 4) throw new WireError('truncated fixed32');
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }

  float(): number {
    if (this.buf.byteLength - this.pos < 4) throw new WireError('truncated float');
    const v = this.view.getFloat32(this.pos, true);
    this.pos += 4;
    return v;
  }

  double(): number {
    if (this.buf.byteLength - this.pos < 8) throw new WireError('truncated double');
    const v = this.view.getFloat64(this.pos, true);
    this.pos += 8;
    return v;
  }

  fixed64(): bigint {
    if (this.buf.byteLength - this.pos < 8) throw new WireError('truncated fixed64');
    const v = this.view.getBigUint64(this.pos, true);
    this.pos += 8;
    return v;
  }

  skip(wire: number): void {
    switch (wire) {
      case WIRE_VARINT:
        this.varint64();
        return;
      case WIRE_FIXED64:
        this.fixed64();
        return;
      case WIRE_BYTES:
        this.bytes();
        return;
      case WIRE_FIXED32:
        this.fixed32();
        return;
      default:
        throw new WireError(`unsupported wire type ${wire}`);
    }
  }

  expect(field: number, got: number, want: number): void {
    if (got !== want) throw new WireError(`field ${field} has wire type ${got}, want ${want}`);
  }
}

/** Converts a bigint to number when it is a safe integer. */
export function toSafeNumber(v: bigint): number | bigint {
  return v >= BigInt(Number.MIN_SAFE_INTEGER) && v <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(v) : v;
}

export class Writer {
  private chunks: number[] = [];

  tag(field: number, wire: number): this {
    return this.varint(field * 8 + wire);
  }

  varint(v: number | bigint): this {
    let n = typeof v === 'bigint' ? BigInt.asUintN(64, v) : BigInt.asUintN(64, BigInt(Math.trunc(v)));
    while (n > 0x7fn) {
      this.chunks.push(Number(n & 0x7fn) | 0x80);
      n >>= 7n;
    }
    this.chunks.push(Number(n));
    return this;
  }

  bytes(b: Uint8Array): this {
    this.varint(b.byteLength);
    for (const x of b) this.chunks.push(x);
    return this;
  }

  string(s: string): this {
    return this.bytes(textEncoder.encode(s));
  }

  finish(): Uint8Array<ArrayBuffer> {
    return Uint8Array.from(this.chunks);
  }
}

/** Formats a dirty mask for display. */
export function formatMask(mask: bigint): string {
  return `0x${mask.toString(16)}`;
}
