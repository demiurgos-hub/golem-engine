import Phaser from "phaser";
import type { GameClient } from "golem-engine";
import {
  GolemConnectionLifecycle,
  type GolemConnectionConfig,
  type GolemConnectionStatus,
} from "./connection.js";
import type { EntityViewRegistry } from "./view-registry.js";
import { EntityViewMount } from "./view-mount.js";

/** Phaser global-plugin key used by the Golem integration. */
export const GOLEM_PLUGIN_KEY = "GolemPlugin";

/** Fixed scene mapping that exposes the plugin as `this.golem`. */
export const GOLEM_PLUGIN_MAPPING = "golem";

/** Configuration passed as data to the global Golem plugin. */
export type GolemPluginConfig<C extends GameClient = GameClient> =
  GolemConnectionConfig<C>;

/**
 * GolemPlugin owns one persistent generated client for the Phaser game.
 * Scenes mount presentation registries without owning the network connection.
 */
export class GolemPlugin<
  C extends GameClient = GameClient,
> extends Phaser.Plugins.BasePlugin {
  private connection?: GolemConnectionLifecycle<C>;
  private readonly mounts = new Map<Phaser.Scene, EntityViewMount<C>>();
  private config?: GolemPluginConfig<C>;

  /** Configure the plugin from its Phaser global-plugin data. */
  init(data: GolemPluginConfig<C>): void {
    this.config = data;
  }

  /** Build the generated client and start its persistent connection lifecycle. */
  start(): void {
    if (!this.config) {
      throw new Error(
        "golem-phaser: GolemPlugin requires plugin data configuration",
      );
    }
    if (this.connection) {
      return;
    }
    this.connection = new GolemConnectionLifecycle(this.config);
    this.connection.start();
  }

  /** The persistent generated client. */
  get client(): C {
    return this.requireConnection().client;
  }

  /** Whether the generated client transport is connected. */
  get connected(): boolean {
    return this.connection?.connected ?? false;
  }

  /** Open the configured connection immediately. */
  connect(): void {
    this.requireConnection().connect();
  }

  /** Stop reconnecting and close the current transport. */
  disconnect(): void {
    this.connection?.disconnect();
  }

  /** Subscribe to typed connection status changes for game-owned UI. */
  onStatus(listener: (status: GolemConnectionStatus) => void): () => void {
    return this.requireConnection().onStatus(listener);
  }

  /**
   * Mount a generated entity-view registry onto a scene. Existing entities are
   * created immediately, and shutdown removes only scene-local views.
   */
  mount(
    scene: Phaser.Scene,
    registry: EntityViewRegistry<C>,
  ): EntityViewMount<C> {
    this.unmount(scene);

    const mount = new EntityViewMount(
      scene,
      this.client,
      registry,
      () => {
        if (this.mounts.get(scene) === mount) {
          this.mounts.delete(scene);
        }
      },
    );
    this.mounts.set(scene, mount);
    mount.mount();
    return mount;
  }

  /** Unmount the active entity views for a scene, if any. */
  unmount(scene: Phaser.Scene): void {
    const mount = this.mounts.get(scene);
    if (!mount) {
      return;
    }
    this.mounts.delete(scene);
    mount.unmount();
  }

  /** Disconnect and release all game- and scene-lifetime resources. */
  destroy(): void {
    for (const mount of [...this.mounts.values()]) {
      mount.unmount();
    }
    this.mounts.clear();
    this.connection?.destroy();
    this.connection = undefined;
    super.destroy();
  }

  private requireConnection(): GolemConnectionLifecycle<C> {
    if (!this.connection) {
      throw new Error("golem-phaser: GolemPlugin has not started");
    }
    return this.connection;
  }
}

export type { GolemConnectionStatus };
