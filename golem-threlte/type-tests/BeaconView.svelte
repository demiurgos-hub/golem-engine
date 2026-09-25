<script lang="ts">
  import type { BeaconProps } from './models.js';
  let { view }: BeaconProps = $props();
</script>

<span>{$view.entity.label}</span>
