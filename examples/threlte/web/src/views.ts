import { defineEntityViews } from './generated/threlte/GolemThrelte.js';
import ActorView from './views/ActorView.svelte';
import BeaconView from './views/BeaconView.svelte';

export const entityViews = defineEntityViews((views) => ({
  Actor: views.Actor.component(ActorView),
  Beacon: views.Beacon.component(BeaconView),
  SessionStats: views.SessionStats.headless(),
}));
