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

class ControlledChannel {
  constructor(transport) {
    this.transport = transport;
    this.connected = false;
    this.maxMessageBytes = 32000;
    this.sent = [];
    this.closeCalls = 0;
    this._onOpen = null;
    this._onClose = null;
    this._onMessage = null;
  }

  send(data) {
    this.sent.push(data);
  }

  close() {
    this.closeCalls++;
    this.connected = false;
  }

  onOpen(fn) {
    this._onOpen = fn;
  }

  onClose(fn) {
    this._onClose = fn;
  }

  onMessage(fn) {
    this._onMessage = fn;
  }

  open() {
    this.connected = true;
    this._onOpen?.();
  }

  fail(info = { wasClean: false, error: new Error("dial failed") }) {
    this.connected = false;
    this._onClose?.(info);
  }
}

class AwaitableCloseChannel extends ControlledChannel {
  constructor(transport) {
    super(transport);
    this.closeDeferred = deferred();
  }

  close() {
    super.close();
    return this.closeDeferred.promise;
  }

  finishClose() {
    this.closeDeferred.resolve();
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

class FakeErrorEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.message = init.message ?? "";
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

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
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

function fallbackPlan(resolveOptions) {
  return {
    candidates: [
      { transport: "webtransport", url: "https://example.test/api/wt" },
      { transport: "websocket", url: "wss://example.test/api/ws" },
    ],
    resolveOptions,
  };
}

function fallbackClient(createChannel, supportsTransport = () => true) {
  return new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; }, clear() {} },
    decode: (bytes) => bytes,
    encode: () => new Uint8Array(),
    encodePacket: (frames) => packetBytes(frames),
    createChannel,
    supportsTransport,
  });
}

test("GameClient built-in capability detection skips unsupported WebTransport before resolving credentials", () => {
  const previousWebSocket = globalThis.WebSocket;
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebSocket = FakeWebSocket;
  globalThis.WebTransport = undefined;
  FakeWebSocket.instances.length = 0;
  const resolvedTransports = [];
  let connected = 0;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: () => new Uint8Array(),
    encodePacket: (frames) => packetBytes(frames),
  });
  client.onConnect(() => connected++);

  try {
    client.connect(fallbackPlan((endpoint) => {
      resolvedTransports.push(endpoint.transport);
      return { ...endpoint, url: `${endpoint.url}?ticket=ws-ticket` };
    }));

    assert.deepEqual(resolvedTransports, ["websocket"]);
    assert.equal(FakeWebSocket.instances.length, 1);
    FakeWebSocket.instances[0].onopen?.();
    assert.equal(connected, 1);
  } finally {
    client.disconnect();
    globalThis.WebSocket = previousWebSocket;
    globalThis.WebTransport = previousWebTransport;
    FakeWebSocket.instances.length = 0;
  }
});

test("GameClient resolves fresh credentials and falls back after a synchronous WebTransport setup failure", () => {
  const channels = [];
  const resolved = [];
  const client = fallbackClient((options) => {
    if (options.transport === "webtransport") {
      throw new Error(`failed ${options.url}`);
    }
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  let connects = 0;
  let disconnects = 0;
  client.onConnect(() => connects++);
  client.onDisconnect(() => disconnects++);

  client.connect(fallbackPlan((endpoint) => {
    const ticket = `${endpoint.transport}-ticket-${resolved.length + 1}`;
    resolved.push(ticket);
    return { ...endpoint, url: `${endpoint.url}?ticket=${ticket}` };
  }));

  assert.deepEqual(resolved, [
    "webtransport-ticket-1",
    "websocket-ticket-2",
  ]);
  assert.equal(channels.length, 1);
  assert.equal(channels[0].transport, "websocket");
  channels[0].open();
  assert.equal(connects, 1);
  assert.equal(disconnects, 0);
});

test("GameClient does not fall back after a terminal WebTransport setup response", () => {
  for (const status of [401, 403, 426]) {
    const attempts = [];
    const disconnects = [];
    const client = fallbackClient((options) => {
      attempts.push(options.transport);
      const error = new Error(`HTTP status ${status}`);
      error.status = status;
      throw error;
    });
    client.onDisconnect((info) => disconnects.push(info));

    client.connect(fallbackPlan());

    assert.deepEqual(attempts, ["webtransport"]);
    assert.equal(disconnects.length, 1);
    assert.match(disconnects[0].reason, /authorization or revision rejected/);
    assert.doesNotMatch(disconnects[0].error.message, new RegExp(String(status)));
  }
});

test("GameClient does not fall back after a terminal pre-open close", () => {
  const channels = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan());
  channels[0].fail({ wasClean: false, error: { statusCode: 426 } });

  assert.deepEqual(channels.map((channel) => channel.transport), ["webtransport"]);
  assert.equal(channels[0].closeCalls, 1);
  assert.equal(disconnects.length, 1);
  assert.match(disconnects[0].reason, /authorization or revision rejected/);
});

test("GameClient treats a resolver rejection as a final logical failure without falling through", async () => {
  const channels = [];
  const resolverCalls = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    channels.push(options.transport);
    return new ControlledChannel(options.transport);
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan(async (endpoint) => {
    resolverCalls.push(endpoint.transport);
    throw new Error("ticket=resolver-secret");
  }));
  await flushMicrotasks();
  await flushMicrotasks();

  assert.deepEqual(resolverCalls, ["webtransport"]);
  assert.deepEqual(channels, []);
  assert.equal(disconnects.length, 1);
  assert.match(disconnects[0].error.message, /connection options resolution failed/);
  assert.doesNotMatch(disconnects[0].error.message, /resolver-secret|ticket=/);
});

test("GameClient rejects resolver changes to credential-free endpoint metadata", () => {
  const attempts = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    attempts.push(options.transport);
    return new ControlledChannel(options.transport);
  });
  client.onDisconnect((info) => disconnects.push(info));
  const plan = fallbackPlan((endpoint) => ({
    ...endpoint,
    eventualAckIntervalMs: 99,
  }));
  plan.candidates[0].eventualAckIntervalMs = 1;

  client.connect(plan);

  assert.deepEqual(attempts, []);
  assert.equal(disconnects.length, 1);
  assert.match(disconnects[0].reason, /connection options resolution failed/);
});

test("GameClient aborts and ignores a pending resolver after disconnect", async () => {
  const pending = deferred();
  const channels = [];
  const disconnects = [];
  let resolverSignal;
  const client = fallbackClient((options) => {
    channels.push(options.transport);
    return new ControlledChannel(options.transport);
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan((_endpoint, signal) => {
    resolverSignal = signal;
    return pending.promise;
  }));
  assert.equal(resolverSignal.aborted, false);
  client.disconnect();
  assert.equal(resolverSignal.aborted, true);
  pending.resolve({
    transport: "webtransport",
    url: "https://example.test/api/wt?ticket=stale",
  });
  await pending.promise;
  await flushMicrotasks();

  assert.deepEqual(channels, []);
  assert.deepEqual(disconnects, []);
});

test("GameClient falls back on a pre-open close and fences late WebTransport events", () => {
  const channels = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  let connects = 0;
  let disconnects = 0;
  client.onConnect(() => connects++);
  client.onDisconnect(() => disconnects++);

  client.connect(fallbackPlan());
  const webTransport = channels[0];
  webTransport.fail();
  const webSocket = channels[1];
  assert.equal(webTransport.closeCalls, 1);
  webTransport.open();
  webTransport.fail();
  assert.equal(connects, 0);
  assert.equal(disconnects, 0);

  webSocket.open();
  assert.equal(connects, 1);
  assert.equal(disconnects, 0);
});

test("GameClient waits for an awaitable close before resolving fallback credentials", async () => {
  const channels = [];
  const resolverCalls = [];
  const client = fallbackClient((options) => {
    const channel = options.transport === "webtransport"
      ? new AwaitableCloseChannel(options.transport)
      : new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });

  client.connect(fallbackPlan((endpoint) => {
    resolverCalls.push(endpoint.transport);
    return { ...endpoint, url: `${endpoint.url}?ticket=${resolverCalls.length}` };
  }));
  channels[0].fail();

  assert.deepEqual(resolverCalls, ["webtransport"]);
  assert.deepEqual(channels.map((channel) => channel.transport), ["webtransport"]);

  channels[0].finishClose();
  await flushMicrotasks();
  await flushMicrotasks();

  assert.deepEqual(resolverCalls, ["webtransport", "websocket"]);
  assert.deepEqual(channels.map((channel) => channel.transport), [
    "webtransport",
    "websocket",
  ]);
});

test("GameClient emits one clean disconnect for established intentional closure and replacement", () => {
  const channels = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan());
  channels[0].open();
  client.disconnect();
  channels[0].fail({ wasClean: true, reason: "late close" });
  client.disconnect();

  assert.equal(disconnects.length, 1);
  assert.deepEqual(disconnects[0], {
    code: 1000,
    reason: "client disconnect",
    wasClean: true,
  });

  client.connect(fallbackPlan());
  channels[1].open();
  client.connect(fallbackPlan());
  channels[1].fail({ wasClean: true, reason: "late replacement close" });

  assert.equal(disconnects.length, 2);
  assert.equal(disconnects[1].wasClean, true);
  assert.equal(disconnects[1].reason, "client disconnect");
  client.disconnect();
});

test("GameClient applies the fixed five-second WebTransport establishment deadline", () => {
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  const timers = new Map();
  let nextTimer = 1;
  globalThis.setTimeout = (fn, delayMs) => {
    const id = nextTimer++;
    timers.set(id, { fn, delayMs });
    return id;
  };
  globalThis.clearTimeout = (id) => timers.delete(id);
  const channels = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });

  try {
    client.connect(fallbackPlan());
    assert.equal(channels.length, 1);
    const timer = [...timers.values()][0];
    assert.equal(timer.delayMs, 5000);
    timer.fn();
    assert.deepEqual(channels.map((channel) => channel.transport), [
      "webtransport",
      "websocket",
    ]);
    assert.equal(channels[0].closeCalls, 1);
  } finally {
    client.disconnect();
    globalThis.setTimeout = previousSetTimeout;
    globalThis.clearTimeout = previousClearTimeout;
  }
});

test("GameClient never falls back after WebTransport has opened", () => {
  const channels = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan());
  channels[0].open();
  channels[0].fail({ wasClean: false, error: new Error("post-open") });

  assert.deepEqual(channels.map((channel) => channel.transport), ["webtransport"]);
  assert.equal(disconnects.length, 1);
});

test("GameClient keeps an opened WebSocket sticky until the credential-free plan changes", () => {
  const channels = [];
  const resolverCalls = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  const resolver = (endpoint) => {
    resolverCalls.push(endpoint.transport);
    return { ...endpoint, url: `${endpoint.url}?ticket=${resolverCalls.length}` };
  };
  const plan = fallbackPlan(resolver);

  client.connect(plan);
  channels[0].fail();
  channels[1].open();
  channels[1].fail();
  assert.deepEqual(resolverCalls, ["webtransport", "websocket"]);

  client.connect(fallbackPlan(resolver));
  assert.equal(channels[2].transport, "websocket");
  channels[2].open();
  assert.deepEqual(resolverCalls, ["webtransport", "websocket", "websocket"]);

  client.disconnect();
  client.connect(fallbackPlan(resolver));
  assert.equal(channels[3].transport, "websocket");
  channels[3].fail();
  assert.deepEqual(resolverCalls, [
    "webtransport",
    "websocket",
    "websocket",
    "websocket",
  ]);

  client.connect(fallbackPlan(resolver));
  assert.equal(channels[4].transport, "websocket");
  channels[4].fail();

  const changed = fallbackPlan(resolver);
  changed.candidates[0].url = "https://changed.example.test/api/wt";
  changed.candidates[1].url = "wss://changed.example.test/api/ws";
  client.connect(changed);
  assert.equal(channels[5].transport, "webtransport");
  assert.deepEqual(resolverCalls, [
    "webtransport",
    "websocket",
    "websocket",
    "websocket",
    "websocket",
    "webtransport",
  ]);
  client.disconnect();
});

test("GameClient reports one final failure after both candidates fail", () => {
  const channels = [];
  const disconnects = [];
  const client = fallbackClient((options) => {
    const channel = new ControlledChannel(options.transport);
    channels.push(channel);
    return channel;
  });
  client.onDisconnect((info) => disconnects.push(info));

  client.connect(fallbackPlan());
  channels[0].fail();
  channels[1].fail();
  channels[0].open();
  channels[1].fail();

  assert.equal(disconnects.length, 1);
  assert.equal(disconnects[0].wasClean, false);
});

test("GameClient flushes one small command on the next microtask", async () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  client.send({ bytes: 10 });
  assert.equal(channel.sent.length, 0);

  await flushMicrotasks();

  assert.equal(channel.sent.length, 1);
  assert.equal(channel.sent[0].byteLength, packetBytes([new Uint8Array(10)]).byteLength);
});

test("GameClient stream fallback batches send and sendOrdered in call order", async () => {
  const { client, channel } = makeClient();
  client.connect("ws://example.test");

  client.send({ bytes: [1] });
  client.sendOrdered({ bytes: [2] });
  client.send({ bytes: [3] });

  await flushMicrotasks();

  assert.equal(channel.sent.length, 1);
  assert.deepEqual(
    Array.from(channel.sent[0]),
    Array.from(packetBytes([
      new Uint8Array([1]),
      new Uint8Array([2]),
      new Uint8Array([3]),
    ])),
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

test("WebSocket reliable channel accepts a 150 kB snapshot under the 256 KiB cap", () => {
  const previousWebSocket = globalThis.WebSocket;
  globalThis.WebSocket = FakeWebSocket;
  try {
    const channel = createChannel({ transport: "websocket", url: "ws://example.test" });
    const ws = FakeWebSocket.instances.at(-1);

    channel.send(new Uint8Array(150000));

    assert.equal(channel.maxMessageBytes, 256 * 1024);
    assert.equal(ws.sent.length, 1);
    assert.equal(ws.sent[0].byteLength, 150000);
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

test("WebSocket runtime errors never log credential-bearing ErrorEvent messages", () => {
  const previousWebSocket = globalThis.WebSocket;
  const previousErrorEvent = globalThis.ErrorEvent;
  const originalError = console.error;
  const logs = [];
  globalThis.WebSocket = FakeWebSocket;
  globalThis.ErrorEvent = FakeErrorEvent;
  console.error = (message) => logs.push(String(message));
  const secret = "realtime-ticket-secret";
  const url = `wss://example.test/ws?ticket=${secret}`;
  try {
    createChannel({ transport: "websocket", url });
    const ws = FakeWebSocket.instances.at(-1);

    ws.onerror?.(new FakeErrorEvent("error", {
      message: `WebSocket connection to '${url}' failed`,
    }));

    assert.match(logs.join("\n"), /url=wss:\/\/example\.test\/ws/);
    assert.doesNotMatch(logs.join("\n"), /ticket=/);
    assert.doesNotMatch(logs.join("\n"), new RegExp(secret));
  } finally {
    console.error = originalError;
    globalThis.WebSocket = previousWebSocket;
    globalThis.ErrorEvent = previousErrorEvent;
    FakeWebSocket.instances.length = 0;
  }
});

test("WebSocket close reasons redact ticket-bearing URLs and query values", () => {
  const previousWebSocket = globalThis.WebSocket;
  const originalError = console.error;
  const logs = [];
  const secret = "close-reason-ticket-secret";
  globalThis.WebSocket = FakeWebSocket;
  console.error = (message) => logs.push(String(message));
  try {
    const channel = createChannel({ transport: "websocket", url: "wss://example.test/ws" });
    const ws = FakeWebSocket.instances.at(-1);
    let closeInfo;
    channel.onClose((info) => {
      closeInfo = info;
    });

    ws.onclose?.({
      code: 1006,
      reason: `failed wss://example.test/ws?ticket=${secret} token=${secret}`,
      wasClean: false,
    });

    assert.ok(closeInfo);
    assert.doesNotMatch(closeInfo.reason, new RegExp(secret));
    assert.doesNotMatch(logs.join("\n"), new RegExp(secret));
    assert.doesNotMatch(logs.join("\n"), /ticket=close-reason-ticket-secret/);
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

test("GameClient routes send methods to bare reliable datagram commands when supported", async () => {
  const reliableUnordered = {
    maxDatagramBytes: 1176,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const reliableOrdered = {
    maxDatagramBytes: 1174,
    sent: [],
    send(bytes) {
      this.sent.push(bytes);
    },
  };
  const channel = new MockChannel();
  channel.reliableUnordered = reliableUnordered;
  channel.reliableOrdered = reliableOrdered;
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: (cmd) => new Uint8Array(cmd.bytes),
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });
  client.connect("ws://example.test");

  client.send({ bytes: [1, 2, 3, 4] });
  client.sendOrdered({ bytes: [4, 3, 2, 1] });
  await flushMicrotasks();

  assert.equal(channel.sent.length, 0);
  assert.equal(reliableUnordered.sent.length, 1);
  assert.deepEqual(Array.from(reliableUnordered.sent[0]), [1, 2, 3, 4]);
  assert.equal(reliableOrdered.sent.length, 1);
  assert.deepEqual(Array.from(reliableOrdered.sent[0]), [4, 3, 2, 1]);
});

test("GameClient enforces the reliable unordered datagram payload cap without stream fallback", () => {
  const reliableUnordered = {
    maxDatagramBytes: 3,
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

  assert.throws(
    () => client.send({ bytes: [1, 2, 3, 4] }),
    /reliable unordered command size 4 exceeds max 3/,
  );

  assert.equal(reliableUnordered.sent.length, 0);
  assert.equal(channel.sent.length, 0);
});

test("GameClient enforces the reliable ordered datagram payload cap without stream fallback", () => {
  const reliableOrdered = {
    maxDatagramBytes: 2,
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

  assert.throws(
    () => client.sendOrdered({ bytes: [4, 3, 2] }),
    /reliable ordered command size 3 exceeds max 2/,
  );

  assert.equal(reliableOrdered.sent.length, 0);
  assert.equal(channel.sent.length, 0);
});

test("GameClient command sends are no-ops while disconnected", async () => {
  let encodeCalls = 0;
  const channel = new MockChannel();
  const client = new GameClient({
    entityManager: { applyUpdate() {}, get() { return undefined; } },
    decode: (bytes) => bytes,
    encode: () => {
      encodeCalls++;
      return new Uint8Array([1]);
    },
    encodePacket: (frames) => packetBytes(frames),
    createChannel: () => channel,
  });

  client.send({});
  client.sendOrdered({});
  await flushMicrotasks();

  assert.equal(encodeCalls, 0);
  assert.equal(channel.sent.length, 0);
});

test("GameClient does not expose legacy raw or lane-specific send methods", () => {
  const { client } = makeClient();
  for (const method of [
    "sendUnreliable",
    "sendReliableUnordered",
    "sendReliableOrdered",
    "sendReliableUnorderedCommand",
    "sendReliableOrderedCommand",
  ]) {
    assert.equal(method in client, false);
  }
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

class RejectingWebTransport extends FakeWebTransport {
  constructor(url) {
    super();
    this.ready = Promise.reject(new Error(`WebTransport connection to '${url}' failed`));
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

class NeverClosingWebTransport extends FakeWebTransport {
  close(info = { closeCode: 0, reason: "" }) {
    this.closeInfo = info;
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
    const channel = createChannel({
      transport: "webtransport",
      url: "https://example.test",
      eventualAckIntervalMs: 1000,
    });
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

    channel.send(packetBytes([new Uint8Array([9, 8, 7])]));
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
    channel.close();
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport stream receives a 150 kB reliable snapshot", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = FakeWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = FakeWebTransport.instances.at(-1);
    let received;
    channel.onMessage((bytes) => {
      received = bytes;
    });

    transport.streamReadable.push(framedBatchBytes([new Uint8Array(150000)]));
    await delay(10);

    assert.equal(channel.maxMessageBytes, 256 * 1024);
    assert.equal(received?.byteLength, 150000);
    channel.close();
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

    const closeResult = channel.close();
    assert.equal(typeof closeResult.then, "function");
    await closeResult;

    assert.equal(transport.streamWrites.length, 1);
    assert.deepEqual(Array.from(decodeReliableFrame(transport.streamWrites[0])), [0x00, 0x4f, 0x47, 0x53, 0x01]);
    assert.equal(transport.closeInfo.reason, "client disconnect");
  } finally {
    globalThis.WebTransport = previousWebTransport;
    FakeWebTransport.instances.length = 0;
  }
});

test("WebTransport awaitable close is bounded when transport.closed never settles", async () => {
  const previousWebTransport = globalThis.WebTransport;
  globalThis.WebTransport = NeverClosingWebTransport;
  try {
    const channel = createChannel({ transport: "webtransport", url: "https://example.test" });
    await delay(5);
    const transport = NeverClosingWebTransport.instances.at(-1);

    let timeout;
    try {
      await Promise.race([
        channel.close(),
        new Promise((_, reject) => {
          timeout = setTimeout(
            () => reject(new Error("bounded WebTransport close did not complete")),
            750,
          );
        }),
      ]);
    } finally {
      clearTimeout(timeout);
    }

    assert.equal(transport.closeInfo.reason, "client disconnect");
  } finally {
    globalThis.WebTransport = previousWebTransport;
    NeverClosingWebTransport.instances.length = 0;
  }
});

test("WebTransport runtime errors are sanitized before logs and disconnect callbacks", async () => {
  const previousWebTransport = globalThis.WebTransport;
  const originalError = console.error;
  const logs = [];
  const secret = "realtime-ticket-secret";
  const url = `https://example.test/wt?ticket=${secret}`;
  globalThis.WebTransport = RejectingWebTransport;
  console.error = (message) => logs.push(String(message));
  try {
    const channel = createChannel({ transport: "webtransport", url });
    let closeInfo;
    channel.onClose((info) => {
      closeInfo = info;
    });

    await delay(5);

    assert.ok(closeInfo);
    assert.match(String(closeInfo.error), /webtransport connect failed/);
    assert.doesNotMatch(String(closeInfo.error), /ticket=/);
    assert.doesNotMatch(String(closeInfo.error), new RegExp(secret));
    assert.match(logs.join("\n"), /url=https:\/\/example\.test\/wt/);
    assert.doesNotMatch(logs.join("\n"), /ticket=/);
    assert.doesNotMatch(logs.join("\n"), new RegExp(secret));
  } finally {
    console.error = originalError;
    globalThis.WebTransport = previousWebTransport;
    RejectingWebTransport.instances.length = 0;
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
