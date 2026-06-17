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
const maxUnreliableDatagramPayloadBytes = maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes;
const maxReliableUnorderedDatagramPayloadBytes = maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramReliableMessageIDBytes;
const maxReliableOrderedDatagramPayloadBytes = maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramReliableMessageIDBytes - datagramReliableOrderedSequenceBytes;
const maxEventualStateDatagramPayloadBytes = maxWebTransportDatagramBytes - datagramPacketHeaderBytes - datagramLaneHeaderBytes - datagramEventualStateTokenBytes;
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
const datagramLaneUnreliable = 1;
const datagramLaneReliableUnordered = 2;
const datagramLaneReliableOrdered = 3;
const datagramLaneEventualState = 4;
const scheduleMicrotask = typeof queueMicrotask === "function"
    ? queueMicrotask
    : (fn) => Promise.resolve().then(fn);
function varintSize(v) {
    let size = 1;
    while (v > 0x7f) {
        v >>>= 7;
        size++;
    }
    return size;
}
function clientPacketEntrySize(frame) {
    return 1 + varintSize(frame.byteLength) + frame.byteLength;
}
function toUint8Array(data) {
    if (ArrayBuffer.isView(data)) {
        return new Uint8Array(data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength));
    }
    return new Uint8Array(data.slice(0));
}
function toArrayBuffer(data) {
    const src = typeof data === "string" ? hexToBytes(data) : toUint8Array(data);
    const out = new ArrayBuffer(src.byteLength);
    new Uint8Array(out).set(src);
    return out;
}
function hexToBytes(hex) {
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
function normalizeCertificateHash(hash) {
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
function writeReliableFrame(frame) {
    if (frame.byteLength > maxReliableMessageBytes) {
        throw new Error(`golem-js: reliable message size ${frame.byteLength} exceeds max reliable message ${maxReliableMessageBytes}`);
    }
    const out = new Uint8Array(4 + frame.byteLength);
    const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
    view.setUint32(0, frame.byteLength, false);
    out.set(frame, 4);
    return out;
}
function decodeReliableBatchPayload(bytes) {
    const frames = [];
    let offset = 0;
    while (bytes.byteLength - offset >= 4) {
        const view = new DataView(bytes.buffer, bytes.byteOffset + offset, 4);
        const frameLen = view.getUint32(0, false);
        if (frameLen > maxReliableMessageBytes) {
            throw new Error(`golem-js: reliable frame length ${frameLen} exceeds max reliable message ${maxReliableMessageBytes}`);
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
function isReliableBatchPayload(bytes) {
    if (bytes.byteLength === 0) {
        return false;
    }
    try {
        decodeReliableBatchPayload(bytes);
        return true;
    }
    catch {
        return false;
    }
}
class ReliableFrameReader {
    constructor() {
        this._buffer = new Uint8Array(0);
    }
    push(chunk) {
        if (chunk.byteLength === 0) {
            return [];
        }
        const merged = new Uint8Array(this._buffer.byteLength + chunk.byteLength);
        merged.set(this._buffer, 0);
        merged.set(chunk, this._buffer.byteLength);
        this._buffer = merged;
        const frames = [];
        let offset = 0;
        while (this._buffer.byteLength - offset >= 4) {
            const view = new DataView(this._buffer.buffer, this._buffer.byteOffset + offset, 4);
            const frameLen = view.getUint32(0, false);
            if (frameLen > maxReliableMessageBytes) {
                throw new Error(`golem-js: reliable frame length ${frameLen} exceeds max reliable message ${maxReliableMessageBytes}`);
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
function redactUrl(url) {
    try {
        const parsed = new URL(url);
        return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
    }
    catch {
        return url;
    }
}
function logDisconnect(transport, info) {
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
/**
 * GameClient owns the active reliable transport channel, feeds decoded updates
 * into the EntityManager, WorldManager, and EventManager, and sends encoded
 * commands to the server.
 */
export class GameClient {
    constructor(options) {
        this._channel = null;
        this._queuedFrames = [];
        this._queuedBytes = 0;
        this._flushScheduled = false;
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
    connect(url) {
        this.disconnect();
        const options = typeof url === "string"
            ? { transport: "websocket", url }
            : url;
        console.warn(`golem-js: connecting transport=${options.transport} url=${redactUrl(options.url)}`);
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
    disconnect() {
        this._clearQueuedFrames();
        this._channel?.close();
        this._channel = null;
    }
    /** Encode and queue a command object built by a generated build*Command helper. */
    send(cmd) {
        const channel = this._channel;
        if (!channel?.connected) {
            return;
        }
        const frame = this._encode(cmd);
        const frameBytes = clientPacketEntrySize(frame);
        if (frameBytes > channel.maxMessageBytes) {
            throw new Error(`golem-js: encoded command size ${frameBytes} exceeds max reliable message ${channel.maxMessageBytes}`);
        }
        if (this._queuedBytes > 0 && this._queuedBytes + frameBytes > channel.maxMessageBytes) {
            this._flushQueuedFrames();
        }
        this._queuedFrames.push(frame);
        this._queuedBytes += frameBytes;
        this._scheduleFlush();
    }
    /** Send one lossy datagram when the active transport supports it. */
    sendUnreliable(bytes) {
        const channel = this._channel?.unreliable;
        if (!channel) {
            return;
        }
        if (bytes.byteLength > channel.maxDatagramBytes) {
            throw new Error(`golem-js: datagram size ${bytes.byteLength} exceeds max webtransport datagram ${channel.maxDatagramBytes}`);
        }
        channel.send(bytes);
    }
    /** Send one reliable unordered datagram when the active transport supports it. */
    sendReliableUnordered(bytes) {
        const channel = this._channel?.reliableUnordered;
        if (!channel) {
            return;
        }
        if (bytes.byteLength > channel.maxDatagramBytes) {
            throw new Error(`golem-js: reliable unordered datagram size ${bytes.byteLength} exceeds max ${channel.maxDatagramBytes}`);
        }
        channel.send(bytes);
    }
    /** Encode and send one command over the reliable unordered datagram lane. */
    sendReliableUnorderedCommand(cmd) {
        const channel = this._channel?.reliableUnordered;
        if (!channel) {
            return;
        }
        const frame = this._encode(cmd);
        if (frame.byteLength > channel.maxDatagramBytes) {
            throw new Error(`golem-js: encoded reliable unordered command size ${frame.byteLength} exceeds max ${channel.maxDatagramBytes}`);
        }
        channel.send(frame);
    }
    /** Send one reliable ordered datagram when the active transport supports it. */
    sendReliableOrdered(bytes) {
        const channel = this._channel?.reliableOrdered;
        if (!channel) {
            return;
        }
        if (bytes.byteLength > channel.maxDatagramBytes) {
            throw new Error(`golem-js: reliable ordered datagram size ${bytes.byteLength} exceeds max ${channel.maxDatagramBytes}`);
        }
        channel.send(bytes);
    }
    /** Encode and send one command over the reliable ordered datagram lane. */
    sendReliableOrderedCommand(cmd) {
        const channel = this._channel?.reliableOrdered;
        if (!channel) {
            return;
        }
        const frame = this._encode(cmd);
        if (frame.byteLength > channel.maxDatagramBytes) {
            throw new Error(`golem-js: encoded reliable ordered command size ${frame.byteLength} exceeds max ${channel.maxDatagramBytes}`);
        }
        channel.send(frame);
    }
    get connected() {
        return this._channel?.connected ?? false;
    }
    _scheduleFlush() {
        if (this._flushScheduled) {
            return;
        }
        this._flushScheduled = true;
        scheduleMicrotask(() => {
            this._flushScheduled = false;
            this._flushQueuedFrames();
        });
    }
    _flushQueuedFrames() {
        if (this._queuedFrames.length === 0) {
            return;
        }
        const channel = this._channel;
        const frames = this._queuedFrames;
        this._clearQueuedFrames();
        if (!channel?.connected) {
            return;
        }
        let packetFrames = [];
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
    _sendPacket(channel, frames) {
        const packet = this._encodePacket(frames);
        if (packet.byteLength > channel.maxMessageBytes) {
            console.error(`golem-js: encoded ClientPacket size ${packet.byteLength} exceeds max reliable message ${channel.maxMessageBytes}`);
            return;
        }
        channel.send(packet);
    }
    _handleMessage(bytes) {
        const r = new PbReader(bytes);
        while (!r.done) {
            const { field, wire } = r.tag();
            if (field === 1 && wire === 2) {
                this.entities.applyUpdate(this._decode(r.bytes()));
            }
            else if (field === 2 && wire === 2 && this._decodeWorld && this.world) {
                this.world.applyUpdate(this._decodeWorld(r.bytes()));
            }
            else if (field === 3 && wire === 2 && this.events) {
                this.events.applyRaw(r.bytes());
            }
            else {
                r.skip(wire);
            }
        }
    }
    _handleCompactStateBatch(bytes) {
        if (!this.entities.applyCompactUpdate) {
            throw new Error("golem-js: EntityManager does not support compact state updates");
        }
        for (const frame of decodeReliableBatchPayload(bytes)) {
            this.entities.applyCompactUpdate(frame);
        }
    }
    _clearQueuedFrames() {
        this._queuedFrames = [];
        this._queuedBytes = 0;
        this._flushScheduled = false;
    }
    onConnect(fn) { this._onConnect = fn; }
    onDisconnect(fn) { this._onDisconnect = fn; }
}
class WebSocketReliableChannel {
    constructor(url) {
        this.maxMessageBytes = maxWebSocketPayloadBytes;
        this.unreliable = undefined;
        this.reliableUnordered = undefined;
        this.reliableOrdered = undefined;
        this._closedNotified = false;
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
            const info = {
                code: ev.code,
                reason: ev.reason,
                wasClean: ev.wasClean,
            };
            this._notifyClose(info);
        };
    }
    get connected() {
        return this._ws.readyState === WebSocket.OPEN;
    }
    close() {
        if (this._ws.readyState === WebSocket.OPEN) {
            this._ws.send(clientCloseControlFrame);
        }
        this._ws.close();
    }
    send(bytes) {
        try {
            this._ws.send(bytes);
        }
        catch (error) {
            this._notifyClose({ wasClean: false, error });
        }
    }
    onOpen(fn) { this._onOpen = fn; }
    onMessage(fn) { this._onMessage = fn; }
    onUnreliableStateMessage(_fn) { }
    onReliableOrderedMessage(_fn) { }
    onEventualStateMessage(_fn) { }
    onClose(fn) { this._onClose = fn; }
    _notifyClose(info) {
        if (this._closedNotified) {
            return;
        }
        this._closedNotified = true;
        logDisconnect("websocket", info);
        this._onClose?.(info);
    }
}
class WebTransportDatagramChannel {
    constructor(maxDatagramBytes, send) {
        this.maxDatagramBytes = maxDatagramBytes;
        this._send = send;
    }
    send(bytes) {
        this._send(bytes);
    }
}
class WebTransportReliableDatagramLane {
    constructor(maxDatagramBytes, send) {
        this.maxDatagramBytes = maxDatagramBytes;
        this._send = send;
    }
    send(bytes) {
        this._send(bytes);
    }
}
function emptyAckMask() {
    return [0, 0, 0, 0];
}
function cloneAckMask(mask) {
    return [mask[0] >>> 0, mask[1] >>> 0, mask[2] >>> 0, mask[3] >>> 0];
}
function setAckMaskBit(mask, bit) {
    if (bit < 0 || bit >= datagramPacketAckWindow) {
        return;
    }
    const word = Math.floor(bit / 32);
    const shift = bit % 32;
    mask[word] = (mask[word] | ((1 << shift) >>> 0)) >>> 0;
}
function hasAckMaskBit(mask, bit) {
    if (bit < 0 || bit >= datagramPacketAckWindow) {
        return false;
    }
    const word = Math.floor(bit / 32);
    const shift = bit % 32;
    return (mask[word] & ((1 << shift) >>> 0)) !== 0;
}
function shiftAckMaskLeft(mask, bits) {
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
    constructor() {
        this.init = false;
        this.latest = 0;
        this.mask = emptyAckMask();
    }
    accept(seq) {
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
            }
            else {
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
    constructor() {
        this.nextSeq = 0;
        this.gapSince = 0;
        this.pending = new Map();
    }
    accept(seq, payload, now) {
        if (seq === this.nextSeq) {
            const deliveries = [payload];
            this.nextSeq = uint16(this.nextSeq + 1);
            while (this.pending.has(this.nextSeq)) {
                deliveries.push(this.pending.get(this.nextSeq));
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
    expired(now) {
        return this.gapSince !== 0 && now - this.gapSince > datagramReliableOrderedGapTimeoutMs;
    }
}
function uint16(v) {
    return v & 0xffff;
}
function seqGreater(a, b) {
    if (a === b) {
        return false;
    }
    return ((a > b && a - b <= 0x8000) || (a < b && b - a > 0x8000));
}
function seqDistance(older, newer) {
    return uint16(newer - older);
}
function reliableRetryDelay(attempts) {
    let delay = datagramReliableRetryBaseDelayMs;
    for (let i = 1; i < attempts; i++) {
        delay *= 2;
        if (delay >= datagramReliableRetryMaxDelayMs) {
            return datagramReliableRetryMaxDelayMs;
        }
    }
    return Math.min(delay, datagramReliableRetryMaxDelayMs);
}
function packetAckState(packetSeq, ackSeq, ackMask) {
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
function encodeDatagramPacket(packet) {
    let size = datagramPacketHeaderBytes;
    if ((packet.flags & datagramFlagAckOnly) === 0) {
        size += datagramLaneHeaderBytes;
        if (packet.lane === datagramLaneReliableUnordered) {
            size += datagramReliableMessageIDBytes;
        }
        else if (packet.lane === datagramLaneReliableOrdered) {
            size += datagramReliableMessageIDBytes + datagramReliableOrderedSequenceBytes;
        }
        else if (packet.lane === datagramLaneEventualState) {
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
    view.setUint8(offset, packet.lane);
    offset += 1;
    if (packet.lane === datagramLaneReliableUnordered) {
        view.setUint16(offset, packet.messageID, false);
        offset += 2;
    }
    else if (packet.lane === datagramLaneReliableOrdered) {
        view.setUint16(offset, packet.messageID, false);
        offset += 2;
        view.setUint16(offset, packet.orderedSeq, false);
        offset += 2;
    }
    else if (packet.lane === datagramLaneEventualState) {
        view.setBigUint64(offset, packet.stateToken, false);
        offset += datagramEventualStateTokenBytes;
    }
    if (packet.payload) {
        out.set(packet.payload, offset);
    }
    return out;
}
function decodeDatagramPacket(bytes) {
    if (bytes.byteLength < datagramPacketHeaderBytes) {
        throw new Error("golem-js: datagram packet too small");
    }
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const packet = {
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
    packet.lane = view.getUint8(offset);
    offset += 1;
    if (packet.lane !== datagramLaneUnreliable &&
        packet.lane !== datagramLaneReliableUnordered &&
        packet.lane !== datagramLaneReliableOrdered &&
        packet.lane !== datagramLaneEventualState) {
        throw new Error(`golem-js: unknown datagram lane ${packet.lane}`);
    }
    if (packet.lane === datagramLaneReliableUnordered) {
        packet.messageID = view.getUint16(offset, false);
        offset += 2;
    }
    else if (packet.lane === datagramLaneReliableOrdered) {
        packet.messageID = view.getUint16(offset, false);
        offset += 2;
        packet.orderedSeq = view.getUint16(offset, false);
        offset += 2;
    }
    else if (packet.lane === datagramLaneEventualState) {
        packet.stateToken = view.getBigUint64(offset, false);
        offset += datagramEventualStateTokenBytes;
    }
    packet.payload = bytes.slice(offset);
    return packet;
}
class WebTransportDatagramProtocol {
    constructor(writer, onClose, ackIntervalMs = datagramAckCoalesceDelayMs) {
        this._recvPackets = new SequenceWindow();
        this._recvReliable = new SequenceWindow();
        this._peerPackets = new SequenceWindow();
        this._orderedRecv = new OrderedReceiveBuffer();
        this._pendingOrdered = [];
        this._pendingUnordered = [];
        this._nextPacketSeq = 0;
        this._nextMessageID = 0;
        this._nextOrderedSeq = 0;
        this._ackDirty = false;
        this._ackDueAt = 0;
        this._scheduler = null;
        this._writeQueue = Promise.resolve();
        this._closed = false;
        this.unreliable = new WebTransportDatagramChannel(maxUnreliableDatagramPayloadBytes, (bytes) => {
            if (bytes.byteLength > maxUnreliableDatagramPayloadBytes) {
                throw new Error(`golem-js: datagram size ${bytes.byteLength} exceeds max webtransport datagram payload ${maxUnreliableDatagramPayloadBytes}`);
            }
            this._sendImmediate({
                lane: datagramLaneUnreliable,
                payload: bytes,
            });
        });
        this.reliableUnordered = new WebTransportReliableDatagramLane(maxReliableUnorderedDatagramPayloadBytes, (bytes) => this._enqueueReliable(datagramLaneReliableUnordered, bytes));
        this.reliableOrdered = new WebTransportReliableDatagramLane(maxReliableOrderedDatagramPayloadBytes, (bytes) => this._enqueueReliable(datagramLaneReliableOrdered, bytes));
        this._writer = writer;
        this._onClose = onClose;
        this._ackIntervalMs = Math.max(0, ackIntervalMs);
        this._scheduler = setInterval(() => this._tick(), datagramSchedulerIntervalMs);
    }
    close() {
        if (this._scheduler) {
            clearInterval(this._scheduler);
            this._scheduler = null;
        }
        this._closed = true;
    }
    onUnreliableMessage(fn) {
        this._unreliableMessage = fn;
    }
    onReliableUnorderedMessage(fn) {
        this._reliableUnorderedMessage = fn;
    }
    onReliableOrderedMessage(fn) {
        this._reliableOrderedMessage = fn;
    }
    onEventualStateMessage(fn) {
        this._eventualStateMessage = fn;
    }
    handleIncoming(bytes) {
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
                this._unreliableMessage?.(packet.payload);
                return;
            case datagramLaneReliableUnordered:
                if (!this._recvReliable.accept(packet.messageID)) {
                    return;
                }
                this._reliableUnorderedMessage?.(packet.payload);
                return;
            case datagramLaneReliableOrdered: {
                const deliveries = this._orderedRecv.accept(packet.orderedSeq, packet.payload, now);
                for (const delivery of deliveries) {
                    this._reliableOrderedMessage?.(delivery);
                }
                return;
            }
            case datagramLaneEventualState:
                this._eventualStateMessage?.(packet.payload);
                return;
        }
    }
    _tick() {
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
    _sendFromQueue(queue, now, allowResend) {
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
    _sendReliable(msg, now) {
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
    _enqueueReliable(lane, bytes) {
        const maxBytes = lane === datagramLaneReliableOrdered
            ? maxReliableOrderedDatagramPayloadBytes
            : maxReliableUnorderedDatagramPayloadBytes;
        if (bytes.byteLength > maxBytes) {
            throw new Error(`golem-js: reliable datagram size ${bytes.byteLength} exceeds max ${maxBytes}`);
        }
        const msg = {
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
        }
        else {
            this._pendingUnordered.push(msg);
        }
    }
    _sendImmediate(packet) {
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
    wrapReliablePayloadWithAck(payload, maxMessageBytes) {
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
    _applyPeerAcks(ackSeq, ackMask, now) {
        if (!this._peerPackets.init) {
            this._peerPackets.init = true;
            this._peerPackets.latest = ackSeq;
            this._peerPackets.mask = cloneAckMask(ackMask);
        }
        else if (seqGreater(ackSeq, this._peerPackets.latest) || ackSeq === this._peerPackets.latest) {
            this._peerPackets.latest = ackSeq;
            this._peerPackets.mask = cloneAckMask(ackMask);
        }
        this._pruneQueue(this._pendingOrdered, ackSeq, ackMask, now);
        this._pruneQueue(this._pendingUnordered, ackSeq, ackMask, now);
    }
    _pruneQueue(queue, ackSeq, ackMask, now) {
        for (let i = queue.length - 1; i >= 0; i--) {
            const msg = queue[i];
            const state = packetAckState(msg.lastPacketSeq, ackSeq, ackMask);
            if (state === "delivered") {
                queue.splice(i, 1);
            }
            else if (state === "lost") {
                msg.inFlight = false;
                msg.nextSendAt = Math.min(msg.nextSendAt, now);
            }
        }
    }
    _notifyClose(info) {
        if (this._closed) {
            return;
        }
        this.close();
        this._onClose(info);
    }
}
class WebTransportReliableChannel {
    constructor(options) {
        this.maxMessageBytes = maxReliableMessageBytes;
        this._reader = new ReliableFrameReader();
        this._connected = false;
        this._writeQueue = Promise.resolve();
        this._closedNotified = false;
        this._closeStarted = false;
        this._redactedUrl = redactUrl(options.url);
        const transport = new WebTransport(options.url, {
            serverCertificateHashes: options.serverCertificateHashes?.map(normalizeCertificateHash),
        });
        this._transport = transport;
        const datagramWriter = transport.datagrams.writable.getWriter();
        this._datagramProtocol = new WebTransportDatagramProtocol(datagramWriter, (info) => this._notifyClose(info), options.eventualAckIntervalMs);
        this.unreliable = this._datagramProtocol.unreliable;
        this.reliableUnordered = this._datagramProtocol.reliableUnordered;
        this.reliableOrdered = this._datagramProtocol.reliableOrdered;
        void this._init();
    }
    get connected() {
        return this._connected;
    }
    close() {
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
    send(bytes) {
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
    onOpen(fn) { this._onOpen = fn; }
    onMessage(fn) { this._onMessage = fn; }
    onUnreliableStateMessage(fn) {
        this._onUnreliableStateMessage = fn;
    }
    onReliableOrderedMessage(fn) {
        this._datagramProtocol?.onReliableOrderedMessage(fn);
    }
    onEventualStateMessage(fn) {
        this._datagramProtocol?.onEventualStateMessage(fn);
    }
    onClose(fn) { this._onClose = fn; }
    async _init() {
        try {
            await this._transport.ready;
            const stream = await this._transport.createBidirectionalStream();
            this._writer = stream.writable.getWriter();
            this._connected = true;
            this._onOpen?.();
            void this._readStream(stream.readable);
            void this._readDatagrams();
            this._watchClosed();
        }
        catch (error) {
            console.error(`golem-js: webtransport connect failed url=${this._redactedUrl} error=${String(error)}`);
            this._notifyClose({ wasClean: false, error });
        }
    }
    async _readStream(readable) {
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
        }
        catch (error) {
            this._notifyClose({ wasClean: false, error });
        }
        finally {
            reader.releaseLock();
        }
    }
    async _watchClosed() {
        try {
            const info = await this._transport.closed;
            this._notifyClose({
                code: info?.closeCode,
                reason: info?.reason,
                wasClean: true,
            });
        }
        catch (error) {
            this._notifyClose({ wasClean: false, error });
        }
    }
    async _readDatagrams() {
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
                }
                catch {
                    this._onUnreliableStateMessage?.(value);
                }
            }
        }
        catch (error) {
            this._notifyClose({ wasClean: false, error });
        }
        finally {
            reader.releaseLock();
        }
    }
    _notifyClose(info) {
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
export function createChannel(options) {
    if (options.transport === "webtransport") {
        return new WebTransportReliableChannel(options);
    }
    return new WebSocketReliableChannel(options.url);
}
/** Decode a ServerMessage envelope, extracting the inner payload by field tag. */
export function unwrapServerMessage(bytes) {
    const r = new PbReader(bytes);
    const result = {};
    while (!r.done) {
        const { field, wire } = r.tag();
        if (field === 1 && wire === 2) {
            result.entityUpdate = r.bytes();
        }
        else if (field === 2 && wire === 2) {
            result.worldUpdate = r.bytes();
        }
        else if (field === 3 && wire === 2) {
            result.serverEvent = r.bytes();
        }
        else {
            r.skip(wire);
        }
    }
    return result;
}
