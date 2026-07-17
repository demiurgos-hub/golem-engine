import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { GolemConnectionLifecycle } from "../dist/connection.js";

function fakeClient() {
  let connectHandler;
  let disconnectHandler;
  return {
    connected: false,
    connectCalls: [],
    disconnectCalls: 0,
    connect(options) {
      this.connectCalls.push(options);
    },
    disconnect() {
      this.disconnectCalls++;
      this.connected = false;
    },
    onConnect(handler) {
      connectHandler = handler;
    },
    onDisconnect(handler) {
      disconnectHandler = handler;
    },
    open() {
      this.connected = true;
      connectHandler?.();
    },
    close(info) {
      this.connected = false;
      disconnectHandler?.(info);
    },
  };
}

function fakeScheduler() {
  let nextId = 1;
  const timers = new Map();
  return {
    delays: [],
    setTimeout(fn, delayMs) {
      const id = nextId++;
      timers.set(id, fn);
      this.delays.push(delayMs);
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    runNext() {
      const entry = timers.entries().next().value;
      assert.ok(entry, "expected a pending timer");
      const [id, fn] = entry;
      timers.delete(id);
      fn();
    },
    get size() {
      return timers.size;
    },
  };
}

describe("GolemConnectionLifecycle", () => {
  it("creates one client and auto-connects", () => {
    const client = fakeClient();
    let created = 0;
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle({
      createClient: () => {
        created++;
        return client;
      },
      connectionOptions: () => "ws://localhost/game",
    });
    lifecycle.onStatus((status) => statuses.push(status));

    lifecycle.start();
    lifecycle.start();
    assert.equal(created, 1);
    assert.deepEqual(client.connectCalls, ["ws://localhost/game"]);
    assert.deepEqual(statuses, [{ type: "connecting", attempt: 1 }]);

    client.open();
    assert.equal(lifecycle.connected, true);
    assert.deepEqual(statuses.at(-1), { type: "connected" });
  });

  it("refreshes options and applies exponential reconnect delays", () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    let optionVersion = 0;
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => `ws://localhost/game/${++optionVersion}`,
        reconnectBaseDelay: 100,
      },
      scheduler,
    );
    lifecycle.onStatus((status) => statuses.push(status));
    lifecycle.start();

    client.close({ wasClean: false });
    assert.equal(scheduler.size, 1);
    assert.equal(scheduler.delays.at(-1), 100);
    assert.deepEqual(statuses.at(-1), {
      type: "reconnecting",
      attempt: 1,
      delayMs: 100,
    });
    scheduler.runNext();
    assert.equal(client.connectCalls.at(-1), "ws://localhost/game/2");

    client.close({ wasClean: false });
    assert.equal(scheduler.delays.at(-1), 200);
    scheduler.runNext();
    assert.equal(client.connectCalls.at(-1), "ws://localhost/game/3");
  });

  it("does not reconnect clean or intentional disconnects", () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => "ws://localhost/game",
      },
      scheduler,
    );
    lifecycle.start();
    client.close({ wasClean: true });
    assert.equal(scheduler.size, 0);

    lifecycle.disconnect();
    client.close({ wasClean: false });
    assert.equal(scheduler.size, 0);
  });

  it("reports failure after the configured retry limit", () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => "ws://localhost/game",
        maxReconnectAttempts: 1,
        reconnectBaseDelay: 10,
      },
      scheduler,
    );
    lifecycle.onStatus((status) => statuses.push(status));
    lifecycle.start();

    client.close({ wasClean: false });
    scheduler.runNext();
    client.close({ wasClean: false });
    assert.deepEqual(statuses.at(-1), { type: "failed", attempts: 1 });
    assert.equal(scheduler.size, 0);
  });

  it("clears timers and status listeners on destroy", () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => "ws://localhost/game",
      },
      scheduler,
    );
    const unsubscribe = lifecycle.onStatus((status) =>
      statuses.push(status),
    );
    lifecycle.start();
    client.close({ wasClean: false });
    assert.equal(scheduler.size, 1);

    unsubscribe();
    lifecycle.destroy();
    assert.equal(scheduler.size, 0);
    assert.equal(client.disconnectCalls, 1);
  });
});
