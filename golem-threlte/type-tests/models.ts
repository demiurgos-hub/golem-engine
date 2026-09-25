import type { EntityViewProps } from '../src/core.js';

export class Actor {
  constructor(public readonly entityId: number, public health: number, public x: number) {}
}
export class Beacon {
  constructor(public readonly entityId: number, public label: string) {}
}
export interface ActorEvents { impact: { strength: number } }
export type ActorProps = EntityViewProps<Actor, ActorEvents>;
export type BeaconProps = EntityViewProps<Beacon>;
