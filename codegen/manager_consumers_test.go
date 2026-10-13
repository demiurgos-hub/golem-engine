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
 }
}
`
