import { afterEach, expect, test } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import { GolemConnectionLifecycle } from 'golem-engine';
import { Group } from 'three';
import { Actor, Beacon } from './fixtures.js';
import { browserFixture } from './browser-fixture.js';
import Harness from './Harness.svelte';

const cleanup: (() => unknown)[] = [];
afterEach(async () => {
  for (const fn of cleanup.splice(0).reverse()) await fn();
});

async function start(state: any, source?: any, initialFilter?: number) {
  const target = document.createElement('div');
  document.body.append(target);
  const app = mount(Harness, { target, props: { state, source, initialFilter } });
  cleanup.push(() => { target.remove(); state.sharedGeometry.dispose(); state.client.disconnect(); });
  cleanup.push(() => unmount(app));
  await tick();
  await tick();
  return { app, target };
}

test('actual Svelte HUD and Threlte component share in-place state, world, events, and scene remounts', async () => {
  const actor = new Actor(1, 80, 2);
  const state = browserFixture([actor, new Beacon(2)]);
  const { app, target } = await start(state);
  expect(state.nodes.size).toBe(1);
  expect(target.querySelector('[data-entity="1"]')?.textContent).toBe('1: 80');
  expect(target.querySelector('[data-testid="status"]')?.textContent).toBe('connected');
  const view = [...state.nodes.keys()][0] as any;
  const node = state.nodes.get(view);
  expect(node.children).toHaveLength(1);
  state.step();
  expect(node.position.x).toBe(2);
  state.update(actor, { health: 44 });
  state.updateWorld({ label: 'Live world', tick: 3 });
  state.event(actor, { strength: 5 });
  await tick();
  expect(target.querySelector('[data-entity="1"]')?.textContent).toBe('1: 44');
  expect(target.querySelector('[data-testid="world"]')?.textContent).toBe('Live world:3');
  expect(state.nodes.get(view)).toBe(node);
  expect(node.userData.health).toBe(44);
  expect(node.userData.strength).toBe(5);
  expect(state.events).toEqual([{ id: 1, strength: 5 }]);

  app.showScene(false);
  await tick();
  expect(state.nodes.size).toBe(0);
  expect(state.client.connected).toBe(true);
  expect(state.closeCalls).toBe(0);
  expect(state.listeners.event.size).toBe(0);
  expect(view.signal.aborted).toBe(true);
  expect(state.sharedDisposed).toBe(0);
  expect(state.materialsDisposed).toBe(1);
  state.update(actor, { health: 17 });
  await tick();
  expect(target.querySelector('[data-entity="1"]')?.textContent).toBe('1: 17');
  app.showScene(true);
  await tick(); await tick();
  expect(state.nodes.size).toBe(1);
  expect(state.created).toBe(2);
  expect(state.connectCalls).toBe(1);
  state.remove(1);
  await tick();
  expect(state.nodes.size).toBe(0);
  expect(state.materialsDisposed).toBe(2);
  expect(target.querySelector('[data-entity="1"]')).toBeNull();
});

test('multiple mounted component trees filter independently and fence queued events and late work', async () => {
  const a = new Actor(1);
  const state = browserFixture([a]);
  const { app } = await start(state);
  app.showSecond(true);
  await tick();
  expect(state.nodes.size).toBe(2);
  expect(state.listeners.event.size).toBe(2);
  state.add(new Actor(2));
  state.event(state.entities.get(2), { strength: 7 });
  await tick(); await tick();
  expect(state.nodes.size).toBe(4);
  expect(state.events.filter((event: any) => event.id === 2)).toHaveLength(2);
  app.filter(1);
  await tick();
  expect(state.nodes.size).toBe(3);
  const oldViews = [...state.nodes.keys()].filter((view: any) => view.current.entity.entityId === 1) as any[];
  let delayed = 0;
  const pending = Promise.resolve().then(oldViews[0].guard(() => delayed++));
  state.add(new Actor(1, 25));
  await pending;
  await tick(); await tick();
  expect(delayed).toBe(0);
  expect(oldViews.every(view => view.signal.aborted)).toBe(true);
  expect(state.nodes.size).toBe(3);
  state.event(a, { strength: 99 });
  expect(state.events.some((event: any) => event.strength === 99)).toBe(false);
  app.showSecond(false);
  await tick();
  expect(state.nodes.size).toBe(1);
  expect(state.listeners.event.size).toBe(1);
  expect(state.connectCalls).toBe(1);
});

test('filter closure reacts after initially excluding every known entity', async () => {
  const state = browserFixture([new Actor(1), new Actor(2)]);
  const { app } = await start(state, undefined, 99);
  expect(state.nodes.size).toBe(0);
  app.filter(2);
  await tick(); await tick();
  expect(state.nodes.size).toBe(1);
  expect([...state.nodes.keys()][0].current.entity.entityId).toBe(2);
});

test('native scheduler ordering, interpolation reset and external transform ownership survive teardown', async () => {
  const actor = new Actor(1, 100, 0);
  const state = browserFixture([actor]);
  const { app } = await start(state);
  const view = [...state.nodes.keys()][0] as any;
  const node = state.nodes.get(view);
  state.step(0.05);
  expect(state.taskCalls.slice(-2).map((call: any) => call.phase)).toEqual(['first', 'second']);
  expect(state.taskCalls[0].delta).toBeCloseTo(0.05);
  state.update(actor, { x: 10, scale: 3, angle: Math.PI });
  state.step(0.05);
  expect(node.position.x).toBeCloseTo(5);
  expect(node.scale.x).toBeCloseTo(2);
  expect(node.quaternion.y).toBeCloseTo(Math.SQRT1_2);
  expect(node.quaternion.w).toBeCloseTo(Math.SQRT1_2);
  state.update(actor, { health: 90 });
  state.step(0.05);
  expect(node.position.x).toBeCloseTo(10);
  expect(node.scale.x).toBeCloseTo(3);
  expect(node.quaternion.y).toBeCloseTo(1);
  state.update(actor, { x: 30 });
  state.transforms.get(view).reset();
  state.step(0.05);
  expect(node.position.x).toBeCloseTo(30);
  state.external.add(1);
  node.position.x = 88;
  state.update(actor, { x: 40 });
  state.step(0.05);
  expect(node.position.x).toBe(88);
  expect(actor.x).toBe(40);
  state.external.delete(1);
  state.step(0.05);
  expect(node.position.x).toBe(40);
  state.remove(1);
  const calls = state.taskCalls.length;
  state.step(0.05);
  expect(state.taskCalls).toHaveLength(calls);
  await tick();
  expect(state.stages[0].tasks).toHaveLength(0);
  app.showScene(false);
  await tick();
  expect(state.renderers[0].loop).toBeNull();
  expect(state.renderers[0].disposed).toBe(true);
});

test('initial external transforms stay untouched and replacement targets snap independently', async () => {
  const state = browserFixture([new Actor(1, 100, 10)]);
  state.external.add(1);
  await start(state);
  const view = [...state.nodes.keys()][0] as any;
  const original = state.nodes.get(view);
  original.position.x = 77;
  state.step();
  expect(original.position.x).toBe(77);
  state.external.delete(1);
  state.step();
  expect(original.position.x).toBe(10);
  const replacement = new Group();
  replacement.position.x = -50;
  state.targets.set(view, replacement);
  state.step();
  expect(replacement.position.x).toBe(10);
  state.update(state.entities.get(1), { x: 20 });
  state.step();
  expect(replacement.position.x).toBe(15);
  expect(original.position.x).toBe(10);
});

test('provider borrows a started lifecycle and destroys only an explicitly owned lifecycle', async () => {
  const borrowed = browserFixture();
  const lifecycle = new GolemConnectionLifecycle({ createClient: () => borrowed.client, connectionOptions: () => 'ws://localhost/unused', autoConnect: false });
  lifecycle.start();
  const first = await start(borrowed, { kind: 'lifecycle', lifecycle });
  await unmount(first.app);
  expect(borrowed.client.connected).toBe(true);
  expect(borrowed.closeCalls).toBe(0);
  expect(borrowed.listenerCount()).toBe(0);
  lifecycle.destroy();
  expect(borrowed.closeCalls).toBe(1);

  const owned = browserFixture();
  owned.client.disconnect();
  const before = owned.closeCalls;
  const second = await start(owned, { kind: 'owned', config: { createClient: () => owned.client, connectionOptions: () => 'ws://localhost/owned' } });
  expect(owned.connectCalls).toBe(2);
  owned.channels.at(-1).open();
  await tick();
  expect(second.target.querySelector('[data-testid="status"]')?.textContent).toBe('connected');
  await unmount(second.app);
  expect(owned.closeCalls).toBe(before + 1);
  expect(owned.listenerCount()).toBe(0);
});

test('owned provider cancellation fences unresolved connection options after destruction', async () => {
  const state = browserFixture();
  state.client.disconnect();
  let resolve!: (url: string) => void;
  const pending = new Promise<string>(done => { resolve = done; });
  const { app } = await start(state, { kind: 'owned', config: {
    createClient: () => state.client,
    connectionOptions: () => pending,
  } });
  const before = state.connectCalls;
  await unmount(app);
  resolve('ws://localhost/obsolete');
  await pending;
  await tick();
  expect(state.connectCalls).toBe(before);
  expect(state.listenerCount()).toBe(0);
});
