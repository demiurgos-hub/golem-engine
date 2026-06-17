import Phaser from "phaser";
import type { ConnectOptions, DisconnectInfo, GameClient } from "golem-engine";

function redactConnectionUrl(url: string | ConnectOptions): string {
  const raw = typeof url === "string" ? url : url.url;
  try {
    const parsed = new URL(raw);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return raw;
  }
}

function connectionTransport(url: string | ConnectOptions): string {
  return typeof url === "string" ? "websocket" : url.transport;
}

/**
 * GameScene is a Phaser.Scene base class that owns the Golem Engine client
 * lifecycle: it connects on create(), reconnects with exponential backoff after
 * unexpected drops, and disconnects cleanly on shutdown or destroy.
 *
 * Subclass and implement buildClient() plus either serverUrl() or connectionOptions().
 * Override the optional
 * hook methods to react to connection events.
 *
 * @example
 * class BattleScene extends GameScene<Client> {
 *   protected buildClient() { return createClient(); }
 *   protected serverUrl() { return 'ws://localhost:8080/ws'; }
 *   create() {
 *     super.create(); // wires and connects
 *     this.client.entities.registerPlayer(MyPlayer);
 *   }
 * }
 */
export abstract class GameScene<
  C extends GameClient = GameClient,
> extends Phaser.Scene {
  /** The connected client instance. Set after create() runs. */
  protected client!: C;

  /**
   * Maximum number of reconnect attempts before onReconnectFailed() is called.
   * Set to 0 for infinite retries.
   */
  protected maxReconnectAttempts = 5;

  /** Base reconnect delay in milliseconds. Doubles with each attempt. */
  protected reconnectBaseDelay = 1500;

  private _attempts = 0;
  private _cleaningUp = false;
  private _reconnectTimer: Phaser.Time.TimerEvent | null = null;
  private _overlayBg?: Phaser.GameObjects.Rectangle;
  private _overlayText?: Phaser.GameObjects.Text;

  /** Return a new client instance. Called once per create() invocation. */
  protected abstract buildClient(): C;

  /**
   * Return a legacy WebSocket URL to connect to.
   * Deprecated: override connectionOptions() for transport-aware connections.
   */
  protected serverUrl(): string {
    throw new Error("golem-phaser: override serverUrl() or connectionOptions()");
  }

  /** Return the connection options to use on each connection attempt. */
  protected connectionOptions(): string | ConnectOptions {
    return this.serverUrl();
  }

  /** Called when the connection is established (or re-established). */
  protected onConnect(): void {}

  /** Called when the connection closes for any reason. */
  protected onDisconnect(_ev: DisconnectInfo): void {}

  /** Called when all reconnect attempts have been exhausted. */
  protected onReconnectFailed(): void {}

  /**
   * Show a connection status overlay. The default implementation creates a
   * centered semi-transparent panel using Phaser game objects. Override to
   * replace with your own UI.
   */
  protected showConnectionOverlay(message: string): void {
    const cam = this.cameras.main;
    if (!this._overlayBg) {
      this._overlayBg = this.add
        .rectangle(cam.width / 2, cam.height / 2, 320, 72, 0x000000, 0.72)
        .setScrollFactor(0)
        .setDepth(1000);
    }
    if (!this._overlayText) {
      this._overlayText = this.add
        .text(cam.width / 2, cam.height / 2, "", {
          fontSize: "18px",
          color: "#ffffff",
          align: "center",
        })
        .setOrigin(0.5)
        .setScrollFactor(0)
        .setDepth(1001);
    }
    this._overlayBg.setVisible(true);
    this._overlayText.setText(message).setVisible(true);
  }

  /** Hide the connection overlay. Override if you replaced showConnectionOverlay. */
  protected hideConnectionOverlay(): void {
    this._overlayBg?.setVisible(false);
    this._overlayText?.setVisible(false);
  }

  /**
   * Wire the client and open the transport connection. Subclasses must call
   * super.create() before accessing this.client.
   */
  create(): void {
    this._attempts = 0;
    this._cleaningUp = false;
    this.client = this.buildClient();

    this.client.onConnect(() => {
      this._attempts = 0;
      this._cancelReconnectTimer();
      this.hideConnectionOverlay();
      this.onConnect();
    });

    this.client.onDisconnect((ev) => {
      if (ev.wasClean) {
        console.warn("golem-phaser: disconnected was_clean=true");
      } else {
        const code = ev.code != null ? ` code=${ev.code}` : "";
        const reason = ev.reason ? ` reason=${ev.reason}` : "";
        const error =
          ev.error instanceof Error ? ev.error.message : String(ev.error ?? "");
        console.error(
          `golem-phaser: disconnected was_clean=false${code}${reason} error=${error}`,
        );
      }
      this.onDisconnect(ev);
      if (!this._cleaningUp && !ev.wasClean) {
        this._scheduleReconnect();
      }
    });

    // Use once() so re-entrant create() calls (scene restart) re-register cleanly.
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => this._cleanup());
    this.events.once(Phaser.Scenes.Events.DESTROY, () => this._cleanup());

    const options = this.connectionOptions();
    console.warn(
      `golem-phaser: connecting attempt=1 transport=${connectionTransport(options)} url=${redactConnectionUrl(options)}`,
    );
    this.client.connect(options);
  }

  private _scheduleReconnect(): void {
    if (this.maxReconnectAttempts > 0 && this._attempts >= this.maxReconnectAttempts) {
      console.error(`golem-phaser: reconnect failed attempts=${this._attempts}`);
      this.onReconnectFailed();
      return;
    }
    this._attempts++;
    const delay = this.reconnectBaseDelay * Math.pow(2, this._attempts - 1);
    console.warn(`golem-phaser: reconnect scheduled attempt=${this._attempts} delay_ms=${delay}`);
    const label =
      this.maxReconnectAttempts > 0
        ? `Connecting… (${this._attempts} / ${this.maxReconnectAttempts})`
        : `Connecting… (attempt ${this._attempts})`;
    this.showConnectionOverlay(label);
    // Use Phaser's timer so reconnect respects scene pause/resume.
    this._reconnectTimer = this.time.delayedCall(delay, () => {
      this.client.connect(this.connectionOptions());
    });
  }

  private _cancelReconnectTimer(): void {
    if (this._reconnectTimer) {
      this._reconnectTimer.destroy();
      this._reconnectTimer = null;
    }
  }

  private _cleanup(): void {
    this._cleaningUp = true;
    this._cancelReconnectTimer();
    console.warn("golem-phaser: disconnecting (scene shutdown)");
    this.client?.disconnect();
  }
}
