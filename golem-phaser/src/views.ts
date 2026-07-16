import Phaser from "phaser";

export interface SyncedPositionEntity {
  readonly entityId: number;
  readonly posX: number;
  readonly posY: number;
}

export interface SpriteEntityBridge extends SyncedPositionEntity {
  sprite?: Phaser.GameObjects.Sprite;
  onSpawn(): void;
  onRemove(): void;
}

export type SpriteEntityBridgeConstructor<B extends SpriteEntityBridge> = new (
  entityId: number,
) => B;

export interface EntityViewPosition {
  x: number;
  y: number;
}

export interface EntityViewInterpolation {
  /** Time in milliseconds to move from the displayed position to a new state. */
  duration: number;
  /** Optional normalized easing function. Defaults to linear interpolation. */
  ease?: (amount: number) => number;
}

type EntityValue<E, V> = V | ((entity: E) => V);

export interface SpriteViewConfig<B extends SpriteEntityBridge> {
  /** Texture key used by the default sprite factory. */
  texture: EntityValue<B, string>;
  /** Optional initial and synchronized texture frame. */
  frame?: EntityValue<B, string | number>;
  /** Override default scene.add.sprite creation. */
  create?: (
    scene: Phaser.Scene,
    entity: B,
    texture: string,
    frame?: string | number,
  ) => Phaser.GameObjects.Sprite;
  /** Override the default posX/posY mapping. */
  position?: (entity: B) => EntityViewPosition;
  /** Smooth position changes over a duration, or false for immediate updates. */
  interpolation?: number | EntityViewInterpolation | false;
  /** Apply additional entity fields after each state or delta. */
  sync?: (sprite: Phaser.GameObjects.Sprite, entity: B) => void;
  /** Called after the sprite is created and initially synchronized. */
  onSpawn?: (sprite: Phaser.GameObjects.Sprite, entity: B) => void;
  /** Called immediately before the sprite is destroyed. */
  onRemove?: (sprite: Phaser.GameObjects.Sprite, entity: B) => void;
}

interface NormalizedInterpolation {
  duration: number;
  ease: (amount: number) => number;
}

function resolveValue<E, V>(value: EntityValue<E, V>, entity: E): V {
  return typeof value === "function"
    ? (value as (entity: E) => V)(entity)
    : value;
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

class SpriteViewBinding<B extends SpriteEntityBridge> {
  private readonly interpolation?: NormalizedInterpolation;
  private startX = 0;
  private startY = 0;
  private targetX = 0;
  private targetY = 0;
  private elapsed = 0;

  constructor(
    private readonly entity: B,
    private readonly sprite: Phaser.GameObjects.Sprite,
    private readonly config: SpriteViewConfig<B>,
  ) {
    this.interpolation = normalizeInterpolation(config.interpolation);
    const position = this.position();
    this.startX = position.x;
    this.startY = position.y;
    this.targetX = position.x;
    this.targetY = position.y;
    this.sprite.setPosition(position.x, position.y);
    this.syncView();
  }

  sync(): void {
    const position = this.position();
    if (
      position.x !== this.targetX ||
      position.y !== this.targetY
    ) {
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
    this.syncView();
  }

  update(delta: number): void {
    if (
      !this.interpolation ||
      (this.sprite.x === this.targetX && this.sprite.y === this.targetY)
    ) {
      return;
    }
    this.elapsed = Math.min(
      this.elapsed + delta,
      this.interpolation.duration,
    );
    const amount = this.interpolation.ease(
      this.elapsed / this.interpolation.duration,
    );
    this.sprite.setPosition(
      Phaser.Math.Linear(this.startX, this.targetX, amount),
      Phaser.Math.Linear(this.startY, this.targetY, amount),
    );
  }

  private position(): EntityViewPosition {
    return this.config.position?.(this.entity) ?? {
      x: this.entity.posX,
      y: this.entity.posY,
    };
  }

  private syncView(): void {
    if (this.config.frame !== undefined) {
      this.sprite.setFrame(resolveValue(this.config.frame, this.entity));
    }
    this.config.sync?.(this.sprite, this.entity);
  }
}

interface UpdatableViewBinding {
  update(delta: number): void;
}

class EntityViewSystem {
  private readonly bindings = new Set<UpdatableViewBinding>();

  constructor(private readonly scene: Phaser.Scene) {
    scene.events.on(Phaser.Scenes.Events.UPDATE, this.update, this);
    scene.events.once(Phaser.Scenes.Events.SHUTDOWN, this.shutdown, this);
  }

  add(binding: UpdatableViewBinding): void {
    this.bindings.add(binding);
  }

  remove(binding: UpdatableViewBinding): void {
    this.bindings.delete(binding);
  }

  private update(_time: number, delta: number): void {
    for (const binding of this.bindings) {
      binding.update(delta);
    }
  }

  private shutdown(): void {
    this.scene.events.off(Phaser.Scenes.Events.UPDATE, this.update, this);
    this.bindings.clear();
    viewSystems.delete(this.scene);
  }
}

const viewSystems = new WeakMap<Phaser.Scene, EntityViewSystem>();

function viewSystemFor(scene: Phaser.Scene): EntityViewSystem {
  let system = viewSystems.get(scene);
  if (!system) {
    system = new EntityViewSystem(scene);
    viewSystems.set(scene, system);
  }
  return system;
}

/**
 * createSpriteView returns an entity constructor for a generated registerXxx
 * method. It creates, synchronizes, interpolates, and destroys Phaser sprites.
 *
 * @example
 * client.entities.registerPlayer(createSpriteView(scene, PlayerBridge, {
 *   texture: 'player',
 *   frame: (player) => player.animationFrame,
 *   interpolation: 75,
 * }));
 */
export function createSpriteView<B extends SpriteEntityBridge>(
  scene: Phaser.Scene,
  Bridge: SpriteEntityBridgeConstructor<B>,
  config: SpriteViewConfig<B>,
): SpriteEntityBridgeConstructor<B> {
  const system = viewSystemFor(scene);
  const Base = Bridge as SpriteEntityBridgeConstructor<SpriteEntityBridge>;

  class ConfiguredSpriteView extends Base {
    private _golemViewBinding?: SpriteViewBinding<B>;

    onSpawn(): void {
      super.onSpawn();
      const entity = this as unknown as B;
      if (!this.sprite) {
        const texture = resolveValue(config.texture, entity);
        const frame =
          config.frame === undefined
            ? undefined
            : resolveValue(config.frame, entity);
        this.sprite = config.create
          ? config.create(scene, entity, texture, frame)
          : scene.add.sprite(this.posX, this.posY, texture, frame);
      }
      this._golemViewBinding = new SpriteViewBinding(
        entity,
        this.sprite,
        config,
      );
      system.add(this._golemViewBinding);
      config.onSpawn?.(this.sprite, entity);
    }

    onRemove(): void {
      const entity = this as unknown as B;
      if (this._golemViewBinding) {
        system.remove(this._golemViewBinding);
        this._golemViewBinding = undefined;
      }
      if (this.sprite) {
        config.onRemove?.(this.sprite, entity);
      }
      super.onRemove();
    }

    protected syncToSprite(): void {
      this._golemViewBinding?.sync();
    }
  }

  return ConfiguredSpriteView as unknown as SpriteEntityBridgeConstructor<B>;
}
