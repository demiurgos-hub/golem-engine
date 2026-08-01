package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

// TestGenerateGoSharedTemplateIncludesRestoreWrappers verifies that the shared
// Go helper template emits typed restore wrapper registration hooks.
func TestGenerateGoSharedTemplateIncludesRestoreWrappers(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Entities: []schema.EntityData{
			{Name: "Monster"},
			{Name: "Player"},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"restoreMonsterWrapper func(*SyncedMonster) golem.Entity",
		"restorePlayerWrapper func(*SyncedPlayer) golem.Entity",
		"func RegisterMonsterWrapper(fn func(*SyncedMonster) golem.Entity) {",
		"func RegisterPlayerWrapper(fn func(*SyncedPlayer) golem.Entity) {",
		"if restoreMonsterWrapper != nil {",
		"return restoreMonsterWrapper(e), nil",
		"if restorePlayerWrapper != nil {",
		"return restorePlayerWrapper(e), nil",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoSharedTemplateIncludesDispatchPacket(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Commands: []schema.CommandData{
			{
				Name:      "Ping",
				LowerName: "ping",
				Target:    "session",
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"func (r *CommandRouter) DispatchPacket(sess *golem.Session, data []byte) error {",
		"var packet ClientPacket",
		"sess.Close()",
		"if err := r.Dispatch(sess.ID, frame); err != nil {",
		"func (r *CommandRouter) DispatchDatagram(sess *golem.Session, data []byte) error {",
		"func (r *CommandRouter) DispatchReliableUnordered(sess *golem.Session, data []byte) error {",
		"func (r *CommandRouter) DispatchReliableOrdered(sess *golem.Session, data []byte) error {",
		"type CommandErrorHandler func(sess *golem.Session, err error)",
		"func (r *CommandRouter) BindStreamHandler(onError CommandErrorHandler) {",
		"r.srv.OnMessage(func(sess *golem.Session, data []byte) {",
		"if err := r.DispatchPacket(sess, data); err != nil && onError != nil {",
		"func (r *CommandRouter) BindReliableUnorderedHandler(onError CommandErrorHandler) {",
		"r.srv.OnReliableUnordered(func(sess *golem.Session, data []byte) {",
		"func (r *CommandRouter) BindReliableOrderedHandler(onError CommandErrorHandler) {",
		"r.srv.OnReliableOrdered(func(sess *golem.Session, data []byte) {",
		"func (r *CommandRouter) BindAllHandlers(onError CommandErrorHandler) {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoSharedTemplateIncludesRuntimeHelper(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Commands: []schema.CommandData{
			{Name: "Ping", LowerName: "ping", Target: "session"},
		},
		Events: []schema.EventData{
			{Name: "Toast", Target: "broadcast"},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"type Runtime struct {",
		"Server *golem.Server",
		"Commands *CommandRouter",
		"Events *EventBroadcaster",
		"func NewServer(cfg golem.ServerConfig) (*golem.Server, *Runtime) {",
		"srv.SetRemovalSerializer(MarshalEntityRemoved)",
		"rt.Commands = NewCommandRouter(srv)",
		"rt.Events = NewEventBroadcaster(srv)",
		"func (r *Runtime) SaveSnapshot(path string) <-chan error {",
		"return r.Server.SaveSnapshot(SchemaFingerprint, path)",
		"func (r *Runtime) LoadSnapshot(path string) error {",
		"return r.Server.LoadSnapshot(path, SchemaFingerprint, RestoreEntity)",
		"func (r *Runtime) LoadSnapshotIfExists(path string) (bool, error) {",
		"if errors.Is(err, os.ErrNotExist) {",
		"func (r *Runtime) RunSnapshotAutosave(ctx context.Context, path string, interval time.Duration) error {",
		"func startSnapshotAutosave(ctx context.Context, interval time.Duration, save func() <-chan error, newTicker func(time.Duration) *time.Ticker) error {",
		"func runSnapshotAutosave(ctx context.Context, ticks <-chan time.Time, save func() <-chan error) error {",
		"if err := ctx.Err(); err != nil {",
		`"context"`,
		`"errors"`,
		`"os"`,
		`"time"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoSharedTemplateSnapshotHelpersWithoutOptionalFeatures(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		`"context"`,
		`"errors"`,
		`"os"`,
		`"time"`,
		"func (r *Runtime) LoadSnapshotIfExists(path string) (bool, error) {",
		"func (r *Runtime) RunSnapshotAutosave(ctx context.Context, path string, interval time.Duration) error {",
		"return startSnapshotAutosave(ctx, interval, func() <-chan error {",
		"time.NewTicker",
		"default:",
		"_ = p",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("minimal shared helper missing %q\n%s", want, content)
		}
	}
	for _, ban := range []string{
		"var _ = errors.Is",
		"CommandRouter",
		"EventBroadcaster",
		"EnableCollision",
	} {
		if strings.Contains(content, ban) {
			t.Fatalf("minimal shared helper unexpectedly contains %q", ban)
		}
	}
}

func TestGenerateGoSharedTemplateMarshalEntityRemovedIncludesRevision(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"func MarshalEntityRemoved(entityID int64, revision uint64) ([]byte, error) {",
		"EntityRemoved: &EntityRemoved{EntityId: entityID, Revision: revision}",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoServerTemplateIncludesStateRevision(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:      "Player",
		LowerName: "player",
		TickVars: []schema.VarInfo{
			{
				GoName:      "Health",
				FieldName:   "health",
				GoType:      "int32",
				ProtoType:   "int32",
				ProtoHelper: "Int32",
				BitIndex:    2,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"revision      uint64",
		"func (s *SyncedPlayer) StateRevision() uint64 {",
		"s.revision++",
		"Revision: s.revision",
		"s.revision = st.Revision",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated server helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoServerTemplateIncludesServerBinding(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:      "Player",
		LowerName: "player",
		TickVars: []schema.VarInfo{
			{
				GoName:      "Health",
				FieldName:   "health",
				GoType:      "int32",
				ProtoType:   "int32",
				ProtoHelper: "Int32",
				BitIndex:    2,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"srv *golem.Server",
		"func (s *SyncedPlayer) BindServer(srv *golem.Server) {",
		"func (s *SyncedPlayer) Server() *golem.Server {",
		"or nil if the entity has not yet",
		"Wrapper types that embed *SyncedPlayer",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated server helper missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "s.srv = st.") {
		t.Fatal("ApplyState must not hydrate runtime-only srv from snapshot state")
	}
	if strings.Contains(content, "Srv:") || strings.Contains(content, "srv:") {
		t.Fatal("constructor/FullState must not treat srv as replicated state")
	}
}

func TestGenerateGoServerTemplateIncludesMaskAwareDeltas(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:      "Player",
		LowerName: "player",
		TickVars: []schema.VarInfo{
			{
				GoName:      "Health",
				FieldName:   "health",
				GoType:      "int32",
				ProtoType:   "int32",
				ProtoHelper: "Int32",
				BitIndex:    2,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"lastFlushMask uint64",
		"func (s *SyncedPlayer) LastFlushMask() uint64 {",
		"func (s *SyncedPlayer) deltaFromMaskLocked(mask uint64) *PlayerDelta {",
		"if mask&playerFieldHealth != 0 {",
		"s.lastFlushMask = mask",
		"func (s *SyncedPlayer) MarshalDeltaMask(mask uint64) ([]byte, error) {",
		"s.lastFlushMask = 0",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated server helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoServerTemplateIncludesFloat32PositionsAndCompactDelta(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:      "Player",
		LowerName: "player",
		TickVars: []schema.VarInfo{
			{
				GoName:      "Health",
				FieldName:   "health",
				GoType:      "int32",
				ProtoType:   "int32",
				ProtoHelper: "Int32",
				BitIndex:    2,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"posX     float32",
		"func NewSyncedPlayer(posX float32, posY float32",
		"func (s *SyncedPlayer) Position() (float32, float32)",
		"func (s *SyncedPlayer) SetPosition(x, y float32)",
		"d.PosX = pb.Float32(s.posX)",
		"func (s *SyncedPlayer) MarshalCompactDeltaMask(mask uint64) ([]byte, error) {",
		"w.Int64(entityID)",
		"w.Uint64(revision)",
		"w.Uint64(mask)",
		"w.Raw(d.Marshal())",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated server helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoServerTemplateOwnerScopedMethods(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	withOwner := schema.EntityData{
		Name:      "Player",
		LowerName: "player",
		AllVars: []schema.VarInfo{
			{GoName: "Health", FieldName: "health", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 2},
			{GoName: "Secret", FieldName: "secret", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 3, OwnerOnly: true},
			{GoName: "Token", FieldName: "token", GoType: "string", Sync: "once", OwnerOnly: true},
		},
		TickVars: []schema.VarInfo{
			{GoName: "Health", FieldName: "health", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 2},
			{GoName: "Secret", FieldName: "secret", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 3, OwnerOnly: true},
		},
		OnceVars: []schema.VarInfo{
			{GoName: "Token", FieldName: "token", GoType: "string", OwnerOnly: true},
		},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, withOwner); err != nil {
		t.Fatalf("Execute owner entity: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"var _ golem.OwnerScopedEntity = (*SyncedPlayer)(nil)",
		"playerOwnerOnlyMask uint64 = 0 | playerFieldSecret",
		"func (s *SyncedPlayer) PublicFullUpdate() ([]byte, error) {",
		"func (s *SyncedPlayer) PublicReplicationMask(mask uint64) uint64 {",
		"return mask &^ playerOwnerOnlyMask",
		"Health: s.health,",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("owner-scoped template missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "Secret: s.secret,") && strings.Count(content, "Secret: s.secret,") > 1 {
		// FullState includes Secret; PublicFullUpdate must not.
	}
	publicIdx := strings.Index(content, "func (s *SyncedPlayer) PublicFullUpdate()")
	if publicIdx < 0 {
		t.Fatal("PublicFullUpdate missing")
	}
	publicEnd := strings.Index(content[publicIdx:], "func (s *SyncedPlayer) PublicReplicationMask")
	if publicEnd < 0 {
		t.Fatal("PublicReplicationMask missing after PublicFullUpdate")
	}
	publicBody := content[publicIdx : publicIdx+publicEnd]
	if strings.Contains(publicBody, "Secret:") {
		t.Fatalf("PublicFullUpdate must omit owner-only tick var Secret:\n%s", publicBody)
	}
	if strings.Contains(publicBody, "Token:") {
		t.Fatalf("PublicFullUpdate must omit owner-only once var Token:\n%s", publicBody)
	}

	withoutOwner := schema.EntityData{
		Name:      "Mob",
		LowerName: "mob",
		AllVars: []schema.VarInfo{
			{GoName: "Health", FieldName: "health", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 2},
		},
		TickVars: []schema.VarInfo{
			{GoName: "Health", FieldName: "health", GoType: "int32", ProtoType: "int32", ProtoHelper: "Int32", BitIndex: 2},
		},
	}
	out.Reset()
	if err := tmpl.Execute(&out, withoutOwner); err != nil {
		t.Fatalf("Execute plain entity: %v", err)
	}
	plain := out.String()
	for _, ban := range []string{
		"OwnerScopedEntity",
		"PublicFullUpdate",
		"PublicReplicationMask",
		"OwnerOnlyMask",
	} {
		if strings.Contains(plain, ban) {
			t.Fatalf("entity without owner vars must not emit %q", ban)
		}
	}
}

func TestGenerateGoServerTemplateIncludes3DPositions(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:       "Player",
		LowerName:  "player",
		Dimensions: 3,
		Is3D:       true,
		TickVars: []schema.VarInfo{
			{
				GoName:      "Health",
				FieldName:   "health",
				GoType:      "int32",
				ProtoType:   "int32",
				ProtoHelper: "Int32",
				BitIndex:    3,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"playerFieldPosZ uint64 = 1 << 2",
		"posZ     float32",
		"func NewSyncedPlayer(posX float32, posY float32, posZ float32",
		"func (s *SyncedPlayer) Position3D() (float32, float32, float32)",
		"func (s *SyncedPlayer) SetPosition3D(x, y, z float32)",
		"d.PosZ = pb.Float32(s.posZ)",
		"PosZ:     s.posZ",
		"s.posZ = st.PosZ",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated 3D server helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoServerTemplateIncludes2DCollider(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:       "Player",
		LowerName:  "player",
		Dimensions: 2,
		Collider: &schema.ColliderData{
			Kind:  schema.ColliderKindCircle,
			Layer: "Player",
			R:     0.5,
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"func (s *SyncedPlayer) Collider() (golem.CollisionShape, string, bool) {",
		"return golem.CollisionCircle{R: 0.5}, \"Player\", false",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated 2D collider missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "Collider3D") {
		t.Fatal("2D entity must not emit Collider3D")
	}
}

func TestGenerateGoServerTemplateIncludes3DCollider(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:       "Crate",
		LowerName:  "crate",
		Dimensions: 3,
		Is3D:       true,
		Collider: &schema.ColliderData{
			Kind:    schema.ColliderKindAABB3D,
			Layer:   "Prop",
			Trigger: true,
			W:       1,
			H:       2,
			D:       3,
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"func (s *SyncedCrate) Collider3D() (golem.CollisionShape3D, string, bool) {",
		"return golem.CollisionAABB3D{W: 1, H: 2, D: 3}, \"Prop\", true",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated 3D collider missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "func (s *SyncedCrate) Collider()") {
		t.Fatal("3D entity must not emit 2D Collider()")
	}
}

func TestGenerateGoSharedTemplateIncludesEnableCollision2D(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Dimensions:  2,
		Collision: &schema.CollisionData{
			Layers:   []string{"Player", "Monster"},
			Collides: []schema.CollisionPair{{A: "Player", B: "Monster"}},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"Layers *golem.CollisionLayers",
		"func (r *Runtime) EnableCollision(backend golem.CollisionBackend) {",
		"golem.MustCollisionBackend(backend)",
		`Define("Player", "Monster")`,
		`layers.SetCollides("Player", "Monster")`,
		"r.Server.SetCollisionBackend(backend)",
		"r.Server.SetCollisionLayers(layers)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EnableCollision missing %q\n%s", want, content)
		}
	}
	mustIdx := strings.Index(content, "golem.MustCollisionBackend(backend)")
	setIdx := strings.Index(content, "r.Server.SetCollisionLayers(layers)")
	if mustIdx < 0 || setIdx < 0 || mustIdx > setIdx {
		t.Fatal("MustCollisionBackend must run before SetCollisionLayers")
	}
	if strings.Contains(content, "EnableCollision3D") || strings.Contains(content, "Layers3D") {
		t.Fatal("2D collision config must not emit 3D helpers")
	}
}

func TestGenerateGoSharedTemplateIncludesEnableCollision3D(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Dimensions:  3,
		Is3D:        true,
		Collision: &schema.CollisionData{
			Layers:   []string{"Player", "Wall"},
			Collides: []schema.CollisionPair{{A: "Player", B: "Wall"}},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"Layers3D *golem.CollisionLayers3D",
		"func (r *Runtime) EnableCollision3D(backend golem.CollisionBackend3D) {",
		"golem.MustCollisionBackend3D(backend)",
		`Define("Player", "Wall")`,
		"r.Server.SetCollision3DBackend(backend)",
		"r.Server.SetCollisionLayers3D(layers)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EnableCollision3D missing %q\n%s", want, content)
		}
	}
	mustIdx := strings.Index(content, "golem.MustCollisionBackend3D(backend)")
	setIdx := strings.Index(content, "r.Server.SetCollisionLayers3D(layers)")
	if mustIdx < 0 || setIdx < 0 || mustIdx > setIdx {
		t.Fatal("MustCollisionBackend3D must run before SetCollisionLayers3D")
	}
	if strings.Contains(content, "func (r *Runtime) EnableCollision(") {
		t.Fatal("3D collision config must not emit 2D EnableCollision")
	}
}

func TestGenerateGoSharedTemplateOmitsCollisionWithoutConfig(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, ban := range []string{
		"EnableCollision",
		"Layers *golem.CollisionLayers",
		"Layers3D",
		"SetCollisionLayers",
	} {
		if strings.Contains(content, ban) {
			t.Fatalf("shared helper without collision config unexpectedly contains %q", ban)
		}
	}
}

func TestGenerateGoSharedTemplateSkipsSessionNotFoundDuringFOIEventFanout(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem",
		GoPackage:   "generated",
		Fingerprint: "test-fingerprint",
		Events: []schema.EventData{
			{
				Name:    "Explosion",
				Target:  "entity",
				FOIOnly: true,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"import (",
		"\"errors\"",
		"if errors.Is(err, golem.ErrSessionNotFound) {",
		"continue",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "var _ = errors.Is") {
		t.Fatal("shared helper should not keep errors.Is blank-assign now that snapshot helpers use errors")
	}
}
