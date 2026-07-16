import Phaser from "phaser";
import type {
  SpriteEntityBridgeConstructor,
  SyncedPositionEntity,
} from "./views.js";

export interface GpuEntityBridge extends SyncedPositionEntity {
  onSpawn(): void;
  onRemove(): void;
}

export type GpuEntityBridgeConstructor<B extends GpuEntityBridge> = new (
  entityId: number,
) => B;

export type SpriteGpuMember =
  Partial<Phaser.Types.GameObjects.SpriteGPULayer.Member>;

export interface SpriteGpuEntityPoolConfig<B extends GpuEntityBridge> {
  /** Single-image texture or texture key shared by every pooled member. */
  texture: string | Phaser.Textures.Texture;
  /** Number of stable member slots reserved when the pool is created. */
  capacity: number;
  /** Extra slots to allocate when full. Defaults to false, which throws. */
  growBy?: number | false;
  /** Map synchronized entity state to Phaser GPU member data. */
  member?: (entity: B) => SpriteGpuMember;
  /** GPU-driven frame animations available to members. */
  animations?:
    | Phaser.Animations.Animation[]
    | Phaser.Types.GameObjects.SpriteGPULayer.SetAnimation[];
  /** Configure depth, blend mode, lighting, or other layer-wide properties. */
  configureLayer?: (layer: Phaser.GameObjects.SpriteGPULayer) => void;
  /** Called after an entity receives a stable member slot. */
  onSpawn?: (slot: number, entity: B) => void;
  /** Called before an entity's member slot is hidden and released. */
  onRemove?: (slot: number, entityId: number) => void;
}

/**
 * SpriteGpuEntityPool maps replicated entity IDs to stable SpriteGPULayer
 * member slots. Removed entities are hidden and their slots are reused instead
 * of splicing Phaser's GPU buffer.
 */
export class SpriteGpuEntityPool<B extends GpuEntityBridge> {
  readonly layer: Phaser.GameObjects.SpriteGPULayer;

  private readonly entitySlots = new Map<number, number>();
  private readonly freeSlots: number[] = [];
  private readonly pending = new Map<number, B>();
  private destroyed = false;

  constructor(
    private readonly scene: Phaser.Scene,
    private readonly config: SpriteGpuEntityPoolConfig<B>,
  ) {
    if (scene.sys.game.renderer.type !== Phaser.WEBGL) {
      throw new Error(
        "golem-phaser: SpriteGpuEntityPool requires the WebGL renderer",
      );
    }
    if (!Number.isInteger(config.capacity) || config.capacity <= 0) {
      throw new Error(
        "golem-phaser: SpriteGpuEntityPool capacity must be a positive integer",
      );
    }
    if (
      config.growBy !== undefined &&
      config.growBy !== false &&
      (!Number.isInteger(config.growBy) || config.growBy <= 0)
    ) {
      throw new Error(
        "golem-phaser: SpriteGpuEntityPool growBy must be a positive integer or false",
      );
    }

    this.layer = scene.add.spriteGPULayer(
      config.texture,
      config.capacity,
    );
    if (config.animations) {
      this.layer.setAnimations(config.animations);
    }
    config.configureLayer?.(this.layer);
    this.initializeSlots(0, config.capacity);

    scene.events.on(Phaser.Scenes.Events.PRE_RENDER, this.flush, this);
    scene.events.once(Phaser.Scenes.Events.SHUTDOWN, this.shutdown, this);
  }

  /** Number of entity slots currently in use. */
  get activeCount(): number {
    return this.entitySlots.size;
  }

  /** Return the stable member slot assigned to an entity, if it is active. */
  slotFor(entityId: number): number | undefined {
    return this.entitySlots.get(entityId);
  }

  /** Assign or immediately refresh a pooled member for an entity. */
  spawn(entity: B): number {
    this.assertActive();
    const existing = this.entitySlots.get(entity.entityId);
    if (existing !== undefined) {
      this.layer.editMember(existing, this.memberFor(entity));
      return existing;
    }

    if (this.freeSlots.length === 0) {
      this.grow();
    }
    const slot = this.freeSlots.pop();
    if (slot === undefined) {
      throw new Error(
        `golem-phaser: SpriteGpuEntityPool capacity ${this.layer.size} exhausted`,
      );
    }

    this.entitySlots.set(entity.entityId, slot);
    this.layer.editMember(slot, this.memberFor(entity));
    this.config.onSpawn?.(slot, entity);
    return slot;
  }

  /** Queue the latest entity state for one batched pre-render update. */
  update(entity: B): void {
    if (this.destroyed) {
      return;
    }
    const slot = this.entitySlots.get(entity.entityId);
    if (slot !== undefined) {
      this.pending.set(slot, entity);
    }
  }

  /** Hide an entity member and return its stable slot to the pool. */
  remove(entityId: number): boolean {
    if (this.destroyed) {
      return false;
    }
    const slot = this.entitySlots.get(entityId);
    if (slot === undefined) {
      return false;
    }
    this.config.onRemove?.(slot, entityId);
    this.pending.delete(slot);
    this.layer.editMember(slot, this.hiddenMember());
    this.entitySlots.delete(entityId);
    this.freeSlots.push(slot);
    return true;
  }

  /** Apply all queued entity changes to Phaser's segmented GPU buffer. */
  flush(): void {
    if (this.destroyed || this.pending.size === 0) {
      return;
    }
    for (const [slot, entity] of this.pending) {
      if (this.entitySlots.get(entity.entityId) === slot) {
        this.layer.editMember(slot, this.memberFor(entity));
      }
    }
    this.pending.clear();
  }

  /** Release listeners, mappings, and the owned SpriteGPULayer. */
  destroy(): void {
    this.cleanup(true);
  }

  private memberFor(entity: B): SpriteGpuMember {
    return {
      x: entity.posX,
      y: entity.posY,
      ...this.config.member?.(entity),
    };
  }

  private hiddenMember(): SpriteGpuMember {
    return {
      x: 0,
      y: 0,
      scaleX: 0,
      scaleY: 0,
      alpha: 0,
    };
  }

  private initializeSlots(start: number, count: number): void {
    for (let index = start; index < start + count; index++) {
      this.layer.addMember(this.hiddenMember());
    }
    for (let index = start + count - 1; index >= start; index--) {
      this.freeSlots.push(index);
    }
  }

  private grow(): void {
    const growBy = this.config.growBy;
    if (!growBy) {
      return;
    }
    const previousSize = this.layer.size;
    this.layer.resize(previousSize + growBy);
    this.initializeSlots(previousSize, growBy);
  }

  private assertActive(): void {
    if (this.destroyed) {
      throw new Error("golem-phaser: SpriteGpuEntityPool is destroyed");
    }
  }

  private shutdown(): void {
    this.cleanup(false);
  }

  private cleanup(destroyLayer: boolean): void {
    if (this.destroyed) {
      return;
    }
    this.destroyed = true;
    this.scene.events.off(
      Phaser.Scenes.Events.PRE_RENDER,
      this.flush,
      this,
    );
    this.scene.events.off(
      Phaser.Scenes.Events.SHUTDOWN,
      this.shutdown,
      this,
    );
    this.pending.clear();
    this.entitySlots.clear();
    this.freeSlots.length = 0;
    if (destroyLayer && !this.layer.isDestroyed) {
      this.layer.destroy();
    }
  }
}

/**
 * createGpuEntityView returns an entity constructor that connects a generated
 * Phaser bridge to a pooled SpriteGPULayer member.
 *
 * @example
 * const projectiles = new SpriteGpuEntityPool<ProjectileBridge>(scene, {
 *   texture: 'projectiles',
 *   capacity: 4096,
 *   member: (entity) => ({ frame: entity.kind }),
 * });
 * client.entities.registerProjectile(
 *   createGpuEntityView(projectiles, ProjectileBridge),
 * );
 */
export function createGpuEntityView<B extends GpuEntityBridge>(
  pool: SpriteGpuEntityPool<B>,
  Bridge: GpuEntityBridgeConstructor<B>,
): GpuEntityBridgeConstructor<B> {
  const Base = Bridge as GpuEntityBridgeConstructor<GpuEntityBridge>;

  class ConfiguredGpuEntityView extends Base {
    onSpawn(): void {
      super.onSpawn();
      pool.spawn(this as unknown as B);
    }

    onRemove(): void {
      pool.remove(this.entityId);
      super.onRemove();
    }

    protected syncToView(): void {
      pool.update(this as unknown as B);
    }
  }

  return ConfiguredGpuEntityView as unknown as GpuEntityBridgeConstructor<B>;
}
