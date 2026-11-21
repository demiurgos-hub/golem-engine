# Spaces in golem-engine: implementation plan

## Status

Milestones M1–M9 are implemented on `feature/spaces`:

| Milestone | Commit |
| --- | --- |
| M1 Space identity | `385ed6d` |
| M2 Per-space replication and transitions | `3c49c10` (preceded by the fix `cd202ae`, compact records that overtake their spawn) |
| M3 Client runtimes | `6351092` |
| M4 Per-space world data | `b345418` |
| M5 Space runtimes, legacy collision, 2D navigation | `347b7da` |
| M6 Per-space physics, 3D navigation, transfer | `4bba197` |
| M7 Multi-space checkpoints | `359a98c` |
| M8 Inspector | `64bfff9` |
| M9 Reconciliation | `1dfe839` |

Review fixes, round 1 (after M9, one commit each):

| Commit | Fixes |
| --- | --- |
| `18226e6` | Datagram-lane events stay on the stream until SpaceAck (also for stream-lane WebTransport sessions); SpaceAcks bypass the droppable queue; world pushes and space events keep the old space until the transition is queued; a spectator's RemoveFOI orphans exit on its next switch; a deleted FOI anchor replaced by a global one exits; `AppendSpaced` skips lookups when unused. |
| `5ffbaf5` | Go/C# held-record eviction order; EntityResync control frame (`"\x00OGS\x04"` + entity ID) answered with a stream full state; `SetOwner` advances the state revision; batches and replays stop once a callback disconnects or clears. |
| `05ef1d2` | `SaveSnapshot` serializes on the caller (`snapshot.WriteAsync` writes); `SaveSnapshotOnTick` and tick-time autosave; clashing generated protobuf message names are rejected; multi-envelope stream frames documented. |
| `c43bc3c` | Transfers detach every source before the registry moves, run traversal cancels before navigation reattaches, reject unbound layers (`Layers.Bound`), queued deletions and geometry bodies, drop stale pre-transfer commands (narrowed in `fcf80ee`), guard setup, attaches and the legacy collision phase, sync moved colliders and source worlds. |
| `7b342ec` | Rebuilt characters keep their controller settings (`physics3d.CharacterControllerReader`, `PhysicsTransferSource.Controller`). |
| `e009add` | No service can serve two spaces; restore reports configuration errors and checks colliders before committing; stricter v2 navigation validation; `NavAgent` drops a path from its previous space; restriction docs. |
| `6c5cb64` | Inspector map follows the space filter; space moves travel with their deltas. |

Review fixes, round 2 (one commit each, on top of `a1ad376`):

| Commit | Fixes |
| --- | --- |
| `c772826` | Server events stay inside their space across switches: events sent before the switching pass are held per session and follow `SpaceSettled` (generated stream-lane events included); once a session has switched, its datagram event records carry the transition (`00 00 <varint> <event>`) and clients drop older ones; clients close the connection when a SpaceAck or EntityResync send fails. |
| `106c5fb` | `AssignFOI` after `RemoveFOI` exits the orphans the new FOI does not cover (single-space too); owner-view bytes are re-checked against the current owner after serialization; EntityResync requests are stored only for known entities, without a cap, and answered inside the next replication pass (generated managers re-request after a stale answer, at most three times); world pushes and space events are ordered with transitions from any goroutine (`announceMu`). |
| `cd78d94` | A snapshot capture started before `Run` holds back the first tick (`preRunCaptureMu`); snapshot writes use unique temp files and finish in request order per path; entity var names whose generated getters, setters or fields clash with generated entity members are rejected, and a var named `id` no longer breaks the generated Go constructor. |
| `bd22fc2` | Transfers check source bodies and worlds before the destructive phase (`preflightTransferSources`) and fault every affected space (physics and navigation readiness) on a later backend failure; entities are compared by registry incarnation (`Registry.Incarnation`, also used by the physics step and deferred deletions); traversal-cancel callbacks cannot move an entity before its navigation is reattached; `SpaceTransfer.MoveShapes` moves game-registered collision shapes, with read-only `CollisionBackend` getters; `DetachPhysics` / `DetachNavigation` are busy during a transfer. |
| `fcf80ee` | Only commands failing with a `physics3d.StaleHandleError` for a body a transfer destroyed after they were queued are dropped (collider and joint handles of moved bodies still fault); typed-nil backends count as nil (`Layers.Bind` and the setters); a restore that panics after publication keeps readiness closed; the Chipmunk backend reindexes shapes moved or added since its last `Step` before querying; layer helpers cannot share a backend across spaces (`Layers.Backend`); `NavAgent` drops its route after a round trip (`Registry.SpaceMoveGeneration`). |
| `820a78f` | The inspector ignores filter spaces without entities (`pruneFilterSpaces`, stored filter kept) and resets filters on target change; single-space games skip per-entity space work in filtered broadcast and the connect snapshot, release replication scratch pointers and take no inbox locks on quiet ticks. Measured against `e3c4ab0` (`-count 6`, interleaved): equal B/op and allocs for blind broadcast, interest, `BenchmarkManagerComputeDiffs` and eventual state; one allocation and 3.4 KB less for filtered broadcast; pass times within noise; quiet-tick drains at 3.4 ns instead of about 40 ns. |

Review fixes, round 3 (one commit each, on top of `bbda8c7`):

| Commit | Fixes |
| --- | --- |
| `7e3e220` | Transfers admit rebuilt bodies with their own placement tolerance (`SpaceTransfer.Placement`, zero selects `DefaultTransferMaxPenetration` = 1 cm; the entity's own tolerance applies when larger), so bodies at rest move by default. First transfer test against a real physics backend (`examples/physics-stress`, Box3D). |
| `b0ed71d` | `Space.BroadcastServerEvent` chooses recipients by the space a session is in or moving to, and makes recipients whose transition is not queued hold the event, so an event sent on the tick of a move plays in the right space. World pushes keep the announced space. |
| `a5e0813` | `SetSessionSpace` stores nothing for a session that is not connected (`closedSessions` mark plus a listener check after storing). |
| `54ff25f` | A session whose avatar is deleted keeps the avatar's last space (`sessionSpaceKept`) in both replication modes until it gets a new avatar or `SetSessionSpace` is called; an explicit space still wins. |
| `97fe62c` | Snapshot capture pre-sizes its record slots and file buffer and, from 4,096 entities, calls `FullUpdate` from several goroutines while the caller waits; the first write to a path removes temp files left by a crashed process. Caller time at 20,000 small entities: about 8 ms and 21.7 MB before, 2.9 ms and 6 MB after (0.33 ms at `e3c4ab0`, where serialization ran in the background). Encoding stays on the caller, because a capture owns its bytes when the call returns. |
| `17ec862` | The legacy collision phase copies positions to all backends in one pass over the entities instead of one pass per space; `Registry.GetSpaced` reads an entity and its space under one lock. |
| `16443b9` | `golem/space_e2e_test.go` runs a real `Server.Run` against the real Go client over WebTransport datagram lanes across a switch and back; the `EventDelivery` godoc states that datagram lanes, the reliable ones included, are reliable within one space only, pinned by one test per client runtime. |

Review fixes, round 4 (after an independent review of `e3c4ab0..39209b7` reported 13 findings; 11 were real, 3 of them introduced by round 3):

| Commit | Fixes |
| --- | --- |
| `5f08f57` | A transition serialized its world frames (game code, `WorldData.MarshalUpdate`) under `announceMu`; a serializer that broadcast a space event or pushed world data deadlocked the tick. World frames are built before the lock and rebuilt when `worldStamp` (new `world.Store.Version`) changed, at most 8 times. `SendStoredWorldData` likewise. From round 2. |
| `21f18a8` | A session's space is resolved from published anchors and avatars only (`PublishedSpace`), so a provisional anchor whose setup fails cannot route another space's event to its client. From round 3's event routing. |
| `7d0f2a5` | An avatar moved and deleted before one pass leaves its session in the avatar's last space in interest mode too (`clearAvatarByEntity` updates the interest manager's missing-anchor space). From round 3. |
| `711fb83` | The snapshot temp-file cleanup of round 3 is removed: it deleted any `.<name>-<digits>.tmp`, including a finished save with such a name. The temp file is created with `O_EXCL` and mode 0644 so the process umask applies again (round 2 had replaced `os.WriteFile` with a chmod to 0644). Verified on Linux through WSL. |
| `9e54d85` | A transfer whose later source cannot be destroyed also faults the sources it already detached from; navigation is reattached only to the same registry incarnation. From round 2. |
| `f1c5753` | The retired-body exception requires every failure in a command's error to be a stale handle of a moved body (no `errors.As`), and retired handles are forgotten once no queued command predates them instead of only when the queue empties. From round 2. |
| `fe6e32b` | World types whose generated world-manager members clash (`ClearWorld`, `ApplyUpdate`, another type's listener) are rejected by the bake; the Go client's `ServerMessage` is back to `Field` + `Payload`, so decoding 200 envelopes allocates 23.8 KB (47.9 KB at `e3c4ab0`, 69.7 KB in between). |

Not fixed by decision: legacy contact events identify pairs by entity ID, so a pair of entities moved together keeps its contact history and reports "stay" (pairs split by a move exit; semantic choice), and older clients' handling of the new frames (no compatibility goal: games upgrade client and server together).

After pushing: bump the root `go.mod` pins of `golem/collision` (`Layers.Bound`, `Layers.Backend`) and `golem/physics3d` (`CharacterControllerReader`, `StaleHandleError`), and box3d's own `physics3d` pin (it now needs `StaleHandleError`); until then the root builds only through the local `go.work`.

Known follow-ups:
- `examples/` and qualification consumers; regenerate the checked-in examples' `entities.proto` (fields 4–6).
- Website docs: a Spaces page (sessions, transfer, world data, per-space services, client callbacks, compatibility notes).
- Fractured worker and client.
- Creating or removing serviced spaces after `Run`.
- A connect-time initial-space hook for broadcast mode.
- Space-scoped generated `EventBroadcaster`, deferred because `Broadcast<X>InSpace` may collide with schema event names; use `Space(id).BroadcastServerEvent` meanwhile.
- Pre-existing, found in M2 and not fixed: ownership-refresh full states and oversized stream-fallback deltas travel on the reliable stream and can be overtaken by datagrams from later ticks. `cd202ae` fixed only the spawn case; transitions themselves are covered by the SpaceAck gate.
- Generated client code adds its own top-level names (`Client`, `EntityManager`, `Synced<E>` and others) that a custom type could clash with in one client language; the new proto-name check does not cover them.
- Found in round 2, not fixed:
  - Entity var names that are keywords of a target language are not validated.
  - Generated names of different entities (such as generated constants) can still clash; the var-name check only covers one entity's own members.
  - Raw stream frames sent with `Server.Send` / `BroadcastEvent` are not held by the event hold.
  - Creating a missing FOI anchor in another space with `CreateEntityIn` does not mark the session for event holding: the switch is planned, but events from that tick's `Post` / `OnTick` are not held.
  - An off-tick space change racing a pass's event release may flush that session's held events one pass early.
  - Collider and joint handles destroyed with a moved body still fault commands queued for them (no API lists them, so only the moved body's own stale handle is dropped).
  - `TestBroadcastBlindDecisionMutationReconciledNextPass` (`golem/visibility_replication_test.go`) is flaky under full-suite load.
- Found in round 3, not fixed:
  - A session that changes space twice before one replication pass still receives the space-scoped events sent to the space in between, delivered after it lands in the final space (held events carry no space).
  - `SaveSnapshot` still blocks its caller for one `FullUpdate` per entity plus one copy of the state; that is the price of a save taken from one tick.
  - A per-event option that exempts an event from the older-transition drop on datagram lanes was considered and declined; use stream delivery.
  - No test joins a real server to the JS or Unity client runtime; only the Go client has one.
- Reported in round 4 and left as designed:
  - After `RemoveFOI` the client keeps the entities it knew frozen until its next transition or `AssignFOI`. That includes an entity that has since moved to another space, as it includes one that was deleted.
  - Generated managers stop asking for an `EntityResync` after three stale answers (round 2). A field carried only by an evicted compact record then stays wrong until it changes again; that needs an eviction plus four full states in a row being overtaken by later datagrams.
- Found in round 4, not fixed:
  - An FOI anchored on an entity that is not the session's avatar keeps the space of the last replication pass when that entity is moved and deleted in one tick.
  - Snapshot temp files left by a process that crashed mid-write are not cleaned up.
  - `TestGenerateGoSharedTemplateSnapshotHelpersBehavior` (codegen) is timing-dependent: its generated autosave test reads a counter right after a channel send and can fail under full-suite load.
- Pre-existing flaky hub test `TestShutdownClosesSocketsWithServiceRestart`.
- The `golem-threlte` browser test was not run (Playwright Chromium build 1243 missing).

## Context

Fractured needs caves hosted in the same Go worker as the overworld. Cave and overworld coordinates overlap, and players walk into a cave carrying a crate or pulling a Handcart. golem-engine has no notion of separate coordinate spaces today:
- `golem.Server` holds one interest grid, plus one each of the physics, collision and navigation services.
- Visibility groups (a single group per entity, public when unset) are meant for invisibility, not for separating maps.

We are adding **Spaces** to the engine as a general feature that other Golem developers can use for caves, dungeons and match instances:
- **Identity:** every entity belongs to a `SpaceID` (`uint32`, default `0`).
- **Isolation:** entities in different spaces never see, collide with, query or navigate with each other.
- **Space changes:** a space change keeps the moving session's carried entities. There is no despawn and respawn, and no stale or early state is applied out of context.
- **No replication cost for single-space games:** no extra bytes on the wire, the same allocations per replication pass, and byte-identical snapshots and checkpoints. This was first written as "zero cost, no API change"; the implementation did change a few signatures and behaviours that also reach single-space games (`DetachNavigation` returns an error, `PhysicsFrameMetrics` gained `Space`, `SaveSnapshot` serializes on the caller, and others). The CHANGELOG lists them under Changed.

**Decisions already made:**
- `uint32` ids.
- No despawn or respawn on a space change.
- All of this lands now: replication, client SDKs, world data, inspector, and per-space engine physics, collision and navigation. There is no guard against combinations.
- Client acknowledges each switch with a `SpaceAck` control frame.
- Navigation worker pools are per space.
- Spaces are strictly isolated; no entity is visible in every space.
- The in-repo `CHANGELOG.md` and `.cursor/rules` are updated as part of this work.

**Deferred** (listed as follow-ups only): `examples/` and qualification consumers, the website docs repo, the Fractured worker and client, and creating or removing serviced spaces after `Run`.

Repo: `C:\Demiurgos\golem-engine`. The first step copies this plan to `plans/spaces.md`.

## Core rules

- **Space metadata lives in the registry, like owner.** The registry keeps `spaces map[int64]SpaceID` with only non-zero entries. Neither the `Entity` interface nor the user schemas change, so the schema fingerprint is unaffected.
- **A session's effective space comes from, in order:** its FOI anchor, then its avatar, then an explicit `SetSessionSpace`, then the last space of its deleted avatar (added in round 3), then 0.
  - The server also keeps the **announced** space per session (`sentSpace`, initially 0). It changes only through a replication transition.
  - Only sessions whose space has changed ever receive extra messages.
- **Visibility stays independent:** an entity is replicated only when it is in the session's space *and* `visibility.Allows` passes. The `golem/visibility` package does not change.
- **Ordering:** entity space mutations (`MoveToSpace` / `SetEntitySpace`) happen on the tick, and several of them in one tick are seen together by that tick's replication pass. Session-level changes (`SetSessionSpace`, `AssignFOI`, `RemoveFOI`) are safe from any goroutine; they take effect on the next replication pass, and world pushes and space events are ordered with transitions from any goroutine (`announceMu`).

## Milestones

Each milestone is committed separately and ends green on `go test ./...` from the repo root, plus its listed package tests. It also adds its CHANGELOG `Unreleased` entries and updates the rules it affects.

The `file.go:line` references in the milestone sections point at the code as it was at `e3c4ab0`, before this work started. They record where each change was planned and no longer match the files; find the code by the function names instead.

### M1: Space identity (registry, creation, snapshots)

- **`golem/registry`:**
  - `type SpaceID uint32`.
  - Space variants of insert and provisional add: `AddWithoutSpawnIn`, `AddOwnedWithoutSpawnIn`, `AddProvisionalIn`.
  - `Space(id)`, which also resolves provisional entities. `SetSpace(id, space)` for published entities only; it records the move.
  - `SpacesInUse()` (atomic count of non-zero entries) and `AppendSpaced(dst)` (one read lock).
  - `FlushResult.Moves []SpaceMove{ID, From, To}`; `RestoreEntry.Space`.
  - Clean up the space entry on delete, discard and provisional delete; swap the space map atomically in `ReplaceWithoutHooks`.
  - Files: `registry.go:24-254`, `provisional.go`, `restore.go`.
- **`golem`:**
  - `type SpaceID = registry.SpaceID`.
  - `CreateEntityIn(space, e, owner...)` (`loop.go:412`) and `CreateEntityWithSetupIn` (`entity_setup.go:30`).
  - `EntitySpace(id)` and `AvatarOptions.Space` (`avatar.go:30`).
  - `ReplicationFrame.Moved []EntitySpaceMove` (`replication_observer.go:19`).
- **Snapshots:**
  - `snapshot.Record.Space`. Write v1 when every entity is in space 0, otherwise v2 with a per-record `uint32`. `Decode` accepts v1 and v2 (`snapshot/snapshot.go:24-170`).
  - `LoadSnapshot` (`snapshot_api.go:32`) and `LoadSnapshotStrict` (`snapshot_strict.go:46`) create entities in their recorded space. The content manifest stays at v1.
- **Tests:**
  - new `registry/space_test.go`;
  - v1 golden bytes unchanged in `snapshot/decode_test.go`, plus a v2 round trip;
  - `snapshot_strict_test.go` and `snapshot_api_test.go` with spaces.

### M2: Per-space replication, session space, transitions (server side)

- **Interest** (`interest/manager.go`):
  - `grids map[SpaceID]*Grid`, with space 0 as the fast path, plus `entitySpace`, `globalsBySpace` and `Diff.Space`.
  - `UpdateGrid` keeps today's loop when `!reg.SpacesInUse()`; otherwise it uses `AppendSpaced` and moves entities between grids.
  - `ComputeDiffsFiltered` queries only the session space's grid and iterates only that space's globals. Known globals in another space get an explicit exit.
  - A missing anchor keeps the last space (the existing globals-only branch, `:180-187`). A global anchor resolves through `globalsBySpace`.
- **Server** (`loop.go`, under `interestMu`):
  - State: `sessionSpaceExplicit` and `sentSpace`, both cleared on disconnect (`cleanupVisibilityOnDisconnect`, `:1690`).
  - New APIs: `SetSessionSpace(sessionID, space)`, `SessionSpace(sessionID)`, `SessionsInSpace(space)` (by announced space).
  - `MoveToSpace(SpaceTransfer)`: in this milestone, for entities without physics or navigation.
  - `Space(id).BroadcastServerEvent(...)` sends to `SessionsInSpace`. Existing broadcasts stay global.
  - *Updated after review:* `SessionsInSpace` and world pushes use the announced space (the old one until the switching pass has queued its transition), ordered with transitions from any goroutine (`announceMu`). Since round 3, `Space(id).BroadcastServerEvent` instead chooses recipients by the space each session is in or moving to, because a moving session's events are held until its transition. Server events sent to a session before the pass that plans its switch are held and follow `SpaceSettled`, and once a session has switched its datagram event records carry the transition so clients drop older ones (see Status).
- **Broadcast mode:**
  - `shouldFilterBroadcastLocked` (`:1373`) also returns true when `SpacesInUse()` or any session space is non-zero.
  - `computeBroadcastDiff` (`:1617`) adds a same-space check.
  - The connect snapshot (`entitySnapshotForSession`, `:1705`) filters by `sentSpace`, which is 0 at connect.
- **Transition** (`replication.go` `deliverReplicationDiff`, `:143-248`): when the effective space differs from `sentSpace`, the stream for that session is built in this order:
  1. Exited (old-space and visibility exits).
  2. `SpaceChanged{space, transition}`, as `ServerMessage` field 4.
  3. World data (M4).
  4. Carried entities: every Stayed entity on this tick. For each, `clearEventualEntity` and a full state in the current owner or public view (`wrappedFullForSession`, `owner_replication.go:207`). Skip `appendOwnershipRefreshFrames` for this session on this tick.
  5. Entered.
  6. `SpaceSettled{transition}`, field 5.

  Frames are packed greedily into as few logical frames as possible (≤256 KiB) by concatenating wrapped `ServerMessage`s. Then `sentSpace` is updated. An interest-mode session without an FOI whose space changed still gets a transition with an empty diff.
- **SpaceAck:**
  - New client→server control frame `"\x00OGS\x03"` + transition (`internal/netwire/framing.go:11-12`; decoded in `golem/net/control_frame.go` and `session.go:494`).
  - From the transition until its ack, the server sends that session's deltas on the ordered stream, not datagrams (the stream fallback path in `eventual_state.go:596-611`).
  - A newer transition supersedes an older ack.
- **Wire:** messages for fields 4, 5 and 6 in `codegen/templates/proto/entities.proto.tmpl:123-133`; wrappers in `servermsg.go`.
- **Invariant:** after each pass, a session's known set lies entirely inside its `sentSpace`.
- **Investigate and fix if real:** an Entered full state on the stream can be overtaken by a datagram from the next tick. The client ignores that datagram as unknown but acknowledges it, so those changes may be lost. Transitions enter many entities at once. Per the repo rule, fix it in the engine.
- **Tests:**
  - `interest/manager_test.go`: same coordinates in two spaces, globals per space, anchor moves, missing anchor, global anchor.
  - new `space_replication_test.go`, in interest and broadcast modes: frame order; no despawn for carried entities; blind path restored once everything is back in space 0; visibility change, owner change and move on the same tick; carried entity left outside the FOI exits.
  - `replication_contract_test.go`: real WebTransport peer in datagram mode, stream-only until ack.
  - `eventual_state_test.go` and `owner_replication_test.go`.
  - Interest and replication benchmarks before and after, to show the no-space path is unchanged.

### M3: Client runtimes and wrappers

- **All three runtimes** (Go `golem-go-client/framing.go` and `game_client.go`; JS `golem-js/src/client.ts` `visitServerMessage` `:2206`; Unity `golem-unity/Runtime/Client/GameClient.cs` `HandleMessage` `:1234`):
  - Decode fields 4, 5 and 6, and expose the current `Space`.
  - Callback: Go `OnSpaceChanged(func(old, new))`, JS `subscribeSpaceChanged`, Unity `event Action<uint,uint> SpaceChanged`. It fires on `SpaceChanged`, before the frames that follow.
  - Send `SpaceAck` once `SpaceSettled` is applied.
  - Hold back compact datagram batches while a transition is open and apply them in order at `SpaceSettled`. The hold-back is bounded, and overflow closes the connection with an error.
  - Space resets to 0 silently on connect and disconnect.
  - Full states for already-known entities apply as updates through the existing generated managers; no despawn.
- **Wrappers:** a readable `space` store in `golem-threlte/src/core.ts`. The Unity spawner, Phaser and Ebiten bridges need nothing.
- **Tests:**
  - `golem-go-client/game_client_test.go`, `golem-js/test/client.test.js`, `golem-unity/Tests/Runtime/GameClientTests.cs`.
  - Cases: late datagram held, then rejected by the revision fence; equal revision is a no-op; ack sent; old clients skip fields 4-6; reset on reconnect.

### M4: Per-space world data

- **API:** `Space(id).World() *world.Store` and `Space(id).PushWorldData(name)`, which sends to sessions whose `sentSpace == id`. Add `Store.Names()` (`world/store.go`).
- **Connect snapshot:** the global store merged with space 0's store, where space 0 overrides (`loop.go:314-324`, `net/listener.go:792`).
- **On a transition to space X**, for each name defined in any space's store:
  - X defines it: send X's value;
  - otherwise the global store defines it: resend the global value;
  - otherwise: send `WorldCleared{name}` (field 6).

  This also clears stale data across reconnects.
- **Global `PushWorldData`** (`loop.go:903`) skips sessions whose space overrides the name. This check only runs when per-space stores exist.
- **Codegen:**
  - Generated world managers (`codegen/templates/{go_client,js,csharp}/world_manager.*.tmpl`, emitters in `codegen/{go,js,csharp}_proto.go`) get `clearWorld(name)` plus cleared listeners.
  - Runtimes call these through an optional interface (Go `WorldClearer`, a JS optional method, C# `IWorldClearer`).
- **Tests:** `world/store_test.go`, `world_data_test.go`, codegen golden and consumer tests in all three languages, and `space_replication_test.go` (clear, override, reconnect).

### M5: Space runtime refactor, legacy collision, 2D navigation

- **Internal `spaceRuntime{id, collision, collision3D, layers, layers3D, navBackend, navigation, physics}`:**
  - `Server.spaces` is a map plus an ascending order slice, and space 0 is always present.
  - `s.physics` and `s.navigation` stay as aliases for space 0, so the ~250 test references keep compiling. Production entity-keyed code must resolve `s.runtimeOf(entityID)`; a review grep enforces this.
- **Public handle `Server.Space(id) *Space`:**
  - `SetCollisionBackend`, `SetCollision3DBackend` and `SetCollisionLayers(3D)`;
  - all the `spatial.go` queries;
  - `SetNavBackend`, `FindPath` and `SetNavWalkable`;
  - `World` and `PushWorldData`.

  Existing `Server` methods act on space 0.

  *Updated after review:* these legacy collision, layer-helper and 2D navigation setters may run before `Run` or on the tick goroutine (documented, not enforced); a setter installing a service in a new space creates that space's runtime. No service may serve two spaces: the setters panic when the value already serves another space, including a backend reached through another space's layer helper (`Layers.Backend`).
- **Tick** (`loop.go:1262-1319`): collision runs per space in ascending order. The legacy `onContact`/`onContact3D` callbacks and `dispatchContactEvents` run once per tick with contacts merged across spaces. The pair maps stay on `Server`, since ids are unique and pairs never cross spaces.
- **Entity-keyed paths:** `registerCollider` and `unregisterCollider` (`collider.go:73-93`) use the entity's space. `NavAgent.SetDestination` (`nav_agent.go:54`) uses its entity's space. `MoveToSpace` moves legacy colliders between layers (*updated after review:* after the registry move, see M6; shapes the game registered on a backend itself move only through `SpaceTransfer.MoveShapes`).
- **Codegen:** `Runtime.EnableSpaceCollision(space, backend)` and its 3D variant (`codegen/templates/go_server/shared.go.tmpl:60-96`).
- **Tests:** `collider_lifecycle_test.go`, `spatial_test.go`, `collision_events_test.go`, `nav_agent_test.go` per space; codegen `go_server_template_test.go`.

### M6: Per-space physics and 3D navigation, and transfer

- **Service links:**
  - `physicsService.space` and `navigationService.space` replace cross-references through `server.physics` / `server.navigation`. These are in `physics.go:167-189`, `physics_policy.go`, `physics_trajectory.go`, `navigation.go:322`, `physics_geometry*.go` and `physics_recovery.go`.
  - Lifecycle stop: all navigation services, then all physics services (`tasks.go:412-421`).
  - Navigation path and build pools stay per space.
- **Handle methods:**
  - physics: `SetPhysicsWorld`, `OnPhysicsContact`, `ObservePhysicsFrames`, `QueuePhysicsCommand`, the geometry APIs, `PhysicsReady`;
  - navigation: `SetSurfaceNavigation`, `NavigationReady`, `NavigationStats`, the build, restore and `NavigationFrame` APIs.

  Physics worlds and surface navigation are configured before `Run` (*updated after review:* the setters return an error once `Run` has started or when the world or backend already serves another space; creating or removing serviced spaces after `Run` stays deferred). `PhysicsFrameMetrics` and the navigation metrics get a `Space` field.
- **Entity-keyed APIs** resolve the entity's space. `AttachPhysics` rejects a body from another world (check `Handle.World` against the space's world). *Updated after review:* `p.World` exposes no world identity, so the root rejects a body attached in, or recognized by, another space's world (`foreignPhysicsBody`), and Box3D rejects other worlds' handles itself.
- **Tick:** each phase runs for every serviced space in ascending order: drain, navigation prepare, physics step or navigation advance, reconcile. Fault, readiness and restoring state are per space. A fault in any space still ends `Run`.
- **Transfer:**

  ```go
  type SpaceTransfer struct {
      To       SpaceID
      Entities []int64
      RebuildPhysics func(dst p.World, src []PhysicsTransferSource) (map[int64]p.BodyHandle, error)
      MoveShapes     func(to SpaceID, moves []ShapeMove) error // added after review
  }
  ```

  *Order and guarantees updated after review.* The approved plan moved colliders before the registry and reattached navigation before attaching physics, and promised that a failed move changes nothing. The implementation (`golem/space_transfer.go`, documented on `MoveToSpace` in `golem/space.go`) runs:

  1. **Validate everything first:** each entity exists, is published, is listed once, is not being deleted (including a deletion deferred from a physics callback), is not awaiting navigation reattachment from an outer transfer, and is not attached to a body of loaded physics geometry. The destination has the services the entity needs: a physics world plus `RebuildPhysics` for a physics attachment, surface navigation for a navigation attachment, and no staged agent may arrive without a body in a physics space. A schema collider needs its layer defined by the destination space's layer helper, and every helper the move touches must be bound to a backend; a destination space without a layer helper drops the collider (corrected in round 4: the plan and the `MoveToSpace` godoc said both spaces need helpers, the code and its test never required that). Return `ErrBusy` while any space is stepping, dispatching, restoring or holds a prepared navigation frame, during the per-space simulation phase and during the legacy collision phase; callbacks must use `Server.Post`.
  2. **Rebuild:** call `RebuildPhysics` once with the synchronized source states (including the source character controller configuration) while the source bodies still exist, and admit each returned body like `AttachPhysics` (same motion mode and, when both worlds report it, the same controller configuration), with the transfer's placement tolerance (`SpaceTransfer.Placement`, 1 cm by default since round 3, so bodies at rest are accepted). Then check that the callback changed no moving entity (space, deletion, attachments, registry incarnation).
  3. **Preflight:** every source body is still alive in a source world that accepts mutations and has not faulted, so the destructive phase cannot fail on a body the callback destroyed.
  4. **`MoveShapes`** (optional, also for batches without physics or navigation) moves the game's own collision shapes, followed by the same no-change check.

     A transfer rejected up to here changes nothing: the returned bodies are destroyed, and only a failure to destroy them faults the destination.
  5. **Commit** (traversal-cancel callbacks of the detached source agents are deferred):
     1. Detach every source attachment: physics (destroying the body and its joints and cancelling its trajectory), then navigation. If a source body still cannot be destroyed, the source and destination spaces fault, the rebuilt bodies are destroyed and the registry and colliders stay unchanged; entities detached before the failure have lost their source bodies, and `MoveShapes` has already run.
     2. Synchronize each source world that lost bodies; a failure faults that space, but the commit continues.
     3. Set the registry space of every entity (under `interestMu`; sessions that now switch hold their server events).
     4. Move schema colliders to the destination layers at the entity's current position.
     5. Attach physics to each new body with the stored config; the new body's gameplay anchor defines the entity position.
     6. Run the deferred traversal-cancel callbacks with the entities already in the destination; a nested move of a moved navigation entity returns `ErrBusy` until step 7.
     7. Reattach navigation with the same config but no destination intent, unless a callback deleted the entity, attached navigation itself or detached the physics a staged agent needs.

     A backend failure during or after the commit faults the affected spaces (`Run` ends; navigation readiness closes), and the move may be partly applied.

  **Not carried over** (documented): trajectories, physics contact history, navigation route and intent, pending forces and controller velocity intent, and joints to bodies that weren't moved. Queued source commands still run in the source space (*updated after review:* one queued before the transfer that fails with a `physics3d.StaleHandleError` for a source body the transfer destroyed is dropped instead of faulting; every other error, including a stale collider or joint handle of a moved body, still faults). Legacy contact events identify pairs by entity ID, so a pair moved together reports "stay" and a pair split by the move exits.
- **Tests:**
  - multi-space versions of `physicsTestWorld` (`physics_test.go:14`) and `navigationTestServer` (`navigation_test.go:170`);
  - new `space_transfer_test.go`: avatar plus crate plus jointed cart in one batch; rollback on rebuild error; mid-frame rejection; no double step; nav agent idle after reattach; faults;
  - per-space step order and metrics.

### M7: Multi-space checkpoints

- **`PhysicsCheckpoint`** (`physics_checkpoint.go:30-749`):
  - v1, byte-identical, when only space 0 is used.
  - Otherwise v2 with `Spaces []PhysicsSpaceCheckpoint{Space, Physics, Desired*, Pending*, AcceptedNavigation, Navigation, Attachments, NavigationAgents, Geometry, Trajectories}`, plus `PhysicsEntityCheckpoint.Space` (omitempty).
  - Per-space options and restore hooks. `NavigationCheckpoint` is reused per section.
- **Restore:**
  - requires the same set of serviced spaces;
  - prepares every space, then commits all of them;
  - if a commit fails partway, all spaces fault and readiness stays closed;
  - resets `sentSpace` and explicit session spaces along with the existing interest and visibility reset (`:609-621`).
- **Entity-only paths:** `SaveContentSnapshot` and strict loading reject when any space has physics.
- **Tests:** `physics_checkpoint_test.go` (v1 golden unchanged, v2 round trip and rejection, multi-space restore), `navigation_checkpoint_test.go`, recovery tests.

### M8: Inspector

- **Agent** (`golem/inspector/stream.go`): `EntityState.space` (field 4, only sent when non-zero) and `Batch.moves` (`SpaceMove{id, space}`), fed by `ReplicationFrame.Moved`.
- **Wire:** `ProtocolVersion` stays 1, because the changes are additive and the decoders skip unknown fields. Regenerate fixtures with `go test ./golem/inspector/wire -update`.
- **Hub** (`hub/hub.go`): store each entity's space and apply moves.
- **UI** (`golem-inspector/src`): space on `entityStore.ts`, a space filter (`lib/filters.ts`, `FilterBar.tsx`), an `EntityList` column, and a `WorldMap` space selector. The selector defaults to the lowest space present, with an "All" option, because spaces overlap in coordinates.
- **Tests:** `wire_test.go`, `hub_test.go`, `entityStore.test.ts`; UI `npm test`, `npm run typecheck`, `npm run build`.

### M9: Reconciliation

- **Rules to update:**
  - `subsystem-golem-registry`, `-interest`, `-world`, `-snapshot`, `-runtime` (tick order, Space handle, transfer);
  - `-physics3d` and `-nav` (per-space services);
  - `-net` and `subsystem-netwire` (fields 4-6, `SpaceAck`);
  - `subsystem-codegen`, `bake-pipeline` if outputs change;
  - the integration rules for unity, js, go-client and threlte;
  - `-inspector`.

  Then fix the drift found along the way: the tick order is missing `physics.drain`, and the snapshot rule's magic doesn't match the code.
- **CHANGELOG:** final pass over `Unreleased`, including a note that the website docs need a Spaces page.
- **Follow-ups:** examples and qualification consumers, website docs, the Fractured worker and client, creating or removing serviced spaces at runtime, and a connect-time initial-space hook for broadcast mode.

## Verification

- **Go:** `go test ./...` at the engine root after each milestone. Run the nested modules that are touched (`golem/objecttemplate`, `golem/nav/navmesh`) explicitly with `GOWORK=off` where the AGENTS.md rules require it.
- **No regression for single-space games:** interest and replication benchmarks before and after M2; v1 golden bytes for snapshots and physics checkpoints unchanged; existing tests pass unmodified apart from the field aliases.
- **Clients:** `go test ./...` in `golem-go-client`; `npm test` in `golem-js`; Unity `GameClientTests` and datagram tests through the Unity CLI (unity-cli skill); `npm test` in `golem-threlte` after building `golem-js`.
- **Inspector:** fixtures regenerated; `npm test`, `npm run typecheck` and `npm run build` in `golem-inspector`.
- **End-to-end:** `replication_contract_test.go` with a real WebTransport peer covers an avatar plus a carried entity moving 0→3→0 under datagram mode: no despawn, the ack ends stream-only delivery, and no early or late state is applied out of context.

## Main risks

- **Silent space-0 bugs in the M5/M6 refactor.** The space-0 aliases could hide entity-keyed code that still targets space 0. Mitigation: a review grep plus per-space tests for every entity-keyed API.
- **Restoring several physics worlds is not atomic across them.** A partial commit faults every space and readiness stays closed.
- **Old clients** get no notification and no world clears, but they keep working.
