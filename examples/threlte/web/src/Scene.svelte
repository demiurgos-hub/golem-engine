<script lang="ts">
  import { T } from '@threlte/core';
  import { EntityViews } from 'golem-threlte/threlte';
  import { SyncedActor } from './generated/client/ActorSynced.js';
  import { entityViews } from './views.js';

  let { overview = false }: { overview?: boolean } = $props();
</script>

<T.Color attach="background" args={['#142125']} />
<T.PerspectiveCamera makeDefault position={overview ? [0, 10, 0.01] : [8, 7, 9]} fov={overview ? 47 : 40} oncreate={(camera) => camera.lookAt(0, 0, 0)} />
<T.AmbientLight intensity={1.7} />
<T.DirectionalLight position={[3, 8, 4]} intensity={3.5} />
<T.DirectionalLight position={[-4, 2, -3]} color="#77d7ce" intensity={2.3} />
<T.Mesh position={[0, -0.15, 0]}>
  <T.CylinderGeometry args={[4.3, 4.5, 0.3, 80]} />
  <T.MeshStandardMaterial color="#263c3d" roughness={0.95} />
</T.Mesh>
<T.Mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.01, 0]}>
  <T.RingGeometry args={[2.75, 2.83, 96]} />
  <T.MeshBasicMaterial color="#59716b" />
</T.Mesh>
{#each [-3, -2, -1, 0, 1, 2, 3] as coordinate}
  <T.Mesh position={[coordinate, 0.012, 0]} rotation={[-Math.PI / 2, 0, 0]}>
    <T.PlaneGeometry args={[0.014, Math.sqrt(16 - coordinate ** 2) * 2]} />
    <T.MeshBasicMaterial color="#324e4d" />
  </T.Mesh>
  <T.Mesh position={[0, 0.013, coordinate]} rotation={[-Math.PI / 2, 0, Math.PI / 2]}>
    <T.PlaneGeometry args={[0.014, Math.sqrt(16 - coordinate ** 2) * 2]} />
    <T.MeshBasicMaterial color="#324e4d" />
  </T.Mesh>
{/each}
<EntityViews registry={entityViews} filter={overview ? (entity) => entity instanceof SyncedActor : undefined} />
