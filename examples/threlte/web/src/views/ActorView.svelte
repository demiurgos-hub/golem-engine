<script lang="ts">
  import { T } from '@threlte/core';
  import { getContext, untrack } from 'svelte';
  import { useEntityEvent } from 'golem-threlte';
  import { useEntityTask, useEntityTransform } from 'golem-threlte/threlte';
  import type { Group } from 'three';
  import type { ActorViewProps } from '../generated/threlte/GolemThrelte.js';
  import { externalMotion } from '../preferences.js';
  import Halo from './Halo.svelte';
  import { sharedAssetsKey, type SharedAssets } from '../shared-assets.js';

  const assets = getContext<SharedAssets>(sharedAssetsKey);
  const { view }: ActorViewProps = $props();
  // A presentation binding is stable until this keyed component is unmounted.
  const binding = untrack(() => view);
  let root: Group | undefined = $state();
  let flash = $state(0);
  let elapsed = 0;
  let warpCount = 0;
  const transform = useEntityTransform(binding, {
    target: () => root,
    select: (entity) => ({ position: [entity.posX, entity.posY, entity.posZ] }),
    interpolation: { durationSeconds: 0.15 },
    external: () => $externalMotion,
  });
  useEntityEvent(binding, 'ActorPulsed', (event) => { flash = event.strength; });
  useEntityTask(binding, (entity, dt) => {
    elapsed += dt;
    flash = Math.max(0, flash - dt * 1.7);
    if (root && $externalMotion) {
      // Application-owned transforms never write to the synchronized entity.
      root.position.set(entity.posX, entity.posY + 0.5 + Math.sin(elapsed * 2) * 0.35, entity.posZ);
    }
  });
  $effect(() => {
    if ($view.entity.warpCount !== warpCount) {
      warpCount = $view.entity.warpCount;
      transform.reset();
    }
  });
</script>

<T.Group bind:ref={root}>
  <T.Mesh rotation={[0, Math.PI / 4, 0]} scale={1 + flash * 0.15}>
    <T is={assets.actorGeometry} dispose={false} />
    <T.MeshStandardMaterial color={flash > 0 ? '#d4fff0' : '#9ddbbe'} emissive="#86e1b7" emissiveIntensity={flash * 1.7} roughness={0.45} metalness={0.15} />
  </T.Mesh>
  <T.Mesh position={[0, 0.17, 0.37]}>
    <T.BoxGeometry args={[0.38, 0.11, 0.06]} />
    <T.MeshBasicMaterial color="#254d45" />
  </T.Mesh>
  <Halo {flash} />
</T.Group>
