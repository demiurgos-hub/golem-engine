package golem

import (
	"fmt"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/world"
)

// World returns the world data store of this space. Every handle for the same
// ID returns the same store, created on first use. Values set here override
// Server.World values of the same name for sessions announced in this space:
//
//   - the connect snapshot sends Server.World merged with space 0's store;
//   - a space transition into this space sends, for every name any space's
//     store defines, this space's value, else the Server.World value, else a
//     WorldCleared message so the client drops the previous space's value;
//   - Server.PushWorldData skips sessions whose space overrides the name.
//
// Names listed in ServerConfig.WorldSnapshotExclude stay caller-delivered:
// neither the connect snapshot nor a transition sends or clears them.
// Space(id).PushWorldData or Server.SendStoredWorldData deliver them.
//
// Like Server.World, the store is concurrency-safe; call PushWorldData after
// changing a value that clients already hold.
func (sp *Space) World() *world.Store {
	s := sp.srv
	if store := s.spaceWorld(sp.id); store != nil {
		return store
	}
	s.spaceWorldMu.Lock()
	defer s.spaceWorldMu.Unlock()
	if store := s.spaceWorlds[sp.id]; store != nil {
		return store
	}
	if s.spaceWorlds == nil {
		s.spaceWorlds = make(map[SpaceID]*world.Store)
	}
	store := world.NewStore()
	s.spaceWorlds[sp.id] = store
	s.spaceWorldsUsed.Store(true)
	return store
}

// PushWorldData sends the current value of name from this space's store to
// every session announced in this space. It returns nil when the space's
// store does not define name or no session is connected, and a non-nil error
// when serialization fails or the frame exceeds the 256 KiB reliable cap.
// Sessions in other spaces receive nothing. Called on the tick goroutine, the
// push is ordered with the space transitions replication sends.
func (sp *Space) PushWorldData(name string) error {
	store := sp.srv.spaceWorld(sp.id)
	if store == nil {
		return nil
	}
	d := store.Get(name)
	if d == nil {
		return nil
	}
	data, err := d.MarshalUpdate()
	if err != nil {
		return err
	}
	return sp.srv.listener.SendRawTo(sp.Sessions(), WrapWorldUpdate(data))
}

// spaceWorld returns the store of space without creating it.
func (s *Server) spaceWorld(space SpaceID) *world.Store {
	if !s.spaceWorldsUsed.Load() {
		return nil
	}
	s.spaceWorldMu.RLock()
	defer s.spaceWorldMu.RUnlock()
	return s.spaceWorlds[space]
}

// spaceWorldNames returns the sorted union of the names defined in any
// per-space store, or nil when no space defines world data.
func (s *Server) spaceWorldNames() []string {
	if !s.spaceWorldsUsed.Load() {
		return nil
	}
	s.spaceWorldMu.RLock()
	defer s.spaceWorldMu.RUnlock()
	var names []string
	for _, store := range s.spaceWorlds {
		names = append(names, store.Names()...)
	}
	return sortedUniqueNames(names)
}

// spacesOverridingWorld returns the spaces whose own store defines name, or
// nil when there are none.
func (s *Server) spacesOverridingWorld(name string) map[SpaceID]struct{} {
	if !s.spaceWorldsUsed.Load() {
		return nil
	}
	s.spaceWorldMu.RLock()
	defer s.spaceWorldMu.RUnlock()
	var out map[SpaceID]struct{}
	for space, store := range s.spaceWorlds {
		if store.Get(name) != nil {
			if out == nil {
				out = make(map[SpaceID]struct{})
			}
			out[space] = struct{}{}
		}
	}
	return out
}

// sessionsOutsideSpaces lists the connected sessions whose announced space is
// not in spaces.
func (s *Server) sessionsOutsideSpaces(spaces map[SpaceID]struct{}) []int64 {
	all := s.listener.SessionIDs()
	s.interestMu.Lock()
	defer s.interestMu.Unlock()
	out := all[:0]
	for _, sid := range all {
		if _, skip := spaces[s.sentSpace[sid]]; !skip {
			out = append(out, sid)
		}
	}
	return out
}

// resolveWorld returns the value of name for sessions in space: the space's
// own value, else the Server.World value, else nil.
func (s *Server) resolveWorld(space SpaceID, name string) world.Data {
	if store := s.spaceWorld(space); store != nil {
		if d := store.Get(name); d != nil {
			return d
		}
	}
	return s.World.Get(name)
}

// storedWorldForSession resolves name for the session's announced space.
func (s *Server) storedWorldForSession(sessionID int64, name string) world.Data {
	if !s.spaceWorldsUsed.Load() {
		return s.World.Get(name)
	}
	s.interestMu.Lock()
	space := s.sentSpace[sessionID]
	s.interestMu.Unlock()
	return s.resolveWorld(space, name)
}

// appendResolvedWorldFrames appends, for each name not excluded from
// snapshots, the value space resolves wrapped as a world update, or a
// WorldCleared message when neither the space nor Server.World defines it.
func (s *Server) appendResolvedWorldFrames(frames [][]byte, space SpaceID, names []string) ([][]byte, error) {
	for _, name := range names {
		if _, skip := s.worldSnapshotExclude[name]; skip {
			continue
		}
		d := s.resolveWorld(space, name)
		if d == nil {
			frames = append(frames, WrapWorldCleared(name))
			continue
		}
		data, err := d.MarshalUpdate()
		if err != nil {
			return nil, fmt.Errorf("golem: world data %q for space %d: %w", name, space, err)
		}
		frames = append(frames, WrapWorldUpdate(data))
	}
	return frames, nil
}

// worldSnapshotFrames builds the world part of a connect snapshot. Every
// session connects in space 0, so it sends Server.World merged with space 0's
// store, and clears names only other spaces define so a reconnecting client
// drops what it kept from its previous space. Without per-space world data the
// output is exactly Server.World's non-excluded values in sorted name order.
func (s *Server) worldSnapshotFrames() ([][]byte, error) {
	spaceNames := s.spaceWorldNames()
	if len(spaceNames) == 0 {
		updates, err := s.World.MarshalAllExcept(s.worldSnapshotExclude)
		if err != nil {
			return nil, err
		}
		wrapped := make([][]byte, len(updates))
		for i, u := range updates {
			wrapped[i] = WrapWorldUpdate(u)
		}
		return wrapped, nil
	}
	return s.appendResolvedWorldFrames(nil, 0, sortedUniqueNames(append(s.World.Names(), spaceNames...)))
}

// appendSpaceWorldFrames is the world-data step of a space transition, between
// SpaceChanged and the carried entities. For every name any space's store
// defines, in sorted order, it sends the new space's value, else the
// Server.World value, else WorldCleared. Names only Server.World defines are
// already current on the client and are not resent.
func (s *Server) appendSpaceWorldFrames(_ int64, plan sessionSpacePlan, frames [][]byte) ([][]byte, error) {
	names := s.spaceWorldNames()
	if len(names) == 0 {
		return frames, nil
	}
	return s.appendResolvedWorldFrames(frames, plan.space, names)
}

func sortedUniqueNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	out := names[:1]
	for _, name := range names[1:] {
		if name != out[len(out)-1] {
			out = append(out, name)
		}
	}
	return out
}
