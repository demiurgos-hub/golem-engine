package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These run the real NUnit policy tests without pretending to simulate Unity's
// GameObject lifecycle. Play Mode tests remain a separate Unity acceptance gate.
func TestUnityRuntimePolicyConsumer(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		if os.Getenv("GOLEM_REQUIRE_CONSUMERS") == "1" {
			t.Fatal(err)
		}
		t.Skip(".NET SDK required for Unity runtime policy checks")
	}
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "Runtime")
	testsDir := filepath.Join(dir, "Tests")
	for _, path := range []string{runtimeDir, testsDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	copySource := func(source, target string) {
		t.Helper()
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, target, filepath.Base(source), string(data))
	}
	for _, name := range []string{
		"AssemblyInfo.cs", "Client/ClientContracts.cs", "Client/GameClient.cs",
		"Client/GolemConnectOptions.cs", "Client/GolemRealtimeBootstrap.cs",
		"Client/GolemBoundedDownloadHandler.cs", "Client/GolemUnityLog.cs",
		"Codec/PbReader.cs", "Codec/PbWriter.cs", "Transport/GolemDatagramProtocol.cs",
		"Transport/GolemReliableFrameCodec.cs", "Transport/GolemWebSocketTransport.cs",
	} {
		copySource(filepath.Join("..", "golem-unity", "Runtime", name), runtimeDir)
	}
	tests, err := filepath.Glob(filepath.Join("..", "golem-unity", "Tests", "Runtime", "*.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		copySource(test, testsDir)
	}
	writeConsumerFile(t, runtimeDir, "Runtime.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework><AssemblyName>GolemEngine.Unity</AssemblyName><LangVersion>9.0</LangVersion><Nullable>disable</Nullable></PropertyGroup></Project>`)
	writeConsumerFile(t, runtimeDir, "UnityBoundaries.cs", unityPolicyBoundaries)
	writeConsumerFile(t, testsDir, "Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework><AssemblyName>GolemEngine.Unity.Tests</AssemblyName><LangVersion>9.0</LangVersion><Nullable>disable</Nullable><IsTestProject>true</IsTestProject></PropertyGroup><ItemGroup><ProjectReference Include="../Runtime/Runtime.csproj"/><PackageReference Include="Microsoft.NET.Test.Sdk" Version="17.11.1"/><PackageReference Include="NUnit" Version="3.14.0"/><PackageReference Include="NUnit3TestAdapter" Version="4.6.0"/></ItemGroup></Project>`)
	runConsumer(t, testsDir, "dotnet", "test", "Tests.csproj", "--nologo", "--logger", "console;verbosity=normal")
}

// No native networking or Unity lifecycle is exercised here. Unexpected use of
// Unity's HTTP boundary fails; connection tests inject their own fetch/transport.
const unityPolicyBoundaries = `using System;
namespace UnityEngine {
 public static class Debug { public static void Log(object value) {} public static void LogWarning(object value) {} public static void LogError(object value) {} }
}
namespace UnityEngine.Networking {
 public class DownloadHandlerScript {
  protected virtual bool ReceiveData(byte[] data,int length) => throw new NotSupportedException();
  protected virtual byte[] GetData() => throw new NotSupportedException();
  protected virtual string GetText() => throw new NotSupportedException();
 }
 public sealed class UnityWebRequest : IDisposable {
  public const string kHttpVerbGET="GET";
  public DownloadHandlerScript downloadHandler;
  public string error;
  public long responseCode;
  public UnityWebRequest(string url,string method) { throw new NotSupportedException("Unity HTTP requires Unity"); }
  public Operation SendWebRequest() => throw new NotSupportedException();
  public void Dispose() {}
  public class Operation { public bool isDone; }
 }
}
namespace GolemEngine.Unity {
 public static class GolemMainThreadDispatcher {
  // EditMode has no frame pump. Tests flush commands explicitly and inspect
  // connection state; callback/frame execution is covered by Unity tests.
  public static void Enqueue(Action action) {}
 }
}
`
