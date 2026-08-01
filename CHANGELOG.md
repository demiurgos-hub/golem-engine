# Changelog

All notable user-facing changes to Golem Engine are documented here.

This project follows the changelog categories from Keep a Changelog: Added, Changed, Deprecated, Removed, Fixed, and Security.

## Unreleased

### Added

- Named visibility groups gate entity replication in interest and broadcast modes: `Server.JoinVisibilityGroup` / `LeaveVisibilityGroup` / `SetEntityVisibilityGroup` (empty group = public). Grouped entities require membership (and FOI in interest mode); grouped globals bypass distance but still require membership. `SessionsKnowing` reflects actual replication known state, not raw membership. Broadcast mode maintains per-session known sets (connect snapshot seeds after successful delivery; blind `BroadcastBatch` remains a fast path when safe). Visibility policy is point-in-time: APIs never wait on network I/O, and a mutation after a replication/snapshot decision takes effect on the next pass via known-set enter/exit (already selected/queued frames are not retroactively revoked). Website docs need a matching note on visibility groups.
- Cross-client realtime bootstrap helpers consume JSON from `Server.RealtimeConfigHandler` (`transport`, `url`, optional hex `serverCertificateHashes`, optional `eventualAckIntervalMs`): JS `fetchRealtimeConfig` / `connectOptionsFromRealtimeConfig` / `withQueryParam` / `withQuery` (optional `signal` overrides `init.signal`; Phaser prefetch/cache into synchronous `connectionOptions`); Unity `GolemRealtimeBootstrap` / `GolemConnectOptions` / `GameClient.Connect(GolemConnectOptions)`; Go `golem-go-client` remains the native reference. Fetch validates transport/url; `eventualAckIntervalMs` absent/zero keeps transport defaults, while negatives, fractions, non-finite values, and values outside signed int32 are rejected. Query helpers preserve existing params (Unity append-only, no UriBuilder rebuild), append encoded values (duplicates allowed), deep-copy hash bytes, and propagate ACK intervals. JS/Unity bound response reads in-stream (error ≤512 B, config ≤64 KiB). Go sanitizes `*url.Error` so endpoint query/fragment tokens are not leaked. Website docs need matching JS/Go/Unity client bootstrap notes.
- Unity generated `ClientFactory` is options-aware (`Func<GolemConnectOptions, IGolemTransport>`). `CreateClient(Func<IGolemTransport>)` / parameterless `GameClient` factories are obsolete: `Connect(string)` still works, but `Connect(GolemConnectOptions)` with a non-empty transport fails clearly instead of ignoring selection. Built-in `GolemWebTransportTransport.FromConnectOptions` rejects unsupported browser-style certificate hashes; custom options-aware factories own certificate policy (e.g. `AllowUntrustedCertificates`). Re-run `golem-bake` for C# clients.
- `golem/auth` provides `UpgradeHandler` / `UpgradeOptions` for token-in-query-param authorization on `Server.OnUpgrade` (default param `token`). Games supply `Validate`; missing/blank tokens are rejected without calling it, validator data becomes `Session.Data`, and validator errors keep `errors.Is`. This is not an account/JWT framework. Pair with client `WithQueryParam` / realtime bootstrap helpers. Website docs may need a short note on upgrade auth.
- Generated Go server `Runtime.LoadSnapshotIfExists` loads a schema-checked snapshot when the file is present (`false, nil` if missing) and preserves `snapshot.ErrFingerprintMismatch` for `errors.Is`. `Runtime.RunSnapshotAutosave` is a blocking, caller-owned loop that validates a positive interval, serializes each asynchronous `SaveSnapshot`, returns save errors, and exits with `ctx.Err()` on cancel after awaiting any in-flight save — if that in-flight save fails, the save error is returned instead of `ctx.Err()`. Re-run `golem-bake`. Website docs may need a short note on load-if-present and caller-owned autosave.
- Generic `OverlapOfType[T](s, entityIDs)` resolves overlap (or any) entity ID lists through `Server.Get`, keeps entries that satisfy `T` in input order, skips missing IDs, and returns nil for empty input or no matches. Compose with `OverlapCircle` / `OverlapBox` / `OverlapSphere` / `OverlapBox3D` (for example `OverlapOfType[*Mob](s, s.OverlapCircle(...))`). A nil `*Server` always panics with `golem: OverlapOfType: Server must be non-nil` (including empty ID lists). Website docs may need a short note on typed overlap helpers.
- Schema-declared colliders and named collision matrices: optional entity `collider` (`aabb`/`circle` in 2D, `aabb3d`/`sphere` in 3D, plus `layer` and optional `trigger`) and optional top-level `collision` in `golem.yaml` (`layers` ≤ 32, symmetric `collides` pairs including self-pairs like `[Player, Player]`; duplicate unordered pairs rejected). Generated Go server `Synced*` types emit `Collider()` or `Collider3D()`; generated `Runtime` exposes `Layers`+`EnableCollision` or `Layers3D`+`EnableCollision3D` (nil backend panics before installing helpers). `CreateEntity` / snapshot restore register matching providers after successful insertion and before `OnSpawn`; `DeleteEntity` removes shapes before `OnRemove`. Call `EnableCollision` / `EnableCollision3D` before spawning or loading snapshots of collidable entities. 2D/3D parity here is named layers plus automatic lifecycle — not per-entity contact-event dispatch (still 2D-only, pre-existing). Collider metadata is bake/runtime only and does not change the schema fingerprint or wire format. Re-run `golem-bake`. Website docs need schema/`golem.yaml` and physics collider updates.
- `CollisionLayers3D` / `NewCollisionLayers3D` provide the 3D named-layer equivalent of `CollisionLayers` (`Bind`, `Define`, `SetCollides`, `Add`, `Set`, `Remove`, `Layer`, `Mask`, `MaskFor`) around `collision3d.Backend`.
- `Server.SpawnAvatar`, `Server.Avatar`, and generic `AvatarOf[T]` bind a session to its player entity with optional FOI (`AvatarOptions`), concurrency-safe session↔entity indexes, and automatic disconnect cleanup (`RemoveFOI`, reap any still-bound avatar — including replacements spawned in `OnDisconnect`) after the user hook. Duplicate `SpawnAvatar` for a live avatar is rejected; negative `FOIRadius` is rejected; `DeleteEntity` clears avatar indexes in O(1). `SetOwner` transfers command authority only — not avatar identity. Server FOI APIs (`AssignFOI`, `RemoveFOI`, `SessionsKnowing`, interest tick grid/diff) are mutex-guarded for concurrent use. Website docs may need a short note on session avatars.
- Generated Go server `Synced*` entities expose `BindServer` / `Server()` so gameplay code can reach the owning `*golem.Server` without package-level globals. `CreateEntity` binds before registry insertion (and thus before `OnSpawn`); `Server()` is nil until then. Re-run `golem-bake`. Website docs may need a short note on this accessor.
- Entity schema var names whose PascalCase form is `Server` or `BindServer` (including `server`, `bind_server`, and literal `Server`/`BindServer`) are rejected as collisions with the generated accessors.
- **Golem Scribe (Unity):** one-way Unity authoring that exports deterministic, generated-but-committed artifacts for `golem-bake` and the collision footprint loader. Ownership is tracked in `scribe.golem.yaml`; handwritten files are never overwritten or deleted. Menus: `Golem/Scribe/Export All`, `Golem/Scribe/Validate` (side-effect-free dry-run), and `Golem/Validate Setup` (setup checks + Scribe dry-run). Project Settings > Golem configures auto-export on asset change, auto-bake on schema change, footprints path, and project-root/schema path diagnostics.
- **Entity authoring:** mark prefab components with `[GolemEntity]` / `[GolemVar]` / `GolemSync` to export entity schemas under `entity_schema` and upsert the prefab registry. Prefab field values are schema-only in v1 (not server defaults). Tags use the entity wire offset (`dimensions + 1`) and reject revision field 1000 collisions.
- **Catalog authoring:** mark ScriptableObjects with `[GolemCatalog]` / `[GolemField]` / `[GolemAssetRef]` to export a custom type schema, catalog world schema, and project-root `catalogs/{name}.golem.yaml` data file (sequence of maps, snake_case keys, opaque GUID asset refs). Custom-type tags are direct protobuf field numbers (no entity offset). Catalog class deletion removes all three managed artifacts; data-only edits do not trigger auto-bake. `golem-bake` generates `Load{Name}Data`; application code must still load and publish world data explicitly.
- **Collision footprints:** mark prefabs with exactly one root `GolemFootprint` (optional unique alias + included layer mask) to export `footprints.golem.yaml` (configurable path). Entity status alone does not export colliders. Export includes enabled colliders on inactive children and skips disabled collider components; unsupported colliders inside the included mask fail that prefab. Hierarchy scale and quarter-turn limits match the Go placer (2D Circle/Box; 3D Sphere/Box). Footprint changes never invoke `golem-bake`.
- **`golem/footprint`:** loads versioned `footprints.golem.yaml` (GUID identity, optional unique alias, diagnostic name/path) and places exact 2D/3D collision shapes on a collision backend with translation, positive uniform scale, and quarter-turn rotation only. Synthetic negative collision IDs are collision-only and may appear in contact callbacks without a registry entity.
- **Scribe validation and CI:** dry-run validation reports missing, stale, orphaned, or manually modified artifacts and checks manifest ownership, footprint version/dimensions, prefab registry parity, and exporter rules (names, aliases, GUIDs, geometry). Batch-mode entry point `-executeMethod GolemEngine.Unity.Editor.GolemScribeCI.ExportAllAndValidate` runs Export All + validation and exits non-zero when committed artifacts are stale or invalid (auto-fix does not count as CI success).
- Coalesced, reentrancy-safe Scribe exports schedule from asset changes; generated C# refreshes do not recursively re-export. Auto-bake runs only after successful entity or catalog type/world schema byte changes.
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

- JS `ConnectOptions` now includes optional `eventualAckIntervalMs` (already consumed by the WebTransport channel at runtime).
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

- Generated `entities.proto` files now declare world collection fields with valid `repeated` or `map` protobuf types.
- Generated C# protobuf decoders now compile for maps and catalogs keyed by unsigned integers.
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
