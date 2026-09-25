<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { T } from '@threlte/core';
  import type { Group } from 'three';
  import type { EntityViewProps } from '../src/core.js';
  import { useEntityEvent, useGolemContext } from '../src/context.js';
  import { useEntityTask, useEntityTransform } from '../src/tasks.js';
  import { browserAdapter } from './browser-fixture.js';

  let { view }: EntityViewProps<any, { impact: { strength: number } }> = $props();
  const incarnation = untrack(() => view);
  const controls = useGolemContext(browserAdapter).client.fixture;
  const id = incarnation.current.entity.entityId;
  let node = $state.raw<Group>();
  let strength = $state(0);
  controls.created++;
  const firstKey = Symbol('first');
  useEntityTask(incarnation, (entity, delta) => {
    controls.taskCalls.push({ id: entity.entityId, phase: 'first', delta });
    strength = Math.max(0, strength - delta);
  }, { key: firstKey });
  useEntityTask(incarnation, entity => {
    controls.taskCalls.push({ id: entity.entityId, phase: 'second' });
  }, { after: firstKey });
  const transform = useEntityTransform(incarnation, {
    target: () => controls.targets.get(incarnation) ?? node,
    select: entity => ({
      position: [entity.x, 0, 0],
      scale: [entity.scale ?? 1, entity.scale ?? 1, entity.scale ?? 1],
      quaternion: [0, Math.sin((entity.angle ?? 0) / 2), 0, Math.cos((entity.angle ?? 0) / 2)],
    }),
    interpolation: { durationSeconds: 0.1 },
    external: () => controls.external.has(id),
  });
  controls.transforms.set(incarnation, transform);
  useEntityEvent(incarnation, 'impact', (event, entity) => {
    strength = event.strength;
    controls.events.push({ id: entity.entityId, strength: event.strength });
  });
  $effect(() => {
    if (node) {
      node.userData.health = $view.entity.health;
      node.userData.strength = strength;
      controls.nodes.set(view, node);
    }
  });
  onDestroy(() => {
    controls.destroyed++;
    controls.nodes.delete(incarnation);
    controls.transforms.delete(incarnation);
  });
</script>

<T.Group bind:ref={node} name={`actor-${id}`}>
  <T.Mesh position={[0, 1, 0]}>
    <T is={controls.sharedGeometry} dispose={false} />
    <T.MeshBasicMaterial oncreate={material => {
      material.addEventListener('dispose', () => controls.materialsDisposed++);
    }} />
  </T.Mesh>
</T.Group>
