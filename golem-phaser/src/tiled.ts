import Phaser from "phaser";

const gpuTileLimit = 4096;

type TiledLayerTileset = string | Phaser.Tilemaps.Tileset;

export type TiledLayerTilesets =
  | TiledLayerTileset
  | string[]
  | Phaser.Tilemaps.Tileset[];
export type TiledLayerRenderMode = "cpu" | "gpu" | "auto";
export type TiledLayer =
  | Phaser.Tilemaps.TilemapLayer
  | Phaser.Tilemaps.TilemapGPULayer;

export interface CreateTiledLayerOptions {
  /** Choose normal CPU rendering, Phaser 4 GPU rendering, or automatic GPU use. */
  mode?: TiledLayerRenderMode;
  /** Optional layer x position. Defaults to the Tiled layer offset. */
  x?: number;
  /** Optional layer y position. Defaults to the Tiled layer offset. */
  y?: number;
  /** When false, explicit GPU mode throws instead of falling back to CPU. */
  fallback?: boolean;
}

function layerLabel(layerID: number | string): string {
  return typeof layerID === "number" ? `#${layerID}` : `"${layerID}"`;
}

function isWebGLScene(scene: Phaser.Scene): boolean {
  return scene.sys.game.renderer.type === Phaser.WEBGL;
}

function gpuCompatibilityReasons(
  map: Phaser.Tilemaps.Tilemap,
  layerID: number | string,
  tileset: TiledLayerTilesets,
): string[] {
  const reasons: string[] = [];

  if (!isWebGLScene(map.scene)) {
    reasons.push("renderer is not WebGL");
  }

  const layer = map.getLayer(layerID);
  if (!layer) {
    reasons.push(`layer ${layerLabel(layerID)} was not found`);
    return reasons;
  }

  if (layer.orientation !== Phaser.Tilemaps.Orientation.ORTHOGONAL) {
    reasons.push("layer orientation is not orthogonal");
  }

  if (layer.width > gpuTileLimit || layer.height > gpuTileLimit) {
    reasons.push(
      `layer is ${layer.width}x${layer.height} tiles; maximum is ${gpuTileLimit}x${gpuTileLimit}`,
    );
  }

  if (Array.isArray(tileset) && tileset.length !== 1) {
    reasons.push("TilemapGPULayer requires exactly one tileset");
  }

  return reasons;
}

/**
 * loadTiledMap loads a Tiled JSON map file and its tileset images into a
 * Phaser scene, then creates and returns the resulting Tilemap.
 *
 * Designed for use in a worldManager.onXxxUpdate callback, after the server
 * delivers a mapUrl:
 *
 * @example
 * client.world.onZoneUpdate = async (d) => {
 *   const map = await loadTiledMap(this, 'zone', d.mapUrl, {
 *     tiles: '/assets/tiles.png',
 *   });
 *   const tileset = map.addTilesetImage('tiles', 'tiles');
 *   createTiledLayer(map, 'Ground', tileset!, { mode: 'auto' });
 * };
 *
 * @param scene    - The Phaser scene to load into.
 * @param key      - Cache key for the tilemap.
 * @param url      - URL of the .tmj / Tiled JSON file.
 * @param tilesets - Optional map of tileset name → image URL for any tileset
 *                   images that are not already loaded. Keys match the Name
 *                   field set in Tiled for each tileset.
 */
export function loadTiledMap(
  scene: Phaser.Scene,
  key: string,
  url: string,
  tilesets?: Record<string, string>,
): Promise<Phaser.Tilemaps.Tilemap> {
  return new Promise((resolve, reject) => {
    if (scene.cache.tilemap.has(key)) {
      resolve(scene.make.tilemap({ key }));
      return;
    }

    scene.load.tilemapTiledJSON(key, url);

    if (tilesets) {
      for (const [name, imageUrl] of Object.entries(tilesets)) {
        if (!scene.textures.exists(name)) {
          scene.load.image(name, imageUrl);
        }
      }
    }

    scene.load.once(Phaser.Loader.Events.COMPLETE, () => {
      if (scene.cache.tilemap.has(key)) {
        resolve(scene.make.tilemap({ key }));
      } else {
        reject(new Error(`golem-phaser: tilemap "${key}" not found after load`));
      }
    });

    scene.load.once(
      Phaser.Loader.Events.FILE_LOAD_ERROR,
      (file: Phaser.Loader.File) => {
        reject(new Error(`golem-phaser: failed to load "${file.src}"`));
      },
    );

    if (!scene.load.isLoading()) {
      scene.load.start();
    }
  });
}

/**
 * createTiledLayer creates a tilemap layer with optional Phaser 4
 * TilemapGPULayer rendering. GPU layers are best for large, mostly static,
 * orthogonal maps that use a single tileset. If you edit tiles after creating
 * a GPU layer, call generateLayerDataTexture() on the returned layer.
 *
 * @example
 * const map = await loadTiledMap(this, 'zone', d.mapUrl, {
 *   tiles: '/assets/tiles.png',
 * });
 * const tileset = map.addTilesetImage('tiles', 'tiles')!;
 * const ground = createTiledLayer(map, 'Ground', tileset, { mode: 'auto' });
 */
export function createTiledLayer(
  map: Phaser.Tilemaps.Tilemap,
  layerID: number | string,
  tileset: TiledLayerTilesets,
  options: CreateTiledLayerOptions = {},
): TiledLayer {
  const mode = options.mode ?? "cpu";
  let useGpu = false;

  if (mode === "gpu" || mode === "auto") {
    const reasons = gpuCompatibilityReasons(map, layerID, tileset);
    useGpu = reasons.length === 0;

    if (!useGpu && mode === "gpu") {
      const message = `golem-phaser: cannot create TilemapGPULayer for layer ${layerLabel(layerID)}: ${reasons.join("; ")}`;
      if (options.fallback === false) {
        throw new Error(message);
      }
      console.warn(`${message}; falling back to TilemapLayer`);
    }
  }

  const layer = map.createLayer(layerID, tileset, options.x, options.y, useGpu) as
    | TiledLayer
    | null;

  if (!layer) {
    throw new Error(`golem-phaser: failed to create tilemap layer ${layerLabel(layerID)}`);
  }

  return layer;
}
