package codegen

// These contracts execute generated managers with real codecs and runtime
// interfaces. Language-specific callback and decoding behavior stays explicit.
const goManagerContract = `package generated
import("testing";"reflect"; "github.com/demiurgos-hub/golem-engine/golem-go-client/pb")
type hookedActor struct { *SyncedActor; remove func() }
func (a *hookedActor) OnSpawn() {}
func (a *hookedActor) OnRemove() { a.remove() }
func TestManagerLifecycle(t *testing.T) {
 m:=NewEntityManager(); var order []string
 m.RegisterActor(func(id int64) ActorClientEntity { return &hookedActor{NewSyncedActor(id),func(){if m.Get(id)!=nil {t.Fatal("membership during lifecycle")}; order=append(order,"hook")}} })
 m.OnRemove(func(id int64){if m.Get(id)!=nil {t.Fatal("membership during notification")};order=append(order,"listener")})
 state:=func(id int64,rev uint64){m.ApplyEntityUpdate(&EntityUpdate{Payload:&EntityUpdate_ActorState{ActorState:&ActorState{EntityId:id,Revision:rev,Items:[]Item{{Id:1}}}}})}
 remove:=func(id int64,rev uint64){m.ApplyEntityUpdate(&EntityUpdate{Payload:&EntityUpdate_EntityRemoved{EntityRemoved:&EntityRemoved{EntityId:id,Revision:rev}}})}
 state(7,5);remove(7,4);if m.Get(7)==nil {t.Fatal("stale removal")};remove(7,5)
 if !reflect.DeepEqual(order,[]string{"hook","listener"}) {t.Fatal(order)}
 remove(7,8);state(7,7);if m.Get(7)!=nil||len(order)!=2 {t.Fatal("tombstone")};state(7,8)
 updates:=0;m.OnUpdate(func(SyncedEntity){updates++})
 m.applyDeltaActor(&ActorDelta{EntityId:7,Revision:9});if len(m.Get(7).(*hookedActor).Items())!=1 {t.Fatal("absent collection")}
 compact:=func(id int64,rev,mask uint64,body []byte){m.ApplyCompactUpdate((&pb.Writer{}).Int64(id).Uint64(rev).Uint64(mask).Raw(body).Finish())}
 compact(7,10,1,(&ActorDelta{EntityId:99,Revision:99,Items:[]Item{}}).Marshal())
 if updates!=2||m.Get(7).(*hookedActor).Revision()!=10||len(m.Get(7).(*hookedActor).Items())!=0 {t.Fatal("compact parity/header/presence")}
 compact(7,9,1,nil);compact(7,11,0,nil);compact(99,11,1,nil);compact(7,11,1,[]byte{0x12,0xff})
 if updates!=2 {t.Fatal("invalid compact notified")}
 compact(7,10,1,nil);if updates!=3 {t.Fatal("equal revision")}
 // The nested clear removes the other snapshotted entity; the outer clear
 // must look it up again, not replay a captured object or callback.
 state(8,10);m.OnRemove(func(int64){order=append(order,"listener");m.Clear()});order=nil;m.Clear()
 if len(order)!=4||len(m.All())!=0 {t.Fatal("reentrant clear",order)}
 state(7,0);if m.Get(7)==nil {t.Fatal("clear did not reset revisions")}
 m.RegisterActor(func(id int64) ActorClientEntity{return &hookedActor{NewSyncedActor(id),func(){panic("hook")}}})
 m.Clear();state(7,20);called:=false;m.OnRemove(func(int64){called=true})
 func(){defer func(){if recover()==nil {t.Fatal("missing panic")}}();m.Clear()}()
 if called||m.Get(7)!=nil {t.Fatal("throw ordering")};state(7,19);if m.Get(7)!=nil {t.Fatal("throw reset tombstone")}
}
// A compact datagram record can overtake the stream spawn of its entity.
func TestManagerHoldsCompactBeforeSpawn(t *testing.T) {
 m:=NewEntityManager();updates:=0;m.OnUpdate(func(SyncedEntity){updates++})
 state:=func(id int64,rev uint64){m.ApplyEntityUpdate(&EntityUpdate{Payload:&EntityUpdate_ActorState{ActorState:&ActorState{EntityId:id,Revision:rev,Items:[]Item{{Id:1}}}}})}
 compact:=func(id int64,rev uint64,items []Item){m.ApplyCompactUpdate((&pb.Writer{}).Int64(id).Uint64(rev).Uint64(1).Raw((&ActorDelta{Items:items}).Marshal()).Finish())}
 compact(5,4,[]Item{});compact(5,6,[]Item{{Id:1},{Id:2}})
 if m.Get(5)!=nil {t.Fatal("held record created an entity")}
 state(5,5);a:=m.Get(5).(*SyncedActor)
 if a.Revision()!=6||len(a.Items())!=2||updates!=1 {t.Fatal("held records not replayed",a.Revision(),a.Items(),updates)}
 compact(6,4,[]Item{{Id:1},{Id:2}});m.ApplyEntityUpdate(&EntityUpdate{Payload:&EntityUpdate_EntityRemoved{EntityRemoved:&EntityRemoved{EntityId:6,Revision:4}}})
 state(6,4);if len(m.Get(6).(*SyncedActor).Items())!=1 {t.Fatal("removal kept held records")}
 for i:=0;i<300;i++ {compact(int64(1000+i),1,nil)}
 if m.heldCount!=256||len(m.heldCompact)!=256 {t.Fatal("held records unbounded",m.heldCount)}
 m.Clear();if m.heldCount!=0||len(m.heldCompact)!=0 {t.Fatal("clear kept held records")}
}
`

const jsManagerContract = `import assert from 'node:assert/strict';
import { EntityManager } from './out/EntityManager.js';
import { SyncedActor } from './out/ActorSynced.js';
import { ActorState, ActorDelta } from './out/entities_pb.js';
import { PbWriter } from 'golem-engine';
const m = new EntityManager(); let order = [];
class Actor extends SyncedActor { onRemove() { assert.equal(m.get(this.entityId),undefined); order.push('hook'); } }
m.registerActor(Actor);
const off = m.onRemove(id => { assert.equal(m.get(id),undefined); order.push('listener'); });
const state = (id,revision) => m.applyUpdate({actorState:Object.assign(new ActorState(),{entityId:id,revision,items:[{id:1}]})});
const remove = (id,revision) => m.applyUpdate({entityRemoved:{entityId:id,revision}});
state(7,5); remove(7,4); assert.ok(m.get(7)); remove(7,5); assert.deepEqual(order,['hook','listener']);
remove(7,8); state(7,7); assert.equal(m.get(7),undefined); assert.equal(order.length,2); state(7,8);
let updates=0; m.onUpdate(()=>updates++);
m.applyUpdate({actorDelta:Object.assign(new ActorDelta(),{entityId:7,revision:9})}); assert.equal(m.get(7).items.length,1);
const compact=(id,revision,mask,body=new Uint8Array())=>{
 const header=new PbWriter().int64(id).uint64(revision).uint64(mask).finish();
 const bytes=new Uint8Array(header.length+body.length);bytes.set(header);bytes.set(body,header.length);m.applyCompactUpdate(bytes);
};
compact(7,10,1,ActorDelta.encode(Object.assign(new ActorDelta(),{entityId:99,revision:99,items:[]})));
assert.equal(updates,2);assert.equal(m.get(7).revision,10);assert.deepEqual(m.get(7).items,[]);
compact(7,9,1);compact(7,11,0);compact(99,11,1);
assert.throws(()=>compact(7,11,1,new Uint8Array([0x12,0xff])));assert.equal(updates,2);
compact(7,10,1);assert.equal(updates,3);
state(8,10);off();m.onRemove(()=>{order.push('listener');m.clear();});order=[];m.clear();assert.equal(order.length,4);assert.equal(m.getAll().size,0);
state(7,0);assert.ok(m.get(7));m.clear();
class ThrowingActor extends SyncedActor { onRemove(){throw new Error('hook');} }
m.registerActor(ThrowingActor);state(7,20);let called=false;m.onRemove(()=>called=true);
assert.throws(()=>m.clear(),/hook/);assert.equal(called,false);assert.equal(m.get(7),undefined);state(7,19);assert.equal(m.get(7),undefined);
// A compact datagram record can overtake the stream spawn of its entity.
const h=new EntityManager();let held=0;h.onUpdate(()=>held++);
const hstate=(id,revision)=>h.applyUpdate({actorState:Object.assign(new ActorState(),{entityId:id,revision,items:[{id:1}]})});
const hcompact=(id,revision,items)=>{
 const body=ActorDelta.encode(Object.assign(new ActorDelta(),{items}));const header=new PbWriter().int64(id).uint64(revision).uint64(1).finish();
 const bytes=new Uint8Array(header.length+body.length);bytes.set(header);bytes.set(body,header.length);h.applyCompactUpdate(bytes);
};
hcompact(5,4,[]);hcompact(5,6,[{id:1},{id:2}]);assert.equal(h.get(5),undefined);
hstate(5,5);assert.equal(h.get(5).revision,6);assert.equal(h.get(5).items.length,2);assert.equal(held,1);
hcompact(6,4,[{id:1},{id:2}]);h.applyUpdate({entityRemoved:{entityId:6,revision:4}});hstate(6,4);assert.equal(h.get(6).items.length,1);
for(let i=0;i<300;i++)hcompact(1000+i,1,[]);assert.equal(h._heldCount,256);assert.equal(h._heldCompact.size,256);
h.clear();assert.equal(h._heldCount,0);
`

const csManagerContract = `using System;using System.Collections.Generic;using System.Linq;using Generated;using GolemEngine.Unity;
static class ManagerContract {
 class Actor : SyncedActor, IEntityLifecycle { public Action Remove; public Actor(long id,Action remove):base(id){Remove=remove;} public void OnSpawn(){} public void OnRemove(){Remove();} }
 static void Check(bool value,string message){if(!value)throw new Exception(message);}
 public static void Run(){
 var m=new EntityManager();var order=new List<string>();
 m.RegisterActor(id=>new Actor(id,()=>{Check(m.Get(id)==null,"membership during hook");order.Add("hook");}));
 m.OnRemove(id=>{Check(m.Get(id)==null,"membership during notification");order.Add("listener");});
 Action<long,ulong> state=(id,rev)=>m.ApplyUpdate(new EntityUpdate{ActorState=new ActorState{EntityId=id,Revision=rev,Items=new List<Item>{new Item{Id=1}}}});
 Action<long,ulong> remove=(id,rev)=>m.ApplyUpdate(new EntityUpdate{EntityRemoved=new EntityRemoved{EntityId=id,Revision=rev}});
 state(7,5);remove(7,4);Check(m.Get(7)!=null,"stale removal");remove(7,5);Check(order.SequenceEqual(new[]{"hook","listener"}),"remove order");
 remove(7,8);state(7,7);Check(m.Get(7)==null&&order.Count==2,"tombstone");state(7,8);
 int updates=0;m.OnUpdate(_=>updates++);m.ApplyUpdate(new EntityUpdate{ActorDelta=new ActorDelta{EntityId=7,Revision=9}});Check(((Actor)m.Get(7)).Items.Count==1,"absent collection");
 Action<long,ulong,ulong,byte[]> compact=(id,rev,mask,body)=>m.ApplyCompactUpdate(new PbWriter().Int64(id).Uint64(rev).Uint64(mask).Finish().Concat(body).ToArray());
 compact(7,10,1,ActorDelta.Encode(new ActorDelta{EntityId=99,Revision=99,Items=new List<Item>()}));
 Check(updates==2&&((Actor)m.Get(7)).Revision==10&&((Actor)m.Get(7)).Items.Count==0,"compact parity/header/presence");
 compact(7,9,1,Array.Empty<byte>());compact(7,11,0,Array.Empty<byte>());compact(99,11,1,Array.Empty<byte>());
 bool failed=false;try{compact(7,11,1,new byte[]{0x12,0xff});}catch{failed=true;}Check(failed&&updates==2,"invalid compact");
 compact(7,10,1,Array.Empty<byte>());Check(updates==3,"equal revision");
 state(8,10);m.OnRemove(_=>{order.Add("listener");m.Clear();});order.Clear();m.Clear();Check(order.Count==4&&m.GetAll().Count==0,"reentrant clear");
 state(7,0);Check(m.Get(7)!=null,"clear revisions");m.Clear();
 m.RegisterActor(id=>new Actor(id,()=>{throw new Exception("hook");}));state(7,20);bool called=false;m.OnRemove(_=>called=true);
 failed=false;try{m.Clear();}catch{failed=true;}Check(failed&&!called&&m.Get(7)==null,"throw ordering");state(7,19);Check(m.Get(7)==null,"throw reset revisions");
 // A compact datagram record can overtake the stream spawn of its entity.
 var h=new EntityManager();int held=0;h.OnUpdate(_=>held++);
 Action<long,ulong> hstate=(id,rev)=>h.ApplyUpdate(new EntityUpdate{ActorState=new ActorState{EntityId=id,Revision=rev,Items=new List<Item>{new Item{Id=1}}}});
 Action<long,ulong,List<Item>> hcompact=(id,rev,items)=>h.ApplyCompactUpdate(new PbWriter().Int64(id).Uint64(rev).Uint64(1).Finish().Concat(ActorDelta.Encode(new ActorDelta{Items=items})).ToArray());
 hcompact(5,4,new List<Item>());hcompact(5,6,new List<Item>{new Item{Id=1},new Item{Id=2}});Check(h.Get(5)==null,"held record created an entity");
 hstate(5,5);var a=(SyncedActor)h.Get(5);Check(a.Revision==6&&a.Items.Count==2&&held==1,"held records not replayed");
 hcompact(6,4,new List<Item>{new Item{Id=1},new Item{Id=2}});h.ApplyUpdate(new EntityUpdate{EntityRemoved=new EntityRemoved{EntityId=6,Revision=4}});
 hstate(6,4);Check(((SyncedActor)h.Get(6)).Items.Count==1,"removal kept held records");
 for(int i=0;i<300;i++)hcompact(1000+i,1,new List<Item>());
 for(long id=1000;id<1300;id++){hstate(id,1);}
 Check(held==1+256,"held records unbounded");h.Clear();
 }
}
`

const jsEventManagerContract = `import assert from 'node:assert/strict';
import { EntityManager } from './out/EntityManager.js';
import { EventManager } from './out/EventManager.js';
import { SyncedActor } from './out/ActorSynced.js';
import { SyncedBeacon } from './out/BeaconSynced.js';
import { PbWriter } from 'golem-engine';
const entities = new EntityManager(); const events = new EventManager(entities); const order=[];
const actor=new SyncedActor(1);actor.onHit=()=>order.push('hook');const beacon=new SyncedBeacon(2);
entities._entities.set(1,actor);entities._entities.set(2,beacon);
const registrations=[];
for(const name of ['Hit','Pulse']) { const original=events['on'+name].bind(events);events['on'+name]=fn=>{registrations.push(name);return original(fn);}; }
events.onHit(()=>order.push('typed'));registrations.length=0;
const calls=[];const stop=events.subscribeEntityEvents((entity,name,event)=>{order.push('composite');calls.push([entity,name,event]);});
assert.deepEqual(registrations,['Hit','Pulse']);
const other=[];const stopOther=events.subscribeEntityEvents((entity,name)=>other.push(name));
const hit={entityId:1};events.applyUpdate({hit});assert.deepEqual(order,['hook','typed','composite']);assert.deepEqual(calls,[[actor,'Hit',hit]]);
events.applyUpdate({pulse:{entityId:2}});assert.equal(calls[1][0],beacon);assert.equal(calls[1][1],'Pulse');
events.applyUpdate({hit:{entityId:2}});events.applyUpdate({hit:{entityId:99}});events.applyUpdate({notice:{}});events.applyUpdate({private:{}});assert.equal(calls.length,2);
stop();stop();events.applyUpdate({hit});assert.equal(calls.length,2);assert.deepEqual(other,['Hit','Pulse','Hit']);
stopOther();events.applyRaw(new PbWriter().tag(1,2).bytes(new Uint8Array([8,1])).finish());assert.equal(other.length,3);
// Global/session-only managers return a valid empty composite (covered by the
// generated framework consumers); independent listeners survive this cleanup.
assert.deepEqual(order.slice(-2),['hook','typed']);
`

// World manager contracts: ClearWorld drops one world type by its WorldName,
// notifies cleared listeners only when a value was held, ignores unknown
// names and satisfies the runtimes' optional clearer contracts.
const goWorldManagerContract = `package generated
import("reflect";"testing";golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client")
var _ golemclient.WorldManagerLike = (*WorldManager)(nil)
var _ golemclient.WorldClearer = (*WorldManager)(nil)
func TestWorldManagerClear(t *testing.T) {
 m:=NewWorldManager();var log []string
 m.OnArenaUpdate(func(*ArenaData){log=append(log,"update")});m.OnArenaCleared(func(){log=append(log,"cleared")})
 m.ClearWorld((&ArenaData{}).WorldName());if len(log)!=0 {t.Fatal("clear without a value notified")}
 m.ApplyUpdate(&WorldUpdate{Payload:&WorldUpdate_ArenaData{ArenaData:&ArenaData{Counts:[]int32{1}}}})
 m.ApplyUpdate(&WorldUpdate{Payload:&WorldUpdate_CatalogData{CatalogData:&CatalogData{}}})
 m.ClearWorld("Unknown");m.ClearWorld("arena");if m.Arena()==nil {t.Fatal("unknown name cleared")}
 m.ClearWorld("Arena");if m.Arena()!=nil||m.Catalog()==nil {t.Fatal("ClearWorld cleared the wrong world")}
 if !reflect.DeepEqual(log,[]string{"update","cleared"}) {t.Fatal(log)}
}
`

const jsWorldManagerCheck = `import type { WorldManagerLike } from 'golem-engine';
import { WorldManager } from './WorldManager.js';
const manager: WorldManagerLike = new WorldManager();
export const clearThroughContract = (name: string): void => manager.clearWorld?.(name);
`

const jsWorldManagerContract = `import assert from 'node:assert/strict';
import { WorldManager } from './out/WorldManager.js';
import { clearThroughContract } from './out/world_check.js';
const m=new WorldManager();const log=[];
m.onArenaUpdate(()=>log.push('update'));const off=m.onArenaCleared(()=>log.push('cleared'));m.onArenaCleared(()=>log.push('second'));
m.clearWorld('Arena');assert.deepEqual(log,[]);
m.applyUpdate({arenaData:{counts:[1],items:{}}});m.applyUpdate({catalogData:{items:{}}});
m.clearWorld('Unknown');m.clearWorld('arena');assert.ok(m.arena);
m.clearWorld('Arena');assert.equal(m.arena,undefined);assert.ok(m.catalog);assert.deepEqual(log,['update','cleared','second']);
off();m.applyUpdate({arenaData:{counts:[],items:{}}});m.clearWorld('Arena');assert.deepEqual(log,['update','cleared','second','update','second']);
clearThroughContract('Catalog');
`

const csWorldManagerContract = `using System;using System.Collections.Generic;using System.Linq;using Generated;using GolemEngine.Unity;
static class WorldContract {
 static void Check(bool value,string message){if(!value)throw new Exception(message);}
 public static void Run(){
 var m=new WorldManager();IWorldClearer clearer=m;IWorldManager manager=m;var log=new List<string>();
 m.OnArenaUpdate(_=>log.Add("update"));m.OnArenaCleared(()=>log.Add("cleared"));
 clearer.ClearWorld("Arena");Check(log.Count==0,"clear without a value notified");
 manager.ApplyUpdate(new WorldUpdate{ArenaData=new ArenaData()});m.ApplyUpdate(new WorldUpdate{CatalogData=new CatalogData()});
 clearer.ClearWorld("Unknown");clearer.ClearWorld("arena");Check(m.Arena!=null,"unknown name cleared");
 clearer.ClearWorld("Arena");Check(m.Arena==null&&m.Catalog!=null,"ClearWorld cleared the wrong world");
 Check(log.SequenceEqual(new[]{"update","cleared"}),"cleared notification");
 }
}
`
