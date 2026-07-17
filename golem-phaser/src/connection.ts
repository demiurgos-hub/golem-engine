import type { ConnectOptions, DisconnectInfo, GameClient } from "golem-engine";

/** Connection lifecycle status emitted by the Phaser integration. */
export type GolemConnectionStatus =
  | { type: "connecting"; attempt: number }
  | { type: "connected" }
  | { type: "reconnecting"; attempt: number; delayMs: number }
  | { type: "disconnected"; info: DisconnectInfo }
  | { type: "failed"; attempts: number };

/** Configuration for the persistent Golem client connection. */
export interface GolemConnectionConfig<C extends GameClient = GameClient> {
  /** Called once when the connection lifecycle starts. */
  createClient: () => C;
  /** Called for every initial or reconnect attempt. */
  connectionOptions: () => string | ConnectOptions;
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

/**
 * GolemConnectionLifecycle owns one generated client and reconnects unexpected
 * transport drops independently of Phaser scene lifetimes.
 *
 * @internal
 */
export class GolemConnectionLifecycle<C extends GameClient = GameClient> {
  private readonly statusListeners = new Set<
    (status: GolemConnectionStatus) => void
  >();
  private clientInstance?: C;
  private reconnectAttempts = 0;
  private reconnectTimer?: unknown;
  private stopped = true;
  private destroyed = false;

  constructor(
    private readonly config: GolemConnectionConfig<C>,
    private readonly scheduler: ConnectionScheduler = defaultScheduler,
  ) {
    if (typeof config.createClient !== "function") {
      throw new Error("golem-phaser: createClient must be a function");
    }
    if (typeof config.connectionOptions !== "function") {
      throw new Error("golem-phaser: connectionOptions must be a function");
    }
    if (
      config.maxReconnectAttempts !== undefined &&
      (!Number.isInteger(config.maxReconnectAttempts) ||
        config.maxReconnectAttempts < 0)
    ) {
      throw new Error(
        "golem-phaser: maxReconnectAttempts must be a non-negative integer",
      );
    }
    if (
      config.reconnectBaseDelay !== undefined &&
      (!Number.isFinite(config.reconnectBaseDelay) ||
        config.reconnectBaseDelay < 0)
    ) {
      throw new Error(
        "golem-phaser: reconnectBaseDelay must be a non-negative number",
      );
    }
  }

  /** The generated client created by start(). */
  get client(): C {
    if (!this.clientInstance) {
      throw new Error(
        "golem-phaser: Golem connection has not been started",
      );
    }
    return this.clientInstance;
  }

  /** Whether the underlying client transport is connected. */
  get connected(): boolean {
    return this.clientInstance?.connected ?? false;
  }

  /** Create and wire the client, then optionally connect. */
  start(): void {
    if (this.destroyed) {
      throw new Error("golem-phaser: Golem connection is destroyed");
    }
    if (this.clientInstance) {
      return;
    }

    this.stopped = false;
    const client = this.config.createClient();
    this.clientInstance = client;
    client.onConnect(() => this.handleConnect());
    client.onDisconnect((info) => this.handleDisconnect(info));

    if (this.config.autoConnect !== false) {
      this.connect();
    }
  }

  /** Open the configured connection immediately. */
  connect(): void {
    if (this.destroyed) {
      throw new Error("golem-phaser: Golem connection is destroyed");
    }
    this.stopped = false;
    this.cancelReconnect();

    const attempt = this.reconnectAttempts + 1;
    this.emit({ type: "connecting", attempt });

    let options: string | ConnectOptions;
    try {
      options = this.config.connectionOptions();
    } catch (error) {
      this.handleDisconnect({
        wasClean: false,
        error,
        reason: "connection options failed",
      });
      return;
    }

    try {
      this.client.connect(options);
    } catch (error) {
      this.handleDisconnect({
        wasClean: false,
        error,
        reason: "connect failed",
      });
    }
  }

  /** Stop reconnecting and close the current transport. */
  disconnect(): void {
    this.stopped = true;
    this.cancelReconnect();
    this.clientInstance?.disconnect();
  }

  /** Subscribe to connection status changes. */
  onStatus(listener: (status: GolemConnectionStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  }

  /** Permanently stop the connection lifecycle and release listeners. */
  destroy(): void {
    if (this.destroyed) {
      return;
    }
    this.destroyed = true;
    this.disconnect();
    this.statusListeners.clear();
  }

  private handleConnect(): void {
    this.reconnectAttempts = 0;
    this.cancelReconnect();
    this.emit({ type: "connected" });
  }

  private handleDisconnect(info: DisconnectInfo): void {
    this.emit({ type: "disconnected", info });
    if (!this.stopped && !info.wasClean) {
      this.scheduleReconnect();
    }
  }

  private scheduleReconnect(): void {
    const maximum = this.config.maxReconnectAttempts ?? 5;
    if (maximum > 0 && this.reconnectAttempts >= maximum) {
      this.emit({ type: "failed", attempts: this.reconnectAttempts });
      return;
    }

    this.reconnectAttempts++;
    const baseDelay = this.config.reconnectBaseDelay ?? 1500;
    const delayMs = baseDelay * 2 ** (this.reconnectAttempts - 1);
    this.emit({
      type: "reconnecting",
      attempt: this.reconnectAttempts,
      delayMs,
    });
    this.reconnectTimer = this.scheduler.setTimeout(() => {
      this.reconnectTimer = undefined;
      this.connect();
    }, delayMs);
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer === undefined) {
      return;
    }
    this.scheduler.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = undefined;
  }

  private emit(status: GolemConnectionStatus): void {
    for (const listener of this.statusListeners) {
      listener(status);
    }
  }
}
