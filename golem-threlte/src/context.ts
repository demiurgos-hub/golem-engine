import { getContext, onDestroy } from 'svelte';
import type { GameClient, GolemConnectionConfig, GolemConnectionLifecycle, GolemConnectionSnapshot } from 'golem-engine';
import type { Readable } from 'svelte/store';
import { entityCollection, type EntityCollection, type EntityIdentity, type EntityViewBinding, type GolemAdapter, type Unsubscribe } from './core.js';

export type GolemSource<C extends GameClient> =
  | { kind: 'client'; client: C; lifecycle?: never; config?: never }
  | { kind: 'lifecycle'; lifecycle: GolemConnectionLifecycle<C>; client?: never; config?: never }
  | { kind: 'owned'; config: GolemConnectionConfig<C>; client?: never; lifecycle?: never };

export type GolemContext<C extends GameClient, B = {}, E extends EntityIdentity = EntityIdentity> = B & {
  readonly client: C;
  readonly connection?: GolemConnectionLifecycle<C>;
  readonly status: Readable<GolemConnectionSnapshot>;
  readonly entities: EntityCollection<E>;
};
export interface ContextHolder { current?: GolemContext<any, any, any> }

/** Access the provider for the generated schema; usable above or inside Canvas. */
export function useGolemContext<C extends GameClient, B, E extends EntityIdentity>(adapter: GolemAdapter<C, B, E>): GolemContext<C, B, E> {
  const holder = getContext<ContextHolder | undefined>(adapter.key);
  if (!holder?.current) throw new Error('golem-threlte: a ready GolemProvider is required');
  return holder.current;
}

/** Subscribe to a generated event for the current Svelte component lifetime. */
export function useGolemEvent<A extends unknown[]>(subscribe: (handler: (...args: A) => void) => Unsubscribe,
  handler: (...args: A) => void): Unsubscribe {
  const unsubscribe = subscribe(handler);
  onDestroy(unsubscribe);
  return unsubscribe;
}

/** Subscribe to this presentation incarnation's typed entity events. */
export function useEntityEvent<E extends EntityIdentity, Events extends object, K extends keyof Events>(
  view: EntityViewBinding<E, Events>, name: K, handler: (event: Events[K], entity: E) => void
): Unsubscribe {
  const unsubscribe = view.onEvent(name, handler);
  onDestroy(unsubscribe);
  return unsubscribe;
}

/** Construct adaptation state only; this never opens or closes a connection. */
export function createGolemContext<C extends GameClient, B, E extends EntityIdentity>(
  adapter: GolemAdapter<C, B, E>, client: C, status: Readable<GolemConnectionSnapshot>, connection?: GolemConnectionLifecycle<C>
): GolemContext<C, B, E> {
  return { ...adapter.createBindings(client), client, status, connection, entities: entityCollection(adapter.entities(client)) };
}

/** Release only generated binding groups, never model or renderer resources. */
export function disposeGolemContext(context: GolemContext<any, any, any>): void {
  context.entities.destroy();
  for (const group of [context.world, context.entityTypes]) {
    if (group) for (const binding of Object.values(group) as { destroy(): void }[]) binding.destroy();
  }
}
