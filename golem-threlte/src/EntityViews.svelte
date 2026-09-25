<script lang="ts" generics="C extends GameClient, E extends EntityIdentity">
  import { onMount, onDestroy, untrack } from 'svelte';
  import { useThrelte } from '@threlte/core';
  import type { GameClient } from 'golem-engine';
  import { EntityPresentationMount, type EntityViewRegistry, type EntityIdentity } from './core.js';
  import { useGolemContext } from './context.js';
  import Presentation from './Presentation.svelte';
  let { registry, filter = () => true }: {
    registry: EntityViewRegistry<C, any, E>;
    filter?: (entity: E) => boolean;
  } = $props();
  const initialRegistry = untrack(() => registry);
  const context = useGolemContext(initialRegistry.adapter);
  const { invalidate } = useThrelte();
  const mount = new EntityPresentationMount(context.client, initialRegistry, entity => filter(entity), invalidate);
  const presentations = mount.presentations;
  const changes = mount.changes;
  $effect(() => { $changes; mount.setFilter(filter); });
  onMount(() => { mount.mount(); return () => mount.unmount(); });
  onDestroy(() => mount.unmount());
</script>

{#each $presentations as presentation (presentation.key)}
  <Presentation {presentation} />
{/each}
