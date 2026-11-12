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

func physicsBindingFixture(bound bool) schema.ProtoTemplateData {
	vars := map[string]schema.SchemaVarDef{}
	roles := []string{"orientation_x", "orientation_y", "orientation_z", "orientation_w", "velocity_x", "velocity_y", "velocity_z", "angular_velocity_x", "angular_velocity_y", "angular_velocity_z", "active"}
	for i, role := range roles {
		kind := "float"
		if role == "active" {
			kind = "bool"
		}
		def := schema.SchemaVarDef{Type: kind, Tag: i + 1}
		if bound {
			def.Physics = role
		}
		if i == 0 {
			def.Visibility = "owner"
		}
		vars[role] = def
	}
	e := schema.BuildEntityData(schema.SchemaFile{Entity: "PhysicsActor", Vars: vars}, 3, nil)
	return schema.BuildProtoData(&schema.Config{Simulation: schema.SimulationConfig{Dimensions: 3}}, []schema.EntityData{e}, nil, nil, nil, nil)
}

func TestPhysicsBindingsPreserveOrdinaryWireAndClients(t *testing.T) {
	a, b := physicsBindingFixture(false), physicsBindingFixture(true)
	if ComputeSchemaFingerprint(a.Entities) != ComputeSchemaFingerprint(b.Entities) {
		t.Fatal("binding metadata changed fingerprint")
	}
	proto, err := loadEmbeddedTemplate("templates/proto/entities.proto.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	var beforeProto, afterProto bytes.Buffer
	if err := proto.Execute(&beforeProto, a); err != nil {
		t.Fatal(err)
	}
	if err := proto.Execute(&afterProto, b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeProto.Bytes(), afterProto.Bytes()) {
		t.Fatal("binding metadata changed protobuf schema")
	}
	for _, target := range []string{"go", "js", "cs"} {
		before, after := t.TempDir(), t.TempDir()
		for i, pd := range []schema.ProtoTemplateData{a, b} {
			dir := before
			if i == 1 {
				dir = after
			}
			var err error
			switch target {
			case "go":
				err = generateGoProto(dir, "github.com/demiurgos-hub/golem-engine/golem", "generated", pd.Entities, nil, nil, nil, pd)
			case "js":
				err = generateJSProto(dir, "golem-engine", pd.Entities, nil, nil, nil, pd)
			case "cs":
				err = generateCSharpProto(dir, "GolemEngine.Unity", "Generated", pd.Entities, nil, nil, nil, pd)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		files, err := os.ReadDir(before)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			x, err := os.ReadFile(filepath.Join(before, f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			y, err := os.ReadFile(filepath.Join(after, f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(x, y) {
				t.Fatalf("%s changed ordinary codec %s", target, f.Name())
			}
		}
	}
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, pd := range []schema.ProtoTemplateData{a, b} {
		var out bytes.Buffer
		e := pd.Entities[0]
		e.GoPackage = "generated"
		e.GolemImport = "github.com/demiurgos-hub/golem-engine/golem"
		if err := tmpl.Execute(&out, e); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "func (s *SyncedPhysicsActor) SetPhysicsRotation") != (e.Physics != nil) {
			t.Fatal("physics writer must be opt-in")
		}
	}
}

func TestPhysicsBindingGeneratedConsumers(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pd := physicsBindingFixture(true)
	entity := pd.Entities[0]
	goDir := filepath.Join(dir, "go")
	writeConsumerFile(t, goDir, "go.mod", fmt.Sprintf(`module physicsconsumer
go 1.25.4
require github.com/demiurgos-hub/golem-engine v0.0.0
replace github.com/demiurgos-hub/golem-engine => %q
replace github.com/demiurgos-hub/golem-engine/golem/collision => %q
replace github.com/demiurgos-hub/golem-engine/golem/nav => %q
replace github.com/demiurgos-hub/golem-engine/golem/physics3d => %q
`, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "golem/collision")), filepath.ToSlash(filepath.Join(root, "golem/nav")), filepath.ToSlash(filepath.Join(root, "golem/physics3d"))))
	if err := generateGoProto(goDir, "github.com/demiurgos-hub/golem-engine/golem", "generated", pd.Entities, nil, nil, nil, pd); err != nil {
		t.Fatal(err)
	}
	entity.GoPackage = "generated"
	entity.GolemImport = "github.com/demiurgos-hub/golem-engine/golem"
	renderConsumerTemplate(t, goDir, "go_server/server.go.tmpl", "actor.go", entity)
	writeConsumerFile(t, goDir, "actor_test.go", `package generated
import("testing";"os";p "github.com/demiurgos-hub/golem-engine/golem/physics3d")
func TestBindings(t *testing.T){
 e:=NewSyncedPhysicsActor(1,2,3,0,0,0,1,0,0,0,0,0,0,false,7)
 e.SetPhysicsRotation(p.Quat{X:0.6,W:0.8}); e.SetPhysicsVelocity(p.Vec3{X:3},p.Vec3{Z:4});e.SetPhysicsActive(true)
 if e.StateRevision()!=3 {t.Fatal("group revision",e.StateRevision())}
 public,err:=e.PublicFullUpdate();if err!=nil {t.Fatal(err)};var envelope EntityUpdate;if err:=envelope.Unmarshal(public);err!=nil {t.Fatal(err)}
 if envelope.Payload.(*EntityUpdate_PhysicsActorState).PhysicsActorState.OrientationX!=0||e.PublicReplicationMask(physicsActorFieldOrientationX)!=0 {t.Fatal("owner visibility lost")}
 d:=e.FlushDelta();if d.OrientationX==nil||*d.OrientationX!=0.6||d.VelocityX==nil||*d.VelocityX!=3||d.Active==nil||!*d.Active||d.PosX!=nil {t.Fatal("ordinary dirty bits",d)}
 e.SetPhysicsRotation(p.Quat{X:0.6,W:0.8});e.SetPhysicsVelocity(p.Vec3{X:3},p.Vec3{Z:4});e.SetPhysicsActive(true)
 if e.FlushDelta()!=nil||e.StateRevision()!=3 {t.Fatal("unchanged writes became dirty")}
 state:=e.FullState();var restored PhysicsActorState;if err:=restored.Unmarshal(state.Marshal());err!=nil {t.Fatal(err)}
 if restored.PosZ!=3||restored.OrientationW!=0.8||restored.AngularVelocityZ!=4||!restored.Active {t.Fatal("state codec",restored)}
 if err:=os.WriteFile("../state.bin",state.Marshal(),0600);err!=nil {t.Fatal(err)}
}
`)
	runConsumer(t, goDir, "go", "mod", "tidy")
	goArgs := []string{"test", "-count=1", "./..."}
	if os.Getenv("GOLEM_CONSUMER_RACE") == "1" {
		goArgs = append(goArgs, "-race")
	}
	runConsumer(t, goDir, "go", goArgs...)
	jsDir := filepath.Join(dir, "js")
	module := filepath.Join(jsDir, "node_modules/golem-engine")
	if err := os.CopyFS(module, os.DirFS(filepath.Join(root, "golem-js/dist"))); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, module, "package.json", `{"name":"golem-engine","type":"module","exports":{"types":"./index.d.ts","import":"./index.js"}}`)
	writeConsumerFile(t, jsDir, "package.json", `{"type":"module"}`)
	if err := generateJSProto(jsDir, "golem-engine", pd.Entities, nil, nil, nil, pd); err != nil {
		t.Fatal(err)
	}
	renderConsumerTemplate(t, jsDir, "js/client.ts.tmpl", "PhysicsActorSynced.ts", entity)
	writeConsumerFile(t, jsDir, "tsconfig.json", `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"skipLibCheck":true,"outDir":"out"},"include":["*.ts"]}`)
	writeConsumerFile(t, jsDir, "contract.mjs", `import assert from 'node:assert/strict';import fs from 'node:fs';import {PhysicsActorState,PhysicsActorDelta} from './out/entities_pb.js';import {SyncedPhysicsActor} from './out/PhysicsActorSynced.js';const raw=fs.readFileSync('../state.bin');const state=PhysicsActorState.decode(raw);assert.deepEqual([...PhysicsActorState.encode(state)],[...raw]);const e=new SyncedPhysicsActor(7);e.applyState(state);assert.equal(e.posZ,3);assert.equal(e.velocityX,3);assert.equal(e.angularVelocityZ,4);assert.equal(e.active,true);e.applyDelta(Object.assign(new PhysicsActorDelta(),{active:false,orientationX:0}));assert.equal(e.active,false);assert.equal(e.orientationX,0);assert.equal(e.velocityX,3);`)
	runConsumer(t, jsDir, "node", filepath.Join(root, "golem-js/node_modules/typescript/lib/tsc.js"), "-p", "tsconfig.json")
	runConsumer(t, jsDir, "node", "contract.mjs")
	csDir := filepath.Join(dir, "cs")
	if err := os.MkdirAll(csDir, 0755); err != nil {
		t.Fatal(err)
	}
	entity.GoPackage = "Generated"
	if err := generateCSharpProto(csDir, "GolemEngine.Unity", "Generated", pd.Entities, nil, nil, nil, pd); err != nil {
		t.Fatal(err)
	}
	renderConsumerTemplate(t, csDir, "csharp/client.cs.tmpl", "Actor.cs", entity)
	renderConsumerTemplate(t, csDir, "csharp/manager.cs.tmpl", "EntityManager.cs", schema.SharedData{GoPackage: "Generated", Entities: pd.Entities})
	for _, name := range []string{"Codec/PbReader.cs", "Codec/PbWriter.cs", "Client/ClientContracts.cs"} {
		data, err := os.ReadFile(filepath.Join(root, "golem-unity/Runtime", name))
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, csDir, filepath.Base(name), string(data))
	}
	writeConsumerFile(t, csDir, "consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework><Nullable>disable</Nullable></PropertyGroup></Project>`)
	writeConsumerFile(t, csDir, "Program.cs", `using System;using System.IO;using System.Linq;using Generated;class Program{static void Main(){var raw=File.ReadAllBytes("../state.bin");var state=PhysicsActorState.Decode(raw);if(!raw.SequenceEqual(PhysicsActorState.Encode(state)))throw new Exception("bytes");var e=new SyncedPhysicsActor(7);e.ApplyState(state);if(e.PosZ!=3||e.VelocityX!=3||e.AngularVelocityZ!=4||!e.Active)throw new Exception("state");e.ApplyDelta(new PhysicsActorDelta{Active=false,OrientationX=0});if(e.Active||e.OrientationX!=0||e.VelocityX!=3)throw new Exception("delta");}}`)
	runConsumer(t, csDir, "dotnet", "run", "--project", "consumer.csproj", "--nologo")
}
