# Changelog

All notable user-facing changes to Golem Engine are documented here.

This project follows the changelog categories from Keep a Changelog: Added, Changed, Deprecated, Removed, Fixed, and Security.

## Unreleased

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.2.1] - 2026-07-07

### Added

- Repository `README.md` with an overview, feature list, layout, quick start, client package pointers, and links to the documentation site.
- `CHANGELOG.md` to track user-facing changes across releases.

### Changed

- **Breaking:** Go module path renamed from `golem-engine` to `github.com/demiurgos-hub/golem-engine`. Update `go.mod` `require` directives and Go import paths in consumer projects, then re-run `golem-bake` so generated Go server, Go client, and Ebiten code uses the new default import paths.
- **Breaking:** Collision and navigation nested modules renamed from `golem.collision` and `golem.nav` to `github.com/demiurgos-hub/golem-engine/golem/collision` and `github.com/demiurgos-hub/golem-engine/golem/nav`. Projects that import these modules directly must update paths; workspace `replace` directives must use the new module paths.
- Unity editor default `golem-bake` command is now `go run github.com/demiurgos-hub/golem-engine/cmd/golem-bake`.
