import type {
  ConnectOptions,
  ConnectOptionsResolver,
  ConnectPlan,
  RealtimeEndpoint,
  TransportKind,
  WebTransportCertificateHash,
} from "./client.js";

const certificateHashSHA256 = "sha-256";
const maxErrorBodyBytes = 512;
const maxConfigBodyBytes = 64 * 1024;
const maxInt32 = 0x7fffffff;

function redactURLForError(value: string): string {
  try {
    const parsed = new URL(value);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return "<invalid-url>";
  }
}

function sanitizeFetchErrorText(value: unknown, endpoint: string): string {
  let text = value instanceof Error ? value.message : String(value);
  text = text
    .replace(
      /\b(?:https?|wss?):\/\/[^\s"'<>]+/gi,
      (url) => redactURLForError(url),
    )
    .replace(
      /([?&][A-Za-z0-9_.~-]+)=([^&#\s"'<>]*)/g,
      "$1=<redacted>",
    )
    .replace(
      /\b(ticket|token|access_token|authorization|auth|session|secret|api_key)\s*([=:])\s*[^\s,;"']+/gi,
      "$1$2<redacted>",
    )
    .replace(
      /(["']?(?:ticket|token|access_token|authorization|auth|session|secret|api_key)["']?\s*:\s*["']?)[^"',}\s]+/gi,
      "$1<redacted>",
    );

  try {
    const parsed = new URL(endpoint);
    const values = [parsed.username, parsed.password];
    for (const value of parsed.searchParams.values()) {
      values.push(value, encodeURIComponent(value));
    }
    for (const raw of parsed.search.slice(1).split("&")) {
      const equals = raw.indexOf("=");
      if (equals >= 0) {
        values.push(raw.slice(equals + 1));
      }
    }
    for (const secret of values) {
      if (secret !== "") {
        text = text.split(secret).join("<redacted>");
      }
    }
  } catch {
    // The endpoint itself is never included when it cannot be parsed.
  }
  return text;
}

/** Wire shape served by golem.Server.RealtimeConfigHandler (hex certificate hash values). */
export interface RealtimeConfig {
  transport: TransportKind;
  url: string;
  serverCertificateHashes?: WebTransportCertificateHash[];
  eventualAckIntervalMs?: number;
  fallback?: RealtimeEndpoint;
}

/**
 * Injectable fetch knobs for fetchRealtimeConfig (defaults to global fetch).
 * `signal` takes precedence over `init.signal` when both are set.
 */
export interface FetchRealtimeConfigOptions {
  fetch?: typeof fetch;
  init?: RequestInit;
  signal?: AbortSignal;
}

type ConnectOptionsMutator = (options: ConnectOptions, query: URLSearchParams) => void;

/**
 * Customizes ConnectOptions built from RealtimeConfig.
 * Prefer withQueryParam / withQuery for auth query params (e.g. token, char_id).
 */
export type ConnectOption = ConnectOptionsMutator;

interface realtimeConfigResponse {
  transport?: unknown;
  url?: unknown;
  serverCertificateHashes?: unknown;
  eventualAckIntervalMs?: unknown;
  fallback?: unknown;
}

/**
 * Loads and decodes a realtime config JSON endpoint.
 * Success bodies are capped at 64 KiB; error snippets at 512 bytes via streaming reads.
 * Never includes caller-supplied query token values in thrown errors.
 *
 * Phaser may return connectPlanFromRealtimeConfig's result from connectionOptions;
 * credentials should remain in the plan's lazy per-candidate resolver.
 */
export async function fetchRealtimeConfig(
  endpoint: string,
  options?: FetchRealtimeConfigOptions,
): Promise<RealtimeConfig> {
  const fetchImpl = options?.fetch ?? globalThis.fetch;
  if (typeof fetchImpl !== "function") {
    throw new Error("golem-js: fetch is not available; pass options.fetch");
  }
  const init = mergeFetchInit(options);
  let response: Response;
  try {
    response = await fetchImpl(endpoint, init);
  } catch (err) {
    const message = sanitizeFetchErrorText(err, endpoint);
    throw new Error(`golem-js: fetching realtime config: ${message}`);
  }
  if (!response.ok) {
    const snippet = await readBoundedText(response, maxErrorBodyBytes);
    if (snippet.length > 0) {
      const safeSnippet = sanitizeFetchErrorText(snippet, endpoint);
      throw new Error(`golem-js: fetching realtime config: status ${response.status} body=${JSON.stringify(safeSnippet)}`);
    }
    throw new Error(`golem-js: fetching realtime config: status ${response.status}`);
  }
  const { text, truncated } = await readBoundedTextWithFlag(response, maxConfigBodyBytes);
  if (truncated) {
    throw new Error(`golem-js: realtime config response exceeds ${maxConfigBodyBytes} bytes`);
  }
  let body: realtimeConfigResponse;
  try {
    body = JSON.parse(text) as realtimeConfigResponse;
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new Error(`golem-js: decoding realtime config: ${message}`);
  }
  return parseRealtimeConfig(body);
}

/** Appends one query parameter to the realtime transport URL. */
export function withQueryParam(key: string, value: string): ConnectOption {
  return (_options, query) => {
    if (key === "") {
      throw new Error("golem-js: query parameter key cannot be empty");
    }
    if (typeof value !== "string") {
      throw new Error("golem-js: query parameter values must be strings");
    }
    query.append(key, value);
  };
}

/** Appends query parameters to the realtime transport URL. Duplicate keys are preserved in order. */
export function withQuery(values: Record<string, string | string[]> | URLSearchParams): ConnectOption {
  return (_options, query) => {
    if (values instanceof URLSearchParams) {
      for (const [key, value] of values.entries()) {
        if (key === "") {
          throw new Error("golem-js: query parameter key cannot be empty");
        }
        query.append(key, value);
      }
      return;
    }
    for (const [key, raw] of Object.entries(values)) {
      if (key === "") {
        throw new Error("golem-js: query parameter key cannot be empty");
      }
      if (Array.isArray(raw)) {
        for (const value of raw) {
          if (typeof value !== "string") {
            throw new Error("golem-js: query parameter values must be strings");
          }
          query.append(key, value);
        }
      } else if (typeof raw !== "string") {
        throw new Error("golem-js: query parameter values must be strings");
      } else {
        query.append(key, raw);
      }
    }
  };
}

/**
 * Converts realtime config JSON into a fresh ConnectOptions value.
 * Preserves existing URL query parameters, appends supplied query values with URL encoding,
 * clones certificate hash arrays, and propagates eventualAckIntervalMs.
 *
 * Phaser: call after fetchRealtimeConfig, cache the returned options, and return them from
 * connectionOptions() so reconnects stay synchronous.
 */
export function connectOptionsFromRealtimeConfig(cfg: RealtimeConfig, ...opts: ConnectOption[]): ConnectOptions {
  switch (cfg.transport) {
    case "websocket":
    case "webtransport":
      break;
    default:
      throw new Error(`golem-js: unsupported transport ${JSON.stringify(cfg.transport)}`);
  }
  if (typeof cfg.url !== "string" || cfg.url.trim() === "") {
    throw new Error("golem-js: realtime config url is required");
  }
  if (cfg.eventualAckIntervalMs !== undefined) {
    assertEventualAckIntervalMs(cfg.eventualAckIntervalMs);
  }

  const options: ConnectOptions = {
    transport: cfg.transport,
    url: cfg.url,
    serverCertificateHashes: cfg.serverCertificateHashes?.map(cloneCertificateHash),
    eventualAckIntervalMs: cfg.eventualAckIntervalMs,
  };

  const query = new URLSearchParams();
  for (const opt of opts) {
    if (opt == null) {
      continue;
    }
    opt(options, query);
  }
  if (query.toString() !== "") {
    let parsed: URL;
    try {
      parsed = new URL(options.url);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      throw new Error(`golem-js: parsing realtime transport URL: ${message}`);
    }
    for (const [key, value] of query.entries()) {
      parsed.searchParams.append(key, value);
    }
    options.url = parsed.toString();
  }
  return options;
}

/**
 * Converts realtime config JSON into an ordered, credential-free connection plan.
 * The optional resolver is invoked lazily before each physical transport dial.
 */
export function connectPlanFromRealtimeConfig(
  cfg: RealtimeConfig,
  resolveOptions?: ConnectOptionsResolver,
): ConnectPlan {
  const primary = connectOptionsFromRealtimeConfig(cfg);
  const candidates: RealtimeEndpoint[] = [primary];
  if (cfg.fallback != null) {
    if (
      primary.transport !== "webtransport" ||
      cfg.fallback.transport !== "websocket"
    ) {
      throw new Error(
        "golem-js: realtime config fallback must be webtransport then websocket",
      );
    }
    const fallback = connectOptionsFromRealtimeConfig({
      transport: cfg.fallback.transport,
      url: cfg.fallback.url,
    });
    candidates.push(fallback);
  }
  return { candidates, resolveOptions };
}

/** Reads a Response body stream up to maxBytes; cancels the reader when the cap is hit. */
export async function readBoundedText(response: Response, maxBytes: number): Promise<string> {
  const { text } = await readBoundedTextWithFlag(response, maxBytes);
  return text;
}

function mergeFetchInit(options?: FetchRealtimeConfigOptions): RequestInit | undefined {
  if (options?.init == null && options?.signal == null) {
    return undefined;
  }
  const init: RequestInit = options?.init ? { ...options.init } : {};
  if (options?.signal != null) {
    init.signal = options.signal;
  }
  return init;
}

function parseRealtimeConfig(body: realtimeConfigResponse): RealtimeConfig {
  if (typeof body.transport !== "string" || body.transport.trim() === "") {
    throw new Error("golem-js: realtime config transport is required");
  }
  if (typeof body.url !== "string" || body.url.trim() === "") {
    throw new Error("golem-js: realtime config url is required");
  }
  const transport = body.transport.trim() as TransportKind;
  switch (transport) {
    case "websocket":
    case "webtransport":
      break;
    default:
      throw new Error(`golem-js: unsupported transport ${JSON.stringify(body.transport)}`);
  }

  const cfg: RealtimeConfig = {
    transport,
    url: body.url,
  };
  if (body.eventualAckIntervalMs !== undefined && body.eventualAckIntervalMs !== null) {
    cfg.eventualAckIntervalMs = assertEventualAckIntervalMs(body.eventualAckIntervalMs);
  }
  if (body.serverCertificateHashes != null) {
    if (!Array.isArray(body.serverCertificateHashes)) {
      throw new Error("golem-js: serverCertificateHashes must be an array");
    }
    cfg.serverCertificateHashes = body.serverCertificateHashes.map((entry, index) => {
      if (entry == null || typeof entry !== "object") {
        throw new Error(`golem-js: serverCertificateHashes[${index}] must be an object`);
      }
      const hash = entry as { algorithm?: unknown; value?: unknown };
      if (typeof hash.algorithm !== "string" || typeof hash.value !== "string") {
        throw new Error(`golem-js: serverCertificateHashes[${index}] requires string algorithm and value`);
      }
      return decodeCertificateHash(hash.algorithm, hash.value);
    });
  }
  if (body.fallback != null) {
    if (typeof body.fallback !== "object" || Array.isArray(body.fallback)) {
      throw new Error("golem-js: realtime config fallback must be an object");
    }
    const fallback = body.fallback as {
      transport?: unknown;
      url?: unknown;
      serverCertificateHashes?: unknown;
      eventualAckIntervalMs?: unknown;
    };
    if (typeof fallback.transport !== "string" || fallback.transport.trim() === "") {
      throw new Error("golem-js: realtime config fallback transport is required");
    }
    if (typeof fallback.url !== "string" || fallback.url.trim() === "") {
      throw new Error("golem-js: realtime config fallback url is required");
    }
    const fallbackTransport = fallback.transport.trim() as TransportKind;
    if (transport !== "webtransport" || fallbackTransport !== "websocket") {
      throw new Error(
        "golem-js: realtime config fallback must be webtransport then websocket",
      );
    }
    if (
      fallback.serverCertificateHashes !== undefined ||
      fallback.eventualAckIntervalMs !== undefined
    ) {
      throw new Error(
        "golem-js: websocket fallback cannot include WebTransport settings",
      );
    }
    cfg.fallback = {
      transport: fallbackTransport,
      url: fallback.url,
    };
  }
  return cfg;
}

function assertEventualAckIntervalMs(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error("golem-js: eventualAckIntervalMs must be a finite integer");
  }
  if (!Number.isInteger(value)) {
    throw new Error("golem-js: eventualAckIntervalMs must be a finite integer");
  }
  if (value < 0 || value > maxInt32) {
    throw new Error("golem-js: eventualAckIntervalMs out of int32 range");
  }
  return value;
}

function decodeCertificateHash(algorithm: string, value: string): WebTransportCertificateHash {
  const normalizedAlgorithm = algorithm.trim().toLowerCase();
  if (normalizedAlgorithm !== certificateHashSHA256) {
    throw new Error(`golem-js: unsupported certificate hash algorithm ${JSON.stringify(algorithm)}`);
  }
  const bytes = hexToBytes(value);
  if (bytes.byteLength !== 32) {
    throw new Error(`golem-js: sha-256 certificate hash length ${bytes.byteLength}, want 32`);
  }
  return { algorithm: normalizedAlgorithm, value };
}

function hexToBytes(hex: string): Uint8Array {
  const normalized = hex.replace(/\s+/g, "");
  if (normalized.length % 2 !== 0) {
    throw new Error("golem-js: certificate hash hex string must have even length");
  }
  if (!/^[0-9a-fA-F]*$/.test(normalized)) {
    throw new Error("golem-js: certificate hash hex string contains non-hex characters");
  }
  const out = new Uint8Array(normalized.length / 2);
  for (let i = 0; i < normalized.length; i += 2) {
    out[i / 2] = Number.parseInt(normalized.slice(i, i + 2), 16);
  }
  return out;
}

function cloneCertificateHash(hash: WebTransportCertificateHash): WebTransportCertificateHash {
  if (typeof hash.value === "string") {
    return { algorithm: hash.algorithm, value: hash.value };
  }
  if (ArrayBuffer.isView(hash.value)) {
    const view = hash.value;
    const copy = new Uint8Array(view.byteLength);
    copy.set(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
    return { algorithm: hash.algorithm, value: copy.buffer };
  }
  const copy = new Uint8Array(hash.value.byteLength);
  copy.set(new Uint8Array(hash.value));
  return { algorithm: hash.algorithm, value: copy.buffer };
}

async function readBoundedTextWithFlag(
  response: Response,
  maxBytes: number,
): Promise<{ text: string; truncated: boolean }> {
  const body = response.body;
  if (body && typeof body.getReader === "function") {
    const reader = body.getReader();
    const chunks: Uint8Array[] = [];
    let total = 0;
    let truncated = false;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        if (!value || value.byteLength === 0) {
          continue;
        }
        if (total + value.byteLength > maxBytes) {
          const take = maxBytes - total;
          if (take > 0) {
            chunks.push(value.subarray(0, take));
            total = maxBytes;
          }
          truncated = true;
          try {
            await reader.cancel();
          } catch {
            // ignore cancel failures
          }
          break;
        }
        chunks.push(value);
        total += value.byteLength;
      }
    } catch {
      return fallbackBoundedText(response, maxBytes);
    } finally {
      try {
        reader.releaseLock();
      } catch {
        // already released/canceled
      }
    }
    return { text: decodeChunks(chunks, total), truncated };
  }
  return fallbackBoundedText(response, maxBytes);
}

async function fallbackBoundedText(
  response: Response,
  maxBytes: number,
): Promise<{ text: string; truncated: boolean }> {
  try {
    if (typeof response.arrayBuffer === "function") {
      const buffer = await response.arrayBuffer();
      const truncated = buffer.byteLength > maxBytes;
      const bytes = new Uint8Array(buffer, 0, Math.min(buffer.byteLength, maxBytes));
      return { text: new TextDecoder().decode(bytes), truncated };
    }
    const text = await response.text();
    if (text.length > maxBytes) {
      return { text: text.slice(0, maxBytes), truncated: true };
    }
    return { text, truncated: false };
  } catch {
    return { text: "", truncated: false };
  }
}

function decodeChunks(chunks: Uint8Array[], total: number): string {
  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(merged);
}
