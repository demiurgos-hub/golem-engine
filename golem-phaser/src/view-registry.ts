import type {
  EntityViewDefinition,
  SyncedPositionEntity,
} from "./views.js";

/** Function returned by generated listener registration methods. */
export type Unsubscribe = () => void;

/** Callbacks used by a generated registry to observe entity lifecycle. */
export interface EntityViewObserver {
  spawn(entity: SyncedPositionEntity): void;
  update(entity: SyncedPositionEntity): void;
  remove(entityId: number): void;
}

/** One generated entity-type discriminator and its presentation definition. */
export interface EntityViewRegistration<
  E extends SyncedPositionEntity = SyncedPositionEntity,
> {
  matches(entity: SyncedPositionEntity): boolean;
  definition: EntityViewDefinition<E>;
}

/** Event dispatch callback wired by generated entity-event subscriptions. */
export type EntityViewEventDispatch = (
  entity: SyncedPositionEntity,
  eventName: string,
  event: unknown,
) => void;

/**
 * Scene-independent, generated registry for one concrete client schema.
 * Registries can be mounted by multiple scenes against the same client.
 */
export interface EntityViewRegistry<C> {
  readonly registrations: readonly EntityViewRegistration<any>[];
  entities(client: C): Iterable<SyncedPositionEntity>;
  subscribeEntities(client: C, observer: EntityViewObserver): Unsubscribe;
  subscribeEvents?(
    client: C,
    dispatch: EntityViewEventDispatch,
  ): Unsubscribe;
}

/** Preserve the generated client type while constructing a registry. */
export function createEntityViewRegistry<C>(
  registry: EntityViewRegistry<C>,
): EntityViewRegistry<C> {
  return registry;
}

/** Combine generated listener cleanup functions into one unsubscribe. */
export function combineUnsubscribers(
  ...unsubscribers: Unsubscribe[]
): Unsubscribe {
  let active = true;
  return () => {
    if (!active) {
      return;
    }
    active = false;
    for (const unsubscribe of unsubscribers) {
      unsubscribe();
    }
  };
}
