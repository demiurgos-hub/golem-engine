import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  combineUnsubscribers,
  createEntityViewRegistry,
} from "../dist/view-registry.js";
import { EntityViewMount } from "../dist/view-mount.js";
import {
  createEntityViewBuilder,
  gpuView,
  headlessView,
  prefabView,
  spriteView,
} from "../dist/views.js";

class Player {
  constructor(entityId, posX, posY, health = 100) {
    this.entityId = entityId;
    this.posX = posX;
    this.posY = posY;
    this.health = health;
  }
}

class Trigger {
  constructor(entityId) {
    this.entityId = entityId;
    this.posX = 0;
    this.posY = 0;
  }
}

function fakeScene() {
  const listeners = new Map();
  const existing = [];
  const sprites = [];

  const events = {
    on(name, fn, context) {
      const set = listeners.get(name) ?? new Set();
      set.add({ fn, context, once: false });
      listeners.set(name, set);
      return this;
    },
    once(name, fn, context) {
      const set = listeners.get(name) ?? new Set();
      set.add({ fn, context, once: true });
      listeners.set(name, set);
      return this;
    },
    off(name, fn, context) {
      const set = listeners.get(name);
      if (!set) return this;
      for (const entry of [...set]) {
        if (entry.fn === fn && entry.context === context) {
          set.delete(entry);
        }
      }
      return this;
    },
    emit(name, ...args) {
      const set = listeners.get(name);
      if (!set) return this;
      for (const entry of [...set]) {
        entry.fn.apply(entry.context, args);
        if (entry.once) set.delete(entry);
      }
      return this;
    },
  };

  return {
    events,
    existing,
    sprites,
    add: {
      existing(view) {
        existing.push(view);
        return view;
      },
      sprite(x, y, texture, frame) {
        const sprite = {
          x,
          y,
          texture,
          frame,
          destroyed: false,
          setPosition(nextX, nextY) {
            this.x = nextX;
            this.y = nextY;
            return this;
          },
          setFrame(nextFrame) {
            this.frame = nextFrame;
            return this;
          },
          destroy() {
            this.destroyed = true;
          },
        };
        sprites.push(sprite);
        return sprite;
      },
    },
  };
}

function fakeClient(initial = []) {
  const all = new Map(initial.map((entity) => [entity.entityId, entity]));
  const spawnListeners = new Set();
  const updateListeners = new Set();
  const removeListeners = new Set();
  const hitListeners = new Set();

  return {
    entities: {
      getAll: () => all,
      onSpawn(fn) {
        spawnListeners.add(fn);
        return () => spawnListeners.delete(fn);
      },
      onUpdate(fn) {
        updateListeners.add(fn);
        return () => updateListeners.delete(fn);
      },
      onRemove(fn) {
        removeListeners.add(fn);
        return () => removeListeners.delete(fn);
      },
    },
    events: {
      onHit(fn) {
        hitListeners.add(fn);
        return () => hitListeners.delete(fn);
      },
    },
    spawn(entity) {
      all.set(entity.entityId, entity);
      for (const fn of spawnListeners) fn(entity);
    },
    update(entity) {
      all.set(entity.entityId, entity);
      for (const fn of updateListeners) fn(entity);
    },
    remove(entityId) {
      all.delete(entityId);
      for (const fn of removeListeners) fn(entityId);
    },
    hit(entity, event) {
      for (const fn of hitListeners) fn(entity, event);
    },
    listenerCount() {
      return (
        spawnListeners.size +
        updateListeners.size +
        removeListeners.size +
        hitListeners.size
      );
    },
  };
}

function registry(definitions) {
  return createEntityViewRegistry({
    registrations: [
      {
        matches: (entity) => entity instanceof Player,
        definition: definitions.Player,
      },
      {
        matches: (entity) => entity instanceof Trigger,
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

describe("EntityViewMount", () => {
  it("backfills, synchronizes, removes, and remounts prefab views", () => {
    const client = fakeClient([new Player(1, 10, 20, 80)]);
    const created = [];
    const definition = prefabView({
      create(_scene, player) {
        const view = {
          health: 0,
          destroyed: false,
          destroy() {
            this.destroyed = true;
          },
        };
        created.push(view);
        return view;
      },
      sync(view, player) {
        view.health = player.health;
      },
    });
    const views = registry({
      Player: definition,
      Trigger: headlessView(),
    });

    const firstScene = fakeScene();
    const firstMount = new EntityViewMount(firstScene, client, views);
    firstMount.mount();
    assert.equal(created.length, 1);
    assert.equal(created[0].health, 80);
    assert.equal(firstScene.existing.length, 1);

    client.update(new Player(1, 15, 25, 55));
    assert.equal(created[0].health, 55);

    firstMount.unmount();
    assert.equal(created[0].destroyed, true);
    assert.equal(client.entities.getAll().size, 1);
    assert.equal(client.listenerCount(), 0);

    const secondMount = new EntityViewMount(fakeScene(), client, views);
    secondMount.mount();
    assert.equal(created.length, 2);
    assert.equal(created[1].health, 55);
    client.remove(1);
    assert.equal(created[1].destroyed, true);
  });

  it("supports multiple scene mounts and typed entity events", () => {
    const player = new Player(2, 0, 0);
    const client = fakeClient([player]);
    const hits = [];
    const definition = createEntityViewBuilder().prefab({
      create() {
        return {
          destroy() {},
        };
      },
      events: {
        Hit(_view, entity, event) {
          hits.push([entity.entityId, event.amount]);
        },
      },
    });
    const views = registry({
      Player: definition,
      Trigger: headlessView(),
    });

    const mountA = new EntityViewMount(fakeScene(), client, views);
    const mountB = new EntityViewMount(fakeScene(), client, views);
    mountA.mount();
    mountB.mount();
    client.hit(player, { amount: 4 });
    assert.deepEqual(hits, [
      [2, 4],
      [2, 4],
    ]);

    mountA.unmount();
    client.hit(player, { amount: 7 });
    assert.deepEqual(hits.at(-1), [2, 7]);
    assert.equal(hits.length, 3);
  });

  it("interpolates sprites while externalPosition leaves prediction in control", () => {
    const client = fakeClient([
      new Player(3, 0, 0),
      new Trigger(9),
    ]);
    const scene = fakeScene();
    const views = registry({
      Player: spriteView({
        texture: "player",
        interpolation: 100,
      }),
      Trigger: headlessView(),
    });
    const mount = new EntityViewMount(scene, client, views);
    mount.mount();
    client.update(new Player(3, 100, 0));
    scene.events.emit("update", 0, 50);
    assert.equal(scene.sprites[0].x, 50);

    mount.unmount();
    const predictedScene = fakeScene();
    const predictedViews = registry({
      Player: spriteView({
        texture: "player",
        interpolation: 100,
        externalPosition: true,
      }),
      Trigger: headlessView(),
    });
    const predictedMount = new EntityViewMount(
      predictedScene,
      client,
      predictedViews,
    );
    predictedMount.mount();
    predictedScene.sprites[0].setPosition(42, 24);
    client.update(new Player(3, 200, 200));
    predictedScene.events.emit("update", 0, 50);
    assert.equal(predictedScene.sprites[0].x, 42);
    assert.equal(predictedScene.sprites[0].y, 24);
  });

  it("owns one GPU pool per mount and supports headless events", () => {
    const player = new Player(4, 5, 6);
    const trigger = new Trigger(10);
    const client = fakeClient([player, trigger]);
    const calls = [];
    const pool = {
      spawn(entity) {
        calls.push(["spawn", entity.entityId]);
        return 12;
      },
      update(entity) {
        calls.push(["update", entity.entityId]);
      },
      remove(entityId) {
        calls.push(["remove", entityId]);
      },
      destroy() {
        calls.push(["destroy"]);
      },
    };
    const triggerHits = [];
    const views = registry({
      Player: gpuView({
        createPool: () => pool,
      }),
      Trigger: headlessView({
        events: {
          Hit(entity, event) {
            triggerHits.push([entity.entityId, event.amount]);
          },
        },
      }),
    });

    const mount = new EntityViewMount(fakeScene(), client, views);
    mount.mount();
    client.update(new Player(4, 8, 9));
    client.hit(trigger, { amount: 1 });
    mount.unmount();

    assert.deepEqual(calls, [
      ["spawn", 4],
      ["update", 4],
      ["remove", 4],
      ["destroy"],
    ]);
    assert.deepEqual(triggerHits, [[10, 1]]);
  });
});
