import assert from "node:assert/strict";
import test from "node:test";

import { GameClient, PbWriter, createChannel } from "../dist/index.js";

class MockChannel {
  constructor() {
    this.connected = true;
    this.maxMessageBytes = 32000;
    this.sent = [];
    this.unreliable = undefined;
    this._onOpen = null;
    this._onClose = null;
    this._onMessage = null;
  }

  send(data) {
    this.sent.push(data);
  }

  close() {
    this.connected = false;
    this._onClose?.({ code: 1000, reason: "", wasClean: true });
  }

  onOpen(fn) {
    this._onOpen = fn;
    fn();
  }

  onClose(fn) {
    this._onClose = fn;
  }

  onMessage(fn) {
    this._onMessage = fn;
  }
}

class FakeWebSocket {
  static OPEN = 1;
  static CLOSED = 3;
  static instances = [];

  constructor() {
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
    this.closeCalled = false;
    FakeWebSocket.instances.push(this);
  }

  send(data) {
    this.sent.push(new Uint8Array(data));
  }

  close() {
    this.closeCalled = true;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code: 1000, reason: "", wasClean: true });
  }
}

function packetBytes(frames) {
  const w = new PbWriter();
  for (const frame of frames) {
    w.tag(1, 2).bytes(frame);
  }
  return w.finish();
}

function framedBatchBytes(frames) {
  const total = frames.reduce((sum, frame) => sum + 4 + frame.byteLength, 0);
  const out = new Uint8Array(total);
  const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
  let offset = 0;
  for (const frame of frames) {
    view.setUint32(offset, frame.byteLength, false);
    offset += 4;
    out.set(frame, offset);
    offset += frame.byteLength;
  }
  return out;
}

function decodeReliableFrame(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const n = view.getUint32(0, false);
  return bytes.slice(4, 4 + n);
}

function flushMicrotasks() {
  return new Promise((resolve) => queueMicrotask(resolve));
}

function makeClient() {
  const channel = new MockChannel();
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  return { client, channel };
}

test("GameClient flushes one small command on the next microtask", async () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  client.send({ bytes: 10 });
  assert.equal(channel.sent.length, 0);

  await flushMicrotasks();

  assert.equal(channel.sent.length, 1);
  assert.equal(channel.sent[0].byteLength, packetBytes([new Uint8Array(10)]).byteLength);
});

test("GameClient batches back-to-back sends into one ClientPacket", async () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  client.send({ bytes: 10 });
  client.send({ bytes: 20 });

  await flushMicrotasks();

  assert.equal(channel.sent.length, 1);
  assert.equal(
    channel.sent[0].byteLength,
    packetBytes([new Uint8Array(10), new Uint8Array(20)]).byteLength,
  );
});

test("GameClient flushes immediately when the next command would overflow the cap", async () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  client.send({ bytes: 20000 });
  client.send({ bytes: 20000 });

  assert.equal(channel.sent.length, 1);
  assert.equal(channel.sent[0].byteLength, packetBytes([new Uint8Array(20000)]).byteLength);

  await flushMicrotasks();

  assert.equal(channel.sent.length, 2);
  assert.equal(channel.sent[1].byteLength, packetBytes([new Uint8Array(20000)]).byteLength);
});

test("GameClient throws when a single encoded command cannot fit in one ClientPacket", () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  assert.throws(() => client.send({ bytes: 40000 }), /exceeds max reliable message/);
  assert.equal(channel.sent.length, 0);
});

test("GameClient logs and drops packets that exceed the cap during async flush", async () => {
  const { client } = makeClient();
  client.connect("ws://example.test");
  const originalError = console.error;
  let logged = "";
  console.error = (message) => {
    logged = String(message);
  };

  try {
    client.disconnect();
    const customChannel = new MockChannel();
    const customClient = new GameClient({
      entityManager: { applyUpdate() {}, get() { return undefined; } },
      decode: (bytes) => bytes,
      encode: (cmd) => new Uint8Array(cmd.bytes),
      encodePacket: () => new Uint8Array(40000),
      createChannel: () => customChannel,
    });
    customClient.connect("ws://example.test");

    customClient.send({ bytes: 10 });
    await flushMicrotasks();

    assert.match(logged, /exceeds max reliable message/);
    assert.equal(customChannel.sent.length, 0);
  } finally {
    console.error = originalError;
  }
});

test("WebSocket close sends the Golem close control frame before closing", () => {
  const previousWebSocket = globalThis.WebSocket;
  globalThis.WebSocket = FakeWebSocket;
  try {
    const channel = createChannel({ transport: "websocket", url: "ws://example.test" });
    const ws = FakeWebSocket.instances.at(-1);

    channel.close();

    assert.equal(ws.closeCalled, true);
    assert.equal(ws.sent.length, 1);
    assert.deepEqual(Array.from(ws.sent[0]), [0x00, 0x4f, 0x47, 0x53, 0x01]);
  } finally {
    globalThis.WebSocket = previousWebSocket;
    FakeWebSocket.instances.length = 0;
  }
});

test("WebSocket unclean close logs code and reason", () => {
  const previousWebSocket = globalThis.WebSocket;
  const originalError = console.error;
  let logged = "";
  globalThis.WebSocket = FakeWebSocket;
  console.error = (message) => {
    logged = String(message);
  };
  try {
    createChannel({ transport: "websocket", url: "ws://example.test" });
    const ws = FakeWebSocket.instances.at(-1);

    ws.onclose?.({ code: 1006, reason: "abnormal", wasClean: false });

    assert.match(logged, /transport=websocket/);
    assert.match(logged, /was_clean=false/);
    assert.match(logged, /code=1006/);
    assert.match(logged, /reason=abnormal/);
  } finally {
    console.error = originalError;
    globalThis.WebSocket = previousWebSocket;
    FakeWebSocket.instances.length = 0;
  }
});

test("WebTransport certificate hashes are validated before connect", () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    assert.throws(
      () => createChannel({
        transport: "webtransport",
        url: "https://example.test",
        serverCertificateHashes: [{ algorithm: "sha-512", value: "00" }],
      }),
      /unsupported certificate hash algorithm/,
    );
    assert.throws(
      () => createChannel({
        transport: "webtransport",
        url: "https://example.test",
        serverCertificateHashes: [{ algorithm: "sha-256", value: "zz" }],
      }),
      /non-hex/,
    );
    assert.throws(
      () => createChannel({
        transport: "webtransport",
        url: "https://example.test",
        serverCertificateHashes: [{ algorithm: "sha-256", value: "00" }],
      }),
      /length 1, want 32/,
    );
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("GameClient sends unreliable datagrams through the optional channel", () => {
  const unreliable = {
    maxDatagramBytes: 1280,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.unreliable = unreliable;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.sendUnreliable(new Uint8Array([1, 2, 3]));

  assert.equal(unreliable.sent.length, 1);
  assert.deepEqual(Array.from(unreliable.sent[0]), [1, 2, 3]);
});

test("GameClient sends reliable unordered datagrams through the optional channel", () => {
  const reliableUnordered = {
    maxDatagramBytes: 1256,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.reliableUnordered = reliableUnordered;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.sendReliableUnordered(new Uint8Array([4, 5, 6]));

  assert.equal(reliableUnordered.sent.length, 1);
  assert.deepEqual(Array.from(reliableUnordered.sent[0]), [4, 5, 6]);
});

test("GameClient sends reliable ordered datagrams through the optional channel", () => {
  const reliableOrdered = {
    maxDatagramBytes: 1254,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.reliableOrdered = reliableOrdered;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.sendReliableOrdered(new Uint8Array([7, 8, 9]));

  assert.equal(reliableOrdered.sent.length, 1);
  assert.deepEqual(Array.from(reliableOrdered.sent[0]), [7, 8, 9]);
});

test("GameClient encodes and sends reliable unordered commands through the datagram lane", () => {
  const reliableUnordered = {
    maxDatagramBytes: 1256,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.reliableUnordered = reliableUnordered;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.sendReliableUnorderedCommand({ bytes: [1, 2, 3, 4] });

  assert.equal(reliableUnordered.sent.length, 1);
  assert.deepEqual(Array.from(reliableUnordered.sent[0]), [1, 2, 3, 4]);
});

test("GameClient encodes and sends reliable ordered commands through the datagram lane", () => {
  const reliableOrdered = {
    maxDatagramBytes: 1254,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.reliableOrdered = reliableOrdered;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.sendReliableOrderedCommand({ bytes: [4, 3, 2, 1] });

  assert.equal(reliableOrdered.sent.length, 1);
  assert.deepEqual(Array.from(reliableOrdered.sent[0]), [4, 3, 2, 1]);
});

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function uint16(v) {
  return v & 0xffff;
}

function emptyAckMask() {
  return [0, 0, 0, 0];
}

function hasAckMaskBit(mask, bit) {
  const word = Math.floor(bit / 32);
  const shift = bit % 32;
  return (mask[word] & ((1 << shift) >>> 0)) !== 0;
}

function encodeDatagramPacket(packet) {
  let size = 21;
  if ((packet.flags & 1) === 0) {
    size += 1;
    if (packet.lane === 2) {
      size += 2;
    } else if (packet.lane === 3) {
      size += 4;
    } else if (packet.lane === 4) {
      size += 8;
    }
    size += packet.payload?.byteLength ?? 0;
  }
  const out = new Uint8Array(size);
  const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
  view.setUint16(0, packet.packetSeq, false);
  view.setUint16(2, packet.ackSeq, false);
  for (let i = 0; i < 4; i++) {
    view.setUint32(4 + i * 4, packet.ackMask?.[i] ?? 0, false);
  }
  view.setUint8(20, packet.flags);
  if ((packet.flags & 1) !== 0) {
    return out;
  }
  let offset = 21;
  view.setUint8(offset, packet.lane);
  offset += 1;
  if (packet.lane === 2) {
    view.setUint16(offset, packet.messageID, false);
    offset += 2;
  } else if (packet.lane === 3) {
    view.setUint16(offset, packet.messageID, false);
    offset += 2;
    view.setUint16(offset, packet.orderedSeq, false);
    offset += 2;
  } else if (packet.lane === 4) {
    view.setBigUint64(offset, packet.stateToken ?? 0n, false);
    offset += 8;
  }
  if (packet.payload) {
    out.set(packet.payload, offset);
  }
  return out;
}

function decodeDatagramPacket(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const packet = {
    packetSeq: view.getUint16(0, false),
    ackSeq: view.getUint16(2, false),
    ackMask: [
      view.getUint32(4, false),
      view.getUint32(8, false),
      view.getUint32(12, false),
      view.getUint32(16, false),
    ],
    flags: view.getUint8(20),
  };
  if ((packet.flags & 1) !== 0) {
    return packet;
  }
  let offset = 21;
  packet.lane = view.getUint8(offset);
  offset += 1;
  if (packet.lane === 2) {
    packet.messageID = view.getUint16(offset, false);
    offset += 2;
  } else if (packet.lane === 3) {
    packet.messageID = view.getUint16(offset, false);
    offset += 2;
    packet.orderedSeq = view.getUint16(offset, false);
    offset += 2;
  } else if (packet.lane === 4) {
    packet.stateToken = view.getBigUint64(offset, false);
    offset += 8;
  }
  packet.payload = bytes.slice(offset);
  return packet;
}

class MockReadableQueue {
  constructor() {
    this.items = [];
    this.waiters = [];
  }

  push(value) {
    if (this.waiters.length > 0) {
      this.waiters.shift()({ value, done: false });
      return;
    }
    this.items.push(value);
  }

  getReader() {
    return {
      read: () => {
        if (this.items.length > 0) {
          return Promise.resolve({ value: this.items.shift(), done: false });
        }
        return new Promise((resolve) => this.waiters.push(resolve));
      },
      releaseLock() {},
    };
  }
}

class MockWritableQueue {
  constructor(target) {
    this.target = target;
  }

  getWriter() {
    return {
      write: async (value) => {
        this.target.push(new Uint8Array(value));
      },
      releaseLock() {},
    };
  }
}

class FakeWebTransport {
  static instances = [];

  constructor() {
    this.datagramWrites = [];
    this.streamWrites = [];
    this.datagramReadable = new MockReadableQueue();
    this.streamReadable = new MockReadableQueue();
    this.datagrams = {
      readable: this.datagramReadable,
      writable: new MockWritableQueue(this.datagramWrites),
    };
    this.ready = Promise.resolve();
    this.closed = new Promise((resolve) => {
      this._resolveClosed = resolve;
    });
    FakeWebTransport.instances.push(this);
  }

  async createBidirectionalStream() {
    return {
      readable: this.streamReadable,
      writable: new MockWritableQueue(this.streamWrites),
    };
  }

  close(info = { closeCode: 0, reason: "" }) {
    this.closeInfo = info;
    this._resolveClosed?.(info);
  }
}

class BlockingWritableQueue {
  getWriter() {
    return {
      write: () => new Promise(() => {}),
      releaseLock() {},
    };
  }
}

class BlockingStreamWriteWebTransport extends FakeWebTransport {
  async createBidirectionalStream() {
    return {
      readable: this.streamReadable,
      writable: new BlockingWritableQueue(),
    };
  }
}

test("WebTransport reliable ordered datagrams encode lane metadata and order sequence", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    channel.reliableOrdered.send(new Uint8Array([1]));
    channel.reliableOrdered.send(new Uint8Array([2]));
    await delay(30);

    assert.equal(transport.datagramWrites.length, 2);
    const first = decodeDatagramPacket(transport.datagramWrites[0]);
    const second = decodeDatagramPacket(transport.datagramWrites[1]);
    assert.equal(first.lane, 3);
    assert.equal(first.messageID, 0);
    assert.equal(first.orderedSeq, 0);
    assert.deepEqual(Array.from(first.payload), [1]);
    assert.equal(second.lane, 3);
    assert.equal(second.messageID, 1);
    assert.equal(second.orderedSeq, 1);
    assert.deepEqual(Array.from(second.payload), [2]);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport reliable unordered datagrams resend when not acked", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    channel.reliableUnordered.send(new Uint8Array([9]));
    await delay(110);

    assert.ok(transport.datagramWrites.length >= 2);
    const first = decodeDatagramPacket(transport.datagramWrites[0]);
    const second = decodeDatagramPacket(transport.datagramWrites[1]);
    assert.equal(first.lane, 2);
    assert.equal(second.lane, 2);
    assert.equal(first.messageID, second.messageID);
    assert.deepEqual(Array.from(first.payload), [9]);
    assert.deepEqual(Array.from(second.payload), [9]);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport datagram protocol emits ack-only packets for received datagrams", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 1,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 1,
      payload: new Uint8Array([5]),
    }));
    await delay(20);

    assert.ok(transport.datagramWrites.length >= 1);
    const ack = decodeDatagramPacket(transport.datagramWrites[0]);
    assert.equal(ack.flags, 1);
    assert.equal(uint16(ack.ackSeq), 1);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport stream sends piggyback eventual ACK state", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const client = new GameClient({
      entityManager: { applyUpdate() {}, applyCompactUpdate() {}, get() { return undefined; } },
      decode: (bytes) => bytes,
      encode: (cmd) => new Uint8Array(cmd.bytes),
      encodePacket: (frames) => packetBytes(frames),
      createChannel,
    });
    client.connect({ transport: "webtransport", url: "https://example.test", eventualAckIntervalMs: 1000 });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 7,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 4,
      stateToken: 1n,
      payload: framedBatchBytes([new Uint8Array([1])]),
    }));
    await delay(10);

    client.send({ bytes: [9, 8, 7] });
    await flushMicrotasks();
    await delay(5);

    assert.equal(transport.datagramWrites.length, 0);
    assert.equal(transport.streamWrites.length, 1);
    const streamPayload = decodeReliableFrame(transport.streamWrites[0]);
    assert.deepEqual(Array.from(streamPayload.slice(0, 5)), [0x00, 0x4f, 0x47, 0x53, 0x02]);
    const view = new DataView(streamPayload.buffer, streamPayload.byteOffset, streamPayload.byteLength);
    assert.equal(view.getUint16(5, false), 7);
    assert.deepEqual(
      Array.from(streamPayload.slice(23)),
      Array.from(packetBytes([new Uint8Array([9, 8, 7])])),
    );
    client.disconnect();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport standalone ACK interval is configurable", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test", eventualAckIntervalMs: 50 });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 3,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 1,
      payload: new Uint8Array([5]),
    }));
    await delay(20);
    assert.equal(transport.datagramWrites.length, 0);

    await delay(50);
    assert.ok(transport.datagramWrites.length >= 1);
    const ack = decodeDatagramPacket(transport.datagramWrites[0]);
    assert.equal(ack.flags, 1);
    assert.equal(ack.ackSeq, 3);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport reliable datagrams piggyback eventual ACK state", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test", eventualAckIntervalMs: 1000 });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 9,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 4,
      stateToken: 1n,
      payload: framedBatchBytes([new Uint8Array([1])]),
    }));
    await delay(10);

    channel.reliableOrdered.send(new Uint8Array([4]));
    await delay(10);

    assert.equal(transport.datagramWrites.length, 1);
    const packet = decodeDatagramPacket(transport.datagramWrites[0]);
    assert.equal(packet.flags, 0);
    assert.equal(packet.lane, 3);
    assert.equal(packet.ackSeq, 9);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport close writes the Golem close control frame before closing", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    channel.close();
    await delay(20);

    assert.equal(transport.streamWrites.length, 1);
    assert.deepEqual(Array.from(decodeReliableFrame(transport.streamWrites[0])), [0x00, 0x4f, 0x47, 0x53, 0x01]);
    assert.equal(transport.closeInfo.reason, "client disconnect");
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport close proceeds when the close control frame write stalls", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = BlockingStreamWriteWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = BlockingStreamWriteWebTransport.instances.at(-1);

    channel.close();
    await delay(150);

    assert.equal(transport.closeInfo.reason, "client disconnect");
  } finally {
    globalThis.WebTransport = previousWebTransport;
    BlockingStreamWriteWebTransport.instances.length = 0;
  }
});

test("WebTransport datagram protocol does not ack ack-only packets", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 1,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 1,
    }));
    await delay(20);

    assert.equal(transport.datagramWrites.length, 0);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport ack-only packets encode bits beyond the old 32-packet window", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 1,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 1,
      payload: new Uint8Array([1]),
    }));
    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 40,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 1,
      payload: new Uint8Array([2]),
    }));
    await delay(20);

    const ackPackets = transport.datagramWrites
      .map((bytes) => decodeDatagramPacket(bytes))
      .filter((packet) => (packet.flags & 1) !== 0);
    assert.ok(ackPackets.length >= 1);
    const ack = ackPackets.at(-1);
    assert.equal(ack.ackSeq, 40);
    assert.equal(hasAckMaskBit(ack.ackMask, 38), true);
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("GameClient applies ordered datagram state batches through the compact state path", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const applied = [];
    const client = new GameClient({
      entityManager: {
        applyUpdate() {
          throw new Error("ordered compact state should not use applyUpdate");
        },
        applyCompactUpdate(frame) {
          applied.push(frame);
        },
        get() {
          return undefined;
        },
      },
      decode: (bytes) => bytes,
      encode: (cmd) => new Uint8Array(cmd.bytes),
      encodePacket: (frames) => packetBytes(frames),
      createChannel,
    });
    client.connect({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    const frameA = new Uint8Array([1, 2]);
    const frameB = new Uint8Array([3, 4]);
    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 1,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 3,
      messageID: 1,
      orderedSeq: 0,
      payload: framedBatchBytes([frameA, frameB]),
    }));
    await delay(20);

    assert.equal(applied.length, 2);
    assert.deepEqual(Array.from(applied[0]), [1, 2]);
    assert.deepEqual(Array.from(applied[1]), [3, 4]);
    client.disconnect();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("GameClient applies eventual state datagram batches through the compact state path", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const applied = [];
    const client = new GameClient({
      entityManager: {
        applyUpdate() {
          throw new Error("eventual compact state should not use applyUpdate");
        },
        applyCompactUpdate(frame) {
          applied.push(frame);
        },
        get() {
          return undefined;
        },
      },
      decode: (bytes) => bytes,
      encode: (cmd) => new Uint8Array(cmd.bytes),
      encodePacket: (frames) => packetBytes(frames),
      createChannel,
    });
    client.connect({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    const frameA = new Uint8Array([5, 6]);
    const frameB = new Uint8Array([7, 8]);
    transport.datagramReadable.push(encodeDatagramPacket({
      packetSeq: 1,
      ackSeq: 0,
      ackMask: [0, 0, 0, 0],
      flags: 0,
      lane: 4,
      stateToken: 123n,
      payload: framedBatchBytes([frameA, frameB]),
    }));
    await delay(20);

    assert.equal(applied.length, 2);
    assert.deepEqual(Array.from(applied[0]), [5, 6]);
    assert.deepEqual(Array.from(applied[1]), [7, 8]);
    client.disconnect();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("GameClient applies raw unreliable state datagram batches through the compact state path", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const applied = [];
    const client = new GameClient({
      entityManager: {
        applyUpdate() {
          throw new Error("raw compact state should not use applyUpdate");
        },
        applyCompactUpdate(frame) {
          applied.push(frame);
        },
        get() {
          return undefined;
        },
      },
      decode: (bytes) => bytes,
      encode: (cmd) => new Uint8Array(cmd.bytes),
      encodePacket: (frames) => packetBytes(frames),
      createChannel,
    });
    client.connect({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);

    const frameA = new Uint8Array([9, 10]);
    const frameB = new Uint8Array([11, 12]);
    transport.datagramReadable.push(framedBatchBytes([frameA, frameB]));
    await delay(20);

    assert.equal(applied.length, 2);
    assert.deepEqual(Array.from(applied[0]), [9, 10]);
    assert.deepEqual(Array.from(applied[1]), [11, 12]);
    assert.equal(transport.datagramWrites.length, 0);
    client.disconnect();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});
