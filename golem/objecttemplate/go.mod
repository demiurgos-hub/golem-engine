module github.com/demiurgos-hub/golem-engine/golem/objecttemplate

go 1.27.1

require (
	github.com/demiurgos-hub/golem-engine/golem/collision v0.3.1-0.20261115012239-c9fd88dc8adc
	github.com/demiurgos-hub/golem-engine/golem/geometry v0.0.0-20261115012239-c9fd88dc8adc
	github.com/demiurgos-hub/golem-engine/golem/physics3d v0.0.0-20261121202043-e7dc52314ca4
	github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d v0.0.0-20261115012239-c9fd88dc8adc
	github.com/demiurgos-hub/navmesh-go v0.0.0-20261116105330-b17cd9f21c78
)

require github.com/gataengine/box3d-go v0.0.0-20261019212002-2a00e926a8d9 // indirect

// The Box3D fork preserves upstream's module path. Go does not inherit the
// backend dependency's replacement, so tests that use box3d select it here.
replace github.com/gataengine/box3d-go => github.com/demiurgos-hub/box3d-go v0.0.0-20261114223637-579b93c759ee
