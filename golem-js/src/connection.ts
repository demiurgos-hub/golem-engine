import type { ConnectInput, DisconnectInfo, GameClient } from "./client.js";

/** Connection lifecycle status emitted by the persistent client. */
export type GolemConnectionStatus =
  | { type: "connecting"; attempt: number }
  | { type: "connected" }
  | { type: "reconnecting"; attempt: number; delayMs: number }
  | { type: "disconnected"; info: DisconnectInfo }
  | { type: "failed"; attempts: number };

/** Observable lifecycle snapshot, including its initial or cancelled idle state. */
export type GolemConnectionSnapshot = GolemConnectionStatus | { type: "idle" };

/** Configuration for the persistent Golem client connection. */
export interface GolemConnectionConfig<C extends GameClient = GameClient> {
  /** Called once when the connection lifecycle starts. */
  createClient: () => C;
  /**
   * Called and awaited for every initial or reconnect attempt. Throwing or
   * rejecting is handled like an unexpected connection failure.
   */
  connectionOptions: () => ConnectInput | Promise<ConnectInput>;
  /** Maximum reconnect attempts. Zero means unlimited. Defaults to 5. */
  maxReconnectAttempts?: number;
  /** Base reconnect delay in milliseconds. Defaults to 1500. */
  reconnectBaseDelay?: number;
  /** Connect immediately when started. Defaults to true. */
  autoConnect?: boolean;
}

interface ConnectionScheduler {
  setTimeout(fn: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

const defaultScheduler: ConnectionScheduler = {
  setTimeout: (fn, delayMs) => globalThis.setTimeout(fn, delayMs),
  clearTimeout: (handle) =>
    globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
};

function isPromise<T>(value: T | Promise<T>): value is Promise<T> {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as Promise<T>).then === "function"
  );
}

/**
 * GolemConnectionLifecycle owns one generated client and reconnects unexpected
 * transport drops independently of renderer or scene lifetimes.
 */
export class GolemConnectionLifecycle<C extends GameClient = GameClient> {
  private readonly statusListeners = new Set<
    (status: GolemConnectionStatus) => void
  >();
  private readonly snapshotListeners = new Set<
    (status: GolemConnectionSnapshot) => void
  >();
  private currentStatus: GolemConnectionSnapshot = { type: "idle" };
  private unsubscribeConnectionState?: () => void;
  private disconnectNotifications = 0;
  private clientInstance?: C;
  private reconnectAttempts = 0;
  private reconnectTimer?: unknown;
  private stopped = true;
  private destroyed = false;
  private connectionAttempt = 0;

  constructor(
    private readonly config: GolemConnectionConfig<C>,
    private readonly scheduler: ConnectionScheduler = defaultScheduler,
  ) {
    if (typeof config.createClient !== "function") {
      throw new Error("golem-engine: createClient must be a function");
    }
    if (typeof config.connectionOptions !== "function") {
      throw new Error("golem-engine: connectionOptions must be a function");
    }
    if (
      config.maxReconnectAttempts !== undefined &&
      (!Number.isInteger(config.maxReconnectAttempts) ||
        config.maxReconnectAttempts < 0)
    ) {
      throw new Error(
        "golem-engine: maxReconnectAttempts must be a non-negative integer",
      );
    }
    if (
      config.reconnectBaseDelay !== undefined &&
      (!Number.isFinite(config.reconnectBaseDelay) ||
        config.reconnectBaseDelay < 0)
    ) {
      throw new Error(
        "golem-engine: reconnectBaseDelay must be a non-negative number",
      );
    }
  }

  /** The generated client created by start(). */
  get client(): C {
    if (!this.clientInstance) {
      throw new Error(
        "golem-engine: Golem connection has not been started",
      );
    }
    return this.clientInstance;
  }

  /** Whether the underlying client transport is connected. */
  get connected(): boolean {
    return this.clientInstance?.connected ?? false;
  }

  /** Current lifecycle status, including idle before connection or after cancellation. */
  get status(): GolemConnectionSnapshot {
    return this.currentStatus;
  }

  /** Create and wire the client, then optionally connect. */
  start(): void {
    if (this.destroyed) {
      throw new Error("golem-engine: Golem connection is destroyed");
    }
    if (this.clientInstance) {
      return;
    }

    this.stopped = false;
    const client = this.config.createClient();
    this.clientInstance = client;
    const connectionAttempt = this.connectionAttempt;
    const unsubscribe = client.subscribeConnectionState((state) => {
      if (state.type === "connected") {
        this.handleConnect();
      } else if (state.type === "disconnected") {
        this.handleDisconnect(state.info);
      } else if (state.type === "idle") {
        this.publishSnapshot({ type: "idle" });
      }
    });
    // Immediate replay may synchronously destroy the lifecycle before subscribe returns.
    if (this.destroyed) {
      unsubscribe();
      return;
    }
    this.unsubscribeConnectionState = unsubscribe;

    if (this.isCurrentAttempt(connectionAttempt) && this.config.autoConnect !== false) {
      this.connect();
    }
  }

  /** Open the configured connection immediately. */
  connect(): void {
    if (this.destroyed) {
      throw new Error("golem-engine: Golem connection is destroyed");
    }
    this.stopped = false;
    this.cancelReconnect();
    const connectionAttempt = ++this.connectionAttempt;

    const attempt = this.reconnectAttempts + 1;
    this.emit({ type: "connecting", attempt });
    if (!this.isCurrentAttempt(connectionAttempt)) return;

    let options: ConnectInput | Promise<ConnectInput>;
    try {
      options = this.config.connectionOptions();
    } catch (error) {
      this.handleAttemptFailure(
        connectionAttempt,
        error,
        "connection options failed",
      );
      return;
    }

    if (isPromise(options)) {
      void Promise.resolve(options).then(
        (resolved) => this.openConnection(connectionAttempt, resolved),
        (error: unknown) =>
          this.handleAttemptFailure(
            connectionAttempt,
            error,
            "connection options failed",
          ),
      );
      return;
    }

    this.openConnection(connectionAttempt, options);
  }

  /** Stop reconnecting and close the current transport. */
  disconnect(): void {
    this.stopped = true;
    const connectionAttempt = ++this.connectionAttempt;
    this.cancelReconnect();
    const connected = this.connected;
    this.clientInstance?.disconnect();
    if (!connected && this.connectionAttempt === connectionAttempt) {
      this.publishSnapshot({ type: "idle" });
    }
  }

  /** Subscribe to connection status changes. */
  onStatus(listener: (status: GolemConnectionStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  }

  /** Observe current and subsequent status; cancelling from a callback fences the interrupted attempt. */
  subscribeStatus(listener: (status: GolemConnectionSnapshot) => void): () => void {
    if (!this.destroyed) {
      this.snapshotListeners.add(listener);
    }
    listener(this.currentStatus);
    return () => {
      this.snapshotListeners.delete(listener);
    };
  }

  /** Permanently stop the connection lifecycle and release listeners. */
  destroy(): void {
    if (this.destroyed) {
      return;
    }
    this.destroyed = true;
    this.disconnect();
    this.unsubscribeConnectionState?.();
    this.unsubscribeConnectionState = undefined;
    this.statusListeners.clear();
    this.snapshotListeners.clear();
  }

  private handleConnect(): void {
    this.reconnectAttempts = 0;
    this.cancelReconnect();
    this.emit({ type: "connected" });
  }

  private handleDisconnect(info: DisconnectInfo): void {
    this.disconnectNotifications++;
    const connectionAttempt = this.connectionAttempt;
    const status: GolemConnectionStatus = { type: "disconnected", info };
    this.emit(status);
    if (this.currentStatus === status && this.isCurrentAttempt(connectionAttempt) && !info.wasClean) {
      this.scheduleReconnect();
    }
  }

  private openConnection(
    connectionAttempt: number,
    options: ConnectInput,
  ): void {
    if (!this.isCurrentAttempt(connectionAttempt)) {
      return;
    }

    const disconnectNotifications = this.disconnectNotifications;
    try {
      this.client.connect(options);
    } catch (error) {
      if (this.disconnectNotifications === disconnectNotifications) {
        this.handleAttemptFailure(connectionAttempt, error, "connect failed");
      }
    }
  }

  private handleAttemptFailure(
    connectionAttempt: number,
    error: unknown,
    reason: string,
  ): void {
    if (!this.isCurrentAttempt(connectionAttempt)) {
      return;
    }
    this.handleDisconnect({ wasClean: false, error, reason });
  }

  private isCurrentAttempt(connectionAttempt: number): boolean {
    return (
      connectionAttempt === this.connectionAttempt &&
      !this.stopped &&
      !this.destroyed
    );
  }

  private scheduleReconnect(): void {
    const connectionAttempt = this.connectionAttempt;
    const maximum = this.config.maxReconnectAttempts ?? 5;
    if (maximum > 0 && this.reconnectAttempts >= maximum) {
      this.emit({ type: "failed", attempts: this.reconnectAttempts });
      return;
    }

    this.reconnectAttempts++;
    const baseDelay = this.config.reconnectBaseDelay ?? 1500;
    const delayMs = baseDelay * 2 ** (this.reconnectAttempts - 1);
    const status: GolemConnectionStatus = {
      type: "reconnecting",
      attempt: this.reconnectAttempts,
      delayMs,
    };
    this.emit(status);
    if (this.currentStatus !== status || !this.isCurrentAttempt(connectionAttempt)) return;
    const timer = this.scheduler.setTimeout(() => {
      if (this.reconnectTimer !== timer || !this.isCurrentAttempt(connectionAttempt)) return;
      this.reconnectTimer = undefined;
      this.connect();
    }, delayMs);
    this.reconnectTimer = timer;
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer === undefined) {
      return;
    }
    this.scheduler.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = undefined;
  }

  private emit(status: GolemConnectionStatus): void {
    this.publishSnapshot(status);
    for (const listener of [...this.statusListeners]) {
      if (this.currentStatus !== status) break;
      if (this.statusListeners.has(listener)) listener(status);
    }
  }

  private publishSnapshot(status: GolemConnectionSnapshot): void {
    if (status.type === "idle" && this.currentStatus.type === "idle") {
      return;
    }
    this.currentStatus = status;
    for (const listener of [...this.snapshotListeners]) {
      if (this.currentStatus !== status) {
        break;
      }
      if (this.snapshotListeners.has(listener)) listener(status);
    }
  }
}

