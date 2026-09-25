<script lang="ts">
  import { T } from '@threlte/core';
  import { untrack } from 'svelte';
  import { useEntityEvent } from '../src/context.js';
  import { useEntityTask } from '../src/tasks.js';
  import type { ActorProps } from './models.js';
  let { view }: ActorProps = $props();
  const incarnation = untrack(() => view);
  let strength = $state(0);
  useEntityEvent(incarnation, 'impact', event => { strength = event.strength; });
  useEntityTask(incarnation, (entity, deltaSeconds) => {
    const health: number = entity.health;
    strength = Math.max(0, strength - deltaSeconds + health * 0);
  }, { after: 'input', autoInvalidate: false });
</script>

<T.Group position={[$view.entity.x, 0, 0]}>
  <T.Mesh scale={1 + strength}>
    <T.BoxGeometry />
    <T.MeshBasicMaterial />
  </T.Mesh>
</T.Group>
