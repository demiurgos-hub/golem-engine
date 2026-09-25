import { GameClient } from 'golem-engine';

export class Actor {
  constructor(entityId, health = 100, x = 0) {
    this.entityId = entityId;
    this.health = health;
    this.x = x;
  }
}

export class Beacon {
  constructor(entityId, label = 'headless') {
    this.entityId = entityId;
    this.label = label;
  }
}

export function fixture(initial = []) {
  const values = new Map(initial.map(entity => [entity.entityId, entity]));
  const listeners = { spawn: new Set(), update: new Set(), remove: new Set(), event: new Set(), world: new Set() };
  const listen = (name, listener) => {
    listeners[name].add(listener);
    return () => listeners[name].delete(listener);
  };
  const emit = (name, ...args) => {
    for (const listener of [...listeners[name]]) if (listeners[name].has(listener)) listener(...args);
  };
  const entities = {
    get: id => values.get(id),
    getAll: () => values,
    onSpawn: listener => listen('spawn', listener),
    onUpdate: listener => listen('update', listener),
    onRemove: listener => listen('remove', listener),
    applyUpdate() {},
    clear() { for (const id of [...values.keys()]) controls.remove(id); },
  };
  const world = { current: { label: 'First world', tick: 0 }, applyUpdate() {} };
  const controls = {
    entities,
    world,
    listeners,
    channels: [],
    connectCalls: 0,
    closeCalls: 0,
    add(entity) { values.set(entity.entityId, entity); emit('spawn', entity); },
    update(entity, changes) { Object.assign(entity, changes); emit('update', entity); },
    remove(id) { values.delete(id); emit('remove', id); },
    event(entity, payload, name = 'impact') { emit('event', entity, name, payload); },
    subscribeEvents: dispatch => listen('event', dispatch),
    updateWorld(changes) { Object.assign(world.current, changes); emit('world', world.current); },
    subscribeWorld: listener => listen('world', listener),
    listenerCount() { return Object.values(listeners).reduce((sum, group) => sum + group.size, 0); },
  };
  const client = new GameClient({
    entityManager: entities,
    worldManager: world,
    decode: bytes => bytes,
    encode: () => new Uint8Array(),
    encodePacket: () => new Uint8Array(),
    createChannel() {
      controls.connectCalls++;
      const channel = {
        connected: false,
        maxMessageBytes: 32000,
        onOpen(fn) { this.openHandler = fn; },
        onClose(fn) { this.closeHandler = fn; },
        onMessage() {},
        send() {},
        close() { controls.closeCalls++; this.connected = false; },
        open() { this.connected = true; this.openHandler?.(); },
        fail() { this.connected = false; this.closeHandler?.({ wasClean: false, reason: 'network lost' }); },
      };
      controls.channels.push(channel);
      return channel;
    },
  });
  client.fixture = controls;
  controls.client = client;
  return controls;
}
