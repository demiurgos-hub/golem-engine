import { PbReader } from "./codec.js";

const maxReliableMessageBytes = 256 * 1024;
const maxWebSocketPayloadBytes = maxReliableMessageBytes;
const maxWebTransportDatagramBytes = 1200;
const datagramAckMaskWordCount = 4;
const datagramAckMaskWordBytes = 4;
const datagramAckMaskBytes = datagramAckMaskWordCount * datagramAckMaskWordBytes;
const datagramPacketHeaderBytes = 2 + 2 + datagramAckMaskBytes + 1;
const datagramLaneHeaderBytes = 1;
const datagramReliableMessageIDBytes = 2;
const datagramReliableOrderedSequenceBytes = 2;
const datagramEventualStateTokenBytes = 8;
const maxUnreliableDatagramPayloadBytes =
  maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes;
const maxReliableUnorderedDatagramPayloadBytes =
  maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramReliableMessageIDBytes;
const maxReliableOrderedDatagramPayloadBytes =
  maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramReliableMessageIDBytes - datagramReliableOrderedSequenceBytes;
const maxEventualStateDatagramPayloadBytes =
  maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramEventualStateTokenBytes;
const datagramPacketAckWindow = datagramAckMaskWordCount * 32;
const datagramReliableRetryBaseDelayMs = 75;
const datagramReliableRetryMaxDelayMs = 400;
const datagramReliableRetryLimit = 8;
const datagramReliableMessageTTLms = 3000;
const datagramReliableOrderedGapTimeoutMs = 3000;
const datagramAckCoalesceDelayMs = 1;
const datagramSchedulerIntervalMs = 2;
const datagramResendBudgetPerWake = 16;
const clientCloseControlFrame = new Uint8Array([0x00, 0x4f, 0x47, 0x53, 0x01]);
const clientReliableAckControlFrame = new Uint8Array([0x00, 0x4f, 0x47, 0x53, 0x02]);
const clientReliableAckControlHeaderBytes = clientReliableAckControlFrame.byteLength + 2 + datagramAckMaskBytes;
const webTransportCloseFrameTimeoutMs = 100;
const webTransportCloseCompletionTimeoutMs = 250;
const webTransportEstablishmentTimeoutMs = 5000;
const suppressTransportLogs = Symbol("golem-js.suppressTransportLogs");

const datagramFlagAckOnly = 1;

type DatagramLane = 1 | 2 | 3 | 4;
type AckMask = [number, number, number, number];

const datagramLaneUnreliable: DatagramLane = 1;
const datagramLaneReliableUnordered: DatagramLane = 2;
const datagramLaneReliableOrdered: DatagramLane = 3;
const datagramLaneEventualState: DatagramLane = 4;

const scheduleMicrotask =
  typeof queueMicrotask === "function"
    ? queueMicrotask
    : (fn: () => void) => Promise.resolve().then(fn);

function waitForSettlement(
  promise: Promise<unknown>,
  timeoutMs: number,
): Promise<void> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      resolve();
    };
    const timeout = setTimeout(finish, timeoutMs);
    void promise.then(finish, finish);
  });
}

function varintSize(v: number): number {
  let size = 1;
  while (v > 0x7f) {
    v >>>= 7;
    size++;
  }
  return size;
}

function clientPacketEntrySize(frame: Uint8Array): number {
  return 1 + varintSize(frame.byteLength) + frame.byteLength;
}

function toUint8Array(data: ArrayBuffer | ArrayBufferView): Uint8Array {
  if (ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength));
  }
  return new Uint8Array(data.slice(0));
}

function toArrayBuffer(data: ArrayBuffer | ArrayBufferView | string): ArrayBuffer {
  const src = typeof data === "string" ? hexToBytes(data) : toUint8Array(data);
  const out = new ArrayBuffer(src.byteLength);
  new Uint8Array(out).set(src);
  return out;
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

function normalizeCertificateHash(hash: WebTransportCertificateHash): WebTransportHash {
  const algorithm = hash.algorithm.trim().toLowerCase();
  if (algorithm !== "sha-256") {
    throw new Error(`golem-js: unsupported certificate hash algorithm ${JSON.stringify(hash.algorithm)}`);
  }
  const value = toArrayBuffer(hash.value);
  if (value.byteLength !== 32) {
    throw new Error(`golem-js: sha-256 certificate hash length ${value.byteLength}, want 32`);
  }
  return { algorithm, value };
}

function writeReliableFrame(frame: Uint8Array): Uint8Array {
  if (frame.byteLength > maxReliableMessageBytes) {
    throw new Error(
      `golem-js: reliable message size ${frame.byteLength} exceeds max reliable message ${maxReliableMessageBytes}`,
    );
  }
  const out = new Uint8Array(4 + frame.byteLength);
  const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
  view.setUint32(0, frame.byteLength, false);
  out.set(frame, 4);
  return out;
}

function decodeReliableBatchPayload(bytes: Uint8Array): Uint8Array[] {
  const frames: Uint8Array[] = [];
  let offset = 0;
  while (bytes.byteLength - offset >= 4) {
    const view = new DataView(bytes.buffer, bytes.byteOffset + offset, 4);
    const frameLen = view.getUint32(0, false);
    if (frameLen > maxReliableMessageBytes) {
      throw new Error(
        `golem-js: reliable frame length ${frameLen} exceeds max reliable message ${maxReliableMessageBytes}`,
      );
    }
    if (bytes.byteLength - offset - 4 < frameLen) {
      throw new Error("golem-js: incomplete reliable batch payload");
    }
    frames.push(bytes.slice(offset + 4, offset + 4 + frameLen));
    offset += 4 + frameLen;
  }
  if (offset !== bytes.byteLength) {
    throw new Error("golem-js: incomplete reliable batch payload");
  }
  return frames;
}

function isReliableBatchPayload(bytes: Uint8Array): boolean {
  if (bytes.byteLength === 0) {
    return false;
  }
  try {
    decodeReliableBatchPayload(bytes);
    return true;
  } catch {
    return false;
  }
}

class ReliableFrameReader {
  private _buffer = new Uint8Array(0);

  push(chunk: Uint8Array): Uint8Array[] {
    if (chunk.byteLength === 0) {
      return [];
    }
    const merged = new Uint8Array(this._buffer.byteLength + chunk.byteLength);
    merged.set(this._buffer, 0);
    merged.set(chunk, this._buffer.byteLength);
    this._buffer = merged;

    const frames: Uint8Array[] = [];
    let offset = 0;
    while (this._buffer.byteLength - offset >= 4) {
      const view = new DataView(this._buffer.buffer, this._buffer.byteOffset + offset, 4);
      const frameLen = view.getUint32(0, false);
      if (frameLen > maxReliableMessageBytes) {
        throw new Error(
          `golem-js: reliable frame length ${frameLen} exceeds max reliable message ${maxReliableMessageBytes}`,
        );
      }
      if (this._buffer.byteLength - offset - 4 < frameLen) {
        break;
      }
      frames.push(this._buffer.slice(offset + 4, offset + 4 + frameLen));
      offset += 4 + frameLen;
    }
    this._buffer = this._buffer.slice(offset);
    return frames;
  }
}

/**
 * Contract that generated EntityManagers satisfy.
 * GameClient calls applyUpdate for every decoded binary frame.
 */
export interface EntityManagerLike {
  applyUpdate(update: unknown): void;
  applyCompactUpdate?(frame: Uint8Array): void;
  get(entityId: number): unknown;
  /** Optional: drop all entities (fires remove listeners). Called on disconnect. */
  clear?(): void;
}

/**
 * Contract that generated WorldManagers satisfy.
 * GameClient calls applyUpdate for every decoded world data frame.
 */
export interface WorldManagerLike {
  applyUpdate(update: unknown): void;
}

/**
 * Contract that the generated EventManager satisfies.
 * GameClient calls applyRaw for every received server event frame.
 */
export interface EventManagerLike {
  applyRaw(bytes: Uint8Array): void;
}

/**
 * Optional lifecycle hooks that entity subclasses may implement.
 * The EntityManager calls these automatically when present.
 */
export interface EntityLifecycle {
  onSpawn?(): void;
  onRemove?(): void;
}

/** Parsed ServerMessage envelope fields. */
export interface ServerMessage {
  entityUpdate?: Uint8Array;
  worldUpdate?: Uint8Array;
  serverEvent?: Uint8Array;
}

/** Normalized connection-close data shared across transports. */
export interface DisconnectInfo {
  code?: number;
  reason?: string;
  wasClean: boolean;
  error?: unknown;
}

function redactUrl(url: string): string {
  try {
    const parsed = new URL(url);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return "<invalid-url>";
  }
}

class SanitizedTransportError extends Error {
  readonly httpStatus?: number;

  constructor(message: string, httpStatus?: number) {
    super(message);
    this.httpStatus = httpStatus;
  }
}

function reportedHTTPStatus(value: unknown, depth = 0): number | undefined {
  if (depth > 3 || value == null) {
    return undefined;
  }
  if (typeof value === "string") {
    const match = value.match(
      /\b(?:http(?:\/[0-9.]+)?|status(?:\s+code)?|response(?:\s+(?:status|code))?|responded(?:\s+with)?)\s*[:=]?\s*(401|403|426)\b/i,
    );
    return match ? Number(match[1]) : undefined;
  }
  if (typeof value !== "object") {
    return undefined;
  }
  const record = value as Record<string, unknown>;
  for (const key of ["httpStatus", "status", "statusCode", "closeCode", "code"]) {
    const candidate = record[key];
    const status = typeof candidate === "number"
      ? candidate
      : typeof candidate === "string" && /^\d{3}$/.test(candidate)
        ? Number(candidate)
        : undefined;
    if (status === 401 || status === 403 || status === 426) {
      return status;
    }
  }
  for (const key of ["message", "reason", "cause", "error"]) {
    const status = reportedHTTPStatus(record[key], depth + 1);
    if (status !== undefined) {
      return status;
    }
  }
  return undefined;
}

function isTerminalPreOpenFailure(value: unknown): boolean {
  return reportedHTTPStatus(value) !== undefined;
}

function sanitizedTransportError(message: string, source?: unknown): Error {
  return new SanitizedTransportError(
    `golem-js: ${message}`,
    reportedHTTPStatus(source),
  );
}

function sanitizeCredentialText(value: string): string {
  return value
    .replace(
      /\b(?:https?|wss?):\/\/[^\s"'<>]+/gi,
      (url) => redactUrl(url),
    )
    .replace(
      /([?&][A-Za-z0-9_.~-]+)=([^&#\s"'<>]*)/g,
      "$1=<redacted>",
    )
    .replace(
      /\b(ticket|token|access_token|authorization|auth|session|secret|api_key)\s*([=:])\s*[^\s,;"']+/gi,
      "$1$2<redacted>",
    );
}

function sanitizeDisconnectReason(reason: string | undefined): string | undefined {
  return reason == null || reason === ""
    ? reason
    : sanitizeCredentialText(reason);
}

function sanitizeDisconnectInfo(transport: string, info: DisconnectInfo): DisconnectInfo {
  const reason = sanitizeDisconnectReason(info.reason);
  if (
    (info.error == null || info.error instanceof SanitizedTransportError) &&
    reason === info.reason
  ) {
    return info;
  }
  return {
    ...info,
    reason,
    error: info.error == null || info.error instanceof SanitizedTransportError
      ? info.error
      : sanitizedTransportError(`${transport} transport failed`, info.error),
  };
}

function logDisconnect(transport: string, info: DisconnectInfo): void {
  if (info.wasClean && info.error == null) {
    const code = info.code != null ? ` code=${info.code}` : "";
    const reason = info.reason ? ` reason=${info.reason}` : "";
    console.warn(`golem-js: disconnected transport=${transport} was_clean=true${code}${reason}`);
    return;
  }
  const error = info.error instanceof Error ? info.error.message : String(info.error ?? "");
  const code = info.code != null ? ` code=${info.code}` : "";
  const reason = info.reason ? ` reason=${info.reason}` : "";
  console.error(`golem-js: disconnected transport=${transport} was_clean=false${code}${reason} error=${error}`);
}

/** Supported built-in transport kinds for GameClient.connect(). */
export type TransportKind = "websocket" | "webtransport";

/** Browser certificate hash accepted by WebTransport. */
export interface WebTransportCertificateHash {
  algorithm: string;
  value: ArrayBuffer | ArrayBufferView | string;
}

/** Options for the built-in WebTransport adapter. */
export interface WebTransportConnectOptions {
  url: string;
  serverCertificateHashes?: WebTransportCertificateHash[];
  /** Delay before sending standalone eventual-state ACK packets when ACKs are not piggybacked. */
  eventualAckIntervalMs?: number;
}

/** Credential-free realtime endpoint advertised by a Golem server. */
export interface RealtimeEndpoint {
  transport: TransportKind;
  url: string;
  serverCertificateHashes?: WebTransportCertificateHash[];
  /** Delay before sending standalone eventual-state ACK packets when ACKs are not piggybacked. */
  eventualAckIntervalMs?: number;
}

/** Transport-aware connection options for GameClient.connect(). */
export interface ConnectOptions extends RealtimeEndpoint {}

/** Resolves fresh credentials immediately before one physical transport dial. */
export type ConnectOptionsResolver = (
  endpoint: RealtimeEndpoint,
  signal: AbortSignal,
) => ConnectOptions | Promise<ConnectOptions>;

/** Ordered credential-free endpoints for one logical connection attempt. */
export interface ConnectPlan {
  candidates: readonly RealtimeEndpoint[];
  resolveOptions?: ConnectOptionsResolver;
}

/** Every connection input accepted by GameClient.connect(). */
export type ConnectInput = string | ConnectOptions | ConnectPlan;

type InternalConnectOptions = ConnectOptions & {
  [suppressTransportLogs]?: boolean;
};

/** Send-only unreliable lane exposed by transports that support datagrams. */
export interface UnreliableMessageChannel {
  readonly maxDatagramBytes: number;
  send(bytes: Uint8Array): void;
}

/** Send-only reliable unordered datagram lane exposed by transports that support datagrams. */
export interface ReliableUnorderedMessageChannel {
  readonly maxDatagramBytes: number;
  send(bytes: Uint8Array): void;
}

/** Send-only reliable ordered datagram lane exposed by transports that support datagrams. */
export interface ReliableOrderedMessageChannel {
  readonly maxDatagramBytes: number;
  send(bytes: Uint8Array): void;
}

/** Transport-neutral reliable channel used by GameClient. */
export interface ReliableMessageChannel {
  readonly connected: boolean;
  readonly maxMessageBytes: number;
  readonly unreliable?: UnreliableMessageChannel;
  readonly reliableUnordered?: ReliableUnorderedMessageChannel;
  readonly reliableOrdered?: ReliableOrderedMessageChannel;
  close(): void | Promise<void>;
  send(bytes: Uint8Array): void;
  onOpen(fn: () => void): void;
  onMessage(fn: (bytes: Uint8Array) => void): void;
  onUnreliableStateMessage?(fn: (bytes: Uint8Array) => void): void;
  onReliableOrderedMessage?(fn: (bytes: Uint8Array) => void): void;
  onEventualStateMessage?(fn: (bytes: Uint8Array) => void): void;
  onClose(fn: (info: DisconnectInfo) => void): void;
}

/** Configuration accepted by GameClient's constructor. */
export interface GameClientOptions {
  /** Decode a binary EntityUpdate frame from the server. */
  decode: (bytes: Uint8Array) => unknown;
  /** Encode a command object to a ClientMessage binary frame. */
  encode: (cmd: object) => Uint8Array;
  /** Encode one or more ClientMessage frames into a ClientPacket payload. */
  encodePacket: (frames: Uint8Array[]) => Uint8Array;
  /** The generated EntityManager instance. */
  entityManager: EntityManagerLike;
  /** Decode a binary WorldUpdate frame from the server (optional). */
  decodeWorld?: (bytes: Uint8Array) => unknown;
  /** The generated WorldManager instance (optional). */
  worldManager?: WorldManagerLike;
  /** The generated EventManager instance (optional). */
  eventManager?: EventManagerLike;
  /** Build a reliable transport channel for connect(options). */
  createChannel?: (options: ConnectOptions) => ReliableMessageChannel;
  /**
   * Reports whether a custom channel factory can use a transport. When omitted,
   * custom factories are assumed to support both transports and built-in
   * factories check the corresponding browser global.
   */
  supportsTransport?: (transport: TransportKind) => boolean;
}

function isConnectPlan(input: ConnectInput): input is ConnectPlan {
  return typeof input === "object" && input !== null && "candidates" in input;
}

function cloneClientCertificateHash(
  hash: WebTransportCertificateHash,
): WebTransportCertificateHash {
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

function cloneRealtimeEndpoint(endpoint: RealtimeEndpoint): RealtimeEndpoint {
  return {
    transport: endpoint.transport,
    url: endpoint.url,
    serverCertificateHashes: endpoint.serverCertificateHashes?.map(
      cloneClientCertificateHash,
    ),
    eventualAckIntervalMs: endpoint.eventualAckIntervalMs,
  };
}

function validateRealtimeEndpoint(
  endpoint: RealtimeEndpoint,
  label: string,
): void {
  if (endpoint == null || typeof endpoint !== "object") {
    throw new Error(`golem-js: ${label} must be an object`);
  }
  if (endpoint.transport !== "websocket" && endpoint.transport !== "webtransport") {
    throw new Error(`golem-js: ${label} has an unsupported transport`);
  }
  if (typeof endpoint.url !== "string" || endpoint.url.trim() === "") {
    throw new Error(`golem-js: ${label} url is required`);
  }
  try {
    new URL(endpoint.url);
  } catch {
    throw new Error(`golem-js: ${label} url is invalid`);
  }
  if (
    endpoint.eventualAckIntervalMs !== undefined &&
    (!Number.isInteger(endpoint.eventualAckIntervalMs) ||
      endpoint.eventualAckIntervalMs < 0 ||
      endpoint.eventualAckIntervalMs > 0x7fffffff)
  ) {
    throw new Error(`golem-js: ${label} eventualAckIntervalMs is invalid`);
  }
}

function validateConnectionPlan(plan: ConnectPlan): RealtimeEndpoint[] {
  if (!Array.isArray(plan.candidates) || plan.candidates.length === 0) {
    throw new Error("golem-js: connection plan requires at least one candidate");
  }
  if (plan.candidates.length > 2) {
    throw new Error("golem-js: connection plan supports at most two candidates");
  }
  const candidates = plan.candidates.map((candidate, index) => {
    validateRealtimeEndpoint(candidate, `connection plan candidate ${index}`);
    return cloneRealtimeEndpoint(candidate);
  });
  if (
    candidates.length === 2 &&
    (candidates[0].transport !== "webtransport" ||
      candidates[1].transport !== "websocket")
  ) {
    throw new Error(
      "golem-js: connection plan fallback must be webtransport then websocket",
    );
  }
  if (typeof plan.resolveOptions !== "undefined" && typeof plan.resolveOptions !== "function") {
    throw new Error("golem-js: connection plan resolveOptions must be a function");
  }
  return candidates;
}

function urlWithoutQuery(url: string): string {
  const parsed = new URL(url);
  parsed.search = "";
  return parsed.toString();
}

function resolvedConnectOptions(
  endpoint: RealtimeEndpoint,
  resolved: ConnectOptions,
): ConnectOptions {
  if (resolved == null || typeof resolved !== "object") {
    throw new Error("golem-js: resolved connection options must be an object");
  }
  if (resolved.transport !== endpoint.transport) {
    throw new Error("golem-js: connection options resolver changed transport");
  }
  if (typeof resolved.url !== "string" || resolved.url.trim() === "") {
    throw new Error("golem-js: resolved connection url is required");
  }
  try {
    if (urlWithoutQuery(resolved.url) !== urlWithoutQuery(endpoint.url)) {
      throw new Error("different endpoint");
    }
  } catch {
    throw new Error(
      "golem-js: connection options resolver may only change URL query parameters",
    );
  }
  if (
    resolved.eventualAckIntervalMs !== endpoint.eventualAckIntervalMs ||
    !sameCertificateHashes(
      resolved.serverCertificateHashes,
      endpoint.serverCertificateHashes,
    )
  ) {
    throw new Error(
      "golem-js: connection options resolver changed credential-free endpoint metadata",
    );
  }
  return {
    ...cloneRealtimeEndpoint(endpoint),
    url: resolved.url,
  };
}

function sameCertificateHashes(
  left: readonly WebTransportCertificateHash[] | undefined,
  right: readonly WebTransportCertificateHash[] | undefined,
): boolean {
  const a = left ?? [];
  const b = right ?? [];
  return a.length === b.length && a.every(
    (hash, index) => certificateHashAffinityValue(hash) === certificateHashAffinityValue(b[index]),
  );
}

function certificateHashAffinityValue(
  hash: WebTransportCertificateHash,
): string {
  if (typeof hash.value === "string") {
    return `${hash.algorithm}:${hash.value}`;
  }
  const bytes = ArrayBuffer.isView(hash.value)
    ? new Uint8Array(hash.value.buffer, hash.value.byteOffset, hash.value.byteLength)
    : new Uint8Array(hash.value);
  let value = "";
  for (const byte of bytes) {
    value += byte.toString(16).padStart(2, "0");
  }
  return `${hash.algorithm}:${value}`;
}

function connectionPlanAffinityKey(candidates: readonly RealtimeEndpoint[]): string {
  return JSON.stringify(candidates.map((candidate) => ({
    transport: candidate.transport,
    url: candidate.url,
    serverCertificateHashes: candidate.serverCertificateHashes?.map(
      certificateHashAffinityValue,
    ) ?? [],
    eventualAckIntervalMs: candidate.eventualAckIntervalMs ?? 0,
  })));
}

function builtInSupportsTransport(transport: TransportKind): boolean {
  if (transport === "webtransport") {
    return typeof globalThis.WebTransport === "function";
  }
  return typeof globalThis.WebSocket === "function";
}

/**
 * GameClient owns the active reliable transport channel, feeds decoded updates
 * into the EntityManager, WorldManager, and EventManager, and sends encoded
 * commands to the server.
 */
export class GameClient {
  readonly entities: EntityManagerLike;
  readonly world: WorldManagerLike | undefined;
  readonly events: EventManagerLike | undefined;
  private _channel: ReliableMessageChannel | null = null;
  private _decode: (bytes: Uint8Array) => unknown;
  private _encode: (cmd: object) => Uint8Array;
  private _encodePacket: (frames: Uint8Array[]) => Uint8Array;
  private _decodeWorld?: (bytes: Uint8Array) => unknown;
  private _createChannel: (options: ConnectOptions) => ReliableMessageChannel;
  private _supportsTransport: (transport: TransportKind) => boolean;
  private _onConnect?: () => void;
  private _onDisconnect?: (ev: DisconnectInfo) => void;
  private _queuedFrames: Uint8Array[] = [];
  private _queuedBytes = 0;
  private _flushScheduled = false;
  private _connectionGeneration = 0;
  private _connectionOpened = false;
  private _attemptAbort?: AbortController;
  private _attemptTimeout?: ReturnType<typeof setTimeout>;
  private _finalizedGeneration = -1;
  private _planAffinityKey?: string;
  private _stickyWebSocket = false;

  constructor(options: GameClientOptions) {
    this.entities = options.entityManager;
    this.world = options.worldManager;
    this.events = options.eventManager;
    this._decode = options.decode;
    this._encode = options.encode;
    this._encodePacket = options.encodePacket;
    this._decodeWorld = options.decodeWorld;
    this._createChannel = options.createChannel ?? createChannel;
    this._supportsTransport = options.supportsTransport ?? (
      options.createChannel == null
        ? builtInSupportsTransport
        : () => true
    );
  }

  /** Open a connection using the built-in transport adapter. */
  connect(input: ConnectInput): void {
    if (isConnectPlan(input)) {
      this._connectPlan(input);
      return;
    }

    this._planAffinityKey = undefined;
    this._stickyWebSocket = false;
    const generation = this._beginConnection();
    if (!this._isCurrentGeneration(generation)) {
      return;
    }
    const options: ConnectOptions = typeof input === "string"
      ? { transport: "websocket", url: input }
      : input;
    console.warn(
      `golem-js: connecting transport=${options.transport} url=${redactUrl(options.url)}`,
    );
    const channel = this._createChannel(options);
    this._channel = channel;
    channel.onClose((ev) => {
      if (!this._isCurrentChannel(generation, channel)) {
        return;
      }
      const safeInfo = sanitizeDisconnectInfo(options.transport, ev);
      this._connectionOpened = false;
      this._clearQueuedFrames();
      this._clearEntities();
      this._channel = null;
      this._onDisconnect?.(safeInfo);
    });
    channel.onMessage((bytes) => {
      if (this._isCurrentChannel(generation, channel)) {
        this._handleMessage(bytes);
      }
    });
    channel.onUnreliableStateMessage?.((bytes) => {
      if (this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onReliableOrderedMessage?.((bytes) => {
      if (this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onEventualStateMessage?.((bytes) => {
      if (this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onOpen(() => {
      if (this._isCurrentChannel(generation, channel)) {
        this._connectionOpened = true;
        this._onConnect?.();
      }
    });
  }

  /** Close the current connection, if any. */
  disconnect(): void {
    this._beginConnection();
  }

  private _connectPlan(plan: ConnectPlan): void {
    const candidates = validateConnectionPlan(plan);
    const affinityKey = connectionPlanAffinityKey(candidates);
    if (this._planAffinityKey !== affinityKey) {
      this._planAffinityKey = affinityKey;
      this._stickyWebSocket = false;
    }
    const orderedCandidates = this._stickyWebSocket
      ? candidates.filter((candidate) => candidate.transport === "websocket")
      : candidates;
    const generation = this._beginConnection();
    if (!this._isCurrentGeneration(generation)) {
      return;
    }
    this._attemptAbort = new AbortController();
    this._tryPlanCandidate(
      generation,
      affinityKey,
      orderedCandidates,
      plan.resolveOptions,
      0,
    );
  }

  private _tryPlanCandidate(
    generation: number,
    affinityKey: string,
    candidates: RealtimeEndpoint[],
    resolver: ConnectOptionsResolver | undefined,
    index: number,
    attempted = false,
  ): void {
    if (!this._isCurrentGeneration(generation)) {
      return;
    }
    if (index >= candidates.length) {
      this._finishPlanFailure(
        generation,
        candidates[candidates.length - 1]?.transport ?? "websocket",
        attempted
          ? "all realtime transport candidates failed"
          : "no supported realtime transport is available",
      );
      return;
    }

    const endpoint = candidates[index];
    let supported: boolean;
    try {
      supported = this._supportsTransport(endpoint.transport);
    } catch {
      this._finishPlanFailure(
        generation,
        endpoint.transport,
        "transport capability check failed",
      );
      return;
    }
    if (!supported) {
      this._tryPlanCandidate(
        generation,
        affinityKey,
        candidates,
        resolver,
        index + 1,
        attempted,
      );
      return;
    }

    if (!resolver) {
      this._dialPlanCandidate(
        generation,
        affinityKey,
        candidates,
        resolver,
        index,
        { ...endpoint },
      );
      return;
    }

    const controller = this._attemptAbort;
    if (!controller) {
      return;
    }
    let result: ConnectOptions | Promise<ConnectOptions>;
    try {
      result = resolver(cloneRealtimeEndpoint(endpoint), controller.signal);
    } catch {
      this._finishPlanFailure(
        generation,
        endpoint.transport,
        "connection options resolution failed",
      );
      return;
    }

    if (result != null && typeof (result as Promise<ConnectOptions>).then === "function") {
      void Promise.resolve(result).then(
        (resolved) => {
          if (!this._isCurrentGeneration(generation) || controller.signal.aborted) {
            return;
          }
          let options: ConnectOptions;
          try {
            options = resolvedConnectOptions(endpoint, resolved);
          } catch {
            this._finishPlanFailure(
              generation,
              endpoint.transport,
              "connection options resolution failed",
            );
            return;
          }
          this._dialPlanCandidate(
            generation,
            affinityKey,
            candidates,
            resolver,
            index,
            options,
          );
        },
        () => {
          if (!this._isCurrentGeneration(generation) || controller.signal.aborted) {
            return;
          }
          this._finishPlanFailure(
            generation,
            endpoint.transport,
            "connection options resolution failed",
          );
        },
      );
      return;
    }

    let options: ConnectOptions;
    try {
      options = resolvedConnectOptions(endpoint, result as ConnectOptions);
    } catch {
      this._finishPlanFailure(
        generation,
        endpoint.transport,
        "connection options resolution failed",
      );
      return;
    }
    this._dialPlanCandidate(
      generation,
      affinityKey,
      candidates,
      resolver,
      index,
      options,
    );
  }

  private _dialPlanCandidate(
    generation: number,
    affinityKey: string,
    candidates: RealtimeEndpoint[],
    resolver: ConnectOptionsResolver | undefined,
    index: number,
    options: ConnectOptions,
  ): void {
    if (!this._isCurrentGeneration(generation)) {
      return;
    }
    const dialOptions: InternalConnectOptions = {
      ...options,
      serverCertificateHashes: options.serverCertificateHashes?.map(
        cloneClientCertificateHash,
      ),
      [suppressTransportLogs]: true,
    };
    console.warn(
      `golem-js: connecting transport=${options.transport} url=${redactUrl(options.url)}`,
    );

    let channel: ReliableMessageChannel;
    try {
      channel = this._createChannel(dialOptions);
    } catch (error) {
      if (isTerminalPreOpenFailure(error)) {
        this._finishPlanFailure(
          generation,
          options.transport,
          "realtime authorization or revision rejected",
        );
        return;
      }
      this._tryPlanCandidate(
        generation,
        affinityKey,
        candidates,
        resolver,
        index + 1,
        true,
      );
      return;
    }

    let phase: "opening" | "open" | "done" = "opening";
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const clearEstablishmentTimeout = () => {
      if (timeout !== undefined) {
        clearTimeout(timeout);
        if (this._attemptTimeout === timeout) {
          this._attemptTimeout = undefined;
        }
        timeout = undefined;
      }
    };
    const closeThen = (continuation?: () => void) => {
      let result: void | Promise<void>;
      try {
        result = channel.close();
      } catch {
        continuation?.();
        return;
      }
      if (result != null && typeof (result as Promise<void>).then === "function") {
        void Promise.resolve(result).then(
          () => continuation?.(),
          () => continuation?.(),
        );
        return;
      }
      continuation?.();
    };
    const advanceFallback = () => {
      if (phase !== "opening" || !this._isCurrentGeneration(generation)) {
        return;
      }
      phase = "done";
      clearEstablishmentTimeout();
      if (this._channel === channel) {
        this._channel = null;
      }
      closeThen(() => {
        if (!this._isCurrentGeneration(generation)) {
          return;
        }
        this._tryPlanCandidate(
          generation,
          affinityKey,
          candidates,
          resolver,
          index + 1,
          true,
        );
      });
    };

    this._channel = channel;
    channel.onClose((_info) => {
      if (!this._isCurrentGeneration(generation)) {
        return;
      }
      if (phase === "opening") {
        if (isTerminalPreOpenFailure(_info)) {
          phase = "done";
          clearEstablishmentTimeout();
          if (this._channel === channel) {
            this._channel = null;
          }
          closeThen(() => this._finishPlanFailure(
            generation,
            options.transport,
            "realtime authorization or revision rejected",
          ));
          return;
        }
        advanceFallback();
        return;
      }
      if (phase !== "open" || this._channel !== channel) {
        return;
      }
      phase = "done";
      clearEstablishmentTimeout();
      this._channel = null;
      this._connectionOpened = false;
      this._attemptAbort = undefined;
      this._clearQueuedFrames();
      this._clearEntities();
      const info = sanitizeDisconnectInfo(options.transport, {
        wasClean: _info.wasClean,
        code: _info.code,
        reason: _info.reason,
        error: _info.error == null
          ? undefined
          : sanitizedTransportError(`${options.transport} transport failed`),
      });
      logDisconnect(options.transport, info);
      this._onDisconnect?.(info);
    });
    channel.onMessage((bytes) => {
      if (phase === "open" && this._isCurrentChannel(generation, channel)) {
        this._handleMessage(bytes);
      }
    });
    channel.onUnreliableStateMessage?.((bytes) => {
      if (phase === "open" && this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onReliableOrderedMessage?.((bytes) => {
      if (phase === "open" && this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onEventualStateMessage?.((bytes) => {
      if (phase === "open" && this._isCurrentChannel(generation, channel)) {
        this._handleCompactStateBatch(bytes);
      }
    });
    channel.onOpen(() => {
      if (phase !== "opening" || !this._isCurrentChannel(generation, channel)) {
        closeThen();
        return;
      }
      phase = "open";
      clearEstablishmentTimeout();
      this._attemptAbort = undefined;
      this._connectionOpened = true;
      if (options.transport === "websocket" && this._planAffinityKey === affinityKey) {
        this._stickyWebSocket = true;
      }
      this._onConnect?.();
    });

    if (phase === "opening" && options.transport === "webtransport") {
      timeout = setTimeout(advanceFallback, webTransportEstablishmentTimeoutMs);
      this._attemptTimeout = timeout;
    }
  }

  private _finishPlanFailure(
    generation: number,
    transport: TransportKind,
    reason: string,
  ): void {
    if (
      !this._isCurrentGeneration(generation) ||
      this._finalizedGeneration === generation
    ) {
      return;
    }
    this._finalizedGeneration = generation;
    this._clearAttemptTimeout();
    this._attemptAbort = undefined;
    this._channel = null;
    this._connectionOpened = false;
    this._clearQueuedFrames();
    this._clearEntities();
    const info: DisconnectInfo = {
      wasClean: false,
      reason,
      error: sanitizedTransportError(reason),
    };
    logDisconnect(transport, info);
    this._onDisconnect?.(info);
  }

  private _beginConnection(): number {
    const channel = this._channel;
    const notifyCleanDisconnect = channel != null && this._connectionOpened;
    const generation = ++this._connectionGeneration;
    this._attemptAbort?.abort();
    this._attemptAbort = undefined;
    this._clearAttemptTimeout();
    this._channel = null;
    this._connectionOpened = false;
    this._clearQueuedFrames();
    this._clearEntities();
    try {
      const closeResult = channel?.close();
      if (
        closeResult != null &&
        typeof (closeResult as Promise<void>).then === "function"
      ) {
        void Promise.resolve(closeResult).catch(() => {});
      }
    } catch {
      // Closing a superseded channel must not block the new attempt.
    }
    if (notifyCleanDisconnect) {
      this._onDisconnect?.({
        code: 1000,
        reason: "client disconnect",
        wasClean: true,
      });
    }
    return generation;
  }

  private _clearAttemptTimeout(): void {
    if (this._attemptTimeout === undefined) {
      return;
    }
    clearTimeout(this._attemptTimeout);
    this._attemptTimeout = undefined;
  }

  private _isCurrentGeneration(generation: number): boolean {
    return generation === this._connectionGeneration;
  }

  private _isCurrentChannel(
    generation: number,
    channel: ReliableMessageChannel,
  ): boolean {
    return this._isCurrentGeneration(generation) && this._channel === channel;
  }

  /** Send a command over reliable-unordered datagrams, or the reliable stream when unavailable. */
  send(cmd: object): void {
    this._sendCommand(cmd, "reliableUnordered");
  }

  /** Send a command over reliable-ordered datagrams, or the reliable stream when unavailable. */
  sendOrdered(cmd: object): void {
    this._sendCommand(cmd, "reliableOrdered");
  }

  private _sendCommand(
    cmd: object,
    lane: "reliableUnordered" | "reliableOrdered",
  ): void {
    const channel = this._channel;
    if (!channel?.connected) {
      return;
    }

    const frame = this._encode(cmd);
    const datagramChannel = channel[lane];
    if (datagramChannel) {
      if (frame.byteLength > datagramChannel.maxDatagramBytes) {
        const laneName = lane === "reliableUnordered" ? "reliable unordered" : "reliable ordered";
        throw new Error(
          `golem-js: encoded ${laneName} command size ${frame.byteLength} exceeds max ${datagramChannel.maxDatagramBytes}`,
        );
      }
      datagramChannel.send(frame);
      return;
    }

    const frameBytes = clientPacketEntrySize(frame);
    if (frameBytes > channel.maxMessageBytes) {
      throw new Error(
        `golem-js: encoded command size ${frameBytes} exceeds max reliable message ${channel.maxMessageBytes}`,
      );
    }

    if (this._queuedBytes > 0 && this._queuedBytes + frameBytes > channel.maxMessageBytes) {
      this._flushQueuedFrames();
    }

    this._queuedFrames.push(frame);
    this._queuedBytes += frameBytes;
    this._scheduleFlush();
  }

  get connected(): boolean {
    return this._channel?.connected ?? false;
  }

  private _scheduleFlush(): void {
    if (this._flushScheduled) {
      return;
    }
    this._flushScheduled = true;
    const generation = this._connectionGeneration;
    scheduleMicrotask(() => {
      if (!this._isCurrentGeneration(generation)) {
        return;
      }
      this._flushScheduled = false;
      this._flushQueuedFrames();
    });
  }

  private _flushQueuedFrames(): void {
    if (this._queuedFrames.length === 0) {
      return;
    }

    const channel = this._channel;
    const frames = this._queuedFrames;
    this._clearQueuedFrames();

    if (!channel?.connected) {
      return;
    }

    let packetFrames: Uint8Array[] = [];
    let packetBytes = 0;
    for (const frame of frames) {
      const frameBytes = clientPacketEntrySize(frame);
      if (packetBytes > 0 && packetBytes + frameBytes > channel.maxMessageBytes) {
        this._sendPacket(channel, packetFrames);
        packetFrames = [];
        packetBytes = 0;
      }
      packetFrames.push(frame);
      packetBytes += frameBytes;
    }

    if (packetFrames.length > 0) {
      this._sendPacket(channel, packetFrames);
    }
  }

  private _sendPacket(channel: ReliableMessageChannel, frames: Uint8Array[]): void {
    const packet = this._encodePacket(frames);
    if (packet.byteLength > channel.maxMessageBytes) {
      console.error(
        `golem-js: encoded ClientPacket size ${packet.byteLength} exceeds max reliable message ${channel.maxMessageBytes}`,
      );
      return;
    }
    channel.send(packet);
  }

  private _handleMessage(bytes: Uint8Array): void {
    const r = new PbReader(bytes);
    while (!r.done) {
      const { field, wire } = r.tag();
      if (field === 1 && wire === 2) {
        this.entities.applyUpdate(this._decode(r.bytes()));
      } else if (field === 2 && wire === 2 && this._decodeWorld && this.world) {
        this.world.applyUpdate(this._decodeWorld(r.bytes()));
      } else if (field === 3 && wire === 2 && this.events) {
        this.events.applyRaw(r.bytes());
      } else {
        r.skip(wire);
      }
    }
  }

  private _handleCompactStateBatch(bytes: Uint8Array): void {
    if (!this.entities.applyCompactUpdate) {
      throw new Error("golem-js: EntityManager does not support compact state updates");
    }
    for (const frame of decodeReliableBatchPayload(bytes)) {
      this.entities.applyCompactUpdate(frame);
    }
  }

  private _clearQueuedFrames(): void {
    this._queuedFrames = [];
    this._queuedBytes = 0;
    this._flushScheduled = false;
  }

  private _clearEntities(): void {
    this.entities.clear?.();
  }

  onConnect(fn: () => void): void { this._onConnect = fn; }
  onDisconnect(fn: (ev: DisconnectInfo) => void): void { this._onDisconnect = fn; }
}

class WebSocketReliableChannel implements ReliableMessageChannel {
  readonly maxMessageBytes = maxWebSocketPayloadBytes;
  readonly unreliable = undefined;
  readonly reliableUnordered = undefined;
  readonly reliableOrdered = undefined;
  private _ws: WebSocket;
  private _onOpen?: () => void;
  private _onMessage?: (bytes: Uint8Array) => void;
  private _onClose?: (info: DisconnectInfo) => void;
  private _closedNotified = false;
  private _logFailures: boolean;

  constructor(url: string, logFailures = true) {
    this._logFailures = logFailures;
    const redacted = redactUrl(url);
    try {
      this._ws = new WebSocket(url);
    } catch {
      throw sanitizedTransportError("websocket connection setup failed");
    }
    this._ws.binaryType = "arraybuffer";
    this._ws.onopen = () => this._onOpen?.();
    this._ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        this._onMessage?.(new Uint8Array(ev.data));
      }
    };
    this._ws.onerror = (ev) => {
      // Browser ErrorEvent messages can repeat the full credential-bearing URL.
      if (this._logFailures) {
        console.error(`golem-js: websocket error url=${redacted} type=${ev.type}`);
      }
    };
    this._ws.onclose = (ev) => {
      const info: DisconnectInfo = {
        code: ev.code,
        reason: ev.reason,
        wasClean: ev.wasClean,
      };
      this._notifyClose(info);
    };
  }

  get connected(): boolean {
    return this._ws.readyState === WebSocket.OPEN;
  }

  close(): void {
    if (this._ws.readyState === WebSocket.OPEN) {
      this._ws.send(clientCloseControlFrame);
    }
    this._ws.close();
  }

  send(bytes: Uint8Array): void {
    try {
      this._ws.send(bytes);
    } catch {
      this._notifyClose({ wasClean: false, error: sanitizedTransportError("websocket send failed") });
    }
  }

  onOpen(fn: () => void): void { this._onOpen = fn; }
  onMessage(fn: (bytes: Uint8Array) => void): void { this._onMessage = fn; }
  onUnreliableStateMessage(_fn: (bytes: Uint8Array) => void): void {}
  onReliableOrderedMessage(_fn: (bytes: Uint8Array) => void): void {}
  onEventualStateMessage(_fn: (bytes: Uint8Array) => void): void {}
  onClose(fn: (info: DisconnectInfo) => void): void { this._onClose = fn; }

  private _notifyClose(info: DisconnectInfo): void {
    if (this._closedNotified) {
      return;
    }
    this._closedNotified = true;
    const safeInfo = sanitizeDisconnectInfo("websocket", info);
    if (this._logFailures) {
      logDisconnect("websocket", safeInfo);
    }
    this._onClose?.(safeInfo);
  }
}

class WebTransportDatagramChannel implements UnreliableMessageChannel {
  readonly maxDatagramBytes: number;
  private _send: (bytes: Uint8Array) => void;

  constructor(maxDatagramBytes: number, send: (bytes: Uint8Array) => void) {
    this.maxDatagramBytes = maxDatagramBytes;
    this._send = send;
  }

  send(bytes: Uint8Array): void {
    this._send(bytes);
  }
}

class WebTransportReliableDatagramLane implements ReliableUnorderedMessageChannel, ReliableOrderedMessageChannel {
  readonly maxDatagramBytes: number;
  private _send: (bytes: Uint8Array) => void;

  constructor(maxDatagramBytes: number, send: (bytes: Uint8Array) => void) {
    this.maxDatagramBytes = maxDatagramBytes;
    this._send = send;
  }

  send(bytes: Uint8Array): void {
    this._send(bytes);
  }
}

interface DatagramPacket {
  packetSeq: number;
  ackSeq: number;
  ackMask: AckMask;
  flags: number;
  lane?: DatagramLane;
  messageID?: number;
  orderedSeq?: number;
  stateToken?: bigint;
  payload?: Uint8Array;
}

interface PendingReliableMessage {
  lane: DatagramLane;
  messageID: number;
  orderedSeq?: number;
  payload: Uint8Array;
  queuedAt: number;
  nextSendAt: number;
  attempts: number;
  inFlight: boolean;
  lastPacketSeq: number;
}

function emptyAckMask(): AckMask {
  return [0, 0, 0, 0];
}

function cloneAckMask(mask: AckMask): AckMask {
  return [mask[0] >>> 0, mask[1] >>> 0, mask[2] >>> 0, mask[3] >>> 0];
}

function setAckMaskBit(mask: AckMask, bit: number): void {
  if (bit < 0 || bit >= datagramPacketAckWindow) {
    return;
  }
  const word = Math.floor(bit / 32);
  const shift = bit % 32;
  mask[word] = (mask[word] | ((1 << shift) >>> 0)) >>> 0;
}

function hasAckMaskBit(mask: AckMask, bit: number): boolean {
  if (bit < 0 || bit >= datagramPacketAckWindow) {
    return false;
  }
  const word = Math.floor(bit / 32);
  const shift = bit % 32;
  return (mask[word] & ((1 << shift) >>> 0)) !== 0;
}

function shiftAckMaskLeft(mask: AckMask, bits: number): AckMask {
  if (bits <= 0) {
    return cloneAckMask(mask);
  }
  if (bits >= datagramPacketAckWindow) {
    return emptyAckMask();
  }
  const wordShift = Math.floor(bits / 32);
  const bitShift = bits % 32;
  const shifted = emptyAckMask();
  for (let i = datagramAckMaskWordCount - 1; i >= 0; i--) {
    const src = i - wordShift;
    if (src < 0) {
      continue;
    }
    shifted[i] = (mask[src] << bitShift) >>> 0;
    if (bitShift > 0 && src > 0) {
      shifted[i] = (shifted[i] | (mask[src - 1] >>> (32 - bitShift))) >>> 0;
    }
  }
  return shifted;
}

class SequenceWindow {
  init = false;
  latest = 0;
  mask: AckMask = emptyAckMask();

  accept(seq: number): boolean {
    if (!this.init) {
      this.init = true;
      this.latest = seq;
      this.mask = emptyAckMask();
      return true;
    }
    if (seq === this.latest) {
      return false;
    }
    if (seqGreater(seq, this.latest)) {
      const delta = seqDistance(this.latest, seq);
      if (delta > datagramPacketAckWindow) {
        this.mask = emptyAckMask();
      } else {
        this.mask = shiftAckMaskLeft(this.mask, delta);
        setAckMaskBit(this.mask, delta - 1);
      }
      this.latest = seq;
      return true;
    }
    const delta = seqDistance(seq, this.latest);
    if (delta === 0 || delta > datagramPacketAckWindow) {
      return false;
    }
    if (hasAckMaskBit(this.mask, delta - 1)) {
      return false;
    }
    setAckMaskBit(this.mask, delta - 1);
    return true;
  }
}

class OrderedReceiveBuffer {
  nextSeq = 0;
  gapSince = 0;
  pending = new Map<number, Uint8Array>();

  accept(seq: number, payload: Uint8Array, now: number): Uint8Array[] {
    if (seq === this.nextSeq) {
      const deliveries = [payload];
      this.nextSeq = uint16(this.nextSeq + 1);
      while (this.pending.has(this.nextSeq)) {
        deliveries.push(this.pending.get(this.nextSeq)!);
        this.pending.delete(this.nextSeq);
        this.nextSeq = uint16(this.nextSeq + 1);
      }
      if (this.pending.size === 0) {
        this.gapSince = 0;
      }
      return deliveries;
    }
    if (seqGreater(seq, this.nextSeq)) {
      if (!this.pending.has(seq)) {
        this.pending.set(seq, payload);
        if (this.gapSince === 0) {
          this.gapSince = now;
        }
      }
    }
    return [];
  }

  expired(now: number): boolean {
    return this.gapSince !== 0 && now - this.gapSince > datagramReliableOrderedGapTimeoutMs;
  }
}

function uint16(v: number): number {
  return v & 0xffff;
}

function seqGreater(a: number, b: number): boolean {
  if (a === b) {
    return false;
  }
  return ((a > b && a - b <= 0x8000) || (a < b && b - a > 0x8000));
}

function seqDistance(older: number, newer: number): number {
  return uint16(newer - older);
}

function reliableRetryDelay(attempts: number): number {
  let delay = datagramReliableRetryBaseDelayMs;
  for (let i = 1; i < attempts; i++) {
    delay *= 2;
    if (delay >= datagramReliableRetryMaxDelayMs) {
      return datagramReliableRetryMaxDelayMs;
    }
  }
  return Math.min(delay, datagramReliableRetryMaxDelayMs);
}

function packetAckState(packetSeq: number, ackSeq: number, ackMask: AckMask): "pending" | "delivered" | "lost" {
  if (packetSeq === ackSeq) {
    return "delivered";
  }
  if (!seqGreater(ackSeq, packetSeq)) {
    return "pending";
  }
  const delta = seqDistance(packetSeq, ackSeq);
  if (delta === 0) {
    return "pending";
  }
  if (delta > datagramPacketAckWindow) {
    return "lost";
  }
  return hasAckMaskBit(ackMask, delta - 1) ? "delivered" : "lost";
}

function encodeDatagramPacket(packet: DatagramPacket): Uint8Array {
  let size = datagramPacketHeaderBytes;
  if ((packet.flags & datagramFlagAckOnly) === 0) {
    size += datagramLaneHeaderBytes;
    if (packet.lane === datagramLaneReliableUnordered) {
      size += datagramReliableMessageIDBytes;
    } else if (packet.lane === datagramLaneReliableOrdered) {
      size += datagramReliableMessageIDBytes + datagramReliableOrderedSequenceBytes;
    } else if (packet.lane === datagramLaneEventualState) {
      size += datagramEventualStateTokenBytes;
    }
    size += packet.payload?.byteLength ?? 0;
  }
  if (size > maxWebTransportDatagramBytes) {
    throw new Error(`golem-js: datagram packet size ${size} exceeds max webtransport datagram ${maxWebTransportDatagramBytes}`);
  }
  const out = new Uint8Array(size);
  const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
  view.setUint16(0, packet.packetSeq, false);
  view.setUint16(2, packet.ackSeq, false);
  for (let i = 0; i < datagramAckMaskWordCount; i++) {
    view.setUint32(4 + i * datagramAckMaskWordBytes, packet.ackMask[i] >>> 0, false);
  }
  view.setUint8(datagramPacketHeaderBytes - 1, packet.flags);
  if ((packet.flags & datagramFlagAckOnly) !== 0) {
    return out;
  }
  let offset = datagramPacketHeaderBytes;
  view.setUint8(offset, packet.lane!);
  offset += 1;
  if (packet.lane === datagramLaneReliableUnordered) {
    view.setUint16(offset, packet.messageID!, false);
    offset += 2;
  } else if (packet.lane === datagramLaneReliableOrdered) {
    view.setUint16(offset, packet.messageID!, false);
    offset += 2;
    view.setUint16(offset, packet.orderedSeq!, false);
    offset += 2;
  } else if (packet.lane === datagramLaneEventualState) {
    view.setBigUint64(offset, packet.stateToken!, false);
    offset += datagramEventualStateTokenBytes;
  }
  if (packet.payload) {
    out.set(packet.payload, offset);
  }
  return out;
}

function decodeDatagramPacket(bytes: Uint8Array): DatagramPacket {
  if (bytes.byteLength < datagramPacketHeaderBytes) {
    throw new Error("golem-js: datagram packet too small");
  }
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const packet: DatagramPacket = {
    packetSeq: view.getUint16(0, false),
    ackSeq: view.getUint16(2, false),
    ackMask: emptyAckMask(),
    flags: view.getUint8(datagramPacketHeaderBytes - 1),
  };
  for (let i = 0; i < datagramAckMaskWordCount; i++) {
    packet.ackMask[i] = view.getUint32(4 + i * datagramAckMaskWordBytes, false);
  }
  if ((packet.flags & datagramFlagAckOnly) !== 0) {
    return packet;
  }
  let offset = datagramPacketHeaderBytes;
  packet.lane = view.getUint8(offset) as DatagramLane;
  offset += 1;
  if (
    packet.lane !== datagramLaneUnreliable &&
    packet.lane !== datagramLaneReliableUnordered &&
    packet.lane !== datagramLaneReliableOrdered &&
    packet.lane !== datagramLaneEventualState
  ) {
    throw new Error(`golem-js: unknown datagram lane ${packet.lane}`);
  }
  if (packet.lane === datagramLaneReliableUnordered) {
    packet.messageID = view.getUint16(offset, false);
    offset += 2;
  } else if (packet.lane === datagramLaneReliableOrdered) {
    packet.messageID = view.getUint16(offset, false);
    offset += 2;
    packet.orderedSeq = view.getUint16(offset, false);
    offset += 2;
  } else if (packet.lane === datagramLaneEventualState) {
    packet.stateToken = view.getBigUint64(offset, false);
    offset += datagramEventualStateTokenBytes;
  }
  packet.payload = bytes.slice(offset);
  return packet;
}

class WebTransportDatagramProtocol {
  private _writer: WritableStreamDefaultWriter<Uint8Array>;
  private _onClose: (info: DisconnectInfo) => void;
  private _ackIntervalMs: number;
  private _recvPackets = new SequenceWindow();
  private _recvReliable = new SequenceWindow();
  private _peerPackets = new SequenceWindow();
  private _orderedRecv = new OrderedReceiveBuffer();
  private _pendingOrdered: PendingReliableMessage[] = [];
  private _pendingUnordered: PendingReliableMessage[] = [];
  private _nextPacketSeq = 0;
  private _nextMessageID = 0;
  private _nextOrderedSeq = 0;
  private _ackDirty = false;
  private _ackDueAt = 0;
  private _scheduler: ReturnType<typeof setInterval> | null = null;
  private _writeQueue: Promise<void> = Promise.resolve();
  private _closed = false;
  private _unreliableMessage?: (bytes: Uint8Array) => void;
  private _reliableUnorderedMessage?: (bytes: Uint8Array) => void;
  private _reliableOrderedMessage?: (bytes: Uint8Array) => void;
  private _eventualStateMessage?: (bytes: Uint8Array) => void;

  readonly unreliable = new WebTransportDatagramChannel(maxUnreliableDatagramPayloadBytes, (bytes) => {
    if (bytes.byteLength > maxUnreliableDatagramPayloadBytes) {
      throw new Error(
        `golem-js: datagram size ${bytes.byteLength} exceeds max webtransport datagram payload ${maxUnreliableDatagramPayloadBytes}`,
      );
    }
    this._sendImmediate({
      lane: datagramLaneUnreliable,
      payload: bytes,
    });
  });

  readonly reliableUnordered = new WebTransportReliableDatagramLane(
    maxReliableUnorderedDatagramPayloadBytes,
    (bytes) => this._enqueueReliable(datagramLaneReliableUnordered, bytes),
  );

  readonly reliableOrdered = new WebTransportReliableDatagramLane(
    maxReliableOrderedDatagramPayloadBytes,
    (bytes) => this._enqueueReliable(datagramLaneReliableOrdered, bytes),
  );

  constructor(
    writer: WritableStreamDefaultWriter<Uint8Array>,
    onClose: (info: DisconnectInfo) => void,
    ackIntervalMs = datagramAckCoalesceDelayMs,
  ) {
    this._writer = writer;
    this._onClose = onClose;
    this._ackIntervalMs = Math.max(0, ackIntervalMs);
    this._scheduler = setInterval(() => this._tick(), datagramSchedulerIntervalMs);
  }

  close(): void {
    if (this._scheduler) {
      clearInterval(this._scheduler);
      this._scheduler = null;
    }
    this._closed = true;
  }

  onUnreliableMessage(fn: (bytes: Uint8Array) => void): void {
    this._unreliableMessage = fn;
  }

  onReliableUnorderedMessage(fn: (bytes: Uint8Array) => void): void {
    this._reliableUnorderedMessage = fn;
  }

  onReliableOrderedMessage(fn: (bytes: Uint8Array) => void): void {
    this._reliableOrderedMessage = fn;
  }

  onEventualStateMessage(fn: (bytes: Uint8Array) => void): void {
    this._eventualStateMessage = fn;
  }

  handleIncoming(bytes: Uint8Array): void {
    const now = Date.now();
    const packet = decodeDatagramPacket(bytes);
    this._applyPeerAcks(packet.ackSeq, packet.ackMask, now);
    if ((packet.flags & datagramFlagAckOnly) !== 0) {
      return;
    }
    const accepted = this._recvPackets.accept(packet.packetSeq);
    if (accepted) {
      this._ackDirty = true;
      this._ackDueAt = now + this._ackIntervalMs;
    }
    if (!accepted) {
      return;
    }
    switch (packet.lane) {
      case datagramLaneUnreliable:
        this._unreliableMessage?.(packet.payload!);
        return;
      case datagramLaneReliableUnordered:
        if (!this._recvReliable.accept(packet.messageID!)) {
          return;
        }
        this._reliableUnorderedMessage?.(packet.payload!);
        return;
      case datagramLaneReliableOrdered: {
        const deliveries = this._orderedRecv.accept(packet.orderedSeq!, packet.payload!, now);
        for (const delivery of deliveries) {
          this._reliableOrderedMessage?.(delivery);
        }
        return;
      }
      case datagramLaneEventualState:
        this._eventualStateMessage?.(packet.payload!);
        return;
    }
  }

  private _tick(): void {
    if (this._closed) {
      return;
    }
    const now = Date.now();
    if (this._orderedRecv.expired(now)) {
      this._notifyClose({ wasClean: false, error: sanitizedTransportError("reliable ordered datagram gap expired") });
      return;
    }
    if (this._ackDirty && now >= this._ackDueAt) {
      this._sendImmediate({ flags: datagramFlagAckOnly });
    }
    let resendBudget = datagramResendBudgetPerWake;
    const orderedSend = this._sendFromQueue(this._pendingOrdered, now, resendBudget > 0);
    if (orderedSend === "resend") {
      resendBudget--;
    }
    const unorderedSend = this._sendFromQueue(this._pendingUnordered, now, resendBudget > 0);
    if (unorderedSend === "resend") {
      resendBudget--;
    }
  }

  private _sendFromQueue(
    queue: PendingReliableMessage[],
    now: number,
    allowResend: boolean,
  ): "none" | "fresh" | "resend" {
    for (const msg of queue) {
      if (now - msg.queuedAt > datagramReliableMessageTTLms || msg.attempts >= datagramReliableRetryLimit) {
        this._notifyClose({ wasClean: false, error: sanitizedTransportError("reliable datagram delivery stalled") });
        return "none";
      }
      if (msg.inFlight && now < msg.nextSendAt) {
        continue;
      }
      if (msg.inFlight && !allowResend) {
        continue;
      }
      const resend = msg.inFlight;
      this._sendReliable(msg, now);
      return resend ? "resend" : "fresh";
    }
    return "none";
  }

  private _sendReliable(msg: PendingReliableMessage, now: number): void {
    this._sendImmediate({
      lane: msg.lane,
      messageID: msg.messageID,
      orderedSeq: msg.orderedSeq,
      payload: msg.payload,
    });
    msg.inFlight = true;
    msg.lastPacketSeq = uint16(this._nextPacketSeq - 1);
    msg.attempts++;
    msg.nextSendAt = now + reliableRetryDelay(msg.attempts);
  }

  private _enqueueReliable(lane: DatagramLane, bytes: Uint8Array): void {
    const maxBytes = lane === datagramLaneReliableOrdered
      ? maxReliableOrderedDatagramPayloadBytes
      : maxReliableUnorderedDatagramPayloadBytes;
    if (bytes.byteLength > maxBytes) {
      throw new Error(`golem-js: reliable datagram size ${bytes.byteLength} exceeds max ${maxBytes}`);
    }
    const msg: PendingReliableMessage = {
      lane,
      messageID: this._nextMessageID,
      orderedSeq: lane === datagramLaneReliableOrdered ? this._nextOrderedSeq : undefined,
      payload: bytes,
      queuedAt: Date.now(),
      nextSendAt: Date.now(),
      attempts: 0,
      inFlight: false,
      lastPacketSeq: 0,
    };
    this._nextMessageID = uint16(this._nextMessageID + 1);
    if (lane === datagramLaneReliableOrdered) {
      this._nextOrderedSeq = uint16(this._nextOrderedSeq + 1);
      this._pendingOrdered.push(msg);
    } else {
      this._pendingUnordered.push(msg);
    }
  }

  private _sendImmediate(packet: Partial<DatagramPacket>): void {
    const encoded = encodeDatagramPacket({
      packetSeq: this._nextPacketSeq,
      ackSeq: this._recvPackets.latest,
      ackMask: cloneAckMask(this._recvPackets.mask),
      flags: packet.flags ?? 0,
      lane: packet.lane,
      messageID: packet.messageID,
      orderedSeq: packet.orderedSeq,
      payload: packet.payload,
    });
    this._nextPacketSeq = uint16(this._nextPacketSeq + 1);
    this._ackDirty = false;
    this._writeQueue = this._writeQueue
      .then(() => this._writer.write(encoded))
      .catch(() => {
        this._notifyClose({ wasClean: false, error: sanitizedTransportError("webtransport datagram write failed") });
      });
  }

  wrapReliablePayloadWithAck(payload: Uint8Array, maxMessageBytes: number): Uint8Array {
    if (!this._ackDirty || payload.byteLength + clientReliableAckControlHeaderBytes > maxMessageBytes) {
      return payload;
    }
    const out = new Uint8Array(clientReliableAckControlHeaderBytes + payload.byteLength);
    out.set(clientReliableAckControlFrame, 0);
    const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
    let offset = clientReliableAckControlFrame.byteLength;
    view.setUint16(offset, this._recvPackets.latest, false);
    offset += 2;
    for (let i = 0; i < datagramAckMaskWordCount; i++) {
      view.setUint32(offset, this._recvPackets.mask[i] >>> 0, false);
      offset += datagramAckMaskWordBytes;
    }
    out.set(payload, offset);
    this._ackDirty = false;
    this._ackDueAt = 0;
    return out;
  }

  private _applyPeerAcks(ackSeq: number, ackMask: AckMask, now: number): void {
    if (!this._peerPackets.init) {
      this._peerPackets.init = true;
      this._peerPackets.latest = ackSeq;
      this._peerPackets.mask = cloneAckMask(ackMask);
    } else if (seqGreater(ackSeq, this._peerPackets.latest) || ackSeq === this._peerPackets.latest) {
      this._peerPackets.latest = ackSeq;
      this._peerPackets.mask = cloneAckMask(ackMask);
    }
    this._pruneQueue(this._pendingOrdered, ackSeq, ackMask, now);
    this._pruneQueue(this._pendingUnordered, ackSeq, ackMask, now);
  }

  private _pruneQueue(queue: PendingReliableMessage[], ackSeq: number, ackMask: AckMask, now: number): void {
    for (let i = queue.length - 1; i >= 0; i--) {
      const msg = queue[i];
      const state = packetAckState(msg.lastPacketSeq, ackSeq, ackMask);
      if (state === "delivered") {
        queue.splice(i, 1);
      } else if (state === "lost") {
        msg.inFlight = false;
        msg.nextSendAt = Math.min(msg.nextSendAt, now);
      }
    }
  }

  private _notifyClose(info: DisconnectInfo): void {
    if (this._closed) {
      return;
    }
    this.close();
    this._onClose(info);
  }
}

class WebTransportReliableChannel implements ReliableMessageChannel {
  readonly maxMessageBytes = maxReliableMessageBytes;
  readonly unreliable?: UnreliableMessageChannel;
  readonly reliableUnordered?: ReliableUnorderedMessageChannel;
  readonly reliableOrdered?: ReliableOrderedMessageChannel;
  private _transport: WebTransport;
  private _writer?: WritableStreamDefaultWriter<Uint8Array>;
  private _reader = new ReliableFrameReader();
  private _datagramProtocol?: WebTransportDatagramProtocol;
  private _onOpen?: () => void;
  private _onMessage?: (bytes: Uint8Array) => void;
  private _onUnreliableStateMessage?: (bytes: Uint8Array) => void;
  private _onClose?: (info: DisconnectInfo) => void;
  private _connected = false;
  private _writeQueue: Promise<void> = Promise.resolve();
  private _closedNotified = false;
  private _closePromise?: Promise<void>;
  private _redactedUrl: string;
  private _logFailures: boolean;

  constructor(options: WebTransportConnectOptions, logFailures = true) {
    this._logFailures = logFailures;
    this._redactedUrl = redactUrl(options.url);
    const serverCertificateHashes = options.serverCertificateHashes?.map(normalizeCertificateHash);
    let transport: WebTransport;
    try {
      transport = new WebTransport(options.url, {
        serverCertificateHashes,
      });
    } catch (error) {
      throw sanitizedTransportError("webtransport connection setup failed", error);
    }
    this._transport = transport;
    const datagramWriter = transport.datagrams.writable.getWriter();
    this._datagramProtocol = new WebTransportDatagramProtocol(
      datagramWriter,
      (info) => this._notifyClose(info),
      options.eventualAckIntervalMs,
    );
    this.unreliable = this._datagramProtocol.unreliable;
    this.reliableUnordered = this._datagramProtocol.reliableUnordered;
    this.reliableOrdered = this._datagramProtocol.reliableOrdered;
    void this._init();
  }

  get connected(): boolean {
    return this._connected;
  }

  close(): Promise<void> {
    if (this._closePromise) {
      return this._closePromise;
    }
    this._closePromise = this._closeGracefully();
    return this._closePromise;
  }

  private async _closeGracefully(): Promise<void> {
    this._connected = false;
    this._datagramProtocol?.close();
    const closeTransport = () => {
      try {
        this._transport.close({ closeCode: 0, reason: "client disconnect" });
      } catch {
        if (this._logFailures) {
          console.warn("golem-js: webtransport close failed");
        }
      }
    };
    if (this._writer) {
      const frame = writeReliableFrame(clientCloseControlFrame);
      this._writeQueue = this._writeQueue
        .then(async () => {
          await this._writer?.write(frame);
        })
        .catch(() => {
          if (this._logFailures) {
            console.warn("golem-js: webtransport close frame write failed");
          }
        });
      await waitForSettlement(
        this._writeQueue,
        webTransportCloseFrameTimeoutMs,
      );
    }
    closeTransport();
    await waitForSettlement(
      Promise.resolve(this._transport.closed).then(
        () => undefined,
        () => undefined,
      ),
      webTransportCloseCompletionTimeoutMs,
    );
  }

  send(bytes: Uint8Array): void {
    if (!this._writer) {
      return;
    }
    const payload = this._datagramProtocol?.wrapReliablePayloadWithAck(bytes, this.maxMessageBytes) ?? bytes;
    const frame = writeReliableFrame(payload);
    this._writeQueue = this._writeQueue
      .then(() => this._writer?.write(frame))
      .catch(() => {
        this._notifyClose({ wasClean: false, error: sanitizedTransportError("webtransport stream write failed") });
      });
  }

  onOpen(fn: () => void): void { this._onOpen = fn; }
  onMessage(fn: (bytes: Uint8Array) => void): void { this._onMessage = fn; }
  onUnreliableStateMessage(fn: (bytes: Uint8Array) => void): void {
    this._onUnreliableStateMessage = fn;
  }
  onReliableOrderedMessage(fn: (bytes: Uint8Array) => void): void {
    this._datagramProtocol?.onReliableOrderedMessage(fn);
  }
  onEventualStateMessage(fn: (bytes: Uint8Array) => void): void {
    this._datagramProtocol?.onEventualStateMessage(fn);
  }
  onClose(fn: (info: DisconnectInfo) => void): void { this._onClose = fn; }

  private async _init(): Promise<void> {
    try {
      await this._transport.ready;
      const stream = await this._transport.createBidirectionalStream();
      this._writer = stream.writable.getWriter();
      this._connected = true;
      this._onOpen?.();
      void this._readStream(stream.readable);
      void this._readDatagrams();
      this._watchClosed();
    } catch (error) {
      if (this._logFailures) {
        console.error(
          `golem-js: webtransport connect failed url=${this._redactedUrl}`,
        );
      }
      this._notifyClose({
        wasClean: false,
        error: sanitizedTransportError("webtransport connect failed", error),
      });
    }
  }

  private async _readStream(readable: ReadableStream<Uint8Array>): Promise<void> {
    const reader = readable.getReader();
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) {
          return;
        }
        if (!value) {
          continue;
        }
        for (const frame of this._reader.push(value)) {
          this._onMessage?.(frame);
        }
      }
    } catch {
      this._notifyClose({ wasClean: false, error: sanitizedTransportError("webtransport stream read failed") });
    } finally {
      reader.releaseLock();
    }
  }

  private async _watchClosed(): Promise<void> {
    try {
      const info = await this._transport.closed;
      this._notifyClose({
        code: info?.closeCode,
        reason: info?.reason,
        wasClean: true,
      });
    } catch {
      this._notifyClose({ wasClean: false, error: sanitizedTransportError("webtransport close failed") });
    }
  }

  private async _readDatagrams(): Promise<void> {
    const reader = this._transport.datagrams.readable.getReader();
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) {
          return;
        }
        if (!value || !this._datagramProtocol) {
          continue;
        }
        if (this._onUnreliableStateMessage && isReliableBatchPayload(value)) {
          this._onUnreliableStateMessage(value);
          continue;
        }
        try {
          this._datagramProtocol.handleIncoming(value);
        } catch {
          this._onUnreliableStateMessage?.(value);
        }
      }
    } catch {
      this._notifyClose({ wasClean: false, error: sanitizedTransportError("webtransport datagram read failed") });
    } finally {
      reader.releaseLock();
    }
  }

  private _notifyClose(info: DisconnectInfo): void {
    if (this._closedNotified) {
      return;
    }
    this._closedNotified = true;
    this._connected = false;
    this._datagramProtocol?.close();
    const safeInfo = sanitizeDisconnectInfo("webtransport", info);
    if (this._logFailures) {
      logDisconnect("webtransport", safeInfo);
    }
    this._onClose?.(safeInfo);
  }
}

/** Create a built-in reliable channel for the requested transport. */
export function createChannel(options: ConnectOptions): ReliableMessageChannel {
  const logFailures = !(options as InternalConnectOptions)[suppressTransportLogs];
  if (options.transport === "webtransport") {
    return new WebTransportReliableChannel(options, logFailures);
  }
  return new WebSocketReliableChannel(options.url, logFailures);
}

/** Decode a ServerMessage envelope, extracting the inner payload by field tag. */
export function unwrapServerMessage(bytes: Uint8Array): ServerMessage {
  const r = new PbReader(bytes);
  const result: ServerMessage = {};
  while (!r.done) {
    const { field, wire } = r.tag();
    if (field === 1 && wire === 2) {
      result.entityUpdate = r.bytes();
    } else if (field === 2 && wire === 2) {
      result.worldUpdate = r.bytes();
    } else if (field === 3 && wire === 2) {
      result.serverEvent = r.bytes();
    } else {
      r.skip(wire);
    }
  }
  return result;
}
