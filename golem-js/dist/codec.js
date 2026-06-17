/** Minimal protobuf wire-format writer. Supports the scalar types used by golem schemas. */
export class PbWriter {
    constructor() {
        this._buf = [];
    }
    tag(field, wireType) {
        return this._varint((field << 3) | wireType);
    }
    int32(v) {
        return v < 0 ? this._varint64Neg(v) : this._varint(v);
    }
    int64(v) {
        return v < 0 ? this._varint64Neg(v) : this._varint(v);
    }
    uint32(v) { return this._varint(v >>> 0); }
    uint64(v) { return this._varint(v); }
    sint32(v) { return this._varint((v << 1) ^ (v >> 31)); }
    sint64(v) {
        const lo = v < 0 ? -v * 2 - 1 : v * 2;
        return this._varint(lo);
    }
    bool(v) { return this._varint(v ? 1 : 0); }
    float(v) {
        const buf = new ArrayBuffer(4);
        new DataView(buf).setFloat32(0, v, true);
        const bytes = new Uint8Array(buf);
        for (let i = 0; i < 4; i++)
            this._buf.push(bytes[i]);
        return this;
    }
    double(v) {
        const buf = new ArrayBuffer(8);
        new DataView(buf).setFloat64(0, v, true);
        const bytes = new Uint8Array(buf);
        for (let i = 0; i < 8; i++)
            this._buf.push(bytes[i]);
        return this;
    }
    string(v) {
        const encoded = new TextEncoder().encode(v);
        this._varint(encoded.length);
        for (let i = 0; i < encoded.length; i++)
            this._buf.push(encoded[i]);
        return this;
    }
    bytes(v) {
        this._varint(v.length);
        for (let i = 0; i < v.length; i++)
            this._buf.push(v[i]);
        return this;
    }
    finish() {
        return new Uint8Array(this._buf);
    }
    _varint(v) {
        v >>>= 0;
        while (v > 0x7f) {
            this._buf.push((v & 0x7f) | 0x80);
            v >>>= 7;
        }
        this._buf.push(v);
        return this;
    }
    /** Encode a negative value as a 10-byte two's-complement varint. */
    _varint64Neg(v) {
        let lo = (v | 0) >>> 0;
        let hi = 0xffffffff;
        for (let i = 0; i < 9; i++) {
            this._buf.push((lo & 0x7f) | 0x80);
            lo = ((lo >>> 7) | (hi << 25)) >>> 0;
            hi >>>= 7;
        }
        this._buf.push(lo & 0x01);
        return this;
    }
}
/** Minimal protobuf wire-format reader. */
export class PbReader {
    constructor(buf) {
        this._view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
        this._pos = 0;
        this._end = buf.byteLength;
    }
    get done() { return this._pos >= this._end; }
    /** Read a tag and return { field, wire }. */
    tag() {
        const v = this._uvarint();
        return { field: v >>> 3, wire: v & 7 };
    }
    int32() { return this._uvarint() | 0; }
    int64() { return this._varint64(); }
    uint32() { return this._uvarint() >>> 0; }
    uint64() { return this._varint64(); }
    sint32() {
        const v = this._uvarint();
        return (v >>> 1) ^ -(v & 1);
    }
    sint64() {
        const v = this._varint64();
        return v % 2 === 0 ? v / 2 : -(v + 1) / 2;
    }
    bool() { return this._uvarint() !== 0; }
    float() {
        const v = this._view.getFloat32(this._pos, true);
        this._pos += 4;
        return v;
    }
    double() {
        const v = this._view.getFloat64(this._pos, true);
        this._pos += 8;
        return v;
    }
    string() {
        const len = this._uvarint();
        const bytes = new Uint8Array(this._view.buffer, this._view.byteOffset + this._pos, len);
        this._pos += len;
        return new TextDecoder().decode(bytes);
    }
    bytes() {
        const len = this._uvarint();
        const slice = new Uint8Array(this._view.buffer, this._view.byteOffset + this._pos, len);
        this._pos += len;
        return slice;
    }
    /** Return unread bytes and advance to the end. */
    remaining() {
        const slice = new Uint8Array(this._view.buffer, this._view.byteOffset + this._pos, this._end - this._pos);
        this._pos = this._end;
        return slice;
    }
    /** Skip a field based on its wire type. */
    skip(wireType) {
        switch (wireType) {
            case 0:
                this._uvarint();
                break;
            case 1:
                this._pos += 8;
                break;
            case 2:
                this._pos += this._uvarint();
                break;
            case 5:
                this._pos += 4;
                break;
            default: throw new Error(`unsupported wire type ${wireType}`);
        }
    }
    _uvarint() {
        let result = 0;
        let shift = 0;
        while (this._pos < this._end) {
            const b = this._view.getUint8(this._pos++);
            if (shift < 28) {
                result |= (b & 0x7f) << shift;
            }
            else {
                result += (b & 0x7f) * (2 ** shift);
            }
            if (b < 0x80)
                return result >>> 0;
            shift += 7;
        }
        return result >>> 0;
    }
    /** Read a 64-bit varint as a JS number (safe up to 2^53). */
    _varint64() {
        let lo = 0, hi = 0, shift = 0;
        while (shift < 28 && this._pos < this._end) {
            const b = this._view.getUint8(this._pos++);
            lo |= (b & 0x7f) << shift;
            shift += 7;
            if (b < 0x80)
                return lo >>> 0;
        }
        while (shift < 70 && this._pos < this._end) {
            const b = this._view.getUint8(this._pos++);
            if (shift < 32) {
                lo |= (b & 0x7f) << shift;
                hi |= (b & 0x7f) >>> (32 - shift);
            }
            else {
                hi |= (b & 0x7f) << (shift - 32);
            }
            shift += 7;
            if (b < 0x80)
                break;
        }
        return (hi >>> 0) * 0x100000000 + (lo >>> 0);
    }
}
