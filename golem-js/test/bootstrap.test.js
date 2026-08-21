import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import { ReadableStream } from "node:stream/web";

import {
  connectPlanFromRealtimeConfig,
  connectOptionsFromRealtimeConfig,
  fetchRealtimeConfig,
  readBoundedText,
  withQuery,
  withQueryParam,
} from "../dist/index.js";

function sha256Hex(input) {
  return createHash("sha256").update(input).digest("hex");
}

function streamResponse(status, text, { maxChunk = 32 } = {}) {
  const bytes = new TextEncoder().encode(text);
  let offset = 0;
  let canceled = false;
  return {
    ok: status >= 200 && status < 300,
    status,
    body: new ReadableStream({
      pull(controller) {
        if (canceled || offset >= bytes.byteLength) {
          controller.close();
          return;
        }
        const end = Math.min(offset + maxChunk, bytes.byteLength);
        controller.enqueue(bytes.subarray(offset, end));
        offset = end;
      },
      cancel() {
        canceled = true;
      },
    }),
    async arrayBuffer() {
      return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
    },
    async text() {
      return text;
    },
    async json() {
      return JSON.parse(text);
    },
  };
}

test("fetchRealtimeConfig decodes success JSON", async () => {
  const hash = sha256Hex("cert");
  const body = JSON.stringify({
    transport: "webtransport",
    url: "https://example.com/wt",
    serverCertificateHashes: [{ algorithm: "sha-256", value: hash }],
    eventualAckIntervalMs: 25,
  });
  const cfg = await fetchRealtimeConfig("https://example.test/api/realtime-config", {
    fetch: async (url, init) => {
      assert.equal(url, "https://example.test/api/realtime-config");
      assert.equal(init, undefined);
      return streamResponse(200, body);
    },
  });
  assert.equal(cfg.transport, "webtransport");
  assert.equal(cfg.url, "https://example.com/wt");
  assert.equal(cfg.eventualAckIntervalMs, 25);
  assert.equal(cfg.serverCertificateHashes?.length, 1);
  assert.equal(cfg.serverCertificateHashes[0].value, hash);
});

test("fetchRealtimeConfig decodes a WebSocket fallback and builds an ordered plan", async () => {
  const hash = sha256Hex("cert");
  const cfg = await fetchRealtimeConfig("https://example.test/api/realtime-config", {
    fetch: async () => streamResponse(200, JSON.stringify({
      transport: "webtransport",
      url: "https://example.com/wt",
      serverCertificateHashes: [{ algorithm: "sha-256", value: hash }],
      eventualAckIntervalMs: 25,
      fallback: {
        transport: "websocket",
        url: "wss://example.com/api/ws",
      },
    })),
  });

  assert.deepEqual(cfg.fallback, {
    transport: "websocket",
    url: "wss://example.com/api/ws",
  });
  const resolver = async (endpoint) => endpoint;
  const plan = connectPlanFromRealtimeConfig(cfg, resolver);
  assert.equal(plan.resolveOptions, resolver);
  assert.equal(plan.candidates.length, 2);
  assert.equal(plan.candidates[0].transport, "webtransport");
  assert.equal(plan.candidates[0].eventualAckIntervalMs, 25);
  assert.equal(plan.candidates[0].serverCertificateHashes.length, 1);
  assert.deepEqual(plan.candidates[1], {
    transport: "websocket",
    url: "wss://example.com/api/ws",
    serverCertificateHashes: undefined,
    eventualAckIntervalMs: undefined,
  });

  cfg.url = "https://mutated.example/wt";
  cfg.serverCertificateHashes[0].value = "00".repeat(32);
  cfg.fallback.url = "wss://mutated.example/ws";
  assert.equal(plan.candidates[0].url, "https://example.com/wt");
  assert.equal(plan.candidates[0].serverCertificateHashes[0].value, hash);
  assert.equal(plan.candidates[1].url, "wss://example.com/api/ws");
});

test("fetchRealtimeConfig rejects invalid fallback shapes and WebTransport settings", async () => {
  const invalidFallbacks = [
    "wss://example.com/ws",
    { transport: "websocket", url: "" },
    { transport: "webtransport", url: "https://example.com/wt2" },
    { transport: "websocket", url: "wss://example.com/ws", eventualAckIntervalMs: 1 },
    { transport: "websocket", url: "wss://example.com/ws", serverCertificateHashes: [] },
  ];
  for (const fallback of invalidFallbacks) {
    await assert.rejects(
      () => fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () => streamResponse(200, JSON.stringify({
          transport: "webtransport",
          url: "https://example.com/wt",
          fallback,
        })),
      }),
      /fallback/,
    );
  }

  assert.throws(
    () => connectPlanFromRealtimeConfig({
      transport: "websocket",
      url: "wss://example.com/ws",
      fallback: { transport: "websocket", url: "wss://example.com/other" },
    }),
    /fallback must be webtransport then websocket/,
  );
});

test("fetchRealtimeConfig rejects non-2xx with bounded body detail", async () => {
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/missing", {
        fetch: async () => streamResponse(404, "missing realtime config"),
      }),
    (err) => {
      assert.match(String(err), /status 404/);
      assert.match(String(err), /missing realtime config/);
      return true;
    },
  );
});

test("readBoundedText streams and cancels at the limit", async () => {
  const payload = "x".repeat(1000);
  const response = streamResponse(200, payload, { maxChunk: 64 });
  const text = await readBoundedText(response, 100);
  assert.equal(text.length, 100);
  assert.equal(text, "x".repeat(100));
});

test("fetchRealtimeConfig rejects oversized success bodies", async () => {
  const huge = `{"transport":"websocket","url":"ws://example.com/ws","pad":"${"y".repeat(70 * 1024)}"}`;
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () => streamResponse(200, huge, { maxChunk: 4096 }),
      }),
    /exceeds 65536 bytes/,
  );
});

test("fetchRealtimeConfig rejects malformed JSON", async () => {
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/bad", {
        fetch: async () => streamResponse(200, "{"),
      }),
    /decoding realtime config/,
  );
});

test("fetchRealtimeConfig rejects invalid certificate hashes, transports, and ack values", async () => {
  const hash = sha256Hex("cert");
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () =>
          streamResponse(200, JSON.stringify({
            transport: "webtransport",
            url: "https://example.com/wt",
            serverCertificateHashes: [{ algorithm: "sha-256", value: "nothex" }],
          })),
      }),
    /non-hex/,
  );
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () =>
          streamResponse(200, JSON.stringify({
            transport: "webtransport",
            url: "https://example.com/wt",
            serverCertificateHashes: [{ algorithm: "sha-512", value: hash }],
          })),
      }),
    /unsupported certificate hash algorithm/,
  );
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () => streamResponse(200, JSON.stringify({ transport: "bad", url: "https://example.com/wt" })),
      }),
    /unsupported transport/,
  );
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        fetch: async () => streamResponse(200, JSON.stringify({ transport: "websocket", url: "" })),
      }),
    /url is required/,
  );
  for (const ack of [-1, 1.5, 2147483648]) {
    await assert.rejects(
      () =>
        fetchRealtimeConfig("https://example.test/cfg", {
          fetch: async () =>
            streamResponse(200, JSON.stringify({
              transport: "websocket",
              url: "ws://example.com/ws",
              eventualAckIntervalMs: ack,
            })),
        }),
      /eventualAckIntervalMs/,
    );
  }
  assert.throws(
    () =>
      connectOptionsFromRealtimeConfig({
        transport: "websocket",
        url: "ws://example.com/ws",
        eventualAckIntervalMs: Number.POSITIVE_INFINITY,
      }),
    /eventualAckIntervalMs/,
  );
});

test("fetchRealtimeConfig accepts absent or zero eventualAckIntervalMs", async () => {
  const absent = await fetchRealtimeConfig("https://example.test/cfg", {
    fetch: async () => streamResponse(200, JSON.stringify({ transport: "websocket", url: "ws://example.com/ws" })),
  });
  assert.equal(absent.eventualAckIntervalMs, undefined);
  const zero = await fetchRealtimeConfig("https://example.test/cfg", {
    fetch: async () =>
      streamResponse(200, JSON.stringify({
        transport: "websocket",
        url: "ws://example.com/ws",
        eventualAckIntervalMs: 0,
      })),
  });
  assert.equal(zero.eventualAckIntervalMs, 0);
  const options = connectOptionsFromRealtimeConfig(zero);
  assert.equal(options.eventualAckIntervalMs, 0);
});

test("fetchRealtimeConfig forwards AbortSignal with precedence over init.signal", async () => {
  const controller = new AbortController();
  const ignored = new AbortController();
  await fetchRealtimeConfig("https://example.test/cfg", {
    signal: controller.signal,
    init: { signal: ignored.signal, headers: { "x-test": "1" } },
    fetch: async (_url, init) => {
      assert.equal(init.signal, controller.signal);
      assert.equal(init.headers["x-test"], "1");
      return streamResponse(200, JSON.stringify({ transport: "websocket", url: "ws://example.com/ws" }));
    },
  });
  controller.abort();
  await assert.rejects(
    () =>
      fetchRealtimeConfig("https://example.test/cfg", {
        signal: AbortSignal.abort(),
        fetch: async (_url, init) => {
          assert.equal(init.signal.aborted, true);
          throw init.signal.reason ?? new Error("aborted");
        },
      }),
    /aborted|AbortError|fetching realtime config/,
  );
});

test("fetchRealtimeConfig surfaces fetch failures without leaking query tokens", async () => {
  const endpoint = "https://example.test/cfg?token=super-secret";
  await assert.rejects(
    () =>
      fetchRealtimeConfig(endpoint, {
        fetch: async () => {
          throw new TypeError(`network down for ${endpoint} token=super-secret`);
        },
      }),
    (err) => {
      const text = String(err);
      assert.match(text, /network down/);
      assert.doesNotMatch(text, /super-secret/);
      assert.doesNotMatch(text, /token=super-secret/);
      return true;
    },
  );
});

test("fetchRealtimeConfig redacts endpoint query values echoed by an error body", async () => {
  const endpoint = "https://example.test/cfg?ticket=body-secret";
  await assert.rejects(
    () => fetchRealtimeConfig(endpoint, {
      fetch: async () => streamResponse(
        502,
        `upstream rejected ${endpoint}; {"ticket":"body-secret"}`,
      ),
    }),
    (err) => {
      const text = String(err);
      assert.match(text, /status 502/);
      assert.match(text, /upstream rejected/);
      assert.doesNotMatch(text, /body-secret/);
      assert.doesNotMatch(text, /ticket=body-secret/);
      return true;
    },
  );
});

test("connectOptionsFromRealtimeConfig preserves query, encodes values, and allows duplicates", () => {
  const hash = sha256Hex("cert");
  const cfg = {
    transport: "webtransport",
    url: "https://example.com/wt?room=a",
    serverCertificateHashes: [{ algorithm: "sha-256", value: hash }],
    eventualAckIntervalMs: 50,
  };
  const options = connectOptionsFromRealtimeConfig(
    cfg,
    withQueryParam("token", "a b"),
    withQuery({ char_id: "7", token: ["extra", "third"] }),
  );
  assert.equal(options.transport, "webtransport");
  assert.equal(options.eventualAckIntervalMs, 50);
  const parsed = new URL(options.url);
  assert.equal(parsed.searchParams.get("room"), "a");
  assert.equal(parsed.searchParams.get("char_id"), "7");
  assert.deepEqual(parsed.searchParams.getAll("token"), ["a b", "extra", "third"]);
  assert.match(options.url, /token=a\+b|token=a%20b/);
});

test("connectOptionsFromRealtimeConfig clones hashes for immutability", () => {
  const hash = sha256Hex("cert");
  const hashes = [{ algorithm: "sha-256", value: hash }];
  const cfg = {
    transport: "websocket",
    url: "ws://example.com/ws",
    serverCertificateHashes: hashes,
  };
  const options = connectOptionsFromRealtimeConfig(cfg);
  hashes[0].value = "mutated";
  hashes.pop();
  assert.equal(options.serverCertificateHashes?.length, 1);
  assert.equal(options.serverCertificateHashes[0].value, hash);
});

test("withQuery runtime-validates array values as strings", () => {
  assert.throws(
    () =>
      connectOptionsFromRealtimeConfig(
        { transport: "websocket", url: "ws://example.com/ws" },
        withQuery({ token: [1] }),
      ),
    /query parameter values must be strings/,
  );
});

test("connectOptionsFromRealtimeConfig rejects unsupported transport", () => {
  assert.throws(
    () => connectOptionsFromRealtimeConfig({ transport: "bad", url: "https://example.com/wt" }),
    /unsupported transport/,
  );
});

test("withQueryParam rejects empty keys", () => {
  assert.throws(
    () => connectOptionsFromRealtimeConfig({ transport: "websocket", url: "ws://example.com/ws" }, withQueryParam("", "x")),
    /query parameter key cannot be empty/,
  );
});
