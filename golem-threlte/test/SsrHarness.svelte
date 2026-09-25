<script module>
  export { render } from 'svelte/server';
</script>

<script lang="ts">
  import { Canvas } from '@threlte/core';
  import { GolemProvider } from 'golem-threlte';
  import { EntityViews } from 'golem-threlte/threlte';
  let { adapter, source, registry }: { adapter: any; source: any; registry: any } = $props();
</script>

<GolemProvider {adapter} {source}>
  {#snippet fallback()}<p>Waiting for the browser</p>{/snippet}
  <p>Client available</p>
  <Canvas createRenderer={() => { throw new Error('SSR created a renderer'); }}>
    <EntityViews {registry} />
  </Canvas>
</GolemProvider>
