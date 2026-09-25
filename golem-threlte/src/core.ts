import type { Component } from 'svelte';
import type { Readable } from 'svelte/store';
import type { GameClient } from 'golem-engine';

export type Unsubscribe = () => void;
export interface EntityIdentity { readonly entityId: number }
export interface EntitySnapshot<E> { readonly entity: E; readonly version: number }
export interface EntitySource<E extends EntityIdentity> {
  get(id: number): E | undefined;
  getAll(): ReadonlyMap<number, E>;
  onSpawn(fn: (entity: E) => void): Unsubscribe;
  onUpdate(fn: (entity: E) => void): Unsubscribe;
  onRemove(fn: (id: number) => void): Unsubscribe;
}

/** Release a group of listeners once. */
export function combineUnsubscribers(...unsubscribers: Unsubscribe[]): Unsubscribe {
  let active = true;
  return () => {
    if (!active) return;
    active = false;
    for (const unsubscribe of unsubscribers) unsubscribe();
  };
}

/** A read-only store whose notifications never depend on model identity. */
export class Signal<T> implements Readable<T> {
  private listeners = new Set<(value: T) => void>();
  private generation = 0;
  constructor(public current: T) {}
  subscribe = (listener: (value: T) => void): Unsubscribe => {
    const subscription = (value: T) => listener(value);
    this.listeners.add(subscription);
    try { subscription(this.current); }
    catch (error) { this.listeners.delete(subscription); throw error; }
    return () => { this.listeners.delete(subscription); };
  };
  set(value: T): void {
    this.current = value;
    const generation = ++this.generation;
    for (const listener of [...this.listeners]) {
      if (generation !== this.generation) break;
      if (this.listeners.has(listener)) listener(value);
    }
  }
  clear(): void { this.listeners.clear(); }
}

export interface ManagedReadable<T> extends Readable<T> { destroy(): void }

/** Subscribe on first use and release the source when the last consumer leaves. */
function managedStore<T>(read: () => T, listen: (refresh: () => void) => Unsubscribe,
  scope?: { closed(): boolean; active(store: ManagedReadable<unknown>, value: boolean): void }
): ManagedReadable<T> {
  const signal = new Signal(read());
  let stop: Unsubscribe | undefined;
  let count = 0;
  let destroyed = false;
  const store: ManagedReadable<T> = {
    subscribe(fn) {
      if (destroyed || scope?.closed()) throw new Error('golem-threlte: binding scope is destroyed');
      let unsubscribe: Unsubscribe;
      try {
        if (count++ === 0) {
          stop = listen(() => signal.set(read()));
          signal.set(read());
          scope?.active(store, true);
        }
        unsubscribe = signal.subscribe(fn);
      } catch (error) {
        if (--count === 0) { stop?.(); stop = undefined; scope?.active(store, false); }
        throw error;
      }
      let active = true;
      return () => {
        if (!active) return;
        active = false;
        unsubscribe();
        if (--count === 0) { stop?.(); stop = undefined; scope?.active(store, false); }
      };
    },
    destroy() { destroyed = true; stop?.(); stop = undefined; signal.clear(); scope?.active(store, false); }
  };
  return store;
}

export interface EntityCollection<E extends EntityIdentity> {
  readonly all: ManagedReadable<readonly EntitySnapshot<E>[]>;
  get(id: number): ManagedReadable<EntitySnapshot<E> | undefined>;
  destroy(): void;
}

/** Project the canonical manager into versioned references, without copying models. */
export function entityCollection<E extends EntityIdentity, T extends E = E>(
  source: EntitySource<E>, matches: (entity: E) => entity is T = ((_: E): _ is T => true)
): EntityCollection<T> {
  let version = 0;
  let destroyed = false;
  const reads = new Set<ManagedReadable<unknown>>();
  const scope = {
    closed: () => destroyed,
    active(store: ManagedReadable<unknown>, active: boolean) { if (active) reads.add(store); else reads.delete(store); }
  };
  const listen = (refresh: () => void) => combineUnsubscribers(
    source.onSpawn(() => { version++; refresh(); }),
    source.onUpdate(() => { version++; refresh(); }),
    source.onRemove(() => { version++; refresh(); })
  );
  const all = managedStore(() => [...source.getAll().values()]
    .filter(matches).map(entity => ({ entity, version })), listen, scope);
  return {
    all,
    get(id) {
      const store = managedStore(() => {
        const entity = source.get(id);
        return entity && matches(entity) ? { entity, version } : undefined;
      }, listen, scope);
      return store;
    },
    destroy() { destroyed = true; for (const store of reads) store.destroy(); reads.clear(); }
  };
}

/** Expose the current generated world value and subsequent manager notifications. */
export function worldStore<T>(read: () => T, listen: (fn: (value: T) => void) => Unsubscribe): ManagedReadable<T> {
  return managedStore(read, refresh => listen(refresh));
}

export type EntityEventDispatch<E extends EntityIdentity> = (entity: E, name: string, payload: unknown) => void;
export interface GolemAdapter<C extends GameClient, B, E extends EntityIdentity = EntityIdentity> {
  readonly key: symbol;
  entities(client: C): EntitySource<E>;
  createBindings(client: C): B;
  subscribeEvents?(client: C, dispatch: EntityEventDispatch<E>): Unsubscribe;
}

/** Define the schema-specific manager adapters emitted by golem-bake. */
export function createGolemAdapter<C extends GameClient, B, E extends EntityIdentity>(
  adapter: Omit<GolemAdapter<C, B, E>, 'key'>
): GolemAdapter<C, B, E> {
  return { ...adapter, key: Symbol('golem-context') };
}

export interface EntityViewBinding<E extends EntityIdentity, Events extends object = {}>
  extends Readable<EntitySnapshot<E>> {
  readonly current: EntitySnapshot<E>;
  readonly signal: AbortSignal;
  readonly active: boolean;
  onEvent<K extends keyof Events>(name: K, handler: (event: Events[K], entity: E) => void): Unsubscribe;
  guard<A extends unknown[], R>(callback: (...args: A) => R): (...args: A) => R | undefined;
  /** Type-only invariant keeps entity and event registrations distinct. */
  readonly _types?: (entity: E, events: Events) => [E, Events];
}
export type EntityViewProps<E extends EntityIdentity, Events extends object = {}> = {
  view: EntityViewBinding<E, Events>;
};
export interface EntityViewDefinition<E extends EntityIdentity, Events extends object = {}> {
  readonly component?: Component<EntityViewProps<E, Events>>;
  readonly _types?: (entity: E, events: Events) => [E, Events];
}
export interface EntityViewBuilder<E extends EntityIdentity, Events extends object = {}> {
  component(component: Component<EntityViewProps<E, Events>>): EntityViewDefinition<E, Events>;
  headless(): EntityViewDefinition<E, Events>;
}

/** Choose a typed component or an explicit headless definition. */
export function createEntityViewBuilder<E extends EntityIdentity, Events extends object = {}>(): EntityViewBuilder<E, Events> {
  return { component: component => ({ component }), headless: () => ({}) };
}

export interface EntityViewRegistration<E extends EntityIdentity = EntityIdentity> {
  matches(entity: E): boolean;
  definition: EntityViewDefinition<any, any>;
}
export interface EntityViewRegistry<C extends GameClient, B = any, E extends EntityIdentity = any> {
  readonly adapter: GolemAdapter<C, B, E>;
  readonly registrations: readonly EntityViewRegistration<E>[];
}
/** Bind a complete generated registry to its typed client context. */
export function createEntityViewRegistry<C extends GameClient, B, E extends EntityIdentity>(
  adapter: GolemAdapter<C, B, E>, registrations: readonly EntityViewRegistration<E>[]
): EntityViewRegistry<C, B, E> { return { adapter, registrations }; }

/** One incarnation's event and notification scope; disposed synchronously on removal. */
export class PresentationBinding<E extends EntityIdentity> extends Signal<EntitySnapshot<E>>
  implements EntityViewBinding<E, any> {
  private abort = new AbortController();
  private handlers = new Map<string, Set<(event: any, entity: E) => void>>();
  private pending: [string, unknown][] = [];
  private ready = false;
  get signal(): AbortSignal { return this.abort.signal; }
  get active(): boolean { return !this.signal.aborted; }
  constructor(entity: E) { super({ entity, version: 0 }); }
  update(entity: E): void {
    if (this.active) this.set({ entity, version: this.current.version + 1 });
  }
  onEvent(name: string | number | symbol, fn: (event: any, entity: E) => void): Unsubscribe {
    if (!this.active) return () => {};
    const key = String(name);
    let handlers = this.handlers.get(key);
    if (!handlers) this.handlers.set(key, handlers = new Set());
    handlers.add(fn);
    return () => { handlers.delete(fn); };
  }
  dispatch(name: string, payload: unknown): void {
    if (!this.active) return;
    if (!this.ready) { this.pending.push([name, payload]); return; }
    for (const fn of [...(this.handlers.get(name) ?? [])]) {
      if (this.active && this.handlers.get(name)?.has(fn)) fn(payload, this.current.entity);
    }
  }
  activate(): void {
    if (!this.active || this.ready) return;
    this.ready = true;
    for (const [name, payload] of this.pending.splice(0)) this.dispatch(name, payload);
  }
  guard<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R | undefined {
    return (...args) => this.active ? fn(...args) : undefined;
  }
  destroy(): void { this.abort.abort(); this.pending = []; this.handlers.clear(); this.clear(); }
}

export interface Presentation<E extends EntityIdentity = EntityIdentity> {
  readonly key: symbol;
  readonly component: Component<EntityViewProps<any, any>>;
  readonly view: PresentationBinding<E>;
}

/** Mount-local projections. The generated manager remains the membership authority. */
export class EntityPresentationMount<C extends GameClient, E extends EntityIdentity> {
  readonly presentations = new Signal<readonly Presentation<E>[]>([]);
  /** Invalidate reactive filter dependencies after canonical membership/state changes. */
  readonly changes = new Signal(0);
  private active = false;
  private records = new Map<number, Presentation<E>>();
  private stop?: Unsubscribe;
  private filter: (entity: E) => boolean;
  constructor(private client: C, private registry: EntityViewRegistry<C, any, E>,
    filter: (entity: E) => boolean = () => true, private invalidate: () => void = () => {}) {
    this.filter = filter;
  }
  mount(): void {
    if (this.active) return;
    this.active = true;
    const source = this.registry.adapter.entities(this.client);
    const cleanup: Unsubscribe[] = [];
    try {
      const changed = (entity: E) => {
        this.reconcile(entity);
        this.changes.set(this.changes.current + 1);
      };
      cleanup.push(source.onSpawn(changed));
      cleanup.push(source.onUpdate(changed));
      cleanup.push(source.onRemove(id => {
        const current = source.get(id);
        if (current) this.reconcile(current); else this.remove(id);
        this.changes.set(this.changes.current + 1);
      }));
      if (this.registry.adapter.subscribeEvents) cleanup.push(this.registry.adapter.subscribeEvents(this.client, (entity, name, payload) => {
        if (!this.active || source.get(entity.entityId) !== entity) return;
        this.reconcile(entity, false);
        this.records.get(entity.entityId)?.view.dispatch(name, payload);
        if (this.active) this.invalidate();
      }));
      this.stop = combineUnsubscribers(...cleanup);
      for (const entity of source.getAll().values()) this.reconcile(entity, false);
      this.changes.set(this.changes.current + 1);
    } catch (error) {
      combineUnsubscribers(...cleanup)();
      this.unmount();
      throw error;
    }
  }
  setFilter(filter: (entity: E) => boolean): void {
    this.filter = filter;
    if (!this.active) return;
    for (const entity of this.registry.adapter.entities(this.client).getAll().values()) this.reconcile(entity, false);
  }
  unmount(): void {
    this.active = false;
    this.stop?.(); this.stop = undefined;
    for (const record of this.records.values()) record.view.destroy();
    this.records.clear(); this.publish();
  }
  private reconcile(entity: E, update = true): void {
    if (!this.active || this.registry.adapter.entities(this.client).get(entity.entityId) !== entity) return;
    const definition = this.registry.registrations.find(r => r.matches(entity))?.definition;
    if (!this.filter(entity) || !definition?.component) { this.remove(entity.entityId); return; }
    const record = this.records.get(entity.entityId);
    if (record?.view.current.entity === entity && record.component === definition.component) {
      if (update) { record.view.update(entity); this.invalidate(); }
      return;
    }
    record?.view.destroy();
    this.records.set(entity.entityId, { key: Symbol(), component: definition.component, view: new PresentationBinding(entity) });
    this.publish();
  }
  private remove(id: number): void {
    if (!this.active) return;
    const record = this.records.get(id);
    if (!record) return;
    record.view.destroy(); this.records.delete(id); this.publish();
  }
  private publish(): void { this.presentations.set([...this.records.values()]); this.invalidate(); }
}
