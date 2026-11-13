"""Run one source-identified Unity/Go integration fixture, without publication."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import time
import urllib.request


ENGINE = Path(__file__).resolve().parents[1]
VALIDATION = Path("D:/Demiurgos/tests/physics-validation")
PROJECT = ENGINE / "artifacts/content-validation/20261113-content-01/project"
FIXTURE = ENGINE / "qualification/unity-integration"


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: Path, value: object) -> None:
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def captured(args: list[str], cwd: Path) -> str:
    return subprocess.check_output(args, cwd=cwd, text=True, encoding="utf-8").strip()


def source_identity(root: Path, prefixes: tuple[str, ...] = ()) -> dict:
    names = captured(["git", "ls-files", "--cached", "--others", "--exclude-standard"], root).splitlines()
    files = {}
    for name in sorted(set(names)):
        if prefixes and not any(name == p or name.startswith(p + "/") for p in prefixes):
            continue
        path = root / name
        if path.is_file():
            files[name] = digest(path)
    return {"root": str(root), "commit": captured(["git", "rev-parse", "HEAD"], root),
            "files": files, "sha256": hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest()}


def ensure_editor_closed(project: Path) -> None:
    # Query only the intended project; do not close or operate other Editors.
    escaped = str(project).replace("'", "''")
    command = ("$p='" + escaped + "'; Get-CimInstance Win32_Process -Filter \"Name = 'Unity.exe'\" | "
               "Where-Object { $_.CommandLine -and $_.CommandLine.Replace('/','\\').IndexOf("
               "$p.Replace('/','\\'),[StringComparison]::OrdinalIgnoreCase) -ge 0 } | "
               "ForEach-Object { $_.ProcessId }")
    matches = captured(["powershell", "-NoProfile", "-Command", command], ENGINE)
    if matches:
        raise RuntimeError("The selected fixture project already has an Editor open (PID " + matches + ").")


class SourceStage:
    """Track only fixture source files; preserve scene/assets and unrelated files."""
    def __init__(self, project: Path):
        self.root = (project / "Assets/Integration").resolve()
        self.root.mkdir(parents=True, exist_ok=True)
        self.index = self.root / ".golem-integration-sources.json"
        self.files = json.loads(self.index.read_text(encoding="utf-8")) if self.index.exists() else {}
        for name, expected in self.files.items():
            path = self.path(name)
            if not path.is_file() or digest(path) != expected:
                raise RuntimeError("Previously staged fixture source was edited or removed: " + str(path))

    def path(self, name: str) -> Path:
        path = (self.root / name).resolve()
        if not path.is_relative_to(self.root):
            raise RuntimeError("Invalid fixture source path: " + name)
        return path

    def clear_sources(self) -> None:
        for name in self.files:
            path = self.path(name)
            path.unlink()
            # Retain Unity GUIDs: saved fixture prefabs reference the runtime scripts.
            # Runtime/Editor files are restaged before Unity next imports the project.
        self.files = {}
        write_json(self.index, self.files)

    def put(self, name: str, data: bytes) -> None:
        path = self.path(name)
        if path.exists() and name not in self.files:
            raise RuntimeError("Refusing to overwrite unowned fixture file: " + str(path))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        self.files[name] = digest(path)
        write_json(self.index, self.files)

    def copy(self, source: Path, target: str) -> None:
        for path in sorted(source.rglob("*")):
            if path.is_file() and path.suffix in (".cs", ".asmdef"):
                self.put(target + "/" + path.relative_to(source).as_posix(), path.read_bytes())


def workspace(run: Path, consumer: Path) -> Path:
    modules = [
        ("github.com/demiurgos-hub/golem-engine", "v0.4.0", ENGINE),
        ("github.com/demiurgos-hub/golem-engine/golem/collision", "v0.3.1-0.20261013153120-49b2e72c189a", ENGINE / "golem/collision"),
        ("github.com/demiurgos-hub/golem-engine/golem/nav", "v0.4.0", ENGINE / "golem/nav"),
        ("github.com/demiurgos-hub/golem-engine/golem/nav/navmesh", "v0.1.0", ENGINE / "golem/nav/navmesh"),
        ("github.com/demiurgos-hub/golem-engine/golem/physics3d", "v0.0.0", ENGINE / "golem/physics3d"),
        ("github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d", "v0.0.0", ENGINE / "golem/physics3d/box3d"),
        ("github.com/gataengine/box3d-go", "v0.0.0-20261019212002-2a00e926a8d9", ENGINE.parent / "box3d-go"),
        ("github.com/demiurgos-hub/navmesh-go", "v0.1.0", ENGINE.parent / "navmesh-go"),
    ]
    paths = [p for _, _, p in modules] + [consumer]
    # Go's workspace directory-prefix checks need native Windows separators.
    text = "go 1.27.1\n\nuse (\n" + "".join("  " + json.dumps(str(p)) + "\n" for p in paths) + ")\n\nreplace (\n"
    text += "".join(f"  {name} {version} => {json.dumps(str(path))}\n" for name, version, path in modules) + ")\n"
    path = run / "go.work"
    path.write_text(text, encoding="utf-8")
    return path


def verify_nunit(path: Path, expected: str | None = None) -> None:
    import xml.etree.ElementTree as ET
    report = ET.parse(path).getroot()
    if report.get("result") != "Passed" or int(report.get("passed", "0")) < 1 or int(report.get("failed", "0")) != 0:
        raise RuntimeError("Unity report has failures or no passing tests: " + str(path))
    if expected:
        matches = [test for test in report.iter("test-case") if test.get("fullname") == expected]
        if len(matches) != 1 or matches[0].get("result") != "Passed":
            raise RuntimeError("Expected live integration test did not pass exactly once: " + expected)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", default="unity-integration-" + time.strftime("%Y%m%d-%H%M%S"))
    parser.add_argument("--project", type=Path, default=PROJECT)
    parser.add_argument("--editor-version", default="6000.3.13f1")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", args.run_id) or args.run_id in (".", ".."):
        parser.error("run-id must be one ordinary path component")
    project = args.project.resolve()
    if not (project / "ProjectSettings/ProjectVersion.txt").is_file():
        parser.error("reuse an existing Unity project")
    ensure_editor_closed(project)
    run = (VALIDATION / args.run_id).resolve()
    if not run.is_relative_to(VALIDATION.resolve()):
        parser.error("run directory escapes validation workspace")
    run.mkdir(exist_ok=False)
    env = os.environ.copy()
    for key, relative in {"GOCACHE": "cache/gocache", "GOMODCACHE": "cache/gomod", "GOPATH": "cache/gopath",
                          "DOTNET_CLI_HOME": "cache/dotnet-cli", "NPM_CONFIG_CACHE": "cache/npm"}.items():
        path = VALIDATION / relative
        path.mkdir(parents=True, exist_ok=True)
        env[key] = str(path)
    temp = run / "tmp"
    temp.mkdir()
    env.update(GOTOOLCHAIN="go1.27.1", CGO_ENABLED="0", GOENV="off", GOAMD64="v1", GOOS="windows",
               GOARCH="amd64", GOEXPERIMENT="", GOFLAGS="", GOTMPDIR=str(temp), TEMP=str(temp), TMP=str(temp))
    for key in list(env):
        if key.startswith("GOLEM_PHYSICS_"):
            del env[key]
    evidence = {"version": 1, "scope": "bounded Unity/Go content, physics, navigation and paired recovery integration",
                "project": str(project), "editor_version": args.editor_version, "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "runner_sha256": digest(Path(__file__)), "published_resolution": False, "checks": [], "complete": False}
    manifest = run / "manifest.json"

    def command(name: str, argv: list[str], cwd: Path, timeout: int = 600) -> None:
        print(name, flush=True)
        started = time.monotonic()
        check = {"name": name, "argv": argv, "cwd": str(cwd), "log": name + ".log"}
        evidence["checks"].append(check)
        write_json(manifest, evidence)
        with (run / check["log"]).open("w", encoding="utf-8") as log:
            try:
                result = subprocess.run(argv, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=timeout)
                check["exit_code"] = result.returncode
            except subprocess.TimeoutExpired:
                check["timed_out"] = True
                raise
            finally:
                check["seconds"] = round(time.monotonic() - started, 3)
                write_json(manifest, evidence)
        if result.returncode:
            raise RuntimeError(name + " failed; see " + str(run / check["log"]))

    roots = [(ENGINE, ("go.mod", "go.sum", "cmd", "codegen", "schema", "golem", "golem-unity", "examples/unity-integration", "qualification/unity-integration")),
             (ENGINE.parent / "box3d-go", ()), (ENGINE.parent / "navmesh-go", ())]
    before = [source_identity(root, prefixes) for root, prefixes in roots]
    write_json(run / "source-manifest.json", before)
    try:
        consumer = run / "consumer"
        shutil.copytree(ENGINE / "examples/unity-integration", consumer, ignore=shutil.ignore_patterns("bin", "obj", ".git"))
        env["GOWORK"] = str(workspace(run, consumer))
        env["GOLEM_INTEGRATION_CONSUMER"] = str(consumer)
        env["GOLEM_INTEGRATION_EVIDENCE"] = str(run)
        evidence["environment"] = {k: env[k] for k in ("GOWORK", "GOTOOLCHAIN", "CGO_ENABLED", "GOAMD64", "GOEXPERIMENT", "GOCACHE", "GOMODCACHE")}
        original_manifest = project / "Packages/manifest.json"
        package_manifest = json.loads(original_manifest.read_text(encoding="utf-8-sig"))
        shutil.copy2(original_manifest, run / "project-manifest-before.json")
        for part in ("Runtime", "Editor", "Tests"):
            shutil.copytree(ENGINE / "golem-unity" / part, run / "package" / part,
                            ignore=shutil.ignore_patterns("bin", "obj", "node_modules"))
            meta = ENGINE / "golem-unity" / (part + ".meta")
            if meta.is_file():
                shutil.copy2(meta, run / "package" / meta.name)
        for filename in ("package.json", "package.json.meta"):
            shutil.copy2(ENGINE / "golem-unity" / filename, run / "package" / filename)
        package_manifest["dependencies"]["io.demiurgos.golemengine"] = "file:" + (run / "package").as_posix()
        # Reuse the previously built transport dependency, preserving its old snapshot.
        transport = package_manifest["dependencies"]["io.demiurgos.webtransport"]
        if not transport.startswith("file:"):
            raise RuntimeError("Fixture requires its existing local WebTransport package binding")
        transport_path = Path(transport[5:])
        if not transport_path.is_absolute():
            transport_path = (project / "Packages" / transport_path).resolve()
        if not any(transport_path.rglob("*.dll")):
            raise RuntimeError("Existing transport package has no built managed dependency")
        shutil.copytree(transport_path, run / "webtransport")
        package_manifest["dependencies"]["io.demiurgos.webtransport"] = "file:" + (run / "webtransport").as_posix()
        package_hashes = {p.relative_to(run).as_posix(): digest(p) for directory in (run / "package", run / "webtransport") for p in sorted(directory.rglob("*")) if p.is_file()}
        write_json(run / "staged-package-hashes.json", package_hashes)
        write_json(original_manifest, package_manifest)
        write_json(run / "project-manifest-used.json", package_manifest)
        stage = SourceStage(project)
        stage.clear_sources()
        stage.copy(FIXTURE / "Runtime", "Runtime")
        stage.copy(FIXTURE / "Editor", "Editor")
        command("toolchain", ["go", "version"], consumer)
        command("unity-cli", ["unity", "--version"], ENGINE)
        command("content-export", ["unity", "run", str(project), "--editor-version", args.editor_version,
                "--timeout", "600", "--log-file", str(run / "content-export-editor.log"), "--no-tail", "--json", "--",
                "-nographics", "-executeMethod", "Golem.Integration.Fixture.Editor.IntegrationContentFixture.Export"], ENGINE, 660)
        if not (run / "unity-content-export.json").is_file():
            raise RuntimeError("Unity did not write content export evidence")
        fixture_assets = project / "Assets/Integration/Resources/Integration"
        asset_hashes = {p.relative_to(project).as_posix(): digest(p) for p in sorted(fixture_assets.rglob("*")) if p.is_file()}
        if not asset_hashes:
            raise RuntimeError("Unity did not retain the selected fixture assets")
        shutil.copytree(fixture_assets, run / "fixture-assets")
        write_json(run / "fixture-asset-hashes.json", asset_hashes)
        resolved_packages = json.loads((project / "Packages/packages-lock.json").read_text(encoding="utf-8-sig"))
        for name in ("io.demiurgos.golemengine", "io.demiurgos.webtransport"):
            resolved = resolved_packages["dependencies"].get(name, {})
            if resolved.get("source") != "local" or resolved.get("version") != package_manifest["dependencies"][name]:
                raise RuntimeError("Unity did not resolve the staged candidate package: " + name)
        write_json(run / "project-packages-lock-used.json", resolved_packages)
        command("codegen", ["go", "run", "github.com/demiurgos-hub/golem-engine/cmd/golem-bake"], consumer)
        stage.copy(consumer / "csharp", "Generated")
        stage.put("Generated/Golem.Integration.Generated.asmdef", json.dumps({"name": "Golem.Integration.Generated",
                  "references": ["GolemEngine.Unity", "GolemEngine.Unity.WebTransport"], "autoReferenced": False}).encode())
        stage.copy(FIXTURE / "PlayMode", "PlayMode")
        write_json(run / "generated-hashes.json", {p.relative_to(consumer).as_posix(): digest(p) for directory in (consumer / "generated", consumer / "csharp", consumer / "content") for p in sorted(directory.rglob("*")) if p.is_file()})
        command("consumer-tests", ["go", "test", "-count=1", "./..."], consumer)
        binary = run / "unity-integration.exe"
        command("consumer-build", ["go", "build", "-o", str(binary), "."], consumer)
        evidence["binary_sha256"] = digest(binary)
        command("editmode", ["unity", "test", str(project), "--mode", "EditMode", "--filter", "GolemContent",
                "--output", str(run / "editmode.xml"), "--editor-version", args.editor_version, "--timeout", "600", "--json", "--",
                "-nographics", "-logFile", str(run / "editmode-editor.log")], ENGINE, 660)
        verify_nunit(run / "editmode.xml")
        checkpoint = None
        for mode in ("fresh", "resume"):
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                port = reservation.getsockname()[1]
            base = f"http://127.0.0.1:{port}"
            env["GOLEM_INTEGRATION_URL"] = base
            env["GOLEM_INTEGRATION_MODE"] = mode
            server_evidence = run / (mode + "-server")
            server_evidence.mkdir()
            argv = [str(binary), "--addr", f"127.0.0.1:{port}", "--content", str(consumer / "generated/ContentDefinitions.json"), "--evidence", str(server_evidence)]
            if checkpoint:
                argv += ["--resume", checkpoint]
            with (run / (mode + "-server.log")).open("w", encoding="utf-8") as log:
                process = subprocess.Popen(argv, cwd=consumer, env=env, stdout=log, stderr=subprocess.STDOUT,
                                           creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
                process_record = {"mode": mode, "pid": process.pid, "argv": argv}
                evidence.setdefault("server_processes", []).append(process_record)
                write_json(manifest, evidence)
                try:
                    deadline = time.monotonic() + 45
                    while True:
                        if process.poll() is not None:
                            raise RuntimeError(mode + " server exited during startup")
                        try:
                            with urllib.request.urlopen(base + "/integration/state", timeout=2) as response:
                                state = json.load(response)
                            break
                        except OSError:
                            if time.monotonic() >= deadline:
                                raise RuntimeError(mode + " server did not become reachable")
                            time.sleep(.1)
                    write_json(run / (mode + "-server-initial.json"), state)
                    command(mode + "-playmode", ["unity", "test", str(project), "--mode", "PlayMode", "--filter",
                            "Golem.Integration.Fixture.Tests.LoopbackIntegrationTests.AuthoredDefinitionsCrossRealSocketAndSurviveCheckpoint",
                            "--output", str(run / (mode + "-playmode.xml")), "--editor-version", args.editor_version,
                            "--timeout", "600", "--json", "--", "-nographics", "-logFile", str(run / (mode + "-editor.log"))], ENGINE, 660)
                    verify_nunit(run / (mode + "-playmode.xml"),
                                 "Golem.Integration.Fixture.Tests.LoopbackIntegrationTests.AuthoredDefinitionsCrossRealSocketAndSurviveCheckpoint")
                    if not (run / ("unity-" + mode + "-client.json")).is_file():
                        raise RuntimeError("Missing live client evidence for " + mode)
                    with urllib.request.urlopen(base + "/integration/state", timeout=5) as response:
                        state = json.load(response)
                    write_json(run / (mode + "-server-final.json"), state)
                    if state.get("error"):
                        raise RuntimeError(state["error"])
                    for assertion in ("motion_passed", "query_passed", "pose_equality_passed"):
                        if not state.get(assertion):
                            raise RuntimeError(mode + " server did not verify " + assertion)
                    if mode == "fresh":
                        if not state.get("checkpoint_ready") or not state.get("checkpoint_path"):
                            raise RuntimeError("Fresh process did not complete paired checkpoint")
                        checkpoint = state["checkpoint_path"]
                finally:
                    if process.poll() is None:
                        process.terminate()
                    process_record["exit_code"] = process.wait(timeout=15)
                    write_json(manifest, evidence)
        after = [source_identity(root, prefixes) for root, prefixes in roots]
        if digest(Path(__file__)) != evidence["runner_sha256"]:
            raise RuntimeError("Integration runner changed during execution")
        if before != after:
            write_json(run / "source-manifest-after.json", after)
            raise RuntimeError("Source changed during integration; retain this run and repeat against stable source")
        if json.loads(original_manifest.read_text(encoding="utf-8-sig")) != package_manifest:
            raise RuntimeError("Unity project package bindings changed during integration")
        if json.loads((project / "Packages/packages-lock.json").read_text(encoding="utf-8-sig")) != resolved_packages:
            raise RuntimeError("Unity package resolution changed during integration")
        for name, expected in package_hashes.items():
            if digest(run / name) != expected:
                raise RuntimeError("Unity changed staged package input: " + name)
        for name, expected in stage.files.items():
            if digest(stage.path(name)) != expected:
                raise RuntimeError("Staged generated/fixture source changed during execution: " + name)
        for name, expected in asset_hashes.items():
            if digest(project / name) != expected:
                raise RuntimeError("Selected fixture asset changed during execution: " + name)
        evidence["complete"] = True
    except BaseException as error:
        evidence["error"] = str(error)
        raise
    finally:
        evidence["finished_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        write_json(manifest, evidence)
    print("Integration complete; published dependency resolution remains unverified. Evidence: " + str(run), flush=True)


if __name__ == "__main__":
    main()
