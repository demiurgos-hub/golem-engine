package codegen

import (
	"bytes"
	"strings"
	"testing"

	"golem-engine/schema"
)

// TestGenerateGoSharedTemplateIncludesRestoreWrappers verifies that the shared
// Go helper template emits typed restore wrapper registration hooks.
func TestGenerateGoSharedTemplateIncludesRestoreWrappers(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine/golem",
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
		GolemImport: "golem-engine/golem",
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
		GolemImport: "golem-engine/golem",
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
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoSharedTemplateMarshalEntityRemovedIncludesRevision(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine/golem",
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

func TestGenerateGoSharedTemplateSkipsSessionNotFoundDuringFOIEventFanout(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/shared.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine/golem",
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
		"var _ = errors.Is",
		"if errors.Is(err, golem.ErrSessionNotFound) {",
		"continue",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated shared helper missing %q\n%s", want, content)
		}
	}
}
