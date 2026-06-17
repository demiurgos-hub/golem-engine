import Phaser from "phaser";
import type { ConnectOptions, DisconnectInfo, GameClient } from "golem-engine";
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
export declare abstract class GameScene<C extends GameClient = GameClient> extends Phaser.Scene {
    /** The connected client instance. Set after create() runs. */
    protected client: C;
    /**
     * Maximum number of reconnect attempts before onReconnectFailed() is called.
     * Set to 0 for infinite retries.
     */
    protected maxReconnectAttempts: number;
    /** Base reconnect delay in milliseconds. Doubles with each attempt. */
    protected reconnectBaseDelay: number;
    private _attempts;
    private _cleaningUp;
    private _reconnectTimer;
    private _overlayBg?;
    private _overlayText?;
    /** Return a new client instance. Called once per create() invocation. */
    protected abstract buildClient(): C;
    /**
     * Return a legacy WebSocket URL to connect to.
     * Deprecated: override connectionOptions() for transport-aware connections.
     */
    protected serverUrl(): string;
    /** Return the connection options to use on each connection attempt. */
    protected connectionOptions(): string | ConnectOptions;
    /** Called when the connection is established (or re-established). */
    protected onConnect(): void;
    /** Called when the connection closes for any reason. */
    protected onDisconnect(_ev: DisconnectInfo): void;
    /** Called when all reconnect attempts have been exhausted. */
    protected onReconnectFailed(): void;
    /**
     * Show a connection status overlay. The default implementation creates a
     * centered semi-transparent panel using Phaser game objects. Override to
     * replace with your own UI.
     */
    protected showConnectionOverlay(message: string): void;
    /** Hide the connection overlay. Override if you replaced showConnectionOverlay. */
    protected hideConnectionOverlay(): void;
    /**
     * Wire the client and open the transport connection. Subclasses must call
     * super.create() before accessing this.client.
     */
    create(): void;
    private _scheduleReconnect;
    private _cancelReconnectTimer;
    private _cleanup;
}
