package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGeneratedCSharpFactoryConsumer(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for flags := 0; flags < 8; flags++ {
		pd := resolutionFixture(2)
		if flags&1 == 0 {
			pd.Commands = nil
		}
		if flags&2 == 0 {
			pd.WorldTypes = nil
		}
		if flags&4 == 0 {
			pd.Events = nil
		}
		pd = schema.BuildProtoData(&schema.Config{Simulation: schema.SimulationConfig{Dimensions: 2}}, pd.Entities, pd.Commands, pd.WorldTypes, pd.CustomTypes, pd.Events)
		name := fmt.Sprintf("Generated%d", flags)
		out := filepath.Join(dir, name)
		if err := os.Mkdir(out, 0700); err != nil {
			t.Fatal(err)
		}
		data := schema.SharedData{GoPackage: name, GolemImport: "GolemEngine.Unity", Entities: pd.Entities, Commands: pd.Commands, WorldTypes: pd.WorldTypes, Events: pd.Events, CustomTypes: pd.CustomTypes}
		if err := generateCSharpProto(out, data.GolemImport, name, pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd); err != nil {
			t.Fatal(err)
		}
		for _, output := range selectIntegrationOutputs("csharp-client", data) {
			switch output.role {
			case entityOutput:
				entity := pd.Entities[output.entityIndex]
				entity.GoPackage = name
				renderConsumerTemplate(t, out, output.template, output.file, entity)
			case sharedOutput:
				renderConsumerTemplate(t, out, output.template, output.file, data)
			}
		}
	}
	for _, path := range []string{"Codec/PbReader.cs", "Codec/PbWriter.cs", "Client/ClientContracts.cs", "Client/GolemConnectOptions.cs"} {
		contents, err := os.ReadFile(filepath.Join(root, "golem-unity/Runtime", path))
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, dir, filepath.Base(path), string(contents))
	}
	writeConsumerFile(t, dir, "consumer.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework><Nullable>disable</Nullable></PropertyGroup></Project>`)
	writeConsumerFile(t, dir, "Program.cs", csFactoryContract)
	runConsumer(t, dir, "dotnet", "run", "--project", "consumer.csproj", "--nologo")
}

const csFactoryContract = `using System;using System.Collections.Generic;using System.Reflection;using GolemEngine.Unity;
namespace GolemEngine.Unity {
 // Record overload selection and arguments, without reimplementing GameClient.
 public class GameClient {
  public bool Legacy; public IEntityManager Entities; public IWorldManager World; public IEventManager Events;
  public Delegate Factory; public Func<GolemConnectOptions,bool> Supported;
  public Func<byte[],object> DecodeEntity, DecodeWorld; public Func<object,byte[]> Encode; public Func<IReadOnlyList<byte[]>,byte[]> Packet;
  public GameClient(IEntityManager entities,Func<byte[],object> decode,Func<object,byte[]> encode,Func<IReadOnlyList<byte[]>,byte[]> packet,Func<IGolemTransport> factory,IWorldManager world=null,Func<byte[],object> decodeWorld=null,IEventManager events=null) {
   Legacy=true;Record(entities,decode,encode,packet,factory,world,decodeWorld,events,null);
  }
  public GameClient(IEntityManager entities,Func<byte[],object> decode,Func<object,byte[]> encode,Func<IReadOnlyList<byte[]>,byte[]> packet,Func<GolemConnectOptions,IGolemTransport> factory,IWorldManager world=null,Func<byte[],object> decodeWorld=null,IEventManager events=null,Func<GolemConnectOptions,bool> supported=null) {
   Record(entities,decode,encode,packet,factory,world,decodeWorld,events,supported);
  }
  void Record(IEntityManager e,Func<byte[],object> d,Func<object,byte[]> c,Func<IReadOnlyList<byte[]>,byte[]> p,Delegate f,IWorldManager w,Func<byte[],object> wd,IEventManager v,Func<GolemConnectOptions,bool> s){Entities=e;DecodeEntity=d;Encode=c;Packet=p;Factory=f;World=w;DecodeWorld=wd;Events=v;Supported=s;}
  public void Send(object command){} public void SendOrdered(object command){}
 }
 public class GolemWebSocketTransport : IGolemTransport {
  public bool Connected=>false; public int MaxMessageBytes=>0; public int MaxDatagramBytes=>0;
  public event Action ConnectedEvent; public event Action<byte[]> MessageEvent,UnreliableStateMessageEvent,ReliableOrderedMessageEvent,EventualStateMessageEvent; public event Action<GolemDisconnectInfo> DisconnectedEvent;
  public void Connect(string url){} public void Send(byte[] b){} public void SendUnreliable(byte[] b){} public void SendReliableUnordered(byte[] b){} public void SendReliableOrdered(byte[] b){} public void Close(){}
 }
 public class GolemWebTransportTransport : GolemWebSocketTransport { public static IGolemTransport FromConnectOptions(GolemConnectOptions o)=>new GolemWebTransportTransport(); }
}
class Program {
 static void Check(bool value,string message){if(!value)throw new Exception(message);}
 static void Main(){
  for(int flags=0;flags<8;flags++){
   var ns="Generated"+flags;var type=Type.GetType(ns+".ClientFactory");
   var old=type.GetMethod("CreateClient",new[]{typeof(Func<IGolemTransport>)});
   Func<IGolemTransport> legacy=()=>new GolemWebSocketTransport();
   var a=(GameClient)old.Invoke(null,new object[]{legacy});Check(a.Legacy&&ReferenceEquals(a.Factory,legacy),"legacy constructor");
   bool failed=false;try{old.Invoke(null,new object[]{null});}catch(TargetInvocationException e){failed=e.InnerException is ArgumentNullException;}Check(failed,"legacy null check");
   var modern=type.GetMethod("CreateClient",new[]{typeof(Func<GolemConnectOptions,IGolemTransport>),typeof(Func<GolemConnectOptions,bool>)});
   Func<GolemConnectOptions,IGolemTransport> factory=o=>new GolemWebSocketTransport();Func<GolemConnectOptions,bool> supported=o=>false;
   var b=(GameClient)modern.Invoke(null,new object[]{factory,supported});Check(!b.Legacy&&ReferenceEquals(b.Factory,factory)&&ReferenceEquals(b.Supported,supported),"options forwarding");
   var c=(GameClient)modern.Invoke(null,new object[]{factory,null});Check(c.Supported==null,"custom default capability");
   var builtIn=(GameClient)type.GetMethod("CreateClient",Type.EmptyTypes).Invoke(null,null);
   Check(!builtIn.Legacy&&builtIn.Supported.Method==((Func<GolemConnectOptions,bool>)GolemTransportCapabilities.BuiltInTransportSupported).Method,"built-in capability");
   Check(builtIn.Factory.DynamicInvoke(new GolemConnectOptions("ws://localhost","websocket")) is GolemWebSocketTransport,"built-in websocket");
   Check(builtIn.Factory.DynamicInvoke(new GolemConnectOptions("https://localhost","webtransport")) is GolemWebTransportTransport,"built-in webtransport");
   foreach(var client in new[]{a,b,c,builtIn}){
    Check(client.Entities!=null&&client.DecodeEntity(Array.Empty<byte>()).GetType().FullName==ns+".EntityUpdate","entity setup/codec");
    Check((client.World!=null)==((flags&2)!=0)&&(client.DecodeWorld!=null)==((flags&2)!=0),"optional world");
    Check((client.Events!=null)==((flags&4)!=0),"optional events");
    if(client.World!=null)Check(client.DecodeWorld(Array.Empty<byte>()).GetType().FullName==ns+".WorldUpdate","world codec");
    if(client.Events!=null)Check(ReferenceEquals(client.Events.GetType().GetField("_entities",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(client.Events),client.Entities),"manager identity");
    if((flags&1)==0)Check(client.Encode(null).Length==0&&client.Packet(new[]{new byte[]{1}}).Length==0,"no commands");
    else {Check(client.Encode(Activator.CreateInstance(Type.GetType(ns+".ClientMessage"))).Length==0,"command codec");Check(client.Packet(new[]{new byte[]{1}}).Length>0,"packet codec");}
   }
  }
 }
}
`
