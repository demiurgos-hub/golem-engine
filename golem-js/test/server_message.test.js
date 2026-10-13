import assert from "node:assert/strict";
import test from "node:test";
import { GameClient, PbWriter, unwrapServerMessage } from "../dist/index.js";

function receiver({world=true, events=true, decodeWorld=true}={}) {
  let receive;
  const seen=[];
  const client=new GameClient({
    entityManager:{applyUpdate:b=>seen.push(['entity',...b]),get(){}},
    worldManager:world?{applyUpdate:b=>seen.push(['world',...b])}:undefined,
    eventManager:events?{applyRaw:b=>seen.push(['event',...b])}:undefined,
    decode:b=>b,decodeWorld:decodeWorld?b=>b:undefined,
    encode:()=>new Uint8Array(),encodePacket:()=>new Uint8Array(),
    createChannel:()=>({onMessage:fn=>receive=fn,onClose(){},onOpen(){},close(){}}),
  });
  client.connect('wss://example.test');
  return {receive,seen,client};
}

test("live envelope traversal handles every occurrence in wire order; decoding retains the last", () => {
  const bytes=new PbWriter().tag(1,2).bytes(new Uint8Array([1]))
    .tag(3,2).bytes(new Uint8Array([2])).tag(2,2).bytes(new Uint8Array([3]))
    .tag(1,0).uint32(99).tag(88,0).uint32(42)
    .tag(1,2).bytes(new Uint8Array([4])).tag(2,2).bytes(new Uint8Array([5]))
    .tag(3,2).bytes(new Uint8Array([6])).finish();
  const {receive,seen,client}=receiver();receive(bytes);
  assert.deepEqual(seen,[['entity',1],['event',2],['world',3],['entity',4],['world',5],['event',6]]);
  const result=unwrapServerMessage(bytes);
  assert.deepEqual([...result.entityUpdate],[4]);assert.deepEqual([...result.worldUpdate],[5]);assert.deepEqual([...result.serverEvent],[6]);
  bytes[bytes.length-1]=7;assert.equal(result.serverEvent[0],7,"payloads remain borrowed views");
  client.disconnect();
});

test("later envelope parse failures preserve already applied effects", () => {
  const bytes=new Uint8Array([10,1,7,18,127]);
  const {receive,seen,client}=receiver();
  assert.throws(()=>receive(bytes),RangeError);assert.deepEqual(seen,[['entity',7]]);
  assert.throws(()=>unwrapServerMessage(bytes),RangeError);client.disconnect();
});

test("missing handlers use the reader's existing skip behavior, including malformed lengths", () => {
  for(const options of [{world:false},{decodeWorld:false},{events:false}]) {
    const {receive,seen,client}=receiver(options);
    const tag=options.events===false?26:18;
    assert.doesNotThrow(()=>receive(new Uint8Array([10,1,7,tag,127])));
    assert.deepEqual(seen,[['entity',7]]);client.disconnect();
  }
  const {receive,seen,client}=receiver();
  assert.doesNotThrow(()=>receive(new Uint8Array([10,1,7,34,127])));
  assert.deepEqual(seen,[['entity',7]]);client.disconnect();
});
