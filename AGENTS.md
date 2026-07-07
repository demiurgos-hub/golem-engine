# AGENTS.md

## Project Overview

Golem Engine is a Go module for building multiplayer game backends. It includes a small runtime (`golem`) with a tick-based game loop, entity registry, serialized per-tick deltas, static world data, and a code generator (`golem-bake`) that reads `golem.yaml`, entity YAML, and world YAML to emit protobuf and integration code from templates under `codegen/templates/`.

## Layout

| Path | Role |
| --- | --- |
| `cmd/golem-bake/` | CLI entrypoint: `golem-bake` runs codegen from a consumer project root. |
| `schema/` | `golem.yaml` plus entity, command, and world YAML loading and template data. |
| `codegen/` | `Bake`, embedded templates, and integration targets such as `go-server`, `go-client`, and `js-client`. |
| `golem/` | Runtime loop: `Server`, tick/delta hooks, world data, networking entrypoints, and re-exports for generated code. |
| `golem/registry/` | Thread-safe entity registry and `Entity` interface. |
| `golem/world/` | Thread-safe store for static world data. |
| `golem-go-client/` | Native Go client runtime. |
| `golem-ebiten/` | Ebiten client lifecycle and generated bridge helpers. |
| `golem-js/` | JS/TS runtime npm package (`golem-engine`). |
| `golem-phaser/` | Phaser helper package built on `golem-engine`. |
| `golem-unity/` | Unity client package. |

## Subsystem Boundaries

- Tooling is `cmd/`, `schema/`, and `codegen/`. Runtime library code is `golem/`, `golem/registry/`, and `golem/world/`.
- `golem/registry` and `golem/world` do not import `golem`. Prefer depending on `golem/registry` for storage/entity-only work, and `golem/world` for world-data-only work.
- Consumer repos hold `golem.yaml` and schema YAML at their root. This engine repo may omit them.
- Cursor subsystem rules live in `.cursor/rules/subsystem-*.mdc`. When a subsystem's layout or responsibilities change, update the matching rule description, boundaries, and globs.

## Build and Test

- Go root module: run `go test ./...` from the repo root for changes in the main module.
- Nested Go modules under `golem/collision/` and `golem/nav/` are developed with a local `go.work`. Do not commit `go.work` or `go.work.sum`.
- JS runtime: run `npm test` in `golem-js/`.
- Phaser package: run `npm run build` in `golem-phaser/`.
- `dist/` and other build outputs are generated artifacts; do not hand-edit them.

## Code Style

- When adding a new exported Go function or method, add a brief godoc comment (`// Name ...`) describing what it does.
- When editing an existing exported Go function or method, update its comment if behavior, side effects, or caller expectations change.
- Inside functions, comment sparingly. Prefer comments that explain invariants, workarounds, API quirks, or performance tradeoffs over comments that restate the next line.
- Keep changes scoped to the subsystem being edited and follow existing local patterns before introducing new abstractions.

## Documentation

- User-facing docs live in the separate Astro Starlight website repo under its `docs/` directory, not under this engine repo.
- When public behavior changes, mention whether the website docs need a matching update.
- Documentation must match current public behavior in `cmd/golem-bake`, `schema/`, `codegen/`, `golem/`, and `golem/registry/`.
