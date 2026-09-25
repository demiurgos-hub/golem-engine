module example.com/golem-threlte

go 1.25.4

require github.com/demiurgos-hub/golem-engine v0.3.0

require (
	github.com/alitto/pond/v2 v2.7.1 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/demiurgos-hub/golem-engine/golem/collision v0.3.0 // indirect
	github.com/demiurgos-hub/golem-engine/golem/nav v0.3.0 // indirect
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
