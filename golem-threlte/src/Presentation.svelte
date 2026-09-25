<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { Presentation } from './core.js';
  let { presentation }: { presentation: Presentation<any> } = $props();
  onMount(() => presentation.view.activate());
  onDestroy(() => presentation.view.destroy());
</script>

<presentation.component view={presentation.view} />
