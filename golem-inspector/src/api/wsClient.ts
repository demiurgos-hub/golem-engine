import { WireError } from '../protocol/codec';
import { decodeMessage, encodePing, encodeResync, encodeSubscribe, VersionError } from '../protocol/messages';
import type { ConnectionStore } from '../store/connectionStore';
import type { DebugLog } from '../store/debugLog';
import type { EntityStore } from '../store/entityStore';

export interface BrowserLocation {
  protocol: string;
  host: string;
}

/** Derives the hub's browser WebSocket URL, with an optional dev override. */
export function deriveWsUrl(location: BrowserLocation | undefined, override?: string): string {
  if (override) return override;
  if (location?.host) {
    return `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/client`;
  }
  return 'ws://127.0.0.1:8787/ws/client';
}

/** Same-origin by default so the session cookie accompanies the handshake. */
export function resolveDefaultWsUrl(): string {
  const location = typeof window !== 'undefined' ? window.location : undefined;
  return deriveWsUrl(location, import.meta.env.VITE_WS_URL as string | undefined);
}

export const DEFAULT_WS_URL = resolveDefaultWsUrl();

/** Full-jitter backoff: uniform in [0, min(max, 500ms * 2^(attempt-1))). */
export function reconnectDelay(attempt: number, random: () => number, maxBackoffMs = 10_000): number {
  const cap = Math.min(maxBackoffMs, 500 * 2 ** Math.min(attempt - 1, 5));
  return Math.floor(random() * cap);
}

export interface WsClientOptions {
  url?: string;
  entityStore: EntityStore;
  connectionStore: ConnectionStore;
  debugLog?: DebugLog;
  /** The selected target; null suppresses subscribe until one is chosen. */
  getTargetId: () => string | null;
  autoReconnect?: boolean;
  maxBackoffMs?: number;
  random?: () => number;
  /** Injectable for tests. */
  createSocket?: (url: string) => WebSocket;
}

export interface WsClient {
  start(): void;
  stop(): void;
  subscribe(targetId: string): void;
  resync(): void;
  isOpen(): boolean;
}

export function createWsClient(options: WsClientOptions): WsClient {
  const {
    entityStore,
    connectionStore,
    debugLog,
    getTargetId,
    autoReconnect = true,
    maxBackoffMs = 10_000,
    random = Math.random,
    createSocket = (u) => new WebSocket(u),
  } = options;
  const url = options.url ?? DEFAULT_WS_URL;
  let ws: WebSocket | null = null;
  let stopped = true;
  let attempt = 0;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let subscribeRetryTimer: ReturnType<typeof setTimeout> | null = null;
  let pingTimer: ReturnType<typeof setInterval> | null = null;
  let prevNeedsResync = false;

  const clearTimers = () => {
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (subscribeRetryTimer) clearTimeout(subscribeRetryTimer);
    if (pingTimer) clearInterval(pingTimer);
    reconnectTimer = subscribeRetryTimer = pingTimer = null;
  };

  const send = (frame: Uint8Array<ArrayBuffer>) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(frame);
    if (debugLog) {
      try {
        debugLog.record('out', frame.byteLength, decodeMessage(frame));
      } catch {
        /* outbound frames are always well formed */
      }
    }
  };

  const sendSubscribe = () => {
    const targetId = getTargetId();
    if (targetId) send(encodeSubscribe(targetId));
  };

  // A target can disappear and come back (container restart); retry once.
  const scheduleSubscribeRetry = () => {
    if (stopped || subscribeRetryTimer) return;
    subscribeRetryTimer = setTimeout(() => {
      subscribeRetryTimer = null;
      const s = entityStore.getState();
      if (!s.ready && !s.snapshotProgress) sendSubscribe();
    }, 750);
  };

  const scheduleReconnect = () => {
    if (stopped || !autoReconnect) return;
    attempt += 1;
    connectionStore.setReconnectAttempt(attempt);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, reconnectDelay(attempt, random, maxBackoffMs));
  };

  const handleFrame = (data: ArrayBuffer) => {
    connectionStore.noteMessage();
    const bytes = new Uint8Array(data);
    let msg;
    try {
      msg = decodeMessage(bytes);
    } catch (err) {
      const detail = err instanceof VersionError ? `${err.message}: the hub runs a different inspector protocol` : err instanceof WireError ? `malformed frame: ${err.message}` : String(err);
      debugLog?.record('in', bytes.byteLength, null, detail);
      connectionStore.setStatus('protocol_error', detail);
      return;
    }
    debugLog?.record('in', bytes.byteLength, msg);
    if (!msg) return;
    if (msg.type === 'pong') {
      connectionStore.notePong(msg.clientTime);
      return;
    }
    entityStore.apply(msg);
    if (msg.type === 'error' && msg.code === 'target_not_found') scheduleSubscribeRetry();
    const s = entityStore.getState();
    if (s.needsResync && !prevNeedsResync) {
      const targetId = getTargetId();
      if (targetId) send(encodeResync(targetId));
    }
    prevNeedsResync = s.needsResync;
  };

  const detach = () => {
    if (!ws) return;
    ws.onopen = ws.onclose = ws.onmessage = ws.onerror = null;
    try {
      ws.close();
    } catch {
      /* already closed */
    }
    ws = null;
  };

  const connect = () => {
    if (stopped) return;
    clearTimers();
    connectionStore.setUrl(url);
    connectionStore.setStatus(attempt > 0 ? 'reconnecting' : 'connecting');
    try {
      ws = createSocket(url);
    } catch {
      scheduleReconnect();
      return;
    }
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      attempt = 0; // a successful open resets the backoff
      connectionStore.noteConnected();
      prevNeedsResync = false;
      entityStore.clear();
      sendSubscribe();
      pingTimer = setInterval(() => send(encodePing(Date.now())), 5_000);
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (ev.data instanceof ArrayBuffer) handleFrame(ev.data);
    };
    ws.onclose = () => {
      ws = null;
      clearTimers();
      connectionStore.noteDisconnected();
      if (!stopped) {
        entityStore.clear('disconnected');
        scheduleReconnect();
      }
    };
  };

  return {
    start() {
      stopped = false;
      attempt = 0;
      clearTimers();
      detach();
      connect();
    },
    stop() {
      stopped = true;
      clearTimers();
      detach();
      connectionStore.noteDisconnected();
    },
    subscribe(targetId) {
      send(encodeSubscribe(targetId));
    },
    resync() {
      const targetId = getTargetId();
      if (!targetId) return;
      entityStore.clear('manual_resync');
      prevNeedsResync = true;
      send(encodeResync(targetId));
    },
    isOpen: () => ws?.readyState === WebSocket.OPEN,
  };
}
