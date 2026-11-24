module example.com/golem-threlte

go 1.25.4

require github.com/demiurgos-hub/golem-engine v0.3.0

require (
	github.com/alitto/pond/v2 v2.7.1 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/demiurgos-hub/golem-engine/golem/collision v0.3.1-0.20261121202043-e7dc52314ca4 // indirect
	github.com/demiurgos-hub/golem-engine/golem/geometry v0.0.0-20261113160310-d88e90435bea // indirect
	github.com/demiurgos-hub/golem-engine/golem/nav v0.3.1-0.20261113161118-0c6a59582abb // indirect
	github.com/demiurgos-hub/golem-engine/golem/physics3d v0.0.0-20261121202043-e7dc52314ca4 // indirect
	github.com/dunglas/httpsfv v1.1.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/quic-go/webtransport-go v0.10.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/demiurgos-hub/golem-engine => ../..

replace github.com/demiurgos-hub/golem-engine/golem/collision => ../../golem/collision

replace github.com/demiurgos-hub/golem-engine/golem/nav => ../../golem/nav

replace github.com/demiurgos-hub/golem-engine/golem/geometry => ../../golem/geometry

replace github.com/demiurgos-hub/golem-engine/golem/physics3d => ../../golem/physics3d
