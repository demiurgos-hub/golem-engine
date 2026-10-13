package codegen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func writeConsumerFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func runConsumer(t *testing.T, dir, tool string, args ...string) {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		if os.Getenv("GOLEM_REQUIRE_CONSUMERS") == "1" {
			t.Fatal(err)
		}
		t.Skipf("%s required for generated consumer", tool)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "GOWORK=") {
			cmd.Env = append(cmd.Env, pair)
		}
	}
	cmd.Env = append(cmd.Env, "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", tool, args, err, out)
	}
}

func renderConsumerTemplate(t *testing.T, dir, templateName, output string, data any) {
	t.Helper()
	tmpl, err := loadEmbeddedTemplate("templates/" + templateName)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, dir, output, b.String())
}

func TestResolutionGeneratedGoConsumers(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeConsumerFile(t, dir, "go.mod", fmt.Sprintf(`module resolutionconsumer
go 1.25.4
require github.com/demiurgos-hub/golem-engine v0.0.0
replace github.com/demiurgos-hub/golem-engine => %q
replace github.com/demiurgos-hub/golem-engine/golem/collision => %q
replace github.com/demiurgos-hub/golem-engine/golem/nav => %q
`, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "golem/collision")), filepath.ToSlash(filepath.Join(root, "golem/nav"))))
	for _, target := range []string{"server", "client"} {
		pd := resolutionFixture(2)
		types := map[string]schema.CustomTypeData{"Item": pd.CustomTypes[0]}
		worlds := append(pd.WorldTypes,
			schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "TiledMap", Source: &schema.WorldSourceDef{Format: "tiled", Extract: []schema.WorldExtractDef{{Name: "speed", Type: "float"}}}}, types),
			schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "LDtkMap", Source: &schema.WorldSourceDef{Format: "ldtk", URLPrefix: "/maps/", Extract: []schema.WorldExtractDef{{Name: "limit", Type: "int64"}}}}, types),
			schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "ItemList", Source: &schema.WorldSourceDef{Format: "catalog", Type: "Item"}}, types),
		)
		pd = schema.BuildProtoData(&schema.Config{Simulation: schema.SimulationConfig{Dimensions: 2}}, pd.Entities, pd.Commands, worlds, pd.CustomTypes, pd.Events)
		out := filepath.Join(dir, target)
		imp := "github.com/demiurgos-hub/golem-engine/golem"
		if target == "client" {
			imp += "-go-client"
		}
		if err := generateGoProto(out, imp, "generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd); err != nil {
			t.Fatal(err)
		}
		for _, entity := range pd.Entities {
			entity.GoPackage, entity.GolemImport = "generated", imp
			name := "go_server/server.go.tmpl"
			if target == "client" {
				name = "go_client/client.go.tmpl"
			}
			renderConsumerTemplate(t, out, name, strings.ToLower(entity.Name)+"_synced.go", entity)
		}
		writeConsumerFile(t, out, "codec_test.go", goCodecContract)
		writeConsumerFile(t, out, "world_test.go", goWorldContract)
		if target == "client" {
			renderConsumerTemplate(t, out, "go_client/manager.go.tmpl", "manager.go", schema.SharedData{GoPackage: "generated", GolemImport: imp, Entities: pd.Entities})
			writeConsumerFile(t, out, "client_test.go", goClientContract)
		} else {
			writeConsumerFile(t, out, "state_test.go", goFullStateContract)
		}
	}
	runConsumer(t, dir, "go", "mod", "tidy")
	args := []string{"test", "-count=1", "./..."}
	if os.Getenv("GOLEM_CONSUMER_RACE") == "1" {
		args = append(args, "-race")
	}
	runConsumer(t, dir, "go", args...)
}

func TestResolutionGeneratedJSConsumer(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tsc := filepath.Join(root, "golem-js/node_modules/typescript/lib/tsc.js")
	if _, err := os.Stat(tsc); err != nil {
		if os.Getenv("GOLEM_REQUIRE_CONSUMERS") == "1" {
			t.Fatal(err)
		}
		t.Skip("install golem-js dependencies to check generated TypeScript")
	}
	dir := t.TempDir()
	pd := resolutionFixture(2)
	module := filepath.Join(dir, "node_modules/golem-engine")
	if err := os.CopyFS(module, os.DirFS(filepath.Join(root, "golem-js/dist"))); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, module, "package.json", `{"name":"golem-engine","type":"module","exports":{"types":"./index.d.ts","import":"./index.js"}}`)
	writeConsumerFile(t, dir, "package.json", `{"type":"module"}`)
	if err := generateJSProto(dir, "golem-engine", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd); err != nil {
		t.Fatal(err)
	}
	renderConsumerTemplate(t, dir, "js/client.ts.tmpl", "ActorSynced.ts", pd.Entities[0])
	writeConsumerFile(t, dir, "tsconfig.json", `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"skipLibCheck":true,"outDir":"out"},"include":["*.ts"]}`)
	writeConsumerFile(t, dir, "contract.mjs", jsCodecContract)
	runConsumer(t, dir, "node", tsc, "-p", "tsconfig.json")
	runConsumer(t, dir, "node", "contract.mjs")
}

func TestResolutionGeneratedCSharpConsumer(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		if os.Getenv("GOLEM_REQUIRE_CONSUMERS") == "1" {
			t.Fatal(err)
		}
		t.Skip(".NET SDK required for C# codec checks")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pd := resolutionFixture(2)
	if err := generateCSharpProto(dir, "GolemEngine.Unity", "Generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd); err != nil {
		t.Fatal(err)
	}
	entity := pd.Entities[0]
	entity.GoPackage = "Generated"
	renderConsumerTemplate(t, dir, "csharp/client.cs.tmpl", "ActorSynced.cs", entity)
	for _, name := range []string{"PbReader.cs", "PbWriter.cs"} {
		data, err := os.ReadFile(filepath.Join(root, "golem-unity/Runtime/Codec", name))
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, dir, name, string(data))
	}
	writeConsumerFile(t, dir, "consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework><Nullable>disable</Nullable></PropertyGroup></Project>`)
	writeConsumerFile(t, dir, "Program.cs", csCodecContract)
	runConsumer(t, dir, "dotnet", "run", "--project", "consumer.csproj", "--nologo")
}

const goCodecContract = `package generated
import("bytes";"testing"; "github.com/demiurgos-hub/golem-engine/golem/pb")
func TestWireContract(t *testing.T) {
 zero:=float32(0); health:=int32(-3)
 d:=&ActorDelta{EntityId:7,Revision:11,PosX:&zero,Health:&health,Items:[]Item{},Scores:map[string]int32{}}
 want:=[]byte{8,7,21,0,0,0,0,192,62,11,32,5,82,0,90,0}
 if !bytes.Equal(d.Marshal(),want) { t.Fatalf("delta bytes: %x",d.Marshal()) }
 var got ActorDelta
 if err:=got.Unmarshal(want);err!=nil { t.Fatal(err) }
 if got.PosX==nil || *got.PosX!=0 || got.Health==nil || *got.Health!=-3 || got.Items==nil || len(got.Items)!=0 || got.Scores==nil || got.Secret!=nil { t.Fatalf("delta presence: %+v",got) }
 if len((&ActorState{}).Marshal())!=0 { t.Fatal("ordinary zero values must be omitted") }
 var state ActorState
 raw:=(&pb.Writer{}).Tag(77,2).String("unknown").Tag(4,0).Sint32(-3).Finish()
 if err:=state.Unmarshal(raw);err!=nil || state.Health!=-3 { t.Fatalf("unknown scalar field: %v",err) }
 if err:=state.Unmarshal([]byte{0x12,0xff});err==nil { t.Fatal("malformed payload must return a recovered error") }
 // Go keeps the last recognized oneof; unknown fields do not clear it.
 raw=(&pb.Writer{}).Tag(1,2).Bytes((&ActorState{EntityId:7}).Marshal()).Tag(2,2).Bytes(d.Marshal()).Tag(77,0).Int32(1).Finish()
 var envelope EntityUpdate
 if err:=envelope.Unmarshal(raw);err!=nil { t.Fatal(err) }
 if _,ok:=envelope.Payload.(*EntityUpdate_ActorDelta);!ok { t.Fatalf("oneof: %T",envelope.Payload) }
 command:=&MoveCommand{EntityId:7,Blob:[]byte{1,2},Item:Item{Id:3},Speed:1.5}
 if !bytes.Equal(command.Marshal(),[]byte{8,7,18,2,1,2,26,2,8,3,37,0,0,192,63}) { t.Fatalf("command bytes: %x",command.Marshal()) }
 var decoded MoveCommand
 if err:=decoded.Unmarshal(command.Marshal());err!=nil || decoded.Item.Id!=3 || decoded.Speed!=1.5 || !bytes.Equal(decoded.Blob,command.Blob) { t.Fatalf("nested command: %+v %v",decoded,err) }
 event:=&HitEvent{EntityId:7,Amount:-9,Blob:[]byte{4},Item:Item{Id:2}}
 var decodedEvent HitEvent
 if err:=decodedEvent.Unmarshal(event.Marshal());err!=nil || decodedEvent.Amount!=-9 || decodedEvent.Item.Id!=2 { t.Fatalf("event: %+v %v",decodedEvent,err) }
 repeated:=(&pb.Writer{}).Tag(1,2).Bytes((&ActorState{EntityId:7}).Marshal()).Tag(1,2).Bytes((&ActorState{EntityId:9}).Marshal()).Finish()
 if err:=envelope.Unmarshal(repeated);err!=nil||envelope.Payload.(*EntityUpdate_ActorState).ActorState.EntityId!=9 { t.Fatal("repeated variant") }
 if err:=envelope.Unmarshal([]byte{10,2,18,255});err==nil { t.Fatal("nested error must propagate") }
 var commands ClientMessage
 if err:=commands.Unmarshal((&ClientMessage{Payload:&ClientMessage_MoveCommand{MoveCommand:command}}).Marshal());err!=nil||commands.Payload.(*ClientMessage_MoveCommand).MoveCommand.EntityId!=7 { t.Fatal("command envelope") }
 var events ServerEvent
 if err:=events.Unmarshal((&ServerEvent{Payload:&ServerEvent_HitEvent{HitEvent:event}}).Marshal());err!=nil||events.Payload.(*ServerEvent_HitEvent).HitEvent.Amount!=-9 { t.Fatal("event envelope") }
 // Reusing a decoder preserves absent fields and appends repeated values.
 first:=(&ActorState{Flags:[]bool{true}}).Marshal()
 if err:=state.Unmarshal(first);err!=nil { t.Fatal(err) };if err:=state.Unmarshal(first);err!=nil { t.Fatal(err) }
 if len(state.Flags)!=2 { t.Fatal("decoder reuse changed") }
 // Characterize deferred collection discrepancies, without repairing them.
 goStrings:=(&ActorState{Labels:[]string{"x"}}).Marshal()
 if !bytes.Equal(goStrings,[]byte{98,2,1,'x'}) { t.Fatalf("Go string framing: %x",goStrings) }
 if err:=(&ActorState{}).Unmarshal([]byte{98,1,'x'});err==nil { t.Fatal("legacy JS string framing unexpectedly accepted") }
 var emptyCustom ActorDelta
 if err:=emptyCustom.Unmarshal((&ActorDelta{Items:[]Item{{}}}).Marshal());err!=nil || emptyCustom.Items==nil || len(emptyCustom.Items)!=0 { t.Fatal("empty custom element characterization changed") }
 entry:=[]byte{10,1,'a',24,16,16,7}
 var mapped ActorState
 if err:=mapped.Unmarshal((&pb.Writer{}).Tag(11,2).Bytes(entry).Finish());err!=nil || mapped.Scores["a"]!=16 { t.Fatalf("Go unknown map field characterization: %+v %v",mapped.Scores,err) }
}
`

const goWorldContract = `package generated
import("bytes";"os";"path/filepath";"testing")
func TestWorldSources(t *testing.T) {
 dir:=t.TempDir()
 write:=func(name,body string)string{p:=filepath.Join(dir,name);if err:=os.WriteFile(p,[]byte(body),0600);err!=nil{t.Fatal(err)};return p}
 path:=write("tiled.json","{\"properties\":[{\"name\":\"speed\",\"type\":\"float\",\"value\":1.5}]}")
 tiled,err:=LoadTiledMapData(path);if err!=nil||tiled.Speed!=1.5||len(tiled.TileData)==0{t.Fatalf("Tiled: %+v %v",tiled,err)}
 path=write("world.ldtk","{\"levels\":[{\"fieldInstances\":[{\"__identifier\":\"limit\",\"__type\":\"Int\",\"__value\":19}]}]}")
 ldtk,err:=LoadLDtkMapData(path);if err!=nil||ldtk.Limit!=19||ldtk.MapUrl!="/maps/world.ldtk"{t.Fatalf("LDtk: %+v %v",ldtk,err)}
 path=write("catalog.yaml","- id: 1\n  label: first\n  value: 2.5\n- id: '1'\n  label: second\n  value: '3.5'\n")
 catalog,err:=LoadCatalogData(path);if err!=nil||len(catalog.Items)!=1||catalog.Items[1].Label!="second"||catalog.Items[1].Value!=3.5{t.Fatalf("catalog coercion: %+v %v",catalog,err)}
 list,err:=LoadItemListData(path);if err!=nil||len(list.Items)!=2||list.Items[0].Value!=2.5{t.Fatalf("catalog list: %+v %v",list,err)}
 arena:=&ArenaData{Counts:[]int32{3},Items:map[string]Item{"a":{Id:2}}}
 want:=[]byte{10,1,3,18,7,10,1,'a',18,2,8,2}
 if !bytes.Equal(arena.Marshal(),want){t.Fatalf("world map/list bytes: %x",arena.Marshal())}
 var decoded ArenaData;if err:=decoded.Unmarshal(want);err!=nil||decoded.Counts[0]!=3||decoded.Items["a"].Id!=2{t.Fatalf("world decode: %+v %v",decoded,err)}
 raw,err:=arena.MarshalUpdate();if err!=nil{t.Fatal(err)}
 var update WorldUpdate;if err:=update.Unmarshal(raw);err!=nil{t.Fatal(err)}
 if _,ok:=update.Payload.(*WorldUpdate_ArenaData);!ok{t.Fatalf("world envelope: %T",update.Payload)}
}
`

const goClientContract = `package generated
import "testing"
func TestClientPresence(t *testing.T) {
 s:=NewSyncedActor(7);s.ApplyState(&ActorState{EntityId:7,Health:8,Secret:"keep",Items:[]Item{{Id:1}}})
 zero:=int32(0);s.ApplyDelta(&ActorDelta{Revision:9,Health:&zero,Items:[]Item{}})
 if s.Health()!=0 || s.Secret()!="keep" || len(s.Items())!=0 || s.Revision()!=9 { t.Fatal("delta application changed") }
}
`

const goFullStateContract = `package generated
import("bytes";"testing";"sync";"github.com/demiurgos-hub/golem-engine/golem/pb")
func TestFullStateProjection(t *testing.T) {
 s:=&SyncedActor{entityID:7,health:8,secret:"private",token:"once-private",items:[]Item{{Id:1}},revision:11,dirty:9,lastFlushMask:3}
 authoritative:=s.FullState()
 if authoritative.Secret!="private" || authoritative.Token!="once-private" { t.Fatal("authoritative redaction") }
 authoritative.Items[0].Id=4
 if s.items[0].Id!=4 { t.Fatal("full state collection alias behavior changed") }
 raw,err:=s.PublicFullUpdate();if err!=nil { t.Fatal(err) }
 var update EntityUpdate;if err:=update.Unmarshal(raw);err!=nil { t.Fatal(err) }
 public:=update.Payload.(*EntityUpdate_ActorState).ActorState
 if public.Secret!="" || public.Token!="" || public.Health!=8 || public.Revision!=11 { t.Fatalf("public projection: %+v",public) }
 raw,err=s.FullUpdate();if err!=nil { t.Fatal(err) }
 want:=(&EntityUpdate{Payload:&EntityUpdate_ActorState{ActorState:authoritative}}).Marshal()
 if !bytes.Equal(raw,want) { t.Fatal("full update envelope changed") }
 if s.dirty!=9 || s.lastFlushMask!=3 || s.revision!=11 { t.Fatal("projection mutated bookkeeping") }
 compact,err:=s.MarshalCompactDeltaMask(actorFieldHealth);if err!=nil { t.Fatal(err) }
 reader:=pb.NewReader(compact)
 if reader.Int64()!=7 || reader.Uint64()!=11 || reader.Uint64()!=actorFieldHealth { t.Fatal("compact headers changed") }
 var wg sync.WaitGroup
 for i:=0;i<2;i++ { wg.Add(1);go func(){defer wg.Done();for n:=0;n<100;n++ { s.SetHealth(int32(n));s.FullState();s.PublicFullUpdate() }}() };wg.Wait()
}
`

const jsCodecContract = `import assert from 'node:assert/strict';
import {PbWriter} from 'golem-engine';
import {ActorState,ActorDelta,EntityUpdate,MoveCommand,PingCommand,ClientMessage} from './out/entities_pb.js';
import {HitEvent} from './out/events_pb.js';
import {SyncedActor} from './out/ActorSynced.js';
const d=Object.assign(new ActorDelta(),{entityId:7,revision:11,posX:0,health:-3,items:[],scores:{}});
const want=[8,7,21,0,0,0,0,192,62,11,32,5,82,0,90,0];
assert.deepEqual([...ActorDelta.encode(d)],want);
const decoded=ActorDelta.decode(Uint8Array.from(want));
assert.equal(decoded.secret,undefined);assert.equal(decoded.posX,0);assert.deepEqual(decoded.items,[]);assert.deepEqual(decoded.scores,{});
assert.equal(ActorState.encode(new ActorState()).length,0);
const raw=new PbWriter().tag(1,2).bytes(ActorState.encode(Object.assign(new ActorState(),{entityId:7}))).tag(2,2).bytes(ActorDelta.encode(d)).tag(77,0).int32(1).finish();
const envelope=EntityUpdate.decode(raw);assert.equal(envelope.actorState.entityId,7);assert.equal(envelope.actorDelta.health,-3);
const command=Object.assign(new MoveCommand(),{entityId:7,blob:Uint8Array.from([1,2]),item:{id:3,label:'',value:0},speed:1.5});
assert.deepEqual([...MoveCommand.encode(command)],[8,7,18,2,1,2,26,2,8,3,37,0,0,192,63]);
const multiple=ClientMessage.encode({move:command,ping:new PingCommand()});assert.deepEqual([...multiple.slice(-2)],[18,0]);
const repeated=new PbWriter().tag(1,2).bytes(Uint8Array.from([8,7])).tag(1,2).bytes(Uint8Array.from([8,9])).finish();assert.equal(EntityUpdate.decode(repeated).actorState.entityId,9);
assert.throws(()=>EntityUpdate.decode(Uint8Array.from([10,2,18,255])));
const event=Object.assign(new HitEvent(),{entityId:7,amount:-9,blob:Uint8Array.from([4]),item:{id:2,label:'',value:0}});
assert.equal(HitEvent.decode(HitEvent.encode(event)).item.id,2);
const model=new SyncedActor(7);model.applyState(Object.assign(new ActorState(),{health:8,secret:'keep',items:[{id:1,label:'',value:0}]}));model.applyDelta(d);
assert.equal(model.health,-3);assert.equal(model.secret,'keep');assert.deepEqual(model.items,[]);
// Deferred: JS string-list writes differ from Go and its own inner-reader decoder.
const strings=ActorState.encode(Object.assign(new ActorState(),{labels:['x']}));assert.deepEqual([...strings],[98,1,120]);assert.throws(()=>ActorState.decode(strings));
assert.deepEqual(ActorState.decode(Uint8Array.from([98,2,1,120])).labels,['x']);
assert.deepEqual(ActorDelta.decode(ActorDelta.encode(Object.assign(new ActorDelta(),{items:[{id:0,label:'',value:0}]}))).items,[]);
const map=new PbWriter().tag(11,2).bytes(Uint8Array.from([10,1,97,24,16,16,7])).finish();assert.equal(ActorState.decode(map).scores.a,7);
console.log('generated JS codec contracts passed');
`

const csCodecContract = `using System;using System.Linq;using System.Collections.Generic;using Generated;using GolemEngine.Unity;
namespace Generated { public interface ISyncedEntity { long EntityId {get;} ulong Revision {get;} } }
class Program {
 static void Check(bool condition,string message){if(!condition)throw new Exception(message);}
 static void Main(){
 var d=new ActorDelta{EntityId=7,Revision=11,PosX=0,Health=-3,Items=new List<Item>(),Scores=new Dictionary<string,int>()};
 var want=new byte[]{8,7,21,0,0,0,0,192,62,11,32,5,82,0,90,0};Check(ActorDelta.Encode(d).SequenceEqual(want),"delta bytes");
 var decoded=ActorDelta.Decode(want);Check(decoded.Secret==null&&decoded.PosX==0&&decoded.Items.Count==0&&decoded.Scores.Count==0,"presence");
 Check(ActorState.Encode(new ActorState()).Length==0,"omit zeros");
 var raw=new PbWriter().Tag(1,2).Bytes(ActorState.Encode(new ActorState{EntityId=7})).Tag(2,2).Bytes(ActorDelta.Encode(d)).Tag(77,0).Int32(1).Finish();
 var envelope=EntityUpdate.Decode(raw);Check(envelope.ActorState.EntityId==7&&envelope.ActorDelta.Health==-3,"multiple variants");
 var ev=new HitEvent{EntityId=7,Amount=-9,Blob=new byte[]{4},Item=new Item{Id=2}};Check(HitEvent.Decode(HitEvent.Encode(ev)).Item.Id==2,"nested event");
 var command=new MoveCommand{EntityId=7,Blob=new byte[]{1,2},Item=new Item{Id=3},Speed=1.5f};Check(MoveCommand.Encode(command).SequenceEqual(new byte[]{8,7,18,2,1,2,26,2,8,3,37,0,0,192,63}),"command bytes");
 var multiple=ClientMessage.Encode(new ClientMessage{Move=command,Ping=new PingCommand()});Check(multiple.TakeLast(2).SequenceEqual(new byte[]{18,0}),"multiple command properties");
 var repeated=new PbWriter().Tag(1,2).Bytes(new byte[]{8,7}).Tag(1,2).Bytes(new byte[]{8,9}).Finish();Check(EntityUpdate.Decode(repeated).ActorState.EntityId==9,"repeated variant");
 bool nestedFailed=false;try{EntityUpdate.Decode(new byte[]{10,2,18,255});}catch{nestedFailed=true;}Check(nestedFailed,"nested malformed payload");
 var model=new SyncedActor(7);model.ApplyState(new ActorState{Health=8,Secret="keep",Items=new List<Item>{new Item{Id=1}}});model.ApplyDelta(d);Check(model.Health==-3&&model.Secret=="keep"&&model.Items.Count==0,"delta application");
 var strings=ActorState.Encode(new ActorState{Labels=new List<string>{"x"}});Check(strings.SequenceEqual(new byte[]{98,1,120}),"legacy string framing");
 bool failed=false;try{ActorState.Decode(strings);}catch{failed=true;}Check(failed,"legacy string decoder");
 Check(ActorState.Decode(new byte[]{98,2,1,120}).Labels[0]=="x","Go string framing");
 Check(ActorDelta.Decode(ActorDelta.Encode(new ActorDelta{Items=new List<Item>{new Item()}})).Items.Count==0,"empty custom characterization");
 var map=new PbWriter().Tag(11,2).Bytes(new byte[]{10,1,97,24,16,16,7}).Finish();Check(ActorState.Decode(map).Scores["a"]==7,"unknown map entry field");
 Console.WriteLine("generated C# codec contracts passed");
 }
}
`
