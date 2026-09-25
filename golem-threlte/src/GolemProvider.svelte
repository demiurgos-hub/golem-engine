<script lang="ts" generics="C extends GameClient, B, E extends EntityIdentity">
  import { onMount, onDestroy, setContext, untrack, type Snippet } from 'svelte';
  import { GolemConnectionLifecycle, type GameClient, type GolemConnectionSnapshot } from 'golem-engine';
  import { Signal, type EntityIdentity, type GolemAdapter, type Unsubscribe } from './core.js';
  import { createGolemContext, disposeGolemContext, type ContextHolder, type GolemSource } from './context.js';

  let { adapter, source, children, fallback }: {
    adapter: GolemAdapter<C, B, E>;
    source: GolemSource<C>;
    children: Snippet;
    fallback?: Snippet;
  } = $props();

  // Context keys must be installed during initialization, before child creation.
  const initialAdapter = untrack(() => adapter);
  const initialSource = untrack(() => source);
  const holder: ContextHolder = {};
  setContext(initialAdapter.key, holder);
  let ready = $state(false);
  let owned: GolemConnectionLifecycle<C> | undefined;
  let stopStatus: Unsubscribe | undefined;
  let cleaned = false;
  const status = new Signal<GolemConnectionSnapshot>({ type: 'idle' });

  function validate() {
    const value = initialSource as unknown as Record<string, unknown>;
    const field = { client: 'client', lifecycle: 'lifecycle', owned: 'config' }[initialSource.kind];
    if (!field || value[field] == null || ['client', 'lifecycle', 'config'].some(k => k !== field && value[k] !== undefined)) {
      throw new Error('golem-threlte: provide exactly one client, lifecycle, or owned configuration');
    }
  }
  validate();

  function bind(client: C, lifecycle?: GolemConnectionLifecycle<C>) {
    stopStatus = lifecycle
      ? lifecycle.subscribeStatus(value => status.set(value))
      : client.subscribeConnectionState(value => status.set(value.type === 'connecting' ? { type: 'connecting', attempt: 1 } : value));
    holder.current = createGolemContext(initialAdapter, client, status, lifecycle);
    ready = true;
  }

  function cleanup() {
    if (cleaned) return;
    cleaned = true;
    stopStatus?.();
    if (holder.current) disposeGolemContext(holder.current);
    holder.current = undefined;
    owned?.destroy();
  }
  onDestroy(cleanup);
  try {
    if (initialSource.kind === 'client') bind(initialSource.client);
    else if (initialSource.kind === 'lifecycle') bind(initialSource.lifecycle.client, initialSource.lifecycle);
  } catch (error) { cleanup(); throw error; }
  onMount(() => {
    if (initialSource.kind === 'owned') {
      owned = new GolemConnectionLifecycle(initialSource.config);
      try { owned.start(); bind(owned.client, owned); }
      catch (error) { cleanup(); throw error; }
    }
    return cleanup;
  });
</script>

{#if ready}
  {@render children()}
{:else if fallback}
  {@render fallback()}
{/if}
