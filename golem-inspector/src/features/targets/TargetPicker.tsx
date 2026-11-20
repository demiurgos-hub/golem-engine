import { useMemo, useState } from 'react';
import { filterTargets, groupTargets, labelKeys, targetName, type InspectorTarget } from '../../api/targets';
import { useInspector } from '../../hooks/useInspector';

const GROUP_KEY_STORAGE = 'golem-inspector.groupBy';

function loadGroupKey(): string {
  try {
    return localStorage.getItem(GROUP_KEY_STORAGE) ?? '';
  } catch {
    return '';
  }
}

function saveGroupKey(key: string) {
  try {
    localStorage.setItem(GROUP_KEY_STORAGE, key);
  } catch {
    /* storage unavailable */
  }
}

function optionLabel(t: InspectorTarget): string {
  const name = targetName(t);
  const status = !t.connected ? 'offline' : t.observeState === 'ready' ? `${t.entityCount} entities` : t.observeState;
  const viewers = t.viewerCount > 0 ? ` · ${t.viewerCount} viewing` : '';
  return `${name}${name !== t.targetId ? ` (${t.targetId})` : ''} · ${status}${viewers}`;
}

export function TargetPicker() {
  const { targets, targetsLoading, targetsError, selectedTargetId, selectTarget } = useInspector();
  const [query, setQuery] = useState('');
  const [groupKey, setGroupKey] = useState(loadGroupKey);

  const keys = useMemo(() => labelKeys(targets), [targets]);
  const activeKey = keys.includes(groupKey) ? groupKey : '';
  const visible = useMemo(() => {
    const filtered = filterTargets(targets, query);
    // Keep the selected target selectable even when the search hides it.
    const selected = targets.find((t) => t.targetId === selectedTargetId);
    if (selected && !filtered.includes(selected)) filtered.push(selected);
    return groupTargets(filtered, activeKey || null);
  }, [targets, query, activeKey, selectedTargetId]);
  const shown = visible.reduce((n, g) => n + g.targets.length, 0);

  return (
    <div className="target-picker">
      <span>
        Target{' '}
        <span className="target-picker__count">
          {targets.length === 0 ? '' : shown === targets.length ? `${targets.length}` : `${shown} of ${targets.length}`}
        </span>
      </span>
      <div className="target-picker__controls">
        <input
          type="search"
          className="target-picker__search"
          placeholder="Search targets or labels…"
          aria-label="Search targets"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        {keys.length > 0 ? (
          <select
            className="target-picker__group"
            aria-label="Group targets by label"
            value={activeKey}
            onChange={(e) => {
              setGroupKey(e.target.value);
              saveGroupKey(e.target.value);
            }}
          >
            <option value="">No grouping</option>
            {keys.map((k) => (
              <option key={k} value={k}>
                By {k}
              </option>
            ))}
          </select>
        ) : null}
      </div>
      <select
        aria-label="Inspector target"
        value={selectedTargetId ?? ''}
        disabled={targetsLoading && targets.length === 0}
        onChange={(e) => {
          if (e.target.value) selectTarget(e.target.value);
        }}
      >
        <option value="" disabled>
          {targetsLoading ? 'Loading targets…' : targets.length === 0 ? 'No servers connected' : 'Select a server…'}
        </option>
        {visible.map((group) =>
          activeKey ? (
            <optgroup key={group.key} label={`${activeKey}: ${group.key}`}>
              {group.targets.map((t) => (
                <option key={t.targetId} value={t.targetId}>
                  {optionLabel(t)}
                </option>
              ))}
            </optgroup>
          ) : (
            group.targets.map((t) => (
              <option key={t.targetId} value={t.targetId}>
                {optionLabel(t)}
              </option>
            ))
          ),
        )}
      </select>
      {targetsError ? (
        <span className="target-picker__error" title={targetsError}>
          target list unavailable
        </span>
      ) : null}
    </div>
  );
}
