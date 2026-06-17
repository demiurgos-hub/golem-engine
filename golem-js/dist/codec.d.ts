/** Minimal protobuf wire-format writer. Supports the scalar types used by golem schemas. */
export declare class PbWriter {
    private _buf;
    tag(field: number, wireType: number): this;
    int32(v: number): this;
    int64(v: number): this;
    uint32(v: number): this;
    uint64(v: number): this;
    sint32(v: number): this;
    sint64(v: number): this;
    bool(v: boolean): this;
    float(v: number): this;
    double(v: number): this;
    string(v: string): this;
    bytes(v: Uint8Array): this;
    finish(): Uint8Array;
    private _varint;
    /** Encode a negative value as a 10-byte two's-complement varint. */
    private _varint64Neg;
}
/** Minimal protobuf wire-format reader. */
export declare class PbReader {
    private _view;
    private _pos;
    private _end;
    constructor(buf: Uint8Array);
    get done(): boolean;
    /** Read a tag and return { field, wire }. */
    tag(): {
        field: number;
        wire: number;
    };
    int32(): number;
    int64(): number;
    uint32(): number;
    uint64(): number;
    sint32(): number;
    sint64(): number;
    bool(): boolean;
    float(): number;
    double(): number;
    string(): string;
    bytes(): Uint8Array;
    /** Return unread bytes and advance to the end. */
    remaining(): Uint8Array;
    /** Skip a field based on its wire type. */
    skip(wireType: number): void;
    private _uvarint;
    /** Read a 64-bit varint as a JS number (safe up to 2^53). */
    private _varint64;
}
