import assert from 'node:assert/strict';
import test from 'node:test';
import {
  EntityPresentationMount, PresentationBinding, createEntityViewBuilder,
  createEntityViewRegistry, createGolemAdapter, entityCollection, worldStore,
} from '../dist/core.js';
import { Actor, Beacon, fixture } from './fixtures.js';

const Visual = () => ({});
const actorBuilder = createEntityViewBuilder();
const beaconBuilder = createEntityViewBuilder();
const adapter = createGolemAdapter({
  entities: client => client.entities,
  createBindings: () => ({}),
  subscribeEvents: (client, dispatch) => client.fixture.subscribeEvents(dispatch),
});
const registry = createEntityViewRegistry(adapter, [
  { matches: entity => entity instanceof Actor, definition: actorBuilder.component(Visual) },
  { matches: entity => entity instanceof Beacon, definition: beaconBuilder.headless() },
]);

test('entity bindings invalidate raw instances and follow replacement, disappearance, and subscription scopes', () => {
  const original = new Actor(1, 70);
  const state = fixture([original, new Beacon(2)]);
  const collection = entityCollection(state.entities, entity => entity instanceof Actor);
  const all = [];
  const selected = [];
  const stopAll = collection.all.subscribe(value => all.push(value));
  const stopOne = collection.get(1).subscribe(value => selected.push(value));
  assert.equal(all[0][0].entity, original);
  assert.equal(selected[0].entity, original);

  state.update(original, { health: 31 });
  assert.equal(selected.at(-1).entity, original);
  assert.equal(selected.at(-1).entity.health, 31);
  assert.notEqual(selected[0], selected.at(-1));
  assert.ok(selected.at(-1).version > selected[0].version);

  const replacement = new Actor(1, 12);
  state.add(replacement);
  assert.equal(selected.at(-1).entity, replacement);
  state.remove(1);
  assert.equal(selected.at(-1), undefined);
  assert.deepEqual(all.at(-1), []);
  stopAll(); stopOne();
  assert.equal(state.listenerCount(), 0);
  state.add(original);
  const remounted = [];
  const stopAgain = collection.all.subscribe(value => remounted.push(value));
  assert.equal(remounted[0][0].entity, original);
  stopAgain(); collection.destroy();
  assert.equal(state.listenerCount(), 0);
});

test('world bindings retain generated world identity and release every source listener', () => {
  const state = fixture();
  const binding = worldStore(() => state.world.current, state.subscribeWorld);
  const updates = [];
  const unsubscribe = binding.subscribe(value => updates.push(value));
  state.updateWorld({ label: 'Updated', tick: 2 });
  assert.equal(updates.length, 2);
  assert.equal(updates[0], state.world.current);
  assert.equal(updates[1].label, 'Updated');
  unsubscribe(); binding.destroy();
  assert.equal(state.listenerCount(), 0);
});

test('a failing initial subscriber releases manager listeners and does not poison future consumers', () => {
  const state = fixture([new Actor(1)]);
  const collection = entityCollection(state.entities);
  assert.throws(() => collection.all.subscribe(() => { throw new Error('consumer failed'); }), /consumer failed/);
  assert.equal(state.listenerCount(), 0);
  const values = [];
  const unsubscribe = collection.all.subscribe(value => values.push(value));
  state.update(state.entities.get(1), { health: 27 });
  assert.equal(values.at(-1)[0].entity.health, 27);
  unsubscribe(); collection.destroy();
  assert.equal(state.listenerCount(), 0);
});

test('reentrant manager updates never deliver an older envelope after its replacement', () => {
  const actor = new Actor(1);
  const state = fixture([actor]);
  const collection = entityCollection(state.entities);
  const selected = collection.get(1);
  let nested = false;
  const first = selected.subscribe(snapshot => {
    if (snapshot.entity.health === 50 && !nested) {
      nested = true;
      state.update(actor, { health: 25 });
    }
  });
  const received = [];
  const second = selected.subscribe(snapshot => received.push({ health: snapshot.entity.health, version: snapshot.version }));
  state.update(actor, { health: 50 });
  assert.deepEqual(received.map(value => value.health), [100, 25]);
  assert.ok(received[1].version > received[0].version);
  first(); second(); collection.destroy();
  assert.equal(state.listenerCount(), 0);
});

test('mount backfills once, preserves presentations on updates, and synchronously removes obsolete incarnations', () => {
  const actor = new Actor(1);
  const state = fixture([actor, new Beacon(2)]);
  const mount = new EntityPresentationMount(state.client, registry);
  mount.mount(); mount.mount();
  assert.equal(mount.presentations.current.length, 1);
  const first = mount.presentations.current[0];
  state.update(actor, { health: 50 });
  assert.equal(mount.presentations.current[0], first);
  assert.equal(first.view.current.entity, actor);
  assert.equal(first.view.current.entity.health, 50);

  const replacement = new Actor(1, 20);
  state.add(replacement);
  const second = mount.presentations.current[0];
  assert.notEqual(second.key, first.key);
  assert.equal(first.view.active, false);
  assert.equal(first.view.signal.aborted, true);
  assert.equal(second.view.current.entity, replacement);
  state.remove(1);
  assert.equal(second.view.active, false);
  assert.deepEqual(mount.presentations.current, []);
  mount.unmount();
  assert.equal(state.entities.get(2).label, 'headless');
  assert.equal(state.listenerCount(), 0);
  assert.equal(state.closeCalls, 0);
});

test('subscriptions before backfill do not duplicate a simultaneously discovered entity', () => {
  const actor = new Actor(1);
  const state = fixture();
  const onSpawn = state.entities.onSpawn;
  state.entities.onSpawn = listener => {
    const stop = onSpawn(listener);
    state.add(actor);
    return stop;
  };
  const mount = new EntityPresentationMount(state.client, registry);
  mount.mount();
  assert.equal(mount.presentations.current.length, 1);
  assert.equal(mount.presentations.current[0].view.current.entity, actor);
  mount.unmount();
  assert.equal(state.listenerCount(), 0);
});

test('entity-targeted events wait for component readiness and never cross incarnations or mounts', () => {
  const a = new Actor(1);
  const b = new Actor(2);
  const state = fixture([a, b]);
  let invalidations = 0;
  const mount = new EntityPresentationMount(state.client, registry, () => true, () => invalidations++);
  mount.mount();
  const [first, second] = mount.presentations.current;
  const received = [];
  state.event(a, { strength: 2 });
  first.view.onEvent('impact', (payload, entity) => received.push([payload.strength, entity]));
  second.view.onEvent('impact', () => assert.fail('event reached another entity'));
  first.view.activate(); first.view.activate(); second.view.activate();
  assert.deepEqual(received, [[2, a]]);
  const stop = first.view.onEvent('impact', () => assert.fail('unsubscribed event listener'));
  stop();
  const beforeEvent = invalidations;
  state.event(a, { strength: 3 });
  assert.ok(invalidations > beforeEvent, 'events invalidate on-demand rendering');
  assert.deepEqual(received, [[2, a], [3, a]]);

  state.add(new Actor(1));
  state.event(a, { strength: 9 });
  first.view.dispatch('impact', { strength: 10 });
  mount.unmount();
  state.event(b, { strength: 11 });
  assert.equal(received.length, 2);
  assert.equal(state.listenerCount(), 0);
});

test('independent filtered mounts remount against canonical state without owning a connection', () => {
  const state = fixture([new Actor(1), new Actor(2)]);
  const left = new EntityPresentationMount(state.client, registry, entity => entity.entityId === 1);
  const right = new EntityPresentationMount(state.client, registry);
  left.mount(); right.mount();
  assert.equal(left.presentations.current.length, 1);
  assert.equal(right.presentations.current.length, 2);
  const rightFirst = right.presentations.current[0];
  for (let i = 0; i < 3; i++) {
    left.unmount();
    assert.equal(rightFirst.view.active, true);
    left.mount();
    assert.equal(left.presentations.current.length, 1);
  }
  left.setFilter(entity => entity.entityId === 2);
  assert.equal(left.presentations.current[0].view.current.entity.entityId, 2);
  assert.equal(right.presentations.current[0], rightFirst);
  left.unmount(); right.unmount();
  assert.equal(state.listenerCount(), 0);
  assert.equal(state.connectCalls, 0);
  assert.equal(state.closeCalls, 0);
});

test('runtime reset removes presentations and reconnect population backfills a fresh incarnation', () => {
  const state = fixture();
  state.client.connect('ws://localhost/game');
  state.channels[0].open();
  const actor = new Actor(1);
  state.add(actor);
  const mount = new EntityPresentationMount(state.client, registry);
  mount.mount();
  const old = mount.presentations.current[0];
  const world = state.world.current;
  state.channels[0].fail();
  assert.equal(old.view.active, false);
  assert.deepEqual(mount.presentations.current, []);
  assert.equal(state.world.current, world);
  state.client.connect('ws://localhost/game');
  state.channels[1].open();
  state.add(new Actor(1, 42));
  assert.equal(mount.presentations.current.length, 1);
  assert.notEqual(mount.presentations.current[0].key, old.key);
  mount.unmount();
  assert.equal(state.client.connected, true);
  state.client.disconnect();
});

test('delayed presentation work is fenced immediately on removal and unmount', async () => {
  const state = fixture([new Actor(1)]);
  const mount = new EntityPresentationMount(state.client, registry);
  mount.mount();
  const view = mount.presentations.current[0].view;
  let attached = 0;
  const later = Promise.resolve().then(view.guard(() => attached++));
  state.remove(1);
  await later;
  assert.equal(attached, 0);
  assert.equal(view.signal.aborted, true);
  state.add(new Actor(2));
  const next = mount.presentations.current[0].view;
  const afterUnmount = Promise.resolve().then(next.guard(() => attached++));
  mount.unmount();
  await afterUnmount;
  assert.equal(attached, 0);
});

test('events queued for a removed binding are dropped', () => {
  const binding = new PresentationBinding(new Actor(1));
  binding.dispatch('impact', { strength: 1 });
  binding.onEvent('impact', () => assert.fail('obsolete buffered event'));
  binding.destroy();
  binding.activate();
  assert.equal(binding.active, false);
});
