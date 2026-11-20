import { afterEach, describe, expect, it, vi } from 'vitest';
import { decodeMessage, encodeResync } from '../protocol/messages';
import { createConnectionStore } from '../store/connectionStore';
import { createEntityStore } from '../store/entityStore';
import { AuthError, authBaseFromWs, fetchAuthSession, loginWithPassword } from './auth';
import { chooseTargetId, filterTargets, groupTargets, labelKeys, targetsUrlFromWs, updateTargetSearch, type InspectorTarget } from './targets';
import { createWsClient, deriveWsUrl, reconnectDelay } from './wsClient';

function target(id: string, labels: Record<string, string> = {}, extra: Partial<InspectorTarget> = {}): InspectorTarget {
  return {
    targetId: id,
    labels,
    entityTypes: [],
    connected: true,
    observeState: 'idle',
    ready: false,
    entityCount: 0,
    viewerCount: 0,
    firstRegisteredAt: null,
    registeredAt: null,
    connectedAt: null,
    disconnectedAt: null,
    lastMessageAt: null,
    snapshotStartedAt: null,
    lastSnapshotCompletedAt: null,
    lastSnapshotDurationMs: null,
    lastSnapshotEntityCount: null,
    ...extra,
  };
}

describe('wsClient helpers', () => {
  it('derives same-origin socket URLs', () => {
    expect(deriveWsUrl({ protocol: 'https:', host: 'insp.example:8443' })).toBe('wss://insp.example:8443/ws/client');
    expect(deriveWsUrl({ protocol: 'http:', host: 'localhost:5173' })).toBe('ws://localhost:5173/ws/client');
    expect(deriveWsUrl(undefined)).toBe('ws://127.0.0.1:8787/ws/client');
    expect(deriveWsUrl({ protocol: 'http:', host: 'x' }, 'ws://override/ws/client')).toBe('ws://override/ws/client');
  });

  it('uses capped full-jitter backoff', () => {
    expect(reconnectDelay(1, () => 0.5)).toBe(250);
    expect(reconnectDelay(2, () => 0.5)).toBe(500);
    expect(reconnectDelay(20, () => 0.999)).toBeLessThan(10_000);
    expect(reconnectDelay(3, () => 0)).toBe(0);
  });
});

class FakeSocket {
  static OPEN = 1;
  readyState = 0;
  binaryType = '';
  sent: Uint8Array[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  send(data: Uint8Array) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  receive(frame: Uint8Array) {
    this.onmessage?.({ data: frame.buffer.slice(frame.byteOffset, frame.byteOffset + frame.byteLength) });
  }
}

describe('wsClient', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('subscribes on open and auto-resyncs after resyncRequired', () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    const sockets: FakeSocket[] = [];
    const entityStore = createEntityStore();
    entityStore.setTarget('cave-1');
    const client = createWsClient({
      url: 'ws://hub/ws/client',
      entityStore,
      connectionStore: createConnectionStore('ws://hub/ws/client'),
      getTargetId: () => 'cave-1',
      createSocket: () => {
        const s = new FakeSocket();
        sockets.push(s);
        return s as unknown as WebSocket;
      },
    });
    client.start();
    sockets[0]!.open();
    expect(decodeMessage(sockets[0]!.sent[0]!)).toEqual({ type: 'subscribe', targetId: 'cave-1' });

    const rr = Uint8Array.from([0x08, 0x01, 0xd2, 0x02, 0x0f, 0x0a, 0x06, ...new TextEncoder().encode('cave-1'), 0x1a, 0x05, ...new TextEncoder().encode('stale')]);
    sockets[0]!.receive(rr);
    expect(entityStore.getState().needsResync).toBe(true);
    expect(sockets[0]!.sent.at(-1)).toEqual(encodeResync('cave-1'));
    client.stop();
  });

  it('reports protocol errors for foreign versions', () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    const sockets: FakeSocket[] = [];
    const connectionStore = createConnectionStore('ws://hub/ws/client');
    const client = createWsClient({
      url: 'ws://hub/ws/client',
      entityStore: createEntityStore(),
      connectionStore,
      getTargetId: () => null,
      createSocket: () => {
        const s = new FakeSocket();
        sockets.push(s);
        return s as unknown as WebSocket;
      },
    });
    client.start();
    sockets[0]!.open();
    sockets[0]!.receive(Uint8Array.of(0x08, 0x02, 0x82, 0x01, 0x00));
    expect(connectionStore.getState().status).toBe('protocol_error');
    expect(connectionStore.getState().protocolErrorDetail).toMatch(/version 2/);
    client.stop();
  });
});

describe('targets', () => {
  const list = [target('cave-2', { map: 'caves', region: 'eu' }), target('cave-1', { map: 'caves' }, { connected: false }), target('town', { map: 'overworld', name: 'Town' })];

  it('resolves the REST URL and persists the selection in the query string', () => {
    expect(targetsUrlFromWs('wss://insp.example/ws/client', 'https://insp.example/')).toBe('https://insp.example/api/targets');
    expect(updateTargetSearch('?a=1', 'cave-1')).toBe('?a=1&targetId=cave-1');
    expect(updateTargetSearch('?targetId=x', null)).toBe('');
  });

  it('chooses the current, requested or only target', () => {
    expect(chooseTargetId(list, 'cave-1', 'town')).toBe('town');
    expect(chooseTargetId(list, 'cave-1', 'gone')).toBe('cave-1');
    expect(chooseTargetId(list, null, null)).toBeNull();
    expect(chooseTargetId([list[0]!], null, null)).toBe('cave-2');
  });

  it('searches ids, labels and status', () => {
    expect(filterTargets(list, 'offline').map((t) => t.targetId)).toEqual(['cave-1']);
    expect(filterTargets(list, 'region=eu').map((t) => t.targetId)).toEqual(['cave-2']);
    expect(filterTargets(list, 'caves cave-2').map((t) => t.targetId)).toEqual(['cave-2']);
    expect(filterTargets(list, '  ')).toHaveLength(3);
  });

  it('groups by a label key with unlabeled targets last', () => {
    expect(labelKeys(list)).toEqual(['map', 'name', 'region']);
    const groups = groupTargets(list, 'region');
    expect(groups.map((g) => [g.key, g.targets.map((t) => t.targetId)])).toEqual([
      ['eu', ['cave-2']],
      ['—', ['cave-1', 'town']],
    ]);
    expect(groupTargets(list, null)[0]!.targets.map((t) => t.targetId)).toEqual(['cave-1', 'cave-2', 'town']);
  });
});

describe('auth', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('derives the HTTP base from the socket URL', () => {
    expect(authBaseFromWs('wss://insp.example/ws/client')).toBe('https://insp.example');
    expect(authBaseFromWs('/ws/client', 'http://localhost:5173/app')).toBe('http://localhost:5173');
  });

  it('parses sessions and maps error codes', async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/auth/session')) return new Response(JSON.stringify({ authenticated: false, authDisabled: true }), { status: 200 });
      return new Response(JSON.stringify({ error: 'invalid_credentials' }), { status: 401 });
    });
    vi.stubGlobal('fetch', fetchMock);
    await expect(fetchAuthSession('http://hub')).resolves.toEqual({ authenticated: false, authDisabled: true });
    const err = await loginWithPassword('http://hub', 'nope').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AuthError);
    expect((err as AuthError).code).toBe('invalid_credentials');
    expect((err as AuthError).status).toBe(401);
  });
});
