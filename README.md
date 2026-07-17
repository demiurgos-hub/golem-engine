# Golem Engine

**Multiplayer, done right.**

Golem Engine lets you build server-authoritative multiplayer backends in Go, and generate type-safe clients for multiple languages and engines from one shared protocol. It is built for worlds with high player and entity density, where the server owns the truth and clients just send input.

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/demiurgos-hub/golem-engine.svg)](https://pkg.go.dev/github.com/demiurgos-hub/golem-engine)
[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](go.mod)
[![npm](https://img.shields.io/npm/v/golem-engine.svg)](https://www.npmjs.com/package/golem-engine)
[![Release](https://img.shields.io/badge/release-v0.2.1-orange.svg)](CHANGELOG.md)

[Website](https://golemengine.dev/) · [Documentation](https://golemengine.dev/docs/) · [Changelog](CHANGELOG.md) · [License](LICENSE)

---

## What is Golem?

Clients send input. The Go server runs the authoritative simulation and decides what is true. Interest management (field-of-interest) streams each player only what is near them, and per-session ownership lets a client drive its own entity.

```
 clients                      authoritative Go server                filtered updates
 ─────────                    ───────────────────────                ────────────────
 MoveCommand  ─commands─▶     validate → simulate → diff  ─state─▶   player.position
 CastCommand                  ( physics · nav · interest )           nearby entities
 AimCommand                   tick 12842                             ( hidden entities )
```

You describe your entities, commands, and events in YAML. `golem-bake` generates the server scaffolding, an `entities.proto` reference, and type-safe client classes for every target you enable.

## Features

- **Authoritative by default.** The server runs the simulation and owns state; per-session ownership lets a client drive its own entity.
- **Schema-driven codegen.** Define entities, commands, and events in YAML; generate server scaffolding, `entities.proto`, and typed clients with zero boilerplate.
- **Multi-stack clients.** One protocol shared across Go (Ebiten), JavaScript/TypeScript (Phaser), and C# (Unity).
- **Built for density.** A fixed tick loop and compact binary deltas keep hundreds of players and thousands of entities in sync.
- **Interest management.** Field-of-interest sends each player only what they are meant to see.
- **Physics & navigation.** Both sit behind interfaces with swappable backends, so you can trade speed for depth as your game grows.
- **Batteries included.** World snapshots, map/tile serving for 2D games, and other helpers that cut plumbing.
- **Modern networking.** WebSocket and WebTransport (HTTP/3) with reliable/ordered datagram delivery modes over UDP.

## Repository layout

| Path | Role |
| --- | --- |
| `cmd/golem-bake/` | Codegen CLI: runs `golem-bake` from a consumer project root. |
| `schema/` | `golem.yaml` plus entity, command, event, and world YAML loading and template data. |
| `codegen/` | `Bake`, embedded templates, and integration targets (`go-server`, `go-client`, `js-client`, `phaser`, `ebiten`, `csharp-client`, `unity`). |
| `golem/` | Runtime loop: `Server`, tick/delta hooks, world data, and networking. |
| `golem/registry/` | Thread-safe entity registry and the `Entity` interface. |
| `golem/world/` | Thread-safe store for static world data. |
| `golem/collision/`, `golem/nav/` | Physics and navigation, with swappable backends (nested Go modules). |
| `golem-go-client/` | Native Go client runtime. |
| `golem-ebiten/` | Ebiten client lifecycle and generated bridge helpers. |
| `golem-js/` | JS/TS runtime, published to npm as `golem-engine`. |
| `golem-phaser/` | Phaser 4 helper package built on `golem-engine`. |
| `golem-unity/` | Unity client package. |

> **Note:** `golem/collision/` and `golem/nav/` (and their backends) are nested Go modules resolved by a local `go.work` during development. Consumers building outside a workspace need published versions of these modules.

## Quick start

### Install the CLI

```bash
go install github.com/demiurgos-hub/golem-engine/cmd/golem-bake@latest
```

### Describe your project

Add a `golem.yaml` at your project root:

```yaml
entity_schema: schemas/entities/
command_schema: schemas/commands/
world_schema: schemas/world/
event_schema: schemas/events/
types_schema: schemas/types/

simulation:
  dimensions: 2

proto:
  package: game
  go_package: example.com/game/pb
  out: gen/pb

integrations:
  go-server:
    out: gen/server
    package: server
  js-client:
    out: web/src/gen
```

Define an entity (for example `schemas/entities/player.yaml`):

```yaml
entity: Player
vars:
  health: { type: int32, tag: 1 }
  inventory: { type: list<Item>, tag: 2 }
  equipment: { type: dict<string, Item>, tag: 3 }
```

### Generate

Run the CLI from your project root:

```bash
golem-bake
```

This emits the `entities.proto` reference, server scaffolding, and typed clients for every integration you enabled. See [Minimal server wiring](https://golemengine.dev/docs/) for how to stand up the `Server` and tick loop.

## Client packages

| Stack | Package | Install |
| --- | --- | --- |
| JavaScript / TypeScript | `golem-engine` | `npm install golem-engine` |
| Phaser 4 | `golem-phaser` | `npm install golem-phaser` |
| Go / Ebiten | `golem-ebiten` | `go get github.com/demiurgos-hub/golem-engine/golem-ebiten` |
| Unity (C#) | `io.demiurgos.golemengine` | via Unity Package Manager |

Generated client classes import these runtimes and share one binary protocol with the server.

## Documentation

Full documentation lives at **[golemengine.dev/docs](https://golemengine.dev/docs/)**, including:

- **Configuration & schema** — `golem.yaml`, entity/command/world/event schemas, custom types & collections.
- **Server & simulation** — game loop, creation/destruction, authority, interest management, snapshots, profiling.
- **Physics** — backends & wiring, colliders, contacts & events, overlap/cast queries, 3D collision.
- **Navigation** — backends & wiring, map sources, `NavAgent`.
- **Clients & networking** — channels & transports, state updates, client commands, and per-stack integration guides.

## Building & testing

```bash
# Root Go module
go test ./...

# JS runtime
cd golem-js && npm test

# Phaser package
cd golem-phaser && npm test
```

The nested modules under `golem/collision/` and `golem/nav/` are developed with a local `go.work`. Do not commit `go.work` or `go.work.sum`. The npm package `dist/` directories are generated by `npm run build` and `npm prepack`; do not hand-edit or commit them.

## Status

Golem Engine is at **v0.2.1**. Public APIs and generated output may still change between releases; see [`CHANGELOG.md`](CHANGELOG.md) for details.

## Contributing

Contributions are welcome. Please keep changes scoped to the subsystem you are editing and follow the existing local patterns before introducing new abstractions. Run the relevant tests above before opening a pull request.

## License

Licensed under the [Apache License 2.0](LICENSE).

---

Golem Engine is a property of Demiurgos B.V.
