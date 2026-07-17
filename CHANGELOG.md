# Changelog

All notable user-facing changes to Golem Engine are documented here.

This project follows the changelog categories from Keep a Changelog: Added, Changed, Deprecated, Removed, Fixed, and Security.

## Unreleased

### Added

- `golem-phaser` now includes a `createTiledLayer` helper for creating Phaser 4 GPU tilemap layers with CPU fallback for unsupported maps.
- `golem-phaser` includes `loadTiledWorld` for mounting generated `mapUrl` or embedded `tileData` world updates, loading tilesets, creating layers with automatic GPU selection, replacing prior zone mounts, and refreshing edited GPU layers.
- `golem-phaser` includes a global `GolemPlugin` that owns one persistent generated client, reconnects unexpected drops, and exposes typed connection status subscriptions for game-owned UI.
- The Phaser integration generates a type-safe entity-view registry with required `prefab`, `sprite`, `gpu`, or explicit `headless` choices for every schema entity.
- Scene-local entity-view mounts backfill existing synchronized entities, support multiple simultaneous scene presentations, and tear down views without disconnecting or deleting client state.
- Prefab views support arbitrary typed Phaser GameObjects, including Phaser Editor-generated classes and Containers.
- Sprite views support declarative frame/field synchronization, render-frame position interpolation, and an `externalPosition` predicate for client-owned prediction.
- `golem-phaser` includes an opt-in `SpriteGpuEntityPool` for rendering high-count replicated entities through Phaser 4 `SpriteGPULayer` with stable, reusable member slots.
- Generated JavaScript entity, world, and event listeners support multiple subscribers and return unsubscribe functions; entity-targeted events expose typed `(entity, event)` handlers.
- Generated JavaScript entity managers include typed per-entity getters such as `getPlayer(entityId)`.

### Changed

- **Breaking:** `golem-phaser` now targets Phaser 4 and requires `phaser >=4.0.0`. Phaser 3 projects must upgrade their Phaser dependency before using the package.
- **Breaking:** Phaser clients now register `GolemPlugin` globally with the `golem` scene mapping instead of extending `GameScene`; the connection persists across scene transitions.
- **Breaking:** The `phaser` codegen integration now emits one `GolemPhaser.ts` registry module instead of per-entity `*Bridge.ts` subclasses. Re-run `golem-bake` and remove old bridge files from consumer projects.
- **Breaking:** Phaser entity presentation now uses generated `defineEntityViews` registries and scene mounts instead of `createSpriteView`, `createGpuEntityView`, and generated-manager constructor registration.
- **Breaking:** Generated JavaScript listener registration methods now add listeners rather than replacing a single callback and return an unsubscribe function.
- Entity lifecycle hooks and custom generated-manager subclasses remain available for advanced model logic, but Phaser presentation is kept separate from synchronized entities.
- Website Phaser documentation needs a matching update for the plugin and entity-view registry workflow.

### Deprecated

### Removed

- `GameScene`, `createSpriteView`, `createGpuEntityView`, Phaser bridge interfaces, and generated Phaser `*Bridge.ts` files.

### Fixed

- Generated JavaScript `Client` types expose configured world managers directly, so typed world callbacks no longer require optional-manager checks or casts.

### Security

## [0.2.1] - 2026-07-07

### Added

- Repository `README.md` with an overview, feature list, layout, quick start, client package pointers, and links to the documentation site.
- `CHANGELOG.md` to track user-facing changes across releases.

### Changed

- **Breaking:** Go module path renamed from `golem-engine` to `github.com/demiurgos-hub/golem-engine`. Update `go.mod` `require` directives and Go import paths in consumer projects, then re-run `golem-bake` so generated Go server, Go client, and Ebiten code uses the new default import paths.
- **Breaking:** Collision and navigation nested modules renamed from `golem.collision` and `golem.nav` to `github.com/demiurgos-hub/golem-engine/golem/collision` and `github.com/demiurgos-hub/golem-engine/golem/nav`. Projects that import these modules directly must update paths; workspace `replace` directives must use the new module paths.
- Unity editor default `golem-bake` command is now `go run github.com/demiurgos-hub/golem-engine/cmd/golem-bake`.
