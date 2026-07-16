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
export type TiledMapSource = string | Uint8Array | object;

export interface TiledWorldData {
  /** URL emitted by a world source configured with url_prefix. */
  mapUrl?: string;
  /** Embedded Tiled JSON bytes emitted by a world source without url_prefix. */
  tileData?: Uint8Array;
}

export interface TiledTilesetAsset {
  /** Phaser texture key. Defaults to the Tiled tileset name. */
  key?: string;
  /** Image URL to load. Omit when the texture is already loaded. */
  url?: string;
}

export type TiledTilesetAssets = Record<
  string,
  string | TiledTilesetAsset
>;

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

export interface TiledWorldLayerOptions extends CreateTiledLayerOptions {
  /** Tiled tileset names used by this layer. Defaults to every mounted tileset. */
  tilesets?: string | string[];
}

export interface LoadTiledWorldOptions {
  /** Tiled tileset name to image URL or texture configuration. */
  tilesets: TiledTilesetAssets;
  /** Tile layer names to create. Defaults to every tile layer in the map. */
  layers?: string[];
  /** Default render mode for created layers. */
  mode?: TiledLayerRenderMode;
  /** Default explicit-GPU fallback policy for created layers. */
  fallback?: boolean;
  /** Per-layer tileset, render mode, position, and fallback overrides. */
  layerOptions?: Record<string, TiledWorldLayerOptions>;
  /** Destroy an earlier world mounted with the same scene and key. Defaults to true. */
  replace?: boolean;
}

export interface MountedTiledWorld {
  readonly key: string;
  readonly map: Phaser.Tilemaps.Tilemap;
  readonly layers: ReadonlyMap<string, TiledLayer>;
  readonly tilesets: ReadonlyMap<string, Phaser.Tilemaps.Tileset>;
  /** Regenerate the data textures of every mounted GPU tile layer. */
  refreshGpuLayers(): void;
  /** Destroy mounted layers and remove this map from the tilemap cache. */
  destroy(): void;
}

const mountedWorlds = new WeakMap<
  Phaser.Scene,
  Map<string, MountedTiledWorld>
>();

function layerLabel(layerID: number | string): string {
  return typeof layerID === "number" ? `#${layerID}` : `"${layerID}"`;
}

function isWebGLScene(scene: Phaser.Scene): boolean {
  return scene.sys.game.renderer.type === Phaser.WEBGL;
}

function tilesetTextureKey(
  name: string,
  asset: string | TiledTilesetAsset,
): string {
  return typeof asset === "string" ? name : (asset.key ?? name);
}

function tilesetImageUrl(
  asset: string | TiledTilesetAsset,
): string | undefined {
  return typeof asset === "string" ? asset : asset.url;
}

function decodeTiledMapSource(source: TiledMapSource): string | object {
  if (!(source instanceof Uint8Array)) {
    return source;
  }

  try {
    return JSON.parse(new TextDecoder().decode(source)) as object;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`golem-phaser: invalid embedded Tiled JSON: ${message}`);
  }
}

function resolveTiledWorldSource(
  source: TiledMapSource | TiledWorldData,
): TiledMapSource {
  if (
    typeof source === "string" ||
    source instanceof Uint8Array ||
    !("mapUrl" in source || "tileData" in source)
  ) {
    return source;
  }
  if (source.mapUrl) {
    return source.mapUrl;
  }
  if (source.tileData && source.tileData.byteLength > 0) {
    return source.tileData;
  }
  throw new Error(
    "golem-phaser: Tiled world data has neither mapUrl nor tileData",
  );
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
 * loadTiledMap loads Tiled JSON from a URL, parsed object, or embedded bytes,
 * loads its tileset images into a Phaser scene, then returns the Tilemap.
 *
 * Designed for use in a worldManager.onXxxUpdate callback, after the server
 * delivers a mapUrl:
 *
 * @example
 * client.world.onZoneUpdate(async (d) => {
 *   const map = await loadTiledMap(this, 'zone', d.mapUrl, {
 *     tiles: '/assets/tiles.png',
 *   });
 *   const tileset = map.addTilesetImage('tiles', 'tiles');
 *   createTiledLayer(map, 'Ground', tileset!, { mode: 'auto' });
 * });
 *
 * @param scene    - The Phaser scene to load into.
 * @param key      - Cache key for the tilemap.
 * @param source   - URL, parsed object, or UTF-8 JSON bytes for the Tiled map.
 * @param tilesets - Optional map of Tiled tileset names to image URLs or
 *                   texture configurations.
 */
export function loadTiledMap(
  scene: Phaser.Scene,
  key: string,
  source: TiledMapSource,
  tilesets?: TiledTilesetAssets,
): Promise<Phaser.Tilemaps.Tilemap> {
  return new Promise((resolve, reject) => {
    if (scene.cache.tilemap.has(key)) {
      resolve(scene.make.tilemap({ key }));
      return;
    }

    const decodedSource = decodeTiledMapSource(source);
    const pendingImages: Array<[string, string]> = [];
    const pendingTextureKeys = new Set<string>();

    if (tilesets) {
      for (const [name, asset] of Object.entries(tilesets)) {
        const textureKey = tilesetTextureKey(name, asset);
        const imageUrl = tilesetImageUrl(asset);
        if (
          !scene.textures.exists(textureKey) &&
          !pendingTextureKeys.has(textureKey)
        ) {
          if (!imageUrl) {
            reject(
              new Error(
                `golem-phaser: texture "${textureKey}" for tileset "${name}" is not loaded and has no URL`,
              ),
            );
            return;
          }
          pendingImages.push([textureKey, imageUrl]);
          pendingTextureKeys.add(textureKey);
        }
      }
    }

    scene.load.tilemapTiledJSON(key, decodedSource);
    for (const [textureKey, imageUrl] of pendingImages) {
      scene.load.image(textureKey, imageUrl);
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

/**
 * refreshGpuTiledLayer regenerates a Phaser 4 GPU layer's data texture after
 * tile edits. It returns false for normal CPU tile layers.
 */
export function refreshGpuTiledLayer(layer: TiledLayer): boolean {
  const gpuLayer = layer as Phaser.Tilemaps.TilemapGPULayer;
  if (typeof gpuLayer.generateLayerDataTexture !== "function") {
    return false;
  }
  gpuLayer.generateLayerDataTexture();
  return true;
}

/**
 * loadTiledWorld mounts a server-provided Tiled world in one call. It accepts
 * generated world data containing mapUrl or tileData, registers tilesets,
 * creates tile layers, and replaces an earlier mount with the same scene/key.
 *
 * @example
 * client.world.onZoneUpdate(async (data) => {
 *   const world = await loadTiledWorld(this, 'zone', data, {
 *     tilesets: { tiles: '/assets/tiles.png' },
 *   });
 *   world.refreshGpuLayers();
 * });
 */
export async function loadTiledWorld(
  scene: Phaser.Scene,
  key: string,
  source: TiledMapSource | TiledWorldData,
  options: LoadTiledWorldOptions,
): Promise<MountedTiledWorld> {
  let registry = mountedWorlds.get(scene);
  if (!registry) {
    registry = new Map();
    mountedWorlds.set(scene, registry);
  }

  const replace = options.replace ?? true;
  const previous = registry.get(key);
  if (previous) {
    if (!replace) {
      return previous;
    }
    previous.destroy();
  } else if (replace && scene.cache.tilemap.has(key)) {
    scene.cache.tilemap.remove(key);
  }

  const map = await loadTiledMap(
    scene,
    key,
    resolveTiledWorldSource(source),
    options.tilesets,
  );
  const mountedTilesets = new Map<string, Phaser.Tilemaps.Tileset>();
  const mountedLayers = new Map<string, TiledLayer>();

  try {
    for (const [name, asset] of Object.entries(options.tilesets)) {
      const textureKey = tilesetTextureKey(name, asset);
      const tileset = map.addTilesetImage(name, textureKey);
      if (!tileset) {
        throw new Error(
          `golem-phaser: failed to add Tiled tileset "${name}" using texture "${textureKey}"`,
        );
      }
      mountedTilesets.set(name, tileset);
    }

    const layerNames = options.layers ?? map.layers.map((layer) => layer.name);
    for (const layerName of layerNames) {
      const layerOptions = options.layerOptions?.[layerName];
      const requestedTilesets =
        layerOptions?.tilesets ?? Array.from(mountedTilesets.keys());
      const names =
        typeof requestedTilesets === "string"
          ? [requestedTilesets]
          : requestedTilesets;
      if (names.length === 0) {
        throw new Error(
          `golem-phaser: no tilesets configured for layer "${layerName}"`,
        );
      }

      const selectedTilesets = names.map((name) => {
        const tileset = mountedTilesets.get(name);
        if (!tileset) {
          throw new Error(
            `golem-phaser: unknown tileset "${name}" for layer "${layerName}"`,
          );
        }
        return tileset;
      });
      const tilesetArg =
        selectedTilesets.length === 1 ? selectedTilesets[0] : selectedTilesets;
      const layer = createTiledLayer(map, layerName, tilesetArg, {
        mode: layerOptions?.mode ?? options.mode ?? "auto",
        fallback: layerOptions?.fallback ?? options.fallback,
        x: layerOptions?.x,
        y: layerOptions?.y,
      });
      mountedLayers.set(layerName, layer);
    }
  } catch (error) {
    map.destroy();
    scene.cache.tilemap.remove(key);
    throw error;
  }

  let destroyed = false;
  let mounted!: MountedTiledWorld;
  const destroy = () => {
    if (destroyed) {
      return;
    }
    destroyed = true;
    scene.events.off(Phaser.Scenes.Events.SHUTDOWN, destroy);
    map.destroy();
    scene.cache.tilemap.remove(key);
    if (registry?.get(key) === mounted) {
      registry.delete(key);
    }
  };

  mounted = {
    key,
    map,
    layers: mountedLayers,
    tilesets: mountedTilesets,
    refreshGpuLayers() {
      for (const layer of mountedLayers.values()) {
        refreshGpuTiledLayer(layer);
      }
    },
    destroy,
  };
  registry.set(key, mounted);
  scene.events.once(Phaser.Scenes.Events.SHUTDOWN, destroy);
  return mounted;
}
