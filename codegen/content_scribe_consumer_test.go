package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestScribeContentServerConsumer consumes the same tiny portable documents as
// the C# Scribe fixture, then exercises generated code against the actual server.
func TestScribeContentServerConsumer(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"content-schema.json", "content-source.json"} {
		data, err := os.ReadFile(filepath.Join(root, "golem-unity/Tests/Fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, dir, name, string(data))
	}
	writeConsumerFile(t, dir, "golem.yaml", `entity_schema: entities
simulation:
  dimensions: 3
content:
  schema: content-schema.json
  bundle: content-source.json
proto:
  package: sample
  out: entities.proto
integrations:
  go-server:
    out: generated
    package: generated
  csharp-client:
    out: csharp
    package: ContentGenerated
`)
	writeConsumerFile(t, dir, "entities/monster.yaml", `entity: Monster
persistent: true
vars:
  definition_id: {tag: 1, type: string, sync: once}
  health: {tag: 2, type: int32, sync: tick}
`)
	if err := Bake(dir); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, dir, "go.mod", fmt.Sprintf(`module contentsample
go 1.25.4
require github.com/demiurgos-hub/golem-engine v0.0.0
replace github.com/demiurgos-hub/golem-engine => %q
replace github.com/demiurgos-hub/golem-engine/golem/collision => %q
replace github.com/demiurgos-hub/golem-engine/golem/nav => %q
replace github.com/demiurgos-hub/golem-engine/golem/physics3d => %q
`, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "golem/collision")), filepath.ToSlash(filepath.Join(root, "golem/nav")), filepath.ToSlash(filepath.Join(root, "golem/physics3d"))))
	writeConsumerFile(t, dir, "content_sample_test.go", scribeContentServerContract)
	runConsumer(t, dir, "go", "mod", "tidy")
	runConsumer(t, dir, "go", "test", "-count=1", "./...")
	for _, name := range []string{"Codec/PbReader.cs", "Codec/PbWriter.cs", "Client/ClientContracts.cs"} {
		data, err := os.ReadFile(filepath.Join(root, "golem-unity/Runtime", name))
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, dir, "csharp/"+filepath.Base(name), string(data))
	}
	writeConsumerFile(t, dir, "csharp/consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework><Nullable>disable</Nullable><EnableDefaultCompileItems>false</EnableDefaultCompileItems></PropertyGroup><ItemGroup><Compile Include="EntitiesPb.cs;EntityManager.cs;SyncedMonster.cs;PbReader.cs;PbWriter.cs;ClientContracts.cs;Program.cs" /></ItemGroup></Project>`)
	writeConsumerFile(t, dir, "csharp/Program.cs", scribeContentCSharpContract)
	runConsumer(t, dir, "dotnet", "run", "--project", "csharp/consumer.csproj", "--nologo")
}

const scribeContentServerContract = `package contentsample
import (
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "contentsample/generated"
 "github.com/demiurgos-hub/golem-engine/golem"
 "github.com/demiurgos-hub/golem-engine/golem/content"
 "github.com/demiurgos-hub/golem-engine/golem/snapshot"
)
type monster struct {
 *generated.SyncedMonster
 definition generated.ContentMonster
 ready bool
 spawns int
}
func (m *monster) OnSpawn() { if !m.ready || m.Server()==nil {panic("spawn before setup")};m.spawns++ }
func TestContentToInstancesAndRecovery(t *testing.T) {
 file,err:=os.Open("generated/ContentDefinitions.json");if err!=nil {t.Fatal(err)};defer file.Close()
 defs,err:=generated.LoadDefinitions(file,content.Options{});if err!=nil {t.Fatal(err)}
 if err=defs.Catalog().ValidateFactories(map[string]bool{"monster":true});err!=nil {t.Fatal(err)}
 scout,ok:=defs.LookupMonster("demo/Monster/scout");if !ok {t.Fatal("missing scout")}
 guard,ok:=defs.LookupMonster("demo/Monster/guard");if !ok {t.Fatal("missing guard")}
 if scout.Movement().ID()!=guard.Movement().ID() || scout.Movement().Clearance()!=0.5 || scout.Stats().Speed()!=4.5 {t.Fatal("Scribe nested/shared data")}
 if _,present:=guard.Note();present {t.Fatal("Scribe optional null")}
 if !scout.Traits().Has(generated.ContentTraitsHostile) {t.Fatal("Scribe named flags")}
 srv,_:=generated.NewServer(golem.ServerConfig{})
 spawn:=func(id string)*monster {
  t.Helper();def,ok:=defs.LookupMonster(id);if !ok||def.Factory()!="monster" {t.Fatal("unregistered factory")}
  result:=&monster{SyncedMonster:generated.NewSyncedMonster(0,0,0,def.ID(),def.Health()),definition:def}
  before:=srv.Len()
  err:=srv.CreateEntityWithSetup(result,func(entity golem.Entity)(func()error,error){
   if entity!=result || srv.Len()!=before || len(srv.All())!=before {return nil,fmt.Errorf("provisional entity was published")}
   if got,ok:=srv.Get(result.EntityID());!ok||got!=result {return nil,fmt.Errorf("setup cannot resolve entity")}
   result.ready=true
   return nil,nil
  });if err!=nil {t.Fatal(err)};return result
 }
 first:=spawn(scout.ID());second:=spawn(scout.ID());third:=spawn(guard.ID())
 if first.EntityID()==second.EntityID() || second.EntityID()==third.EntityID() || srv.Len()!=3 {t.Fatal("instance identities")}
 for i,entity:=range []*monster{first,second,third} {
  frame,err:=entity.FullUpdate();if err!=nil {t.Fatal(err)}
  if err=os.WriteFile(fmt.Sprintf("monster-%d.bin",i+1),frame,0600);err!=nil {t.Fatal(err)}
 }
 first.SetHealth(13)
 if first.Health()!=13||second.Health()!=80||third.Health()!=160||scout.Health()!=80 {t.Fatal("mutable state leaked to definitions/instances")}
 if first.spawns!=1 || second.spawns!=1 || third.spawns!=1 {t.Fatal("creation spawn lifecycle")}
 manifest:=filepath.Join(t.TempDir(),"content.snapshot.json")
 if err=<-srv.SaveContentSnapshot(generated.SchemaFingerprint,defs.Hash(),manifest);err!=nil {t.Fatal(err)}
 restore:=func(rec snapshot.Record)(golem.Entity,error){
  entity,err:=generated.RestoreEntity(rec);if err!=nil {return nil,err}
  base,ok:=entity.(*generated.SyncedMonster);if !ok {return nil,fmt.Errorf("unsupported entity %T",entity)}
  def,ok:=defs.LookupMonster(base.DefinitionId());if !ok {return nil,fmt.Errorf("missing definition %s",base.DefinitionId())}
  if def.Factory()!="monster" {return nil,fmt.Errorf("unsupported factory %s",def.Factory())}
  return &monster{SyncedMonster:base,definition:def,ready:true},nil
 }
 restored,_:=generated.NewServer(golem.ServerConfig{})
 if err=restored.LoadContentSnapshot(manifest,generated.SchemaFingerprint,defs.Hash(),restore);err!=nil {t.Fatal(err)}
 if restored.Len()!=3 {t.Fatal("restored count")}
 for _,prior:=range []*monster{first,second,third} {
  entity,ok:=restored.Get(prior.EntityID());if !ok {t.Fatal("missing restored instance")};current:=entity.(*monster)
  if current.Health()!=prior.Health()||current.DefinitionId()!=prior.DefinitionId()||current.spawns!=0||current.Server()!=restored {t.Fatal("restore reapplied defaults or spawn effects")}
 }
 mismatch,_:=generated.NewServer(golem.ServerConfig{})
 if err=mismatch.LoadContentSnapshot(manifest,generated.SchemaFingerprint,strings.Repeat("0",64),restore);err==nil||mismatch.Len()!=0 {t.Fatal("accepted wrong content hash")}
 invalid,_:=generated.NewServer(golem.ServerConfig{})
 bad:=generated.NewSyncedMonster(0,0,0,"demo/Monster/deleted",12)
 if err=invalid.CreateEntityWithSetup(bad,nil);err!=nil {t.Fatal(err)}
 invalidManifest:=filepath.Join(t.TempDir(),"missing.snapshot.json")
 if err=<-invalid.SaveContentSnapshot(generated.SchemaFingerprint,defs.Hash(),invalidManifest);err!=nil {t.Fatal(err)}
 rejected,_:=generated.NewServer(golem.ServerConfig{})
 if err=rejected.LoadContentSnapshot(invalidManifest,generated.SchemaFingerprint,defs.Hash(),restore);err==nil||!strings.Contains(err.Error(),"missing definition")||rejected.Len()!=0 {t.Fatalf("invalid reference was skipped: %v",err)}
}
`

const scribeContentCSharpContract = `using System;
using System.Collections.Generic;
using System.IO;
using ContentGenerated;
using GolemEngine.Unity;

sealed class ProbeMonster : SyncedMonster, IEntityLifecycle
{
 public bool Spawned;
 public ProbeMonster(long id) : base(id) {}
 public void OnSpawn() { Program.CheckInitial(this); Spawned = true; }
 public void OnRemove() {}
}

static class Program
{
 public static void Check(bool value, string message) { if (!value) throw new Exception(message); }
 public static void CheckInitial(SyncedMonster monster)
 {
  string expected = monster.EntityId == 3 ? "demo/Monster/guard" : "demo/Monster/scout";
  int health = monster.EntityId == 3 ? 160 : 80;
  Check(monster.DefinitionId == expected && monster.Health == health, "spawn ran before full initial state");
 }
 static void Main()
 {
  var manager = new EntityManager();
  manager.RegisterMonster(id => new ProbeMonster(id));
  var spawned = new HashSet<long>();
  manager.OnSpawn(entity => {
   var monster = (ProbeMonster)entity;
   CheckInitial(monster);
   Check(monster.Spawned, "manager callback ran before initialized lifecycle callback");
   Check(ReferenceEquals(manager.Get(monster.EntityId), monster), "spawn callback cannot resolve instance");
   Check(spawned.Add(monster.EntityId), "duplicate spawn callback");
  });
  for (int i = 1; i <= 3; i++) manager.ApplyUpdate(EntityUpdate.Decode(File.ReadAllBytes("monster-" + i + ".bin")));
  var first = (SyncedMonster)manager.Get(1);
  var second = (SyncedMonster)manager.Get(2);
  var third = (SyncedMonster)manager.Get(3);
  Check(manager.GetAll().Count == 3 && spawned.Count == 3, "three instances did not share one manager");
  Check(!ReferenceEquals(first, second) && !ReferenceEquals(second, third), "instances share mutable objects");
  Check(first.GetType() == second.GetType() && second.GetType() == third.GetType(), "archetype created another network type");
  manager.ApplyUpdate(new EntityUpdate { MonsterDelta = new MonsterDelta { EntityId = 1, Revision = 1, Health = 13 } });
  Check(first.Health == 13 && second.Health == 80 && third.Health == 160, "delta changed independent instances");
  Check(first.DefinitionId == "demo/Monster/scout" && third.DefinitionId == "demo/Monster/guard" && spawned.Count == 3, "delta changed immutable definition binding or respawned");
 }
}
`
