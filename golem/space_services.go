package golem

import (
	"context"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// This file holds the Space handle's physics and surface navigation methods.
// Each mirrors the Server method of the same name, which acts on space 0, and
// follows the same threading rules. A space has these services only once
// SetPhysicsWorld / SetSurfaceNavigation installed them before Run; until then
// the methods report "not configured" like the Server methods on a server
// without the service. Entity-keyed methods (AttachPhysics, TeleportPhysics,
// SetNavigationDestination, ...) stay on Server and use the entity's space.

// SetPhysicsWorld installs this space's tick-owned physics world before Run.
// The server closes it on shutdown. A world serves one space only.
func (sp *Space) SetPhysicsWorld(world p.World) error {
	return sp.srv.setPhysicsWorld(sp.id, world)
}

// OnPhysicsContact registers this space's accepted-frame and terminal shutdown
// callback. Call at startup.
func (sp *Space) OnPhysicsContact(fn PhysicsContactFunc) error {
	return sp.srv.runtime(sp.id).phys().setContactCallback(fn)
}

// ObservePhysicsFrames installs a read-only, tick-owned observer of this space's
// attempted physics frames (PhysicsFrameMetrics.Space is this space).
func (sp *Space) ObservePhysicsFrames(fn func(PhysicsFrameMetrics)) error {
	return sp.srv.runtime(sp.id).phys().setFrameObserver(fn)
}

// QueuePhysicsCommand schedules a command on this space's world for the next
// tick's input boundary. Tick-owned and bounded by PostQueueCapacity per space.
func (sp *Space) QueuePhysicsCommand(command PhysicsCommand) error {
	return sp.srv.runtime(sp.id).phys().queue(command)
}

// QueuePhysicsGeometry prepares a source lease change for this space's physics
// world and navigation; see Server.QueuePhysicsGeometry.
func (sp *Space) QueuePhysicsGeometry(request PhysicsGeometryRequest) (uint64, error) {
	return sp.srv.queuePhysicsGeometry(sp.srv.runtime(sp.id), request)
}

// ActivatePhysicsGeometry activates prepared physical geometry in this space,
// then queues its navigation; see Server.ActivatePhysicsGeometry.
func (sp *Space) ActivatePhysicsGeometry(change PhysicsGeometryActivation) (uint64, error) {
	return sp.srv.activatePhysicsGeometry(sp.srv.runtime(sp.id), change)
}

// PhysicsGeometry returns this space's accepted source lease for key.
func (sp *Space) PhysicsGeometry(key string) (*content.Instance, bool) {
	return sp.srv.runtime(sp.id).phys().geometryLease(key)
}

// PendingPhysicsGeometry returns this space's copied stable changes still under
// quarantine.
func (sp *Space) PendingPhysicsGeometry() []PhysicsGeometryPending {
	return sp.srv.runtime(sp.id).phys().pendingGeometry()
}

// PhysicsReady reports this space's tick-owned physics readiness.
func (sp *Space) PhysicsReady() (ready, restoring bool, err error) {
	return sp.srv.runtime(sp.id).phys().readiness()
}

// SetSurfaceNavigation installs this space's 3D navigation service before Run,
// with its own backend and path/build worker pools.
func (sp *Space) SetSurfaceNavigation(backend surface.Backend, cfg NavigationConfig) error {
	return sp.srv.setSurfaceNavigation(sp.id, backend, cfg)
}

// NavigationReady reports this space's navigation availability on the tick or
// before Run.
func (sp *Space) NavigationReady() NavigationReadiness {
	return sp.srv.runtime(sp.id).nav().readiness()
}

// NavigationStats reports this space's concurrent-safe queue gauges and
// cumulative counters.
func (sp *Space) NavigationStats() NavigationStats {
	return sp.srv.navigationStats(sp.id)
}

// QueueNavigationBuild queues a build on this space's build pool; see
// Server.QueueNavigationBuild.
func (sp *Space) QueueNavigationBuild(key string, priority int, work func(context.Context) (NavigationBuildResult, error)) (uint64, error) {
	return sp.srv.queueNavigationBuild(sp.srv.runtime(sp.id).nav(), key, priority, work)
}

// CancelNavigationBuild invalidates this space's generations of key.
func (sp *Space) CancelNavigationBuild(key string) {
	sp.srv.runtime(sp.id).nav().cancelBuild(key)
}

// ForgetNavigationBuild removes this space's retained terminal status of key.
func (sp *Space) ForgetNavigationBuild(key string) error {
	return sp.srv.runtime(sp.id).nav().forgetBuild(key)
}

// NavigationBuildStatus returns this space's copied build state for key.
func (sp *Space) NavigationBuildStatus(key string) (NavigationBuildState, bool) {
	return sp.srv.runtime(sp.id).nav().buildStatus(key)
}

// BeginNavigationRestore closes this space's agent readiness barrier.
func (sp *Space) BeginNavigationRestore() error {
	return sp.srv.runtime(sp.id).nav().beginRestore()
}

// CompleteNavigationRestore opens this space's readiness only on success.
func (sp *Space) CompleteNavigationRestore(err error) error {
	return sp.srv.runtime(sp.id).nav().completeRestore(err)
}

// PrepareNavigationFrame runs this space's fair movement admission once for this
// tick; see Server.PrepareNavigationFrame. A space with physics prepares its
// frame inside its own physics step.
func (sp *Space) PrepareNavigationFrame(dt float64) (*NavigationFrame, error) {
	return sp.srv.runtime(sp.id).nav().prepareFrame(dt)
}

// ValidateNavigationFrame checks a frame of this space before simulation.
func (sp *Space) ValidateNavigationFrame(f *NavigationFrame) error {
	return sp.srv.runtime(sp.id).nav().validateFrame(f)
}

// CommitNavigationFrame validates all physical paths of a frame of this space,
// then commits it; see Server.CommitNavigationFrame.
func (sp *Space) CommitNavigationFrame(f *NavigationFrame, results []NavigationMotionResult) error {
	return sp.srv.runtime(sp.id).nav().commitFrame(f, results)
}

// AbortNavigationFrame discards a frame of this space without changing body
// positions.
func (sp *Space) AbortNavigationFrame(f *NavigationFrame) error {
	return sp.srv.runtime(sp.id).nav().abortFrame(f)
}
