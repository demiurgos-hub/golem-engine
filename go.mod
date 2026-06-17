module golem-engine

go 1.25.4

require (
	github.com/coder/websocket v1.8.14
	github.com/hajimehoshi/ebiten/v2 v2.9.9
	github.com/quic-go/quic-go v0.59.0
	github.com/quic-go/webtransport-go v0.10.0
	// golem.collision is a local sibling module resolved by go.work.
	// Consumers building outside a workspace need a replace directive or a published version.
	golem.collision v0.0.0
	// golem.nav is a local sibling module resolved by go.work.
	golem.nav v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)
