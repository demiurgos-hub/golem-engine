<script lang="ts">
  import { Canvas } from '@threlte/core';
  import { untrack } from 'svelte';
  import GolemProvider from '../src/GolemProvider.svelte';
  import EntityViews from '../src/EntityViews.svelte';
  import { browserAdapter, browserRegistry } from './browser-fixture.js';
  import HudProbe from './HudProbe.svelte';
  import StageProbe from './StageProbe.svelte';
  let { state: controls, source = { kind: 'client', client: controls.client }, initialFilter }: { state: any; source?: any; initialFilter?: number } = $props();
  let scene = $state(true);
  let second = $state(false);
  let selected = $state<number | undefined>(untrack(() => initialFilter));
  export function showScene(value: boolean) { scene = value; }
  export function showSecond(value: boolean) { second = value; }
  export function filter(id: number | undefined) { selected = id; }
</script>

<GolemProvider adapter={browserAdapter} {source}>
  <HudProbe />
  {#if scene}
    <div style="width: 320px; height: 180px">
      <Canvas createRenderer={controls.createRenderer}>
        <StageProbe state={controls} />
        <EntityViews registry={browserRegistry} filter={entity => selected === undefined || entity.entityId === selected} />
        {#if second}
          <EntityViews registry={browserRegistry} />
        {/if}
      </Canvas>
    </div>
  {/if}
</GolemProvider>
