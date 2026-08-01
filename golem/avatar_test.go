package golem

import (
	"sync"
	"sync/atomic"
	"testing"

	golemnet "github.com/demiurgos-hub/golem-engine/golem/net"
)

type avatarEntity struct {
	id      int64
	removed bool
}

func (e *avatarEntity) EntityID() int64              { return e.id }
func (e *avatarEntity) SetEntityID(id int64)         { e.id = id }
func (e *avatarEntity) TypeName() string             { return "avatar" }
func (e *avatarEntity) Position() (float32, float32) { return 0, 0 }
func (e *avatarEntity) IsGlobal() bool               { return false }
func (e *avatarEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *avatarEntity) FullUpdate() ([]byte, error)  { return nil, nil }
func (e *avatarEntity) OnRemove()                    { e.removed = true }

type otherAvatarEntity struct {
	avatarEntity
}

func (e *otherAvatarEntity) TypeName() string { return "other-avatar" }

func TestSpawnAvatarAndAvatarOf(t *testing.T) {
	srv := NewServer(ServerConfig{})
	sess := &Session{ID: 7}
	e := &avatarEntity{}

	if err := srv.SpawnAvatar(sess, e, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}
	if e.id == 0 {
		t.Fatal("expected auto-assigned entity ID")
	}
	owner, owned := srv.Owner(e.id)
	if !owned || owner != 7 {
		t.Fatalf("Owner = (%d, %v), want (7, true)", owner, owned)
	}

	got, ok := srv.Avatar(7)
	if !ok || got != e {
		t.Fatalf("Avatar(7) = (%v, %v), want entity", got, ok)
	}
	typed, ok := AvatarOf[*avatarEntity](srv, 7)
	if !ok || typed != e {
		t.Fatalf("AvatarOf = (%v, %v), want entity", typed, ok)
	}
}

func TestSpawnAvatarAssignsFOI(t *testing.T) {
	srv := NewServer(ServerConfig{CellSize: 10})
	sess := &Session{ID: 3}
	e := &avatarEntity{}

	if err := srv.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 12, FOIMargin: 2}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}
	if !srv.hasFOI(3) {
		t.Fatal("expected FOI assigned")
	}
}

func TestSpawnAvatarFOIRequiresInterest(t *testing.T) {
	srv := NewServer(ServerConfig{}) // CellSize == 0
	sess := &Session{ID: 1}
	e := &avatarEntity{}

	err := srv.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 5})
	if err == nil {
		t.Fatal("expected error when FOI requested without interest")
	}
	if srv.Len() != 0 {
		t.Fatalf("entity should be rolled back, Len=%d", srv.Len())
	}
	if _, ok := srv.Avatar(1); ok {
		t.Fatal("avatar index should not remain after FOI failure")
	}
}

func TestSpawnAvatarRejectsNegativeFOIRadius(t *testing.T) {
	srv := NewServer(ServerConfig{CellSize: 10})
	err := srv.SpawnAvatar(&Session{ID: 1}, &avatarEntity{}, AvatarOptions{FOIRadius: -1})
	if err == nil {
		t.Fatal("expected error for negative FOIRadius")
	}
	if srv.Len() != 0 {
		t.Fatalf("Len=%d, want 0", srv.Len())
	}
}

func TestSpawnAvatarRejectsDuplicateLiveAvatar(t *testing.T) {
	srv := NewServer(ServerConfig{})
	sess := &Session{ID: 9}
	first := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, first, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar first: %v", err)
	}
	second := &avatarEntity{}
	err := srv.SpawnAvatar(sess, second, AvatarOptions{})
	if err == nil {
		t.Fatal("expected duplicate SpawnAvatar error")
	}
	if srv.Len() != 1 {
		t.Fatalf("Len=%d, want 1", srv.Len())
	}
	got, ok := srv.Avatar(9)
	if !ok || got != first {
		t.Fatal("original avatar must remain")
	}
}

func TestDisconnectCleansAvatarAfterCallback(t *testing.T) {
	srv := NewServer(ServerConfig{
		CellSize:        8,
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	sess := &Session{ID: 11}
	e := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 10, FOIMargin: 1}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}

	var order []string
	var avatarDuringCallback Entity
	var avatarOK bool
	srv.OnDisconnect(func(s *Session) {
		if s == nil {
			t.Fatal("disconnect session must be non-nil")
		}
		avatarDuringCallback, avatarOK = srv.Avatar(s.ID)
		order = append(order, "callback")
		if e.removed {
			order = append(order, "removed-too-early")
		}
	})

	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: sess}
	srv.drainMessages()
	order = append(order, "after-drain")

	if !avatarOK || avatarDuringCallback != e {
		t.Fatalf("avatar during OnDisconnect = (%v, %v), want live entity", avatarDuringCallback, avatarOK)
	}
	if !e.removed {
		t.Fatal("expected avatar entity deleted after disconnect cleanup")
	}
	if _, ok := srv.Get(e.id); ok {
		t.Fatal("entity still registered after disconnect")
	}
	if _, ok := srv.Avatar(11); ok {
		t.Fatal("avatar index should be cleared")
	}
	if srv.hasFOI(11) {
		t.Fatal("FOI should be removed")
	}
	if len(order) < 2 || order[0] != "callback" {
		t.Fatalf("order=%v, want callback first", order)
	}
}

func TestDisconnectToleratesManualDeleteInCallback(t *testing.T) {
	srv := NewServer(ServerConfig{
		CellSize:        8,
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	sess := &Session{ID: 12}
	e := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 4}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}

	srv.OnDisconnect(func(s *Session) {
		av, ok := srv.Avatar(s.ID)
		if !ok {
			t.Fatal("avatar missing in callback")
		}
		srv.DeleteEntity(av.EntityID())
		srv.DeleteEntity(av.EntityID()) // repeated delete
	})

	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: sess}
	srv.drainMessages() // must not panic; FOI cleared

	if srv.hasFOI(12) {
		t.Fatal("FOI should be removed even when callback deleted avatar")
	}
	if _, ok := srv.Avatar(12); ok {
		t.Fatal("avatar index should stay clear")
	}
}

func TestDisconnectReapsReplacementSpawnedInCallback(t *testing.T) {
	srv := NewServer(ServerConfig{
		CellSize:        8,
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	sess := &Session{ID: 13}
	original := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, original, AvatarOptions{FOIRadius: 6, FOIMargin: 1}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}

	replacement := &avatarEntity{}
	srv.OnDisconnect(func(s *Session) {
		srv.DeleteEntity(original.id)
		if err := srv.SpawnAvatar(s, replacement, AvatarOptions{FOIRadius: 6, FOIMargin: 1}); err != nil {
			t.Fatalf("replacement SpawnAvatar in OnDisconnect: %v", err)
		}
		if _, ok := srv.Avatar(s.ID); !ok {
			t.Fatal("replacement should be visible during OnDisconnect")
		}
	})

	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: sess}
	srv.drainMessages()

	if !original.removed {
		t.Fatal("original avatar should be removed")
	}
	if !replacement.removed {
		t.Fatal("replacement avatar spawned in OnDisconnect must be reaped")
	}
	if srv.Len() != 0 {
		t.Fatalf("Len=%d after disconnect, want 0 (no leaked avatar)", srv.Len())
	}
	if _, ok := srv.Avatar(13); ok {
		t.Fatal("avatar index must be clear after disconnect")
	}
	if srv.hasFOI(13) {
		t.Fatal("FOI must be cleared after disconnect")
	}
}

func TestDisconnectSessionNonNilInvariant(t *testing.T) {
	srv := NewServer(ServerConfig{
		Transport:       golemnet.TransportWebSocket,
		StateUpdateLane: StateUpdateLaneStream,
	})
	sess := &Session{ID: 42}
	var saw *Session
	srv.OnDisconnect(func(s *Session) {
		saw = s
		if s == nil {
			t.Fatal("OnDisconnect must receive non-nil session")
		}
	})
	srv.msgQueue <- pendingMsg{kind: msgDisconnect, sess: sess}
	srv.drainMessages()
	if saw != sess {
		t.Fatalf("OnDisconnect session = %v, want %v", saw, sess)
	}
}

func TestDeleteEntityClearsAvatarIndexes(t *testing.T) {
	srv := NewServer(ServerConfig{})
	sess := &Session{ID: 4}
	e := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, e, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}

	srv.DeleteEntity(e.id)
	srv.DeleteEntity(e.id) // repeated

	if _, ok := srv.Avatar(4); ok {
		t.Fatal("Avatar should be gone after DeleteEntity")
	}
	if _, ok := srv.Get(e.id); ok {
		t.Fatal("entity should be gone")
	}

	// Session can spawn a new avatar after mid-session delete.
	next := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, next, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar after delete: %v", err)
	}
	got, ok := AvatarOf[*avatarEntity](srv, 4)
	if !ok || got != next {
		t.Fatalf("AvatarOf after re-spawn = (%v, %v)", got, ok)
	}
}

func TestAvatarOfWrongType(t *testing.T) {
	srv := NewServer(ServerConfig{})
	sess := &Session{ID: 5}
	e := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, e, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}
	if _, ok := AvatarOf[*otherAvatarEntity](srv, 5); ok {
		t.Fatal("AvatarOf wrong type should fail")
	}
	if _, ok := AvatarOf[*avatarEntity](srv, 5); !ok {
		t.Fatal("AvatarOf correct type should succeed")
	}
}

func TestSetOwnerDoesNotTransferAvatar(t *testing.T) {
	srv := NewServer(ServerConfig{})
	sess := &Session{ID: 20}
	e := &avatarEntity{}
	if err := srv.SpawnAvatar(sess, e, AvatarOptions{}); err != nil {
		t.Fatalf("SpawnAvatar: %v", err)
	}
	if !srv.SetOwner(e.id, 99) {
		t.Fatal("SetOwner failed")
	}
	owner, ok := srv.Owner(e.id)
	if !ok || owner != 99 {
		t.Fatalf("Owner = (%d, %v), want (99, true)", owner, ok)
	}
	got, ok := srv.Avatar(20)
	if !ok || got != e {
		t.Fatal("avatar identity must remain on original session after SetOwner")
	}
	if _, ok := srv.Avatar(99); ok {
		t.Fatal("SetOwner must not create avatar binding for new owner session")
	}
}

func TestConcurrentAvatarLookupAndDelete(t *testing.T) {
	srv := NewServer(ServerConfig{CellSize: 16})
	const n = 32
	sessions := make([]*Session, n)
	entities := make([]*avatarEntity, n)
	seen := make([]chan struct{}, n)
	for i := 0; i < n; i++ {
		sessions[i] = &Session{ID: int64(i + 1)}
		entities[i] = &avatarEntity{}
		seen[i] = make(chan struct{})
		if err := srv.SpawnAvatar(sessions[i], entities[i], AvatarOptions{FOIRadius: 8, FOIMargin: 1}); err != nil {
			t.Fatalf("SpawnAvatar(%d): %v", i, err)
		}
		if !srv.hasFOI(sessions[i].ID) {
			t.Fatalf("session %d missing FOI after spawn", sessions[i].ID)
		}
	}

	var (
		wg        sync.WaitGroup
		lookupsOK atomic.Int64
	)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		i := i
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			sid := sessions[i].ID
			entityID := entities[i].id

			av, ok := srv.Avatar(sid)
			if !ok || av.EntityID() != entityID {
				t.Errorf("session %d: pre-delete Avatar = (%v, %v), want entity %d", sid, av, ok, entityID)
				close(seen[i])
				return
			}
			if typed, ok := AvatarOf[*avatarEntity](srv, sid); !ok || typed.EntityID() != entityID {
				t.Errorf("session %d: pre-delete AvatarOf mismatch", sid)
				close(seen[i])
				return
			}
			lookupsOK.Add(1)
			close(seen[i])

			// Indexes clear before registry delete, so Avatar may briefly return
			// false while Get still succeeds; keep observing until both settle.
			for {
				av, ok := srv.Avatar(sid)
				_, entityOK := srv.Get(entityID)
				if !ok && !entityOK {
					if typed, tok := AvatarOf[*avatarEntity](srv, sid); tok || typed != nil {
						t.Errorf("session %d: AvatarOf after delete = (%v, %v)", sid, typed, tok)
					}
					return
				}
				if ok {
					if av.EntityID() != entityID {
						t.Errorf("session %d: Avatar id=%d, want %d", sid, av.EntityID(), entityID)
						return
					}
					lookupsOK.Add(1)
				}
			}
		}()

		go func() {
			defer wg.Done()
			<-start
			<-seen[i]
			sid := sessions[i].ID
			entityID := entities[i].id
			srv.DeleteEntity(entityID)
			srv.DeleteEntity(entityID)
			// After DeleteEntity returns, both index and entity must be gone.
			if _, ok := srv.Avatar(sid); ok {
				t.Errorf("session %d: avatar index remained after DeleteEntity", sid)
			}
			if _, ok := srv.Get(entityID); ok {
				t.Errorf("session %d: entity %d remained after DeleteEntity", sid, entityID)
			}
		}()
	}

	close(start)
	wg.Wait()

	if lookupsOK.Load() < int64(n) {
		t.Fatalf("lookupsOK=%d, want at least %d pre-delete observations", lookupsOK.Load(), n)
	}
	if srv.Len() != 0 {
		t.Fatalf("Len=%d after concurrent deletes, want 0", srv.Len())
	}
	for i := 0; i < n; i++ {
		if _, ok := srv.Avatar(sessions[i].ID); ok {
			t.Fatalf("session %d still has avatar after test", sessions[i].ID)
		}
	}
}

func TestConcurrentSameSessionSpawnAvatar(t *testing.T) {
	srv := NewServer(ServerConfig{CellSize: 16})
	sess := &Session{ID: 99}
	const n = 24
	var (
		wg       sync.WaitGroup
		success  atomic.Int32
		start    = make(chan struct{})
		winnerID atomic.Int64
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e := &avatarEntity{}
			if err := srv.SpawnAvatar(sess, e, AvatarOptions{FOIRadius: 10, FOIMargin: 2}); err == nil {
				success.Add(1)
				winnerID.Store(e.id)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := success.Load(); got != 1 {
		t.Fatalf("successful SpawnAvatar count = %d, want exactly 1", got)
	}
	if srv.Len() != 1 {
		t.Fatalf("Len=%d, want 1 (no leaked entities)", srv.Len())
	}
	av, ok := srv.Avatar(99)
	if !ok || av.EntityID() != winnerID.Load() {
		t.Fatalf("Avatar(99) = (%v, %v), want entity %d", av, ok, winnerID.Load())
	}
	if !srv.hasFOI(99) {
		t.Fatal("winner must retain FOI")
	}
	if knowing := srv.SessionsKnowing(winnerID.Load()); len(knowing) != 0 {
		// Known set fills on interest ticks; FOI assignment alone does not populate Known.
		// Just ensure SessionsKnowing is race-safe to call alongside spawn.
		_ = knowing
	}
}

func TestSpawnAvatarNilSession(t *testing.T) {
	srv := NewServer(ServerConfig{})
	if err := srv.SpawnAvatar(nil, &avatarEntity{}, AvatarOptions{}); err == nil {
		t.Fatal("expected error for nil session")
	}
}
