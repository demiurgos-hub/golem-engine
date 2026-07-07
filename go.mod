module github.com/demiurgos-hub/golem-engine

go 1.25.4

require (
	github.com/coder/websocket v1.8.14
	github.com/hajimehoshi/ebiten/v2 v2.9.9
	github.com/quic-go/quic-go v0.59.0
	github.com/quic-go/webtransport-go v0.10.0
	// collision/nav backends are nested modules resolved by go.work during development.
	// Consumers building outside a workspace need published versions of these.
	github.com/demiurgos-hub/golem-engine/golem/collision v0.0.0
	github.com/demiurgos-hub/golem-engine/golem/nav v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)
