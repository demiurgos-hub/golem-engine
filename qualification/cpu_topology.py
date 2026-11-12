"""Read Windows CPU topology/settings without changing affinity or power policy."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path("C:/Demiurgos/navmesh-validation/world-test"))
    parser.add_argument("--lane", type=Path, required=True)
    parser.add_argument("--go", default="go")
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("this probe uses documented Windows CPU-set APIs")
    root, lane = args.root.resolve(), args.lane.resolve()
    if not lane.is_relative_to(root / "workspaces"):
        parser.error("lane must be an existing qualification snapshot")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    run_id = "cpu-topology-" + lane.name + "-" + stamp
    evidence = root / "runs" / run_id
    binaries = root / "bin" / lane.name / "windows" / run_id
    evidence.mkdir(parents=True)
    binaries.mkdir(parents=True)
    source = lane / "golem-engine/qualification/cputopologyprobe/main_windows.go"
    executable = binaries / "cpu-topology.exe"
    env = dict(os.environ)
    candidate_env = json.loads((lane / "environment.json").read_text())
    env.update(candidate_env)
    env.update(CGO_ENABLED="0", GOWORK="off", GOFLAGS="-mod=readonly", GOTOOLCHAIN="local")
    go = Path(shutil.which(args.go, path=env.get("PATH")) or args.go).resolve(strict=True)
    pwsh = Path(shutil.which("pwsh", path=env.get("PATH"))).resolve(strict=True)
    powercfg = Path(os.environ["SystemRoot"]) / "System32/powercfg.exe"
    manifest = {
        "read_only_diagnostic": True, "candidate": str(lane), "commands": [],
        "working_directory": str(source.parent),
        "environment": {name: env.get(name) for name in sorted(set(candidate_env) | {"PATH", "CGO_ENABLED", "GOWORK", "GOFLAGS", "GOTOOLCHAIN", "GOENV", "GOMAXPROCS", "GOGC", "GOMEMLIMIT"})},
        "source_sha256": digest(source), "script": str(Path(__file__).resolve()), "script_sha256": digest(__file__),
        "interpretation": "A short query only; no benchmark, affinity/priority/power mutation or global environment write. Process settings describe the probe, not historical tick placement.",
    }
    commands = [
        ("build", [go, "build", "-o", executable, source]),
        ("result", [executable]),
        ("go-environment", [go, "env", "-json"]),
        ("power-plan", [powercfg, "/getactivescheme"]),
        ("processor", [pwsh, "-NoProfile", "-Command", "Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors,MaxClockSpeed,CurrentClockSpeed | ConvertTo-Json"]),
    ]
    for label, command in commands:
        command = [str(value) for value in command]
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        result = subprocess.run(command, cwd=source.parent, env=env, text=True, capture_output=True)
        extension = ".json" if label in ("result", "go-environment", "processor") else ".log"
        (evidence / (label + extension)).write_text(result.stdout)
        (evidence / (label + ".stderr.log")).write_text(result.stderr)
        manifest["commands"].append({"argv": command, "resolved_executable": command[0], "executable_sha256": digest(command[0]), "started_utc": started, "finished_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "exit_code": result.returncode})
        (evidence / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        if result.returncode:
            raise RuntimeError(f"CPU topology diagnostic failed ({result.returncode}); see {evidence}")
    sdk = Path("C:/Program Files (x86)/Windows Kits/10/Include")
    manifest["local_sdk_headers"] = [{"path": str(path), "sha256": digest(path)} for name in ("winnt.h", "processthreadsapi.h") for path in sorted(sdk.glob("*/um/" + name))]
    shutil.copyfile(source, evidence / source.name)
    shutil.copyfile(lane / "manifest.json", evidence / "source-manifest.json")
    manifest["result_sha256"] = digest(evidence / "result.json")
    (evidence / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(evidence)


if __name__ == "__main__":
    main()
