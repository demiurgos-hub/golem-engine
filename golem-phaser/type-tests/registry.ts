import type Phaser from "phaser";
import {
  combineUnsubscribers,
  createEntityViewBuilder,
  createEntityViewRegistry,
  type EntityViewBuilder,
  type EntityViewDefinition,
  type EntityViewRegistry,
} from "../src/index.js";

class Player {
  readonly entityId = 1;
  readonly posX = 0;
  readonly posY = 0;
  readonly health = 100;
}

class Trigger {
  readonly entityId = 2;
  readonly posX = 0;
  readonly posY = 0;
}

interface HitEvent {
  amount: number;
}

interface Client {
  entities: {
    getAll(): ReadonlyMap<number, Player | Trigger>;
    onSpawn(fn: (entity: Player | Trigger) => void): () => void;
    onUpdate(fn: (entity: Player | Trigger) => void): () => void;
    onRemove(fn: (entityId: number) => void): () => void;
  };
  events: {
    onHit(fn: (entity: Player, event: HitEvent) => void): () => void;
  };
}

interface Builders {
  Player: EntityViewBuilder<Player, { Hit: HitEvent }>;
  Trigger: EntityViewBuilder<Trigger>;
}

interface Definitions {
  Player: EntityViewDefinition<Player>;
  Trigger: EntityViewDefinition<Trigger>;
}

function defineEntityViews(
  build: (builders: Builders) => Definitions,
): EntityViewRegistry<Client> {
  const builders: Builders = {
    Player: createEntityViewBuilder<Player, { Hit: HitEvent }>(),
    Trigger: createEntityViewBuilder<Trigger>(),
  };
  const definitions = build(builders);
  return createEntityViewRegistry({
    registrations: [
      {
        matches: (entity): entity is Player => entity instanceof Player,
        definition: definitions.Player,
      },
      {
        matches: (entity): entity is Trigger => entity instanceof Trigger,
        definition: definitions.Trigger,
      },
    ],
    entities: (client) => client.entities.getAll().values(),
    subscribeEntities(client, observer) {
      return combineUnsubscribers(
        client.entities.onSpawn(observer.spawn),
        client.entities.onUpdate(observer.update),
        client.entities.onRemove(observer.remove),
      );
    },
    subscribeEvents(client, dispatch) {
      return client.events.onHit((entity, event) =>
        dispatch(entity, "Hit", event),
      );
    },
  });
}

defineEntityViews((views) => ({
  Player: views.Player.prefab({
    create(_scene, player) {
      player.health satisfies number;
      return null as unknown as Phaser.GameObjects.Container;
    },
    events: {
      Hit(_view, player, event) {
        player.health satisfies number;
        event.amount satisfies number;
      },
    },
  }),
  Trigger: views.Trigger.headless(),
}));

defineEntityViews(
  // @ts-expect-error Trigger must be explicitly configured, even if headless.
  (views) => ({
    Player: views.Player.sprite({ texture: "player" }),
  }),
);
