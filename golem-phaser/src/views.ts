import type Phaser from "phaser";
import type { SpriteGpuEntityPool } from "./gpu-views.js";

/** Minimal generated entity shape needed by Phaser views. */
export interface SyncedPositionEntity {
  readonly entityId: number;
  readonly posX: number;
  readonly posY: number;
}

/** A resolved 2D position for a sprite view. */
export interface EntityViewPosition {
  x: number;
  y: number;
}

/** Duration-based interpolation applied in the Phaser scene update loop. */
export interface EntityViewInterpolation {
  duration: number;
  ease?: (amount: number) => number;
}

type EntityValue<E, V> = V | ((entity: E) => V);

/** Typed handlers for entity-targeted server events. */
export type EntityViewEventHandlers<
  E,
  V,
  Events extends object,
> = Partial<{
  [K in keyof Events]: (view: V, entity: E, event: Events[K]) => void;
}>;

/** A mounted presentation for one synchronized entity. */
export interface MountedEntityView<E extends SyncedPositionEntity> {
  sync(entity: E): void;
  update?(deltaMs: number): void;
  event?(name: string, entity: E, event: unknown): void;
  destroy(entity: E): void;
}

/** A per-scene factory created once for one schema entity type. */
export interface EntityViewFactory<E extends SyncedPositionEntity> {
  spawn(entity: E): MountedEntityView<E>;
  destroy(): void;
}

/** Scene-independent entity presentation definition stored in a registry. */
export interface EntityViewDefinition<E extends SyncedPositionEntity> {
  createFactory(scene: Phaser.Scene): EntityViewFactory<E>;
}

/** Configuration for an arbitrary Phaser GameObject or Editor prefab. */
export interface PrefabViewConfig<
  E extends SyncedPositionEntity,
  V extends Phaser.GameObjects.GameObject,
  Events extends object = Record<never, never>,
> {
  create(scene: Phaser.Scene, entity: E): V;
  /**
   * Add the returned object through `scene.add.existing`. Defaults to true.
   * Set false when `create` already adds the object.
   */
  addToScene?: boolean;
  sync?(view: V, entity: E): void;
  onSpawn?(view: V, entity: E): void;
  onRemove?(view: V, entity: E): void;
  destroy?(view: V, entity: E): void;
  events?: EntityViewEventHandlers<E, V, Events>;
}

/** Declarative sprite presentation configuration. */
export interface SpriteViewConfig<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
> {
  texture: EntityValue<E, string>;
  frame?: EntityValue<E, string | number>;
  create?(
    scene: Phaser.Scene,
    entity: E,
    texture: string,
    frame?: string | number,
  ): Phaser.GameObjects.Sprite;
  position?(entity: E): EntityViewPosition;
  interpolation?: number | EntityViewInterpolation | false;
  /**
   * Skip authoritative position writes after spawn, allowing prediction to own
   * selected entity transforms while other fields continue to synchronize.
   */
  externalPosition?: EntityValue<E, boolean>;
  sync?(sprite: Phaser.GameObjects.Sprite, entity: E): void;
  onSpawn?(sprite: Phaser.GameObjects.Sprite, entity: E): void;
  onRemove?(sprite: Phaser.GameObjects.Sprite, entity: E): void;
  events?: EntityViewEventHandlers<
    E,
    Phaser.GameObjects.Sprite,
    Events
  >;
}

/** Configuration for a per-scene GPU entity pool. */
export interface GpuViewConfig<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
> {
  createPool(scene: Phaser.Scene): SpriteGpuEntityPool<E>;
  events?: EntityViewEventHandlers<E, number, Events>;
}

/** Event configuration for a synchronized entity with no Phaser view. */
export interface HeadlessViewConfig<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
> {
  events?: Partial<{
    [K in keyof Events]: (entity: E, event: Events[K]) => void;
  }>;
}

/** Generated per-entity helper exposed by `defineEntityViews`. */
export interface EntityViewBuilder<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
> {
  prefab<V extends Phaser.GameObjects.GameObject>(
    config: PrefabViewConfig<E, V, Events>,
  ): EntityViewDefinition<E>;
  sprite(config: SpriteViewConfig<E, Events>): EntityViewDefinition<E>;
  gpu(config: GpuViewConfig<E, Events>): EntityViewDefinition<E>;
  headless(config?: HeadlessViewConfig<E, Events>): EntityViewDefinition<E>;
}

function resolveValue<E, V>(value: EntityValue<E, V>, entity: E): V {
  return typeof value === "function"
    ? (value as (entity: E) => V)(entity)
    : value;
}

function dispatchViewEvent<E, V>(
  handlers: EntityViewEventHandlers<E, V, object> | undefined,
  name: string,
  view: V,
  entity: E,
  event: unknown,
): void {
  const handler = (
    handlers as Record<
      string,
      ((view: V, entity: E, event: unknown) => void) | undefined
    >
  )?.[name];
  handler?.(view, entity, event);
}

/** Create the runtime helper used by generated entity-view registries. */
export function createEntityViewBuilder<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
>(): EntityViewBuilder<E, Events> {
  return {
    prefab: (config) => prefabView(config),
    sprite: (config) => spriteView(config),
    gpu: (config) => gpuView(config),
    headless: (config) => headlessView(config),
  };
}

/** Define an arbitrary typed Phaser GameObject / Editor prefab view. */
export function prefabView<
  E extends SyncedPositionEntity,
  V extends Phaser.GameObjects.GameObject,
  Events extends object = Record<never, never>,
>(config: PrefabViewConfig<E, V, Events>): EntityViewDefinition<E> {
  return {
    createFactory(scene) {
      return {
        spawn(entity) {
          const view = config.create(scene, entity);
          if (config.addToScene !== false) {
            scene.add.existing(view);
          }
          config.sync?.(view, entity);
          config.onSpawn?.(view, entity);
          return {
            sync: (next) => config.sync?.(view, next),
            event: (name, next, event) =>
              dispatchViewEvent(config.events, name, view, next, event),
            destroy: (last) => {
              config.onRemove?.(view, last);
              if (config.destroy) {
                config.destroy(view, last);
              } else {
                view.destroy();
              }
            },
          };
        },
        destroy() {},
      };
    },
  };
}

/** Define a declarative Phaser Sprite view. */
export function spriteView<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
>(config: SpriteViewConfig<E, Events>): EntityViewDefinition<E> {
  return {
    createFactory(scene) {
      return {
        spawn(entity) {
          const texture = resolveValue(config.texture, entity);
          const frame =
            config.frame === undefined
              ? undefined
              : resolveValue(config.frame, entity);
          const sprite = config.create
            ? config.create(scene, entity, texture, frame)
            : scene.add.sprite(entity.posX, entity.posY, texture, frame);
          const binding = new SpriteBinding(entity, sprite, config);
          config.onSpawn?.(sprite, entity);
          return {
            sync: (next) => binding.sync(next),
            update: (deltaMs) => binding.update(deltaMs),
            event: (name, next, event) =>
              dispatchViewEvent(config.events, name, sprite, next, event),
            destroy: (last) => {
              config.onRemove?.(sprite, last);
              sprite.destroy();
            },
          };
        },
        destroy() {},
      };
    },
  };
}

/** Define a view backed by a scene-local SpriteGpuEntityPool. */
export function gpuView<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
>(config: GpuViewConfig<E, Events>): EntityViewDefinition<E> {
  return {
    createFactory(scene) {
      const pool = config.createPool(scene);
      return {
        spawn(entity) {
          const slot = pool.spawn(entity);
          return {
            sync: (next) => pool.update(next),
            event: (name, next, event) =>
              dispatchViewEvent(config.events, name, slot, next, event),
            destroy: (last) => {
              pool.remove(last.entityId);
            },
          };
        },
        destroy() {
          pool.destroy();
        },
      };
    },
  };
}

/** Explicitly define an entity type that has no Phaser presentation. */
export function headlessView<
  E extends SyncedPositionEntity,
  Events extends object = Record<never, never>,
>(config: HeadlessViewConfig<E, Events> = {}): EntityViewDefinition<E> {
  return {
    createFactory() {
      return {
        spawn() {
          return {
            sync() {},
            event(name, entity, event) {
              const handler = (
                config.events as Record<
                  string,
                  ((entity: E, event: unknown) => void) | undefined
                >
              )?.[name];
              handler?.(entity, event);
            },
            destroy() {},
          };
        },
        destroy() {},
      };
    },
  };
}

interface NormalizedInterpolation {
  duration: number;
  ease: (amount: number) => number;
}

class SpriteBinding<E extends SyncedPositionEntity> {
  private entity: E;
  private readonly interpolation?: NormalizedInterpolation;
  private startX: number;
  private startY: number;
  private targetX: number;
  private targetY: number;
  private elapsed = 0;

  constructor(
    entity: E,
    private readonly sprite: Phaser.GameObjects.Sprite,
    private readonly config: SpriteViewConfig<E, object>,
  ) {
    this.entity = entity;
    this.interpolation = normalizeInterpolation(config.interpolation);
    const position = this.position();
    this.startX = position.x;
    this.startY = position.y;
    this.targetX = position.x;
    this.targetY = position.y;
    this.sprite.setPosition(position.x, position.y);
    this.syncFields();
  }

  sync(entity: E): void {
    this.entity = entity;
    if (this.hasExternalPosition()) {
      this.cancelInterpolation();
      this.syncFields();
      return;
    }

    const position = this.position();
    if (position.x !== this.targetX || position.y !== this.targetY) {
      if (this.interpolation) {
        this.startX = this.sprite.x;
        this.startY = this.sprite.y;
        this.targetX = position.x;
        this.targetY = position.y;
        this.elapsed = 0;
      } else {
        this.startX = position.x;
        this.startY = position.y;
        this.targetX = position.x;
        this.targetY = position.y;
        this.sprite.setPosition(position.x, position.y);
      }
    }
    this.syncFields();
  }

  update(deltaMs: number): void {
    if (this.hasExternalPosition()) {
      this.cancelInterpolation();
      return;
    }
    if (
      !this.interpolation ||
      (this.sprite.x === this.targetX && this.sprite.y === this.targetY)
    ) {
      return;
    }

    this.elapsed = Math.min(
      this.elapsed + deltaMs,
      this.interpolation.duration,
    );
    const amount = this.interpolation.ease(
      this.elapsed / this.interpolation.duration,
    );
    this.sprite.setPosition(
      this.startX + (this.targetX - this.startX) * amount,
      this.startY + (this.targetY - this.startY) * amount,
    );
  }

  private position(): EntityViewPosition {
    return (
      this.config.position?.(this.entity) ?? {
        x: this.entity.posX,
        y: this.entity.posY,
      }
    );
  }

  private hasExternalPosition(): boolean {
    return this.config.externalPosition === undefined
      ? false
      : resolveValue(this.config.externalPosition, this.entity);
  }

  private cancelInterpolation(): void {
    this.startX = this.sprite.x;
    this.startY = this.sprite.y;
    this.targetX = this.sprite.x;
    this.targetY = this.sprite.y;
    this.elapsed = 0;
  }

  private syncFields(): void {
    if (this.config.frame !== undefined) {
      this.sprite.setFrame(resolveValue(this.config.frame, this.entity));
    }
    this.config.sync?.(this.sprite, this.entity);
  }
}

function normalizeInterpolation(
  interpolation: number | EntityViewInterpolation | false | undefined,
): NormalizedInterpolation | undefined {
  if (interpolation === false || interpolation === undefined) {
    return undefined;
  }
  if (typeof interpolation === "number") {
    return interpolation > 0
      ? { duration: interpolation, ease: (amount) => amount }
      : undefined;
  }
  return interpolation.duration > 0
    ? {
        duration: interpolation.duration,
        ease: interpolation.ease ?? ((amount) => amount),
      }
    : undefined;
}
