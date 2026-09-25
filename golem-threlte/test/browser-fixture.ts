import { BoxGeometry } from 'three';
import { createEntityViewBuilder, createEntityViewRegistry, createGolemAdapter, entityCollection, worldStore } from '../src/core.js';
import { Actor, fixture } from './fixtures.js';
import ActorProbe from './ActorProbe.svelte';

export const browserAdapter = createGolemAdapter({
  entities: (client: any) => client.entities,
  createBindings: (client: any) => ({
    entityTypes: { Actor: entityCollection(client.entities, (entity: any): entity is any => entity instanceof Actor) },
    world: { settings: worldStore(() => client.fixture.world.current, client.fixture.subscribeWorld) },
  }),
  subscribeEvents: (client: any, dispatch) => client.fixture.subscribeEvents(dispatch),
});
const actorViews = createEntityViewBuilder<any, { impact: { strength: number } }>();
const headlessViews = createEntityViewBuilder<any>();
export const browserRegistry = createEntityViewRegistry(browserAdapter, [
  { matches: entity => entity instanceof Actor, definition: actorViews.component(ActorProbe) },
  { matches: () => true, definition: headlessViews.headless() },
]);

export function browserFixture(initial: any[] = []) {
  const state: any = fixture(initial);
  state.nodes = new Map();
  state.transforms = new Map();
  state.targets = new Map();
  state.events = [];
  state.taskCalls = [];
  state.created = 0;
  state.destroyed = 0;
  state.materialsDisposed = 0;
  state.sharedDisposed = 0;
  state.external = new Set();
  state.sharedGeometry = new BoxGeometry();
  state.sharedGeometry.addEventListener('dispose', () => state.sharedDisposed++);
  state.renderers = [];
  state.stages = [];
  state.createRenderer = (canvas: HTMLCanvasElement) => {
    const renderer: any = {
      domElement: canvas,
      xr: { isPresenting: false },
      shadowMap: {},
      time: 0,
      disposed: false,
      setAnimationLoop(callback: ((time: number) => void) | null) { this.loop = callback; },
      setSize() {},
      setPixelRatio() {},
      render() {},
      dispose() { this.disposed = true; },
      step(seconds: number) { this.time += seconds * 1000; this.loop?.(this.time); },
    };
    state.renderers.push(renderer);
    return renderer;
  };
  state.step = (seconds = 0.05) => {
    for (const renderer of state.renderers) renderer.step(seconds);
  };
  state.client.connect('ws://localhost/game');
  state.channels[0].open();
  for (const entity of initial) state.add(entity);
  return state;
}
