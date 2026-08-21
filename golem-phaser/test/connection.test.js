import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { GolemConnectionLifecycle } from "../dist/connection.js";

function fakeClient({ notifyOnDisconnect = false } = {}) {
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
      if (notifyOnDisconnect) {
        disconnectHandler?.({
          code: 1000,
          reason: "client disconnect",
          wasClean: true,
        });
      }
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

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
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

  it("forwards a fallback connection plan as one lifecycle attempt", () => {
    const client = fakeClient();
    const statuses = [];
    const plan = {
      candidates: [
        { transport: "webtransport", url: "https://localhost/api/wt" },
        { transport: "websocket", url: "ws://localhost/api/ws" },
      ],
      resolveOptions: (endpoint) => endpoint,
    };
    const lifecycle = new GolemConnectionLifecycle({
      createClient: () => client,
      connectionOptions: () => plan,
    });
    lifecycle.onStatus((status) => statuses.push(status));

    lifecycle.start();
    assert.equal(client.connectCalls.length, 1);
    assert.equal(client.connectCalls[0], plan);
    assert.deepEqual(statuses, [{ type: "connecting", attempt: 1 }]);

    client.open();
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

  it("awaits asynchronous options for initial and reconnect attempts", async () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    const options = [deferred(), deferred()];
    let optionIndex = 0;
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => options[optionIndex++].promise,
        reconnectBaseDelay: 10,
      },
      scheduler,
    );

    lifecycle.start();
    assert.deepEqual(client.connectCalls, []);
    options[0].resolve("ws://localhost/game/1");
    await options[0].promise;
    assert.deepEqual(client.connectCalls, ["ws://localhost/game/1"]);

    client.close({ wasClean: false });
    scheduler.runNext();
    assert.deepEqual(client.connectCalls, ["ws://localhost/game/1"]);
    options[1].resolve("ws://localhost/game/2");
    await options[1].promise;
    assert.deepEqual(client.connectCalls, [
      "ws://localhost/game/1",
      "ws://localhost/game/2",
    ]);
  });

  it("reconnects when asynchronous options reject", async () => {
    const client = fakeClient();
    const scheduler = fakeScheduler();
    const pending = deferred();
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => pending.promise,
        reconnectBaseDelay: 25,
      },
      scheduler,
    );
    lifecycle.onStatus((status) => statuses.push(status));

    lifecycle.start();
    const failure = new Error("ticket request failed");
    pending.reject(failure);
    await assert.rejects(pending.promise, failure);

    assert.deepEqual(statuses.at(-2), {
      type: "disconnected",
      info: {
        wasClean: false,
        error: failure,
        reason: "connection options failed",
      },
    });
    assert.deepEqual(statuses.at(-1), {
      type: "reconnecting",
      attempt: 1,
      delayMs: 25,
    });
    assert.equal(scheduler.size, 1);
  });

  it("ignores async options from superseded connect attempts", async () => {
    const client = fakeClient();
    const first = deferred();
    const second = deferred();
    let optionIndex = 0;
    const lifecycle = new GolemConnectionLifecycle({
      createClient: () => client,
      connectionOptions: () => [first, second][optionIndex++].promise,
    });

    lifecycle.start();
    lifecycle.connect();
    first.resolve("ws://localhost/stale");
    await first.promise;
    assert.deepEqual(client.connectCalls, []);

    second.resolve("ws://localhost/current");
    await second.promise;
    assert.deepEqual(client.connectCalls, ["ws://localhost/current"]);
  });

  it("ignores pending async options after disconnect or destroy", async () => {
    const disconnectedClient = fakeClient();
    const disconnectedOptions = deferred();
    const disconnected = new GolemConnectionLifecycle({
      createClient: () => disconnectedClient,
      connectionOptions: () => disconnectedOptions.promise,
    });
    disconnected.start();
    disconnected.disconnect();
    disconnectedOptions.resolve("ws://localhost/disconnected");
    await disconnectedOptions.promise;
    assert.deepEqual(disconnectedClient.connectCalls, []);

    const destroyedClient = fakeClient();
    const destroyedOptions = deferred();
    const destroyed = new GolemConnectionLifecycle({
      createClient: () => destroyedClient,
      connectionOptions: () => destroyedOptions.promise,
    });
    destroyed.start();
    destroyed.destroy();
    destroyedOptions.resolve("ws://localhost/destroyed");
    await destroyedOptions.promise;
    assert.deepEqual(destroyedClient.connectCalls, []);
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

  it("reports the GameClient clean close on intentional disconnect without reconnecting", () => {
    const client = fakeClient({ notifyOnDisconnect: true });
    const scheduler = fakeScheduler();
    const statuses = [];
    const lifecycle = new GolemConnectionLifecycle(
      {
        createClient: () => client,
        connectionOptions: () => "ws://localhost/game",
      },
      scheduler,
    );
    lifecycle.onStatus((status) => statuses.push(status));
    lifecycle.start();
    client.open();

    lifecycle.disconnect();

    assert.deepEqual(statuses.at(-1), {
      type: "disconnected",
      info: {
        code: 1000,
        reason: "client disconnect",
        wasClean: true,
      },
    });
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
