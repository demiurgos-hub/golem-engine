import type { Component } from 'svelte';
import type { GameClient } from 'golem-engine';
import { createEntityViewBuilder, type EntityViewBinding, type EntityViewDefinition, type EntityViewProps } from '../src/core.js';
import { useEntityEvent, type GolemSource } from '../src/context.js';
import { useEntityTask, useEntityTransform } from '../src/tasks.js';
import { Actor, Beacon, type ActorEvents } from './models.js';
import ActorView from './ActorView.svelte';
import BeaconView from './BeaconView.svelte';

const actors = createEntityViewBuilder<Actor, ActorEvents>();
const beacons = createEntityViewBuilder<Beacon>();
actors.component(ActorView);
beacons.component(BeaconView);
const headless: EntityViewDefinition<Beacon> = beacons.headless();
void headless;

// @ts-expect-error The component expects a different synchronized entity.
actors.component(BeaconView);
// @ts-expect-error An Actor view cannot accept a positionless Beacon binding.
beacons.component(ActorView);
declare const wrongEvents: Component<EntityViewProps<Actor, { impact: { strength: string } }>>;
// @ts-expect-error Entity-targeted event payloads must match the generated type.
actors.component(wrongEvents);
declare const extraProps: Component<EntityViewProps<Actor, ActorEvents> & { requiredAsset: string }>;
// @ts-expect-error The mount cannot provide an extra required component prop.
actors.component(extraProps);

declare const view: EntityViewBinding<Actor, ActorEvents>;
view.onEvent('impact', event => {
  const strength: number = event.strength;
  // @ts-expect-error Typed payloads do not permit unrelated fields.
  event.damage;
  // @ts-expect-error Generated numeric payload is not a string.
  const bad: string = event.strength;
  void strength; void bad;
});
// @ts-expect-error An undeclared event is not available on this entity.
view.onEvent('missing', () => {});
useEntityEvent(view, 'impact', (event, entity) => { entity.health + event.strength; });
// @ts-expect-error Listener payload parameters cannot disagree with the event.
useEntityEvent(view, 'impact', (event: { strength: string }) => { void event; });
useEntityTask(view, (entity, seconds) => { entity.health + seconds; });
useEntityTransform(view, { target: () => undefined, select: entity => ({ position: [entity.x, 0, 0] }) });

declare const beacon: EntityViewBinding<Beacon>;
useEntityTask(beacon, entity => { entity.label.toUpperCase(); });
// @ts-expect-error Headless models have no implicit position contract.
beacon.current.entity.x;

declare const client: GameClient;
const borrowed: GolemSource<GameClient> = { kind: 'client', client };
const owned: GolemSource<GameClient> = { kind: 'owned', config: { createClient: () => client, connectionOptions: async () => 'ws://localhost/game' } };
// @ts-expect-error Ownership configurations are mutually exclusive.
const ambiguous: GolemSource<GameClient> = { kind: 'client', client, config: owned.config };
void borrowed; void owned; void ambiguous;
