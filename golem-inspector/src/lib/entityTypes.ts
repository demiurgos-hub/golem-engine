import type { Schema } from '../protocol/messages';

export type EntityShape = 'circle' | 'square' | 'diamond' | 'triangle' | 'cross';

const SHAPES: EntityShape[] = ['circle', 'square', 'diamond', 'triangle', 'cross'];

/** FNV-1a: a stable hash so a type keeps its color across sessions and games. */
export function hashString(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/** Color derived from the type name, spread around the hue circle. */
export function typeColor(typeName: string): string {
  const h = hashString(typeName);
  return `hsl(${h % 360} ${58 + (h >> 9) % 14}% ${56 + (h >> 17) % 10}%)`;
}

/** Shape cycles through the schema's entity order so neighbors differ. */
export function typeShape(schema: Schema | null, typeName: string): EntityShape {
  const index = schema?.entities.findIndex((e) => e.name === typeName) ?? -1;
  return SHAPES[(index >= 0 ? index : hashString(typeName)) % SHAPES.length]!;
}

export interface LegendEntry {
  name: string;
  color: string;
  shape: EntityShape;
}

export function legend(schema: Schema | null): LegendEntry[] {
  return (schema?.entities ?? []).map((e) => ({ name: e.name, color: typeColor(e.name), shape: typeShape(schema, e.name) }));
}

/** Map plane: which entity axes become screen X and screen Y. */
export type Plane = 'xz' | 'xy';

/** 3D games are viewed top-down on X/Z; 2D games on X/Y. */
export function defaultPlane(schema: Schema | null): Plane {
  return schema?.dimensions === 3 ? 'xz' : 'xy';
}
