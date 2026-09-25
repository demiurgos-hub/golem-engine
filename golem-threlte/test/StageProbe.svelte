<script lang="ts">
  import { useThrelte } from '@threlte/core';
  import { untrack } from 'svelte';
  let { state }: { state: any } = $props();
  const controls = untrack(() => state);
  controls.stages.push(useThrelte().mainStage);
</script>
