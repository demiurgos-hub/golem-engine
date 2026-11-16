// Package objecttemplate loads reusable, root-local object definitions exported
// once per prefab and places copies of them at runtime.
//
// A Catalog holds templates for one project. A 3D template carries physics3d
// bodies, colliders and optional navigation geometry; a 2D template carries
// circle and box shapes for collision.Backend. Templates never store world
// positions: hosts read placements from their own data and call Placer3D.Place,
// Placer2D.Place and Template.PlaceNavigation with a root transform.
//
// Placer3D registers backend geometry once per template collider and scale and
// shares that shape across every placement that uses it. Placements own only
// their bodies and colliders, so adding and removing them at runtime does not
// re-cook geometry.
package objecttemplate
