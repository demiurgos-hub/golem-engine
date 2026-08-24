module github.com/demiurgos-hub/golem-engine

go 1.25.4

require (
	github.com/alitto/pond/v2 v2.7.1
	github.com/coder/websocket v1.8.14
	// collision/nav backends are nested modules resolved by go.work during development.
	// Consumers building outside a workspace need published versions of these.
	github.com/demiurgos-hub/golem-engine/golem/collision v0.3.0
	github.com/demiurgos-hub/golem-engine/golem/nav v0.3.0
	github.com/hajimehoshi/ebiten/v2 v2.9.9
	github.com/quic-go/quic-go v0.59.0
	github.com/quic-go/webtransport-go v0.10.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/dunglas/httpsfv v1.1.0 // indirect
	github.com/ebitengine/gomobile v0.0.0-20250923094054-ea854a63cce1 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
