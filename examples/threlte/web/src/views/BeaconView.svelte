<script lang="ts">
  import { T } from '@threlte/core';
  import { untrack } from 'svelte';
  import { useEntityTask, useEntityTransform } from 'golem-threlte/threlte';
  import type { Group } from 'three';
  import type { BeaconViewProps } from '../generated/threlte/GolemThrelte.js';

  const { view }: BeaconViewProps = $props();
  const binding = untrack(() => view);
  let root: Group | undefined = $state();
  let crystal: Group | undefined = $state();
  useEntityTransform(binding, {
    target: () => root,
    select: (entity) => ({ position: [entity.posX, entity.posY, entity.posZ] }),
  });
  useEntityTask(binding, (_, dt) => { if (crystal) crystal.rotation.y += dt * 0.45; });
</script>

<T.Group bind:ref={root}>
  <T.Group bind:ref={crystal}>
    <T.Mesh scale={[0.65, 1.1, 0.65]}>
      <T.OctahedronGeometry args={[0.55]} />
      <T.MeshStandardMaterial color="#f2bc82" emissive="#ec9056" emissiveIntensity={$view.entity.intensity * 0.55} roughness={0.35} metalness={0.35} />
    </T.Mesh>
  </T.Group>
  <T.Mesh position={[0, -0.5, 0]}>
    <T.CylinderGeometry args={[0.5, 0.65, 0.18, 32]} />
    <T.MeshStandardMaterial color="#415b55" roughness={0.75} />
  </T.Mesh>
</T.Group>
