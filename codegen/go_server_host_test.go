package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateGoServerHostTemplate(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_server/host.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, schema.SharedData{GoPackage: "synced", GolemImport: "example.com/game/runtime"}); err != nil {
		t.Fatal(err)
	}
	src := out.String()
	for _, want := range []string{
		"package synced",
		`"example.com/game/runtime/host"`,
		"type HostOptions = host.GameOptions",
		"func (r *Runtime) Host(ctx context.Context, opts HostOptions) error",
		"SchemaFingerprint:    SchemaFingerprint",
		"InspectorSchema:      InspectorSchema()",
		"LoadSnapshotIfExists: r.LoadSnapshotIfExists",
		"RunSnapshotAutosave:  r.RunSnapshotAutosave",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("host template missing %q:\n%s", want, src)
		}
	}
	// Only the stable host facade: no feature packages that a project might
	// not use (the inspector import lives behind golem/host).
	for _, ban := range []string{"/inspector", "/snapshot\"", "physics", "collision"} {
		if strings.Contains(src, ban) {
			t.Fatalf("host template imports %q:\n%s", ban, src)
		}
	}
}

// goServerHostProjects is the go-server configuration matrix: every project is
// baked with the real Bake and must compile with the generated Runtime.Host.
var goServerHostProjects = map[string]map[string]string{
	// Bake requires at least one entity; minimal has nothing else (no
	// commands, events, world, collision or content).
	"minimal": {
		"golem.yaml":          "entity_schema: entities\nproto:\n  package: minimal\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/probe.yaml": "entity: Probe\nvars: {}\n",
	},
	"basic2d": {
		"golem.yaml": "entity_schema: entities\ncommand_schema: commands\nevent_schema: events\nworld_schema: world\n" +
			"proto:\n  package: basic\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/marker.yaml": "entity: Marker\nvars:\n  score: { type: int32, tag: 1 }\n",
		"entities/ghost.yaml":  "entity: Ghost\npersistent: false\nvars:\n  label: { type: string, tag: 1 }\n",
		"commands/poke.yaml":   "command: Poke\ntarget: entity\nentity_type: Marker\nfields: {}\n",
		"events/scored.yaml":   "event: Scored\ntarget: session\nfields:\n  points: { type: int32, tag: 1 }\n",
		"world/arena.yaml":     "world: Arena\nfields:\n  title: { type: string }\n",
	},
	"basic3d": {
		"golem.yaml":           "entity_schema: entities\nsimulation:\n  dimensions: 3\nproto:\n  package: basic3d\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/marker.yaml": "entity: Marker\nvars:\n  score: { type: int32, tag: 1 }\n",
	},
	"collision2d": {
		"golem.yaml": "entity_schema: entities\ncollision:\n  layers: [Player, Wall]\n  collides: [[Player, Wall]]\n" +
			"proto:\n  package: collision2d\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/player.yaml": "entity: Player\ncollider: { circle: 0.5, layer: Player }\nvars:\n  hp: { type: int32, tag: 1 }\n",
	},
	"collision3d": {
		"golem.yaml": "entity_schema: entities\nsimulation:\n  dimensions: 3\ncollision:\n  layers: [Player, Wall]\n  collides: [[Player, Wall]]\n" +
			"proto:\n  package: collision3d\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/player.yaml": "entity: Player\ncollider: { sphere: 0.5, layer: Player }\nvars:\n  hp: { type: int32, tag: 1 }\n",
	},
	"content": {
		"golem.yaml": "entity_schema: entities\ncontent:\n  schema: schema.json\n  bundle: source.json\n" +
			"proto:\n  package: contentgame\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/actor.yaml": "entity: Actor\nvars:\n  hp: { type: int32, tag: 1, sync: tick }\n",
		"schema.json":         contentConsumerSchema,
		"source.json":         contentConsumerBundle,
	},
}

// TestGoServerHostGeneratedConsumers bakes the configuration matrix into one
// consumer module, checks that each generated package compiles with
// Runtime.Host, and runs the end-to-end deploy contract test against the
// generated basic2d runtime.
func TestGoServerHostGeneratedConsumers(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeConsumerFile(t, dir, "go.mod", fmt.Sprintf(`module hostconsumer
go 1.25.4
require github.com/demiurgos-hub/golem-engine v0.0.0
replace github.com/demiurgos-hub/golem-engine => %q
replace github.com/demiurgos-hub/golem-engine/golem/collision => %q
replace github.com/demiurgos-hub/golem-engine/golem/nav => %q
replace github.com/demiurgos-hub/golem-engine/golem/physics3d => %q
`, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "golem/collision")), filepath.ToSlash(filepath.Join(root, "golem/nav")), filepath.ToSlash(filepath.Join(root, "golem/physics3d"))))
	for name, files := range goServerHostProjects {
		project := filepath.Join(dir, name)
		for file, content := range files {
			writeConsumerFile(t, project, file, content)
		}
		if err := Bake(project); err != nil {
			t.Fatalf("bake %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(project, "generated", "golem_host.go")); err != nil {
			t.Fatalf("%s: golem_host.go not generated: %v", name, err)
		}
		writeConsumerFile(t, project, "generated/host_signature.go", goHostSignatureContract)
	}
	writeConsumerFile(t, dir, "basic2d/generated/host_e2e_test.go", goHostE2EContract)
	runConsumer(t, dir, "go", "mod", "tidy")
	runConsumer(t, dir, "go", "vet", "./...")
	args := []string{"test", "-count=1", "./..."}
	if os.Getenv("GOLEM_CONSUMER_RACE") == "1" {
		args = append(args, "-race")
	}
	runConsumer(t, dir, "go", args...)
}

const goHostSignatureContract = `package generated

import (
	"context"

	"github.com/demiurgos-hub/golem-engine/golem/host"
)

var (
	_ func(*Runtime, context.Context, HostOptions) error = (*Runtime).Host
	_ host.GameOptions                                   = HostOptions{}
)
`

const goHostE2EContract = `package generated_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/host"
	"hostconsumer/basic2d/generated"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func getJSON(t *testing.T, method, url string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body
}

type game struct {
	srv    *golem.Server
	rt     *generated.Runtime
	marker *generated.SyncedMarker
	done   chan error
	cancel context.CancelFunc
}

// start builds the server the way a game does (golem.ServerConfigFromEnv) and
// runs it through the generated Runtime.Host.
func start(t *testing.T, adminURL string) *game {
	t.Helper()
	srv, rt := generated.NewServer(golem.ServerConfigFromEnv(golem.ServerConfig{
		TickRate: 50, Transport: golem.TransportWebSocket, StateUpdateLane: golem.StateUpdateLaneStream,
	}))
	g := &game{srv: srv, rt: rt, done: make(chan error, 1)}
	generated.RegisterMarkerWrapper(func(m *generated.SyncedMarker) golem.Entity {
		g.marker = m
		return m
	})
	t.Cleanup(func() { generated.RegisterMarkerWrapper(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	go func() {
		g.done <- rt.Host(ctx, generated.HostOptions{
			Name: "host-e2e",
			AfterRestore: func(restored bool) error {
				if !restored {
					g.marker = generated.NewSyncedMarker(1, 2, 10)
					return srv.CreateEntity(g.marker)
				}
				if g.marker == nil {
					return errors.New("restored without the marker")
				}
				return nil
			},
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if status, _ := getJSON(t, http.MethodGet, adminURL+"/ready"); status == http.StatusOK {
			return g
		}
		select {
		case err := <-g.done:
			t.Fatalf("Host returned before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (g *game) stop(t *testing.T) {
	t.Helper()
	g.cancel()
	select {
	case err := <-g.done:
		if err != nil {
			t.Fatalf("Host = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Host did not return")
	}
}

func TestHostDeployContract(t *testing.T) {
	sidecar, admin := freeAddr(t), freeAddr(t)
	snapDir := filepath.Join(t.TempDir(), "state")
	t.Setenv(host.EnvHTTPAddr, sidecar)
	t.Setenv(host.EnvAdminAddr, admin)
	t.Setenv(host.EnvAdminToken, "")
	t.Setenv(host.EnvSnapshotDir, snapDir)
	t.Setenv(host.EnvSnapshotInterval, "")
	t.Setenv(golem.EnvPublicURL, "wss://play.example/api/ws")
	t.Setenv("GOLEM_INSPECTOR_HUB_URL", "")
	adminURL := "http://" + admin
	path := filepath.Join(snapDir, host.SnapshotFileName)

	g := start(t, adminURL)
	status, cfg := getJSON(t, http.MethodGet, "http://"+sidecar+host.DefaultRealtimeConfigPath)
	if status != http.StatusOK || cfg["url"] != "wss://play.example/api/ws" || cfg["transport"] != "websocket" {
		t.Fatalf("realtime config = %d %v", status, cfg)
	}
	status, info := getJSON(t, http.MethodGet, adminURL+"/info")
	if status != http.StatusOK || info["schema_fingerprint"] != generated.SchemaFingerprint || info["name"] != "host-e2e" {
		t.Fatalf("/info = %d %v", status, info)
	}
	if status, _ := getJSON(t, http.MethodPost, adminURL+"/snapshot"); status != http.StatusOK {
		t.Fatalf("POST /snapshot = %d", status)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file after POST /snapshot: %v", err)
	}

	// Changed on the tick goroutine after the admin snapshot: only the final
	// save after the tick loop stops can persist it.
	applied := make(chan struct{})
	g.srv.Post(func(*golem.Server) { g.marker.SetScore(42); close(applied) })
	<-applied
	g.stop(t)

	check, _ := generated.NewServer(golem.ServerConfig{TickRate: 50})
	checkRT := generated.NewRuntime(check)
	if err := checkRT.LoadSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if check.Len() != 1 {
		t.Fatalf("final snapshot holds %d entities, want 1", check.Len())
	}

	g2 := start(t, adminURL)
	if g2.marker == nil || g2.marker.Score() != 42 || g2.srv.Len() != 1 {
		t.Fatalf("second start did not restore the final snapshot: marker=%v len=%d", g2.marker, g2.srv.Len())
	}
	_, info = getJSON(t, http.MethodGet, adminURL+"/info")
	if extra, _ := info["extra"].(map[string]any); extra[host.AdminExtraSnapshotRestored] != "true" {
		t.Fatalf("/info after restore = %v", info)
	}
	g2.stop(t)
}
`
