import Phaser from "phaser";
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
 *   map.createLayer('Ground', tileset!);
 * };
 *
 * @param scene    - The Phaser scene to load into.
 * @param key      - Cache key for the tilemap.
 * @param url      - URL of the .tmj / Tiled JSON file.
 * @param tilesets - Optional map of tileset name → image URL for any tileset
 *                   images that are not already loaded. Keys match the Name
 *                   field set in Tiled for each tileset.
 */
export declare function loadTiledMap(scene: Phaser.Scene, key: string, url: string, tilesets?: Record<string, string>): Promise<Phaser.Tilemaps.Tilemap>;
