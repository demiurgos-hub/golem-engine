import { PbReader } from "./codec.js";

const maxReliableMessageBytes = 32000;
const maxWebSocketPayloadBytes = 32000;
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
    return url;
  }
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

/** Transport-aware connection options for GameClient.connect(). */
export interface ConnectOptions {
  transport: TransportKind;
  url: string;
  serverCertificateHashes?: WebTransportCertificateHash[];
}

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
  close(): void;
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
  private _onConnect?: () => void;
  private _onDisconnect?: (ev: DisconnectInfo) => void;
  private _queuedFrames: Uint8Array[] = [];
  private _queuedBytes = 0;
  private _flushScheduled = false;

  constructor(options: GameClientOptions) {
    this.entities = options.entityManager;
    this.world = options.worldManager;
    this.events = options.eventManager;
    this._decode = options.decode;
    this._encode = options.encode;
    this._encodePacket = options.encodePacket;
    this._decodeWorld = options.decodeWorld;
    this._createChannel = options.createChannel ?? createChannel;
  }

  /** Open a connection using the built-in transport adapter. */
  connect(url: string | ConnectOptions): void {
    this.disconnect();
    const options: ConnectOptions = typeof url === "string"
      ? { transport: "websocket", url }
      : url;
    console.warn(
      `golem-js: connecting transport=${options.transport} url=${redactUrl(options.url)}`,
    );
    const channel = this._createChannel(options);
    channel.onOpen(() => this._onConnect?.());
    channel.onClose((ev) => {
      this._clearQueuedFrames();
      if (this._channel === channel) {
        this._channel = null;
      }
      this._onDisconnect?.(ev);
    });
    channel.onMessage((bytes) => this._handleMessage(bytes));
    channel.onUnreliableStateMessage?.((bytes) => this._handleCompactStateBatch(bytes));
    channel.onReliableOrderedMessage?.((bytes) => this._handleCompactStateBatch(bytes));
    channel.onEventualStateMessage?.((bytes) => this._handleCompactStateBatch(bytes));
    this._channel = channel;
  }

  /** Close the current connection, if any. */
  disconnect(): void {
    this._clearQueuedFrames();
    this._channel?.close();
    this._channel = null;
  }

  /** Encode and queue a command object built by a generated build*Command helper. */
  send(cmd: object): void {
    const channel = this._channel;
    if (!channel?.connected) {
      return;
    }

    const frame = this._encode(cmd);
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

  /** Send one lossy datagram when the active transport supports it. */
  sendUnreliable(bytes: Uint8Array): void {
    const channel = this._channel?.unreliable;
    if (!channel) {
      return;
    }
    if (bytes.byteLength > channel.maxDatagramBytes) {
      throw new Error(
        `golem-js: datagram size ${bytes.byteLength} exceeds max webtransport datagram ${channel.maxDatagramBytes}`,
      );
    }
    channel.send(bytes);
  }

  /** Send one reliable unordered datagram when the active transport supports it. */
  sendReliableUnordered(bytes: Uint8Array): void {
    const channel = this._channel?.reliableUnordered;
    if (!channel) {
      return;
    }
    if (bytes.byteLength > channel.maxDatagramBytes) {
      throw new Error(
        `golem-js: reliable unordered datagram size ${bytes.byteLength} exceeds max ${channel.maxDatagramBytes}`,
      );
    }
    channel.send(bytes);
  }

  /** Encode and send one command over the reliable unordered datagram lane. */
  sendReliableUnorderedCommand(cmd: object): void {
    const channel = this._channel?.reliableUnordered;
    if (!channel) {
      return;
    }
    const frame = this._encode(cmd);
    if (frame.byteLength > channel.maxDatagramBytes) {
      throw new Error(
        `golem-js: encoded reliable unordered command size ${frame.byteLength} exceeds max ${channel.maxDatagramBytes}`,
      );
    }
    channel.send(frame);
  }

  /** Send one reliable ordered datagram when the active transport supports it. */
  sendReliableOrdered(bytes: Uint8Array): void {
    const channel = this._channel?.reliableOrdered;
    if (!channel) {
      return;
    }
    if (bytes.byteLength > channel.maxDatagramBytes) {
      throw new Error(
        `golem-js: reliable ordered datagram size ${bytes.byteLength} exceeds max ${channel.maxDatagramBytes}`,
      );
    }
    channel.send(bytes);
  }

  /** Encode and send one command over the reliable ordered datagram lane. */
  sendReliableOrderedCommand(cmd: object): void {
    const channel = this._channel?.reliableOrdered;
    if (!channel) {
      return;
    }
    const frame = this._encode(cmd);
    if (frame.byteLength > channel.maxDatagramBytes) {
      throw new Error(
        `golem-js: encoded reliable ordered command size ${frame.byteLength} exceeds max ${channel.maxDatagramBytes}`,
      );
    }
    channel.send(frame);
  }

  get connected(): boolean {
    return this._channel?.connected ?? false;
  }

  private _scheduleFlush(): void {
    if (this._flushScheduled) {
      return;
    }
    this._flushScheduled = true;
    scheduleMicrotask(() => {
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

  constructor(url: string) {
    const redacted = redactUrl(url);
    this._ws = new WebSocket(url);
    this._ws.binaryType = "arraybuffer";
    this._ws.onopen = () => this._onOpen?.();
    this._ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        this._onMessage?.(new Uint8Array(ev.data));
      }
    };
    this._ws.onerror = (ev) => {
      const message = ev instanceof ErrorEvent && ev.message ? ` message=${JSON.stringify(ev.message)}` : "";
      console.error(`golem-js: websocket error url=${redacted} type=${ev.type}${message}`);
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
    } catch (error) {
      this._notifyClose({ wasClean: false, error });
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
    logDisconnect("websocket", info);
    this._onClose?.(info);
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
      this._notifyClose({ wasClean: false, error: new Error("golem-js: reliable ordered datagram gap expired") });
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
        this._notifyClose({ wasClean: false, error: new Error("golem-js: reliable datagram delivery stalled") });
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
      .catch((error) => {
        this._notifyClose({ wasClean: false, error });
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
  private _closeStarted = false;
  private _redactedUrl: string;

  constructor(options: WebTransportConnectOptions) {
    this._redactedUrl = redactUrl(options.url);
    const transport = new WebTransport(options.url, {
      serverCertificateHashes: options.serverCertificateHashes?.map(normalizeCertificateHash),
    });
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

  close(): void {
    if (this._closeStarted) {
      return;
    }
    this._closeStarted = true;
    const closeTransport = () => this._transport.close({ closeCode: 0, reason: "client disconnect" });
    if (!this._writer) {
      closeTransport();
      return;
    }
    const frame = writeReliableFrame(clientCloseControlFrame);
    let closed = false;
    const finishClose = () => {
      if (closed) {
        return;
      }
      closed = true;
      clearTimeout(timeout);
      closeTransport();
    };
    const timeout = setTimeout(finishClose, webTransportCloseFrameTimeoutMs);
    this._writeQueue = this._writeQueue
      .then(() => this._writer?.write(frame))
      .catch((error) => {
        console.warn(`golem-js: webtransport close frame write failed error=${String(error)}`);
      })
      .then(finishClose);
  }

  send(bytes: Uint8Array): void {
    if (!this._writer) {
      return;
    }
    const payload = this._datagramProtocol?.wrapReliablePayloadWithAck(bytes, this.maxMessageBytes) ?? bytes;
    const frame = writeReliableFrame(payload);
    this._writeQueue = this._writeQueue
      .then(() => this._writer?.write(frame))
      .catch((error) => {
        this._notifyClose({ wasClean: false, error });
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
      console.error(
        `golem-js: webtransport connect failed url=${this._redactedUrl} error=${String(error)}`,
      );
      this._notifyClose({ wasClean: false, error });
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
    } catch (error) {
      this._notifyClose({ wasClean: false, error });
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
    } catch (error) {
      this._notifyClose({ wasClean: false, error });
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
    } catch (error) {
      this._notifyClose({ wasClean: false, error });
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
    logDisconnect("webtransport", info);
    this._onClose?.(info);
  }
}

/** Create a built-in reliable channel for the requested transport. */
export function createChannel(options: ConnectOptions): ReliableMessageChannel {
  if (options.transport === "webtransport") {
    return new WebTransportReliableChannel(options);
  }
  return new WebSocketReliableChannel(options.url);
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
