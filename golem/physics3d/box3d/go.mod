module github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d

go 1.27.1

require (
	github.com/demiurgos-hub/golem-engine/golem/physics3d v0.0.0-20261113161118-0c6a59582abb
	github.com/gataengine/box3d-go v0.0.0-20261019212002-2a00e926a8d9
)

require github.com/demiurgos-hub/golem-engine/golem/geometry v0.0.0-20261113160310-d88e90435bea // indirect

// The prepared fork preserves upstream's module path. Executable consumers
// must select this fork too because Go does not inherit dependency replacements.
replace github.com/gataengine/box3d-go => github.com/demiurgos-hub/box3d-go v0.0.0-20261112194147-89c6286a8d68
