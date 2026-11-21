import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { chooseTargetId, fetchTargets, targetsUrlFromWs, updateTargetSearch, type InspectorTarget } from '../api/targets';
import { createWsClient, DEFAULT_WS_URL, type WsClient } from '../api/wsClient';
import { defaultPlane, type Plane } from '../lib/entityTypes';
import { EMPTY_FILTERS, filterEntities, pruneFilterSpaces, spacesOf, type ClientFilters } from '../lib/filters';
import { createConnectionStore, type ConnectionState, type ConnectionStore } from '../store/connectionStore';
import { createDebugLog, type DebugLog } from '../store/debugLog';
import { createEntityStore, type EntityRecord, type EntityStore, type EntityStoreSnapshot } from '../store/entityStore';

interface InspectorContextValue {
  entityStore: EntityStore;
  connectionStore: ConnectionStore;
  debugLog: DebugLog;
  client: WsClient;
  /** The filters as the user set them; reset when the target changes. */
  filters: ClientFilters;
  /** `filters` with absent spaces dropped: what the list, map and filter bar apply. */
  effectiveFilters: ClientFilters;
  setFilters: (next: ClientFilters) => void;
  selectedId: number | null;
  selectEntity: (id: number | null) => void;
  targets: InspectorTarget[];
  targetsLoading: boolean;
  targetsError: string | null;
  selectedTargetId: string | null;
  selectedTarget: InspectorTarget | null;
  selectTarget: (targetId: string) => void;
  filteredEntities: EntityRecord[];
  /** Distinct spaces of all entities, ascending; space UI shows only when there are several. */
  spaces: number[];
  plane: Plane;
  setPlane: (plane: Plane) => void;
}

const InspectorContext = createContext<InspectorContextValue | null>(null);

function replaceSearch(targetId: string | null) {
  const search = updateTargetSearch(window.location.search, targetId);
  window.history.replaceState(null, '', `${window.location.pathname}${search}${window.location.hash}`);
}

export function InspectorProvider({ children }: { children: ReactNode }) {
  const requestedRef = useRef(new URLSearchParams(window.location.search).get('targetId'));
  const entityStore = useMemo(() => {
    const store = createEntityStore();
    if (requestedRef.current) store.setTarget(requestedRef.current);
    return store;
  }, []);
  const connectionStore = useMemo(() => createConnectionStore(DEFAULT_WS_URL), []);
  const debugLog = useMemo(() => createDebugLog(), []);

  const [filters, setFilters] = useState<ClientFilters>(EMPTY_FILTERS);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(requestedRef.current);
  const selectedTargetRef = useRef(selectedTargetId);
  selectedTargetRef.current = selectedTargetId;
  const [targets, setTargets] = useState<InspectorTarget[]>([]);
  const [targetsLoading, setTargetsLoading] = useState(true);
  const [targetsError, setTargetsError] = useState<string | null>(null);
  const [planeOverride, setPlaneOverride] = useState<Plane | null>(null);

  const client = useMemo(
    () => createWsClient({ entityStore, connectionStore, debugLog, getTargetId: () => selectedTargetRef.current }),
    [entityStore, connectionStore, debugLog],
  );

  useEffect(() => {
    client.start();
    return () => client.stop();
  }, [client]);

  const selectTarget = useCallback(
    (targetId: string) => {
      if (selectedTargetRef.current === targetId) return;
      selectedTargetRef.current = targetId;
      setSelectedTargetId(targetId);
      setSelectedId(null);
      setPlaneOverride(null);
      // Type names come from the target's schema and spaces from its
      // entities, so no filter carries over to another target.
      setFilters(EMPTY_FILTERS);
      entityStore.setTarget(targetId);
      replaceSearch(targetId);
      if (client.isOpen()) client.subscribe(targetId);
    },
    [client, entityStore],
  );

  useEffect(() => {
    const controller = new AbortController();
    const url = targetsUrlFromWs(DEFAULT_WS_URL);
    const refresh = async () => {
      try {
        const next = await fetchTargets(url, controller.signal);
        setTargets(next);
        setTargetsError(null);
        const chosen = chooseTargetId(next, requestedRef.current, selectedTargetRef.current);
        requestedRef.current = null;
        if (chosen && chosen !== selectedTargetRef.current) {
          selectTarget(chosen);
        } else if (!chosen && selectedTargetRef.current !== null) {
          // The target went away; keep showing nothing rather than stale data.
          selectedTargetRef.current = null;
          setSelectedTargetId(null);
          setSelectedId(null);
          setFilters(EMPTY_FILTERS);
          entityStore.setTarget(null);
          replaceSearch(null);
        }
      } catch (err) {
        if (!controller.signal.aborted) setTargetsError(err instanceof Error ? err.message : 'Target registry failed');
      } finally {
        if (!controller.signal.aborted) setTargetsLoading(false);
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2_000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [entityStore, selectTarget]);

  const snap = useSyncExternalStore(entityStore.subscribe, entityStore.getState, entityStore.getState);
  const spacesKey = useMemo(() => spacesOf(snap.entities.values()).join(','), [snap.entities]);
  // Keyed by content so the array keeps its identity across batches.
  const spaces = useMemo(() => (spacesKey === '' ? [] : spacesKey.split(',').map(Number)), [spacesKey]);
  // Derived, never written back: a space that empties (or a brief empty
  // entity set during resync) stops filtering without erasing the choice.
  const effectiveFilters = useMemo(() => pruneFilterSpaces(filters, spaces), [filters, spaces]);
  const filteredEntities = useMemo(() => filterEntities(snap.entities.values(), effectiveFilters), [snap.entities, effectiveFilters]);

  useEffect(() => {
    if (selectedId !== null && !snap.entities.has(selectedId)) setSelectedId(null);
  }, [selectedId, snap.entities]);

  const plane = planeOverride ?? defaultPlane(snap.schema);

  const value = useMemo<InspectorContextValue>(
    () => ({
      entityStore,
      connectionStore,
      debugLog,
      client,
      filters,
      effectiveFilters,
      setFilters,
      selectedId,
      selectEntity: setSelectedId,
      targets,
      targetsLoading,
      targetsError,
      selectedTargetId,
      selectedTarget: targets.find((t) => t.targetId === selectedTargetId) ?? null,
      selectTarget,
      filteredEntities,
      spaces,
      plane,
      setPlane: setPlaneOverride,
    }),
    [entityStore, connectionStore, debugLog, client, filters, effectiveFilters, selectedId, targets, targetsLoading, targetsError, selectedTargetId, selectTarget, filteredEntities, spaces, plane],
  );

  return <InspectorContext.Provider value={value}>{children}</InspectorContext.Provider>;
}

export function useInspector(): InspectorContextValue {
  const ctx = useContext(InspectorContext);
  if (!ctx) throw new Error('useInspector must be used within InspectorProvider');
  return ctx;
}

export function useEntityStore(): EntityStoreSnapshot {
  const { entityStore } = useInspector();
  return useSyncExternalStore(entityStore.subscribe, entityStore.getState, entityStore.getState);
}

export function useConnectionStore(): ConnectionState {
  const { connectionStore } = useInspector();
  return useSyncExternalStore(connectionStore.subscribe, connectionStore.getState, connectionStore.getState);
}

export function useDebugLog() {
  const { debugLog } = useInspector();
  return useSyncExternalStore(debugLog.subscribe, debugLog.getState, debugLog.getState);
}

export function useSelectedEntity(): EntityRecord | null {
  const { selectedId } = useInspector();
  const snap = useEntityStore();
  return selectedId === null ? null : (snap.entities.get(selectedId) ?? null);
}
