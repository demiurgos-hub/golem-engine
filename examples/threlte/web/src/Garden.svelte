<script lang="ts">
  import { Canvas } from '@threlte/core';
  import { onDestroy, setContext } from 'svelte';
  import { BoxGeometry } from 'three';
  import { useGolemEvent } from 'golem-threlte';
  import { useGolem } from './generated/threlte/GolemThrelte.js';
  import { sendPulse, sendToggleBeacon, sendWarp } from './generated/client/client.js';
  import { externalMotion } from './preferences.js';
  import Scene from './Scene.svelte';
  import { sharedAssetsKey, type SharedAssets } from './shared-assets.js';

  // Both scene mounts borrow this geometry; the persistent application owns it.
  const actorGeometry = new BoxGeometry(0.7, 0.95, 0.7);
  setContext<SharedAssets>(sharedAssetsKey, { actorGeometry });
  onDestroy(() => actorGeometry.dispose());
  const golem = useGolem();
  const { status } = golem;
  const actors = golem.entityTypes.Actor.all;
  const stats = golem.entityTypes.SessionStats.all;
  const arena = golem.world.Arena;
  let actorId = $state<number>();
  let sessionId = $state<number>();
  let sceneVisible = $state(true);
  let overviewVisible = $state(false);
  let remounts = $state(0);
  let receivedPulses = $state(0);
  const actor = $derived($actors.find((item) => item.entity.entityId === actorId));
  const connected = $derived($status.type === 'connected');

  useGolemEvent(golem.events.Assigned, (event) => {
    actorId = Number(event.actorId);
    sessionId = Number(event.sessionId);
  });
  useGolemEvent(golem.events.ActorPulsed, () => { receivedPulses++; });

  function toggleScene() {
    sceneVisible = !sceneVisible;
    if (sceneVisible) remounts++;
  }
</script>

<div class="shell">
  <header class="masthead">
    <a href="#garden" class="brand" aria-label="Signal Garden home"><span class="brand-mark">g.</span><span>GOLEM <i>×</i> THRELTE</span></a>
    <div class="status" class:online={connected}><span></span><span data-testid="connection-status">{$status.type}</span><small>WS · 20 Hz</small></div>
  </header>

  <main id="garden">
    <section class="introduction">
      <div><p class="eyebrow">A SMALL, SHARED WORLD</p><h1>{$arena?.title ?? 'Signal Garden'}<span> / 01</span></h1><p class="lede">One live session. A scene that comes and goes.</p></div>
      <div class="intro-note">Authoritative Go state.<br />Reactive Svelte UI.<br />Your Threlte components.</div>
    </section>

    <div class="workspace">
      <section class="stage" aria-label="Live 3D scene">
        <div class="stage-label"><span class="live-dot"></span>{sceneVisible ? 'LIVE SCENE' : 'SCENE UNMOUNTED'}<span class="stage-coordinate">Y ↑ · METERS</span></div>
        {#if sceneVisible}
          <Canvas dpr={[1, 1.75]}><Scene /></Canvas>
        {:else}
          <div class="empty-scene"><div class="empty-symbol">◌</div><h2>The garden is still growing.</h2><p>The scene is gone. Your session and the live HUD are still here.</p><button class="primary" onclick={toggleScene}>Remount scene <span>↗</span></button></div>
        {/if}
        {#if overviewVisible}
          <div class="overview"><span>ACTOR-ONLY MOUNT</span><Canvas dpr={1}><Scene overview /></Canvas></div>
        {/if}
        <div class="scene-key"><span><i class="actor-dot"></i> Actor</span><span><i class="beacon-dot"></i> Beacon</span><span class="scene-hint">Procedural geometry · no assets</span></div>
      </section>

      <aside class="panel">
        <div class="panel-heading"><p class="eyebrow">PERSISTENT SESSION</p><span class="session-id" data-testid="session-id">#{sessionId ?? '—'}</span></div>
        <h2>{actor?.entity.displayName ?? 'Joining the garden…'}</h2>
        <p class="panel-copy">The server moves your actor. Send a pulse to light up its child halo.</p>
        <button class="primary pulse-button" disabled={!connected || !actor} onclick={() => actorId !== undefined && sendPulse(golem.client, actorId)}>Send pulse <span>↗</span></button>
        <div class="metrics">
          <div><span>Actor pulses</span><strong data-testid="pulse-count">{actor?.entity.pulseCount ?? 0}</strong></div>
          <div><span>World pulses</span><strong data-testid="world-pulse-count">{$arena?.pulseTotal ?? 0}</strong></div>
          <div><span>Received events</span><strong data-testid="event-count">{receivedPulses}</strong></div>
          <div><span>Server uptime</span><strong data-testid="uptime">{$stats[0]?.entity.uptimeSeconds ?? 0}<small>s</small></strong></div>
        </div>

        <div class="panel-section">
          <p class="eyebrow">TRY THE LIFECYCLE</p>
          <button class="secondary" onclick={toggleScene}>{sceneVisible ? 'Unmount scene' : 'Remount scene'}<span>{sceneVisible ? '−' : '+'}</span></button>
          <button class="secondary" disabled={!connected} onclick={() => sendToggleBeacon(golem.client)}>{$arena?.beaconActive ? 'Remove beacon' : 'Restore beacon'}<span>↺</span></button>
          <button class="secondary" disabled={!connected || !actor} onclick={() => actorId !== undefined && sendWarp(golem.client, actorId)}>Teleport actor<span>⇢</span></button>
        </div>

        <div class="panel-section options">
          <label><input type="checkbox" bind:checked={overviewVisible} /><span>Second, filtered scene<small>Independent components, same client</small></span></label>
          <label><input type="checkbox" bind:checked={$externalMotion} /><span>Application transform control<small>Bypass interpolation; local floating motion</small></span></label>
        </div>
        <div class="session-foot"><span data-testid="actor-count">{$actors.length} actor{$actors.length === 1 ? '' : 's'}</span><span data-testid="remount-count">{remounts} remount{remounts === 1 ? '' : 's'}</span></div>
      </aside>
    </div>

    <section class="explanation"><div><span>01</span><p><strong>A persistent provider</strong>The HUD and both canvases share one generated client. Scene teardown leaves it connected.</p></div><div><span>02</span><p><strong>Components, not copies</strong>Actor and Beacon keep their synchronized identities. SessionStats is explicitly headless.</p></div><div><span>03</span><p><strong>Presentation stays yours</strong>Transforms interpolate for 150 ms. Teleports reset them; local control bypasses them.</p></div></section>
  </main>
  <footer><span>GOLEM ENGINE / THRELTE EXAMPLE</span><span>Open another tab to share the garden.</span></footer>
</div>
