<script lang="ts">
  import { T } from '@threlte/core';
  let { flash }: { flash: number } = $props();
</script>

<!-- A reusable child presentation element, owned by the Actor component. -->
<T.Mesh position={[0, 0.85, 0]} rotation={[Math.PI / 2, 0, 0]} scale={1 + flash * 1.5}>
  <T.TorusGeometry args={[0.35, 0.03, 8, 40]} />
  <T.MeshBasicMaterial color="#e4ffc8" transparent opacity={0.45 + flash * 0.55} />
</T.Mesh>
