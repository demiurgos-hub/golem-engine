import { onDestroy } from 'svelte';
import { useTask, type Key, type ThrelteUseTaskOptions } from '@threlte/core';
import { Quaternion, Vector3, type Object3D } from 'three';
import type { EntityIdentity, EntityViewBinding } from './core.js';

export type EntityTaskOptions = ThrelteUseTaskOptions & { key?: Key };

/** Run presentation behavior on Threlte's scheduler, with seconds-based delta. */
export function useEntityTask<E extends EntityIdentity, Events extends object>(view: EntityViewBinding<E, Events>,
  callback: (entity: E, deltaSeconds: number) => void, options: EntityTaskOptions = {}) {
  const { key = Symbol('golem-entity-task'), running, ...taskOptions } = options;
  let active = true;
  const stop = () => { active = false; task.stop(); };
  const task = useTask(key, delta => {
    if (view.active) callback(view.current.entity, delta);
  }, { ...taskOptions, running: () => active && view.active && (running?.() ?? true) });
  view.signal.addEventListener('abort', stop, { once: true });
  if (!view.active) stop();
  onDestroy(() => { view.signal.removeEventListener('abort', stop); });
  return task;
}

export interface EntityTransform {
  position?: readonly [number, number, number];
  quaternion?: readonly [number, number, number, number];
  scale?: readonly [number, number, number];
}
export interface EntityTransformOptions<E> {
  target(): Object3D | undefined;
  select(entity: E): EntityTransform;
  interpolation?: false | { durationSeconds: number };
  external?: () => boolean;
  task?: EntityTaskOptions;
}

/** Apply explicitly mapped transforms without modifying synchronized fields. */
export function useEntityTransform<E extends EntityIdentity, Events extends object>(view: EntityViewBinding<E, Events>,
  options: EntityTransformOptions<E>) {
  const duration = options.interpolation ? options.interpolation.durationSeconds : 0;
  if (!Number.isFinite(duration) || duration < 0) throw new Error('golem-threlte: interpolation duration must be finite and non-negative');
  let reset = true;
  let lastTarget: Object3D | undefined;
  let lastVersion = -1;
  let wasExternal = false;
  let elapsed = 0;
  let selected: EntityTransform = {};
  const startPosition = new Vector3();
  const startScale = new Vector3();
  const startQuaternion = new Quaternion();
  const endPosition = new Vector3();
  const endScale = new Vector3();
  const endQuaternion = new Quaternion();
  useEntityTask(view, (entity, deltaSeconds) => {
    const external = options.external?.() ?? false;
    if (external) { wasExternal = true; reset = true; return; }
    const target = options.target();
    if (!target) { reset = true; return; }
    if (lastTarget !== target || wasExternal) reset = true;
    wasExternal = false;
    if (reset || lastVersion !== view.current.version) {
      const next = options.select(entity);
      // Only retarget changed coordinates; non-transform deltas must not restart interpolation.
      const changed = (a: readonly number[] | undefined, b: readonly number[] | undefined) =>
        a?.length !== b?.length || !!a?.some((v, i) => v !== b?.[i]);
      if (reset || changed(next.position, selected.position) || changed(next.scale, selected.scale) || changed(next.quaternion, selected.quaternion)) {
        selected = { position: next.position && [...next.position], scale: next.scale && [...next.scale], quaternion: next.quaternion && [...next.quaternion] };
        startPosition.copy(target.position); startScale.copy(target.scale); startQuaternion.copy(target.quaternion);
        if (selected.position) endPosition.fromArray(selected.position);
        if (selected.scale) endScale.fromArray(selected.scale);
        if (selected.quaternion) endQuaternion.fromArray(selected.quaternion).normalize();
        elapsed = reset ? duration : 0;
      }
      lastVersion = view.current.version; lastTarget = target; reset = false;
    }
    elapsed = Math.min(duration, elapsed + deltaSeconds);
    apply(target, duration === 0 ? 1 : Math.min(elapsed / duration, 1));
  }, options.task);
  function apply(target: Object3D, amount: number) {
    if (selected.position) target.position.lerpVectors(startPosition, endPosition, amount);
    if (selected.scale) target.scale.lerpVectors(startScale, endScale, amount);
    if (selected.quaternion) target.quaternion.slerpQuaternions(startQuaternion, endQuaternion, amount);
  }
  return { reset() { reset = true; } };
}
