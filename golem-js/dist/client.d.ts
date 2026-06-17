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
export declare class GameClient {
    readonly entities: EntityManagerLike;
    readonly world: WorldManagerLike | undefined;
    readonly events: EventManagerLike | undefined;
    private _channel;
    private _decode;
    private _encode;
    private _encodePacket;
    private _decodeWorld?;
    private _createChannel;
    private _onConnect?;
    private _onDisconnect?;
    private _queuedFrames;
    private _queuedBytes;
    private _flushScheduled;
    constructor(options: GameClientOptions);
    /** Open a connection using the built-in transport adapter. */
    connect(url: string | ConnectOptions): void;
    /** Close the current connection, if any. */
    disconnect(): void;
    /** Encode and queue a command object built by a generated build*Command helper. */
    send(cmd: object): void;
    /** Send one lossy datagram when the active transport supports it. */
    sendUnreliable(bytes: Uint8Array): void;
    /** Send one reliable unordered datagram when the active transport supports it. */
    sendReliableUnordered(bytes: Uint8Array): void;
    /** Encode and send one command over the reliable unordered datagram lane. */
    sendReliableUnorderedCommand(cmd: object): void;
    /** Send one reliable ordered datagram when the active transport supports it. */
    sendReliableOrdered(bytes: Uint8Array): void;
    /** Encode and send one command over the reliable ordered datagram lane. */
    sendReliableOrderedCommand(cmd: object): void;
    get connected(): boolean;
    private _scheduleFlush;
    private _flushQueuedFrames;
    private _sendPacket;
    private _handleMessage;
    private _handleCompactStateBatch;
    private _clearQueuedFrames;
    onConnect(fn: () => void): void;
    onDisconnect(fn: (ev: DisconnectInfo) => void): void;
}
/** Create a built-in reliable channel for the requested transport. */
export declare function createChannel(options: ConnectOptions): ReliableMessageChannel;
/** Decode a ServerMessage envelope, extracting the inner payload by field tag. */
export declare function unwrapServerMessage(bytes: Uint8Array): ServerMessage;
