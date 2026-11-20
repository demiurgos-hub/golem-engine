export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'reconnecting' | 'protocol_error';

export interface ConnectionState {
  status: ConnectionStatus;
  url: string;
  lastConnectedAt: number | null;
  lastDisconnectedAt: number | null;
  lastMessageAt: number | null;
  reconnectAttempt: number;
  protocolErrorDetail: string | null;
  /** Inbound messages over the last second. */
  updateRate: number;
  latencyMs: number | null;
}

export interface ConnectionStore {
  getState(): ConnectionState;
  subscribe(listener: () => void): () => void;
  setStatus(status: ConnectionStatus, detail?: string): void;
  setUrl(url: string): void;
  noteConnected(): void;
  noteDisconnected(): void;
  noteMessage(): void;
  notePong(sentTs: number): void;
  setReconnectAttempt(n: number): void;
}

export function createConnectionStore(url: string): ConnectionStore {
  let state: ConnectionState = {
    status: 'disconnected',
    url,
    lastConnectedAt: null,
    lastDisconnectedAt: null,
    lastMessageAt: null,
    reconnectAttempt: 0,
    protocolErrorDetail: null,
    updateRate: 0,
    latencyMs: null,
  };
  const listeners = new Set<() => void>();
  const stamps: number[] = [];

  const set = (patch: Partial<ConnectionState>) => {
    state = { ...state, ...patch };
    for (const l of listeners) l();
  };

  const recomputeRate = () => {
    const now = Date.now();
    while (stamps.length > 0 && stamps[0]! < now - 1000) stamps.shift();
    if (stamps.length !== state.updateRate) set({ updateRate: stamps.length });
  };

  let rateTimer: ReturnType<typeof setInterval> | null = null;
  const ensureRateTimer = () => {
    if (!rateTimer) rateTimer = setInterval(recomputeRate, 500);
  };

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    setUrl(next) {
      set({ url: next });
    },
    setStatus(status, detail) {
      set({ status, protocolErrorDetail: status === 'protocol_error' ? (detail ?? state.protocolErrorDetail) : null });
      if (status === 'connected' || status === 'connecting') ensureRateTimer();
    },
    noteConnected() {
      set({ status: 'connected', lastConnectedAt: Date.now(), reconnectAttempt: 0, protocolErrorDetail: null });
      ensureRateTimer();
    },
    noteDisconnected() {
      set({ status: state.status === 'protocol_error' ? 'protocol_error' : 'disconnected', lastDisconnectedAt: Date.now(), latencyMs: null });
    },
    noteMessage() {
      const now = Date.now();
      stamps.push(now);
      // Coalesce notifications: lastMessageAt alone does not re-render.
      state = { ...state, lastMessageAt: now };
    },
    notePong(sentTs) {
      set({ latencyMs: Math.max(0, Date.now() - sentTs) });
    },
    setReconnectAttempt(n) {
      set({ reconnectAttempt: n, status: 'reconnecting' });
    },
  };
}
