<script lang="ts">
  import { useGolemContext } from '../src/context.js';
  import { browserAdapter } from './browser-fixture.js';
  const context = useGolemContext(browserAdapter);
  const actors = context.entityTypes.Actor.all;
  const world = context.world.settings;
  const status = context.status;
</script>

<div data-testid="hud">
  <span data-testid="status">{$status.type}</span>
  <span data-testid="world">{$world.label}:{$world.tick}</span>
  {#each $actors as snapshot (snapshot.entity)}
    <span data-entity={snapshot.entity.entityId}>{snapshot.entity.entityId}: {snapshot.entity.health}</span>
  {/each}
</div>
