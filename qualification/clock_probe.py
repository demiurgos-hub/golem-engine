"""Capture a short Windows clock-resolution diagnostic from a staged candidate."""
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
        parser.error("this probe compares Windows QueryPerformanceCounter with Go's clock")
    root, lane = args.root.resolve(), args.lane.resolve()
    if not lane.is_relative_to(root / "workspaces"):
        parser.error("lane must be an existing qualification snapshot")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    run_id = "clock-diagnostics-" + lane.name + "-" + stamp
    evidence = root / "runs" / run_id
    binaries = root / "bin" / lane.name / "windows" / run_id
    evidence.mkdir(parents=True)
    binaries.mkdir(parents=True)
    source = lane / "golem-engine/qualification/clockprobe/main_windows.go"
    executable = binaries / "clock-probe.exe"
    env = dict(os.environ)
    candidate_env = json.loads((lane / "environment.json").read_text())
    env.update(candidate_env)
    env.update(CGO_ENABLED="0", GOWORK="off", GOFLAGS="-mod=readonly", GOTOOLCHAIN="local")
    go = Path(shutil.which(args.go, path=env.get("PATH")) or args.go).resolve(strict=True)
    go_env = json.loads(subprocess.check_output([go, "env", "-json"], env=env, text=True))
    commands = [[str(go), "build", "-o", str(executable), str(source)], [str(executable)]]
    for index, command in enumerate(commands):
        result = subprocess.run(command, cwd=source.parent, env=env, text=True, capture_output=True)
        (evidence / ("build.log" if index == 0 else "result.json")).write_text(result.stdout)
        (evidence / ("build.stderr.log" if index == 0 else "probe.stderr.log")).write_text(result.stderr)
        if result.returncode:
            raise RuntimeError(f"clock diagnostic command failed ({result.returncode}); see {evidence}")
    shutil.copyfile(source, evidence / source.name)
    shutil.copyfile(lane / "manifest.json", evidence / "source-manifest.json")
    clock_sources = {}
    for relative in ("src/runtime/time_windows_amd64.s", "src/testing/testing_windows.go"):
        path = Path(go_env["GOROOT"]) / relative
        if path.exists():
            clock_sources[str(path)] = digest(path)
    manifest = {
        "diagnostic_only": True, "candidate": str(lane), "commands": commands,
        "working_directory": str(source.parent), "environment": {name: env.get(name) for name in sorted(set(candidate_env) | {"PATH", "CGO_ENABLED", "GOWORK", "GOFLAGS", "GOTOOLCHAIN", "GOENV", "GOMAXPROCS", "GOGC", "GOMEMLIMIT"})},
        "source_sha256": digest(source), "executable_sha256": digest(executable),
        "go_executable_sha256": digest(go), "go_environment": go_env,
        "toolchain_clock_sources": clock_sources,
        "script": str(Path(__file__).resolve()), "script_sha256": digest(__file__),
    }
    (evidence / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(evidence)


if __name__ == "__main__":
    main()
