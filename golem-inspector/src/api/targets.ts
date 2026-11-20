/** One entry of GET /api/targets (hub.TargetInfo). */
export interface InspectorTarget {
  targetId: string;
  labels: Record<string, string>;
  engineVersion?: string;
  schemaFingerprint?: string;
  entityTypes: string[];
  connected: boolean;
  observeState: 'idle' | 'starting' | 'ready' | string;
  ready: boolean;
  entityCount: number;
  viewerCount: number;
  firstRegisteredAt: string | null;
  registeredAt: string | null;
  connectedAt: string | null;
  disconnectedAt: string | null;
  lastMessageAt: string | null;
  snapshotStartedAt: string | null;
  lastSnapshotCompletedAt: string | null;
  lastSnapshotDurationMs: number | null;
  lastSnapshotEntityCount: number | null;
  lastSnapshotError?: string;
  lastError?: string;
}

/** Resolves the REST endpoint from the browser WebSocket endpoint. */
export function targetsUrlFromWs(wsUrl: string, baseUrl = window.location.href): string {
  const url = new URL(wsUrl, baseUrl);
  url.protocol = url.protocol === 'wss:' || url.protocol === 'https:' ? 'https:' : 'http:';
  url.pathname = '/api/targets';
  url.search = '';
  url.hash = '';
  return url.toString();
}

/** Returns a search string with the selected target persisted. */
export function updateTargetSearch(search: string, targetId: string | null): string {
  const params = new URLSearchParams(search);
  if (targetId) params.set('targetId', targetId);
  else params.delete('targetId');
  const value = params.toString();
  return value ? `?${value}` : '';
}

/** Keeps a valid current/requested target, or auto-picks the only target. */
export function chooseTargetId(targets: InspectorTarget[], requested: string | null, current: string | null): string | null {
  const ids = new Set(targets.map((t) => t.targetId));
  if (current && ids.has(current)) return current;
  if (requested && ids.has(requested)) return requested;
  return targets.length === 1 ? targets[0]!.targetId : null;
}

/** Display name: the "name" label when set, else the target id. */
export function targetName(t: InspectorTarget): string {
  return t.labels.name || t.labels.label || t.targetId;
}

/** Filters targets by a free-text query over id, labels and status. */
export function filterTargets(targets: InspectorTarget[], query: string): InspectorTarget[] {
  const q = query.trim().toLowerCase();
  if (!q) return targets;
  return targets.filter((t) => {
    const hay = [t.targetId, ...Object.entries(t.labels).map(([k, v]) => `${k}=${v} ${v}`), t.connected ? 'online' : 'offline', t.observeState].join(' ').toLowerCase();
    return q.split(/\s+/).every((part) => hay.includes(part));
  });
}

/** Label keys present on any target, sorted, for the group-by selector. */
export function labelKeys(targets: InspectorTarget[]): string[] {
  const keys = new Set<string>();
  for (const t of targets) for (const k of Object.keys(t.labels)) keys.add(k);
  return [...keys].sort();
}

export interface TargetGroup {
  key: string;
  targets: InspectorTarget[];
}

/** Groups targets by a label value; targets without it go last under "—". */
export function groupTargets(targets: InspectorTarget[], labelKey: string | null): TargetGroup[] {
  const sorted = [...targets].sort((a, b) => targetName(a).localeCompare(targetName(b)) || a.targetId.localeCompare(b.targetId));
  if (!labelKey) return [{ key: '', targets: sorted }];
  const groups = new Map<string, InspectorTarget[]>();
  for (const t of sorted) {
    const key = t.labels[labelKey] ?? '';
    const list = groups.get(key) ?? [];
    list.push(t);
    groups.set(key, list);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
    .map(([key, list]) => ({ key: key || '—', targets: list }));
}

export async function fetchTargets(url: string, signal?: AbortSignal): Promise<InspectorTarget[]> {
  const response = await fetch(url, { signal, credentials: 'include' });
  if (!response.ok) throw new Error(`Target registry request failed (${response.status})`);
  const body = (await response.json()) as { targets: InspectorTarget[] };
  return body.targets;
}
