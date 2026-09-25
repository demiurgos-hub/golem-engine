import type { BoxGeometry } from 'three';

export const sharedAssetsKey = Symbol('signal-garden-assets');
export interface SharedAssets { actorGeometry: BoxGeometry }
