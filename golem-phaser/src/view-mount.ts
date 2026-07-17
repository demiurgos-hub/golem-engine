import type Phaser from "phaser";
import type {
  EntityViewDefinition,
  EntityViewFactory,
  MountedEntityView,
  SyncedPositionEntity,
} from "./views.js";
import type {
  EntityViewRegistration,
  EntityViewRegistry,
  Unsubscribe,
} from "./view-registry.js";

const UPDATE_EVENT = "update";
const SHUTDOWN_EVENT = "shutdown";
const DESTROY_EVENT = "destroy";

interface ActiveEntityView {
  registration: EntityViewRegistration;
  entity: SyncedPositionEntity;
  view: MountedEntityView<SyncedPositionEntity>;
}

/**
 * EntityViewMount owns one scene's presentation of a persistent generated
 * client. Unmounting destroys views and subscriptions, not synchronized state.
 */
export class EntityViewMount<C> {
  private readonly factories = new Map<
    EntityViewRegistration,
    EntityViewFactory<SyncedPositionEntity>
  >();
  private readonly activeViews = new Map<number, ActiveEntityView>();
  private readonly unsubscribers: Unsubscribe[] = [];
  private mounted = false;

  constructor(
    readonly scene: Phaser.Scene,
    private readonly client: C,
    private readonly registry: EntityViewRegistry<C>,
    private readonly onUnmount?: () => void,
  ) {}

  /** Whether this registry is currently mounted on its scene. */
  get isMounted(): boolean {
    return this.mounted;
  }

  /** Subscribe and create views for entities already known to the client. */
  mount(): void {
    if (this.mounted) {
      return;
    }
    this.mounted = true;

    try {
      for (const registration of this.registry.registrations) {
        const definition =
          registration.definition as EntityViewDefinition<SyncedPositionEntity>;
        this.factories.set(
          registration,
          definition.createFactory(this.scene),
        );
      }

      this.unsubscribers.push(
        this.registry.subscribeEntities(this.client, {
          spawn: (entity) => this.spawn(entity),
          update: (entity) => this.update(entity),
          remove: (entityId) => this.remove(entityId),
        }),
      );
      if (this.registry.subscribeEvents) {
        this.unsubscribers.push(
          this.registry.subscribeEvents(
            this.client,
            (entity, eventName, event) =>
              this.dispatchEvent(entity, eventName, event),
          ),
        );
      }

      this.scene.events.on(UPDATE_EVENT, this.updateViews, this);
      this.scene.events.once(SHUTDOWN_EVENT, this.unmount, this);
      this.scene.events.once(DESTROY_EVENT, this.unmount, this);

      for (const entity of this.registry.entities(this.client)) {
        this.spawn(entity);
      }
    } catch (error) {
      this.unmount();
      throw error;
    }
  }

  /** Tear down scene-local views and listeners without touching client state. */
  unmount(): void {
    if (!this.mounted) {
      return;
    }
    this.mounted = false;

    this.scene.events.off(UPDATE_EVENT, this.updateViews, this);
    this.scene.events.off(SHUTDOWN_EVENT, this.unmount, this);
    this.scene.events.off(DESTROY_EVENT, this.unmount, this);

    for (const unsubscribe of this.unsubscribers.splice(0)) {
      unsubscribe();
    }
    for (const entityId of [...this.activeViews.keys()]) {
      this.remove(entityId);
    }
    for (const factory of this.factories.values()) {
      factory.destroy();
    }
    this.factories.clear();
    this.onUnmount?.();
  }

  private spawn(entity: SyncedPositionEntity): void {
    const existing = this.activeViews.get(entity.entityId);
    const registration = this.findRegistration(entity);
    if (!registration) {
      return;
    }

    if (existing) {
      if (existing.registration === registration) {
        existing.entity = entity;
        existing.view.sync(entity);
        return;
      }
      this.remove(entity.entityId);
    }

    const factory = this.factories.get(registration);
    if (!factory) {
      return;
    }
    const view = factory.spawn(entity);
    this.activeViews.set(entity.entityId, {
      registration,
      entity,
      view,
    });
  }

  private update(entity: SyncedPositionEntity): void {
    const active = this.activeViews.get(entity.entityId);
    if (!active) {
      this.spawn(entity);
      return;
    }
    if (!active.registration.matches(entity)) {
      this.remove(entity.entityId);
      this.spawn(entity);
      return;
    }
    active.entity = entity;
    active.view.sync(entity);
  }

  private remove(entityId: number): void {
    const active = this.activeViews.get(entityId);
    if (!active) {
      return;
    }
    this.activeViews.delete(entityId);
    active.view.destroy(active.entity);
  }

  private dispatchEvent(
    entity: SyncedPositionEntity,
    eventName: string,
    event: unknown,
  ): void {
    const active = this.activeViews.get(entity.entityId);
    active?.view.event?.(eventName, entity, event);
  }

  private updateViews(_time: number, deltaMs: number): void {
    for (const active of this.activeViews.values()) {
      active.view.update?.(deltaMs);
    }
  }

  private findRegistration(
    entity: SyncedPositionEntity,
  ): EntityViewRegistration | undefined {
    return this.registry.registrations.find((registration) =>
      registration.matches(entity),
    );
  }
}
