import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { PbReader, PbWriter } from "../dist/index.js";

describe("PbReader.int32", () => {
  it("round-trips negative int32 values used by signed battle HP deltas", () => {
    for (const v of [-1, -7, -128, -32768, -2147483648, 1, 7, 2147483647]) {
      const enc = new PbWriter().tag(14, 0).int32(v).finish();
      const r = new PbReader(enc);
      const tag = r.tag();
      assert.equal(tag.field, 14);
      assert.equal(tag.wire, 0);
      assert.equal(r.int32(), v, `int32 round-trip failed for ${v}`);
    }
  });

  it("keeps sint32 zigzag negatives working", () => {
    const enc = new PbWriter().sint32(-7).finish();
    assert.equal(new PbReader(enc).sint32(), -7);
  });
});
