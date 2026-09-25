import assert from 'node:assert/strict';
import test from 'node:test';
import { createServer } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { createEntityViewRegistry, createGolemAdapter } from '../dist/core.js';
import { fixture } from './fixtures.js';

test('SSR imports public entrypoints and renders providers without browser connection or renderer work', async () => {
  assert.equal(typeof window, 'undefined');
  const server = await createServer({
    configFile: false,
    plugins: [svelte()],
    appType: 'custom',
    server: { middlewareMode: true, watch: null },
    resolve: { dedupe: ['svelte', 'three', '@threlte/core', 'golem-engine'] },
  });
  try {
    const { default: Harness, render } = await server.ssrLoadModule('/test/SsrHarness.svelte');
    const adapter = createGolemAdapter({ entities: client => client.entities, createBindings: () => ({}) });
    const registry = createEntityViewRegistry(adapter, []);
    const owned = render(Harness, { props: { adapter, registry, source: {
      kind: 'owned', config: {
        createClient() { assert.fail('SSR opened an owned client'); },
        connectionOptions() { assert.fail('SSR resolved connection options'); },
      },
    } } });
    assert.match(owned.body, /Waiting for the browser/);
    assert.doesNotMatch(owned.body, /Client available/);

    const state = fixture();
    let activeSubscriptions = 0;
    const subscribe = state.client.subscribeConnectionState.bind(state.client);
    state.client.subscribeConnectionState = listener => {
      activeSubscriptions++;
      const stop = subscribe(listener);
      return () => { activeSubscriptions--; stop(); };
    };
    const borrowed = render(Harness, { props: { adapter, registry, source: { kind: 'client', client: state.client } } });
    assert.match(borrowed.body, /Client available/);
    assert.equal(state.connectCalls, 0);
    assert.equal(state.closeCalls, 0);
    assert.equal(activeSubscriptions, 0, 'SSR destruction releases the status subscription');
    assert.throws(() => render(Harness, { props: { adapter, registry, source: {
      kind: 'client', client: state.client, config: { createClient: () => state.client },
    } } }).body, /provide exactly one/);
  } finally {
    await server.close();
  }
});
