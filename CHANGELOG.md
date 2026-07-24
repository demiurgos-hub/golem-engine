# Changelog

All notable user-facing changes to Golem Engine are documented here.

This project follows the changelog categories from Keep a Changelog: Added, Changed, Deprecated, Removed, Fixed, and Security.

## Unreleased

### Added

- Golem Scribe (Unity) Phase 1: author entity schemas from prefabs with `[GolemEntity]` / `[GolemVar]` / `GolemSync`, export deterministic YAML under `entity_schema`, upsert the prefab registry, and track ownership in `scribe.golem.yaml` so handwritten files are never overwritten or deleted.
- Golem Scribe schedules coalesced, reentrancy-safe exports from asset changes (`Golem/Scribe/Export All`), and can auto-run `golem-bake` when entity or catalog type/world schema bytes change (`Auto Bake On Export` in Project Settings > Golem).
- Golem Scribe (Unity) Phase 2: author ScriptableObject catalogs with `[GolemCatalog]` / `[GolemField]` / `[GolemAssetRef]`, exporting a custom type schema, catalog world schema, and project-root `catalogs/{name}.golem.yaml` data file (sequence of maps, snake_case keys, GUID asset refs). Custom-type tags are direct protobuf field numbers (no entity offset). Catalog class deletion removes all three managed artifacts via the Scribe manifest; data-only edits do not trigger auto-bake.
- Catalog server boundary: `golem-bake` generates `Load{Name}Data`; application code must still load the catalog and store/publish the resulting world data explicitly.
- `golem/footprint` loads versioned `footprints.golem.yaml` (GUID identity, optional unique alias, diagnostic name/path) and places exact 2D/3D collision shapes on a collision backend with translation, positive uniform scale, and quarter-turn rotation only. Synthetic negative collision IDs are collision-only and may appear in contact callbacks without a registry entity.
- Website docs need a future “Author in Unity with Golem Scribe” guide covering source-of-truth rules, ignored prefab field values, managed artifact ownership, catalog loading/publishing, and footprint transform limits.
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

- Golem Scribe catalog export preserves previously committed type/world/data artifacts when a catalog class is temporarily invalid (errors are reported; bake is skipped only for that exporter); only truly removed or renamed catalog type names orphan-delete their managed files. Invalid catalog rows no longer reserve keys that would block a later valid asset with the same key.
- Golem Scribe auto-bake is decoupled per exporter: catalog collect/reconcile errors no longer suppress a required entity-schema bake, and entity errors no longer suppress a required valid catalog-schema bake.
- Golem Scribe YAML scalars now quote/escape newlines, carriage returns, tabs, and other C0 control characters.
- Golem Scribe now rejects absolute/`..` artifact paths, rolls back partial artifact mutations, keeps registry removals pending when registry updates fail, continues orphan cleanup when unrelated prefabs are invalid, and preserves external asset notifications while an export is running.
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
