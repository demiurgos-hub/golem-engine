"""Run serialized native-Linux qualification from an existing exact candidate.

Invoke inside WSL, after the Windows staging/build step has populated the
candidate's private module cache. No source repository is built or modified.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import time

sys.dont_write_bytecode = True
from candidate_inputs import prepare, record, verify, write_new
from gate_policy import evaluate_files


def native_path(value):
    value = str(value).replace("\\", "/")
    if len(value) > 2 and value[1] == ":":
        return Path("/mnt/" + value[0].lower() + value[2:])
    return Path(value)


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def write_json(path, value):
    Path(path).write_text(json.dumps(value, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--lane", required=True)
    parser.add_argument("--root", default="/mnt/c/Demiurgos/navmesh-validation/world-test")
    parser.add_argument("--go", default="/mnt/c/Demiurgos/navmesh-validation/world-test/cache/toolchains/go1.26.4-linux-amd64/go/bin/go")
    parser.add_argument("--mode", choices=("Smoke", "Rehearsal", "Suite", "Soak", "Full", "Capacity", "BuildOnly", "BakeOnly"), default="Smoke")
    parser.add_argument("--family", choices=("terrain", "multilevel", "construction", "capacity"), default="terrain")
    parser.add_argument("--rehearsal-agents", type=int, default=1000)
    parser.add_argument("--soak-agents", type=int, default=1000)
    parser.add_argument("--workers", type=int, default=2)
    parser.add_argument("--build-commits-per-tick", type=int, default=2)
    parser.add_argument("--path-drain-budget", default="1ms")
    parser.add_argument("--profile", action="store_true")
    parser.add_argument("--stop-on-required-failure", action="store_true")
    args = parser.parse_args()
    if platform.system() != "Linux":
        parser.error("run this entrypoint inside native Linux/WSL")
    if args.profile and args.mode != "Rehearsal":
        parser.error("profiling is diagnostic and only allowed for Rehearsal")
    if min(args.rehearsal_agents, args.soak_agents, args.workers, args.build_commits_per_tick) < 1:
        parser.error("agent counts must be positive")
    root, lane, go = native_path(args.root).resolve(), native_path(args.lane).resolve(), native_path(args.go).resolve()
    if not lane.is_relative_to(root / "workspaces"):
        parser.error("candidate lane must stay under the qualification workspaces directory")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    run_id = lane.name + "-linux-" + args.mode.lower() + "-" + stamp
    run, binaries, scratch = root / "runs" / run_id, root / "bin" / lane.name / "linux" / run_id, root / "scratch" / run_id
    for directory in (run, binaries, scratch):
        directory.mkdir(parents=True, exist_ok=False)
    settings = json.loads((lane / "environment.json").read_text())
    for name in ("GOMODCACHE", "GOCACHE", "GOTMPDIR", "TEMP", "TMP"):
        settings[name] = str(native_path(settings[name]))
    settings.update(GOPROXY=(lane / "proxy").as_uri() + ",https://proxy.golang.org", GOTOOLCHAIN="local", GOENV="off", GOCACHE=str(root / "cache" / "regional-linux-build"), GOTMPDIR=str(scratch), TMPDIR=str(scratch), TEMP=str(scratch), TMP=str(scratch))
    env = os.environ.copy()
    env.update(settings)
    env["PATH"] = str(go.parent) + os.pathsep + env.get("PATH", "")
    recorded_env = {name: env.get(name) for name in sorted(set(settings) | {"PATH", "GOMAXPROCS", "GOGC", "GOMEMLIMIT", "GOFLAGS"})}
    shutil.copyfile(lane / "manifest.json", run / "source-manifest.json")
    planned = {"Smoke": 17, "Rehearsal": 77, "Suite": 2181, "Soak": 7327, "Full": 9508, "Capacity": 727}.get(args.mode, 0)
    drain = {"minimum_seconds_per_case": 5, "timeout_seconds_per_case": 30, "maximum_additional_seconds_per_case": 25, "maximum_additional_seconds_total": {"Smoke": 25, "Rehearsal": 25, "Suite": 75, "Soak": 25, "Full": 100, "Capacity": 25}.get(args.mode, 0), "restart_probe_extra_drain_seconds": 0, "interpretation": "Minimum duration includes configured drain and restart. Unsettled live work can extend each primary case by at most 25 seconds, plus tick/capture overhead; timeout fails the gate."}
    go_env = subprocess.check_output([str(go), "env", "-json"], env=env, text=True)
    clock_sources = [{"path": str(path), "sha256": sha256(path)} for path in sorted((lane / "golem-engine/golem").glob("navigation_clock*.go")) if not path.name.endswith("_test.go")]
    write_json(run / "execution.json", {"started_utc": utc(), "platform": "native Linux / WSL", "script_path": str(Path(__file__).resolve()), "script_sha256": sha256(__file__), "lane": str(lane), "options": vars(args), "environment": recorded_env, "go_environment": json.loads(go_env), "navigation_clock_sources": clock_sources, "planned_workload_minimum_seconds_excluding_build_bake_and_overheads": planned, "live_drain": drain, "immutable_input_helper_sha256": sha256(Path(__file__).with_name("candidate_inputs.py")), "source_verification": {"before": str(run / "source-before.json"), "after": str(run / "source-after.json"), "after_runtime": str(run / "source-after-runtime.json")}, "external_module_inputs": {"before": str(run / "module-inputs-before.json"), "after": str(run / "module-inputs-after.json")}})
    cpu = next((line.split(":", 1)[1].strip() for line in Path("/proc/cpuinfo").read_text().splitlines() if line.startswith("model name")), "unknown")
    memory = next((line for line in Path("/proc/meminfo").read_text().splitlines() if line.startswith("MemTotal:")), "unknown")
    write_json(run / "hardware.json", {"cpu": cpu, "logical_cpus": os.cpu_count(), "memory": memory, "kernel": platform.release(), "architecture": platform.machine()})
    (run / "go-version.txt").write_text(subprocess.check_output([str(go), "version"], env=env, text=True))
    gate_failure = False
    scheduling = ["-workers", args.workers, "-build-commits-per-tick", args.build_commits_per_tick, "-path-drain-budget", args.path_drain_budget]

    def logged(cwd, label, command):
        nonlocal gate_failure
        executable = Path(shutil.which(str(command[0]), path=env["PATH"]) or command[0]).resolve()
        start = time.monotonic()
        progress = run / "progress.jsonl"
        with progress.open("a") as stream:
            stream.write(json.dumps({"event": "start", "label": label, "utc": utc(), "working_directory": str(cwd), "command": [str(v) for v in command], "resolved_executable": str(executable), "executable_sha256": sha256(executable), "environment": recorded_env}) + "\n")
        with (run / (label + ".log")).open("w") as log:
            process = subprocess.Popen([str(v) for v in command], cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, errors="replace", bufsize=1)
            for line in process.stdout:
                log.write(line)
                log.flush()
                print(line, end="", flush=True)
            result = process.wait()
        with progress.open("a") as stream:
            stream.write(json.dumps({"event": "finish", "label": label, "utc": utc(), "exit_code": result, "seconds": time.monotonic() - start}) + "\n")
        if args.stop_on_required_failure and "-agents" in command and "-bake-only" not in command:
            summary_path = Path(command[command.index("-out") + 1]) / "summary.json"
            decision = evaluate_files(summary_path, summary_path.with_name("gate-policy.json"))
            if not decision["continue"]:
                raise RuntimeError(f"{label} blocks dependent qualification: {decision['blocking']}; evidence {run}")
        if result == 2:
            gate_failure = True
            print("Raw gates failed; continuing only where the selected stop policy permits.", flush=True)
        elif result:
            raise RuntimeError(f"{label} failed with exit {result}; evidence {run}")

    try:
        original = verify(lane)
        write_new(run / "source-before.json", original)
        if not original["pass"]:
            raise RuntimeError(f"candidate source mismatch before build; see {run}")
        module_inputs = prepare(lane, scratch / "modfiles", [("worldgen", "navmesh-go"), ("stress", "golem-engine/examples/navigation-stress")])
        write_new(run / "module-inputs-before.json", module_inputs)
        try:
            logged(lane / "navmesh-go", "build-worldgen", [go, "build", "-mod=mod", "-modfile=" + module_inputs["modules"]["worldgen"]["modfile"]["path"], "-o", binaries / "worldgen", "./cmd/navmesh-worldgen"])
            logged(lane / "golem-engine/examples/navigation-stress", "build-stress", [go, "build", "-mod=mod", "-modfile=" + module_inputs["modules"]["stress"]["modfile"]["path"], "-o", binaries / "stress", "."])
        finally:
            write_new(run / "module-inputs-after.json", record(module_inputs))
            after = verify(lane)
            write_new(run / "source-after.json", after)
            if not after["pass"]:
                raise RuntimeError(f"candidate source mismatch after build; see {run}")
        if args.mode == "BuildOnly":
            print(f"Binaries: {binaries}")
            return 0
        families = ["capacity"] if args.mode == "Capacity" else ["smoke"] if args.mode == "Smoke" else ["construction"] if args.mode == "Soak" else [args.family] if args.mode in ("Rehearsal", "BakeOnly") else ["terrain", "multilevel", "construction"]
        for family in families:
            world = root / "worlds" / (run_id + "-" + family)
            logged(lane, "generate-" + family, [binaries / "worldgen", "-config", lane / ("navmesh-go/qualification/configs/" + family + ".json"), "-out", world])
            counts = [1000] if args.mode == "Capacity" else [10] if args.mode == "Smoke" else [1] if args.mode == "BakeOnly" else [args.soak_agents] if args.mode == "Soak" else [args.rehearsal_agents] if args.mode == "Rehearsal" else [1000]
            for count in counts:
                warmup = "2s" if args.mode == "Smoke" else "10s" if args.mode == "Rehearsal" else "2m"
                duration = "8s" if args.mode == "Smoke" else "60s" if args.mode == "Rehearsal" else "2h" if args.mode == "Soak" else "10m"
                name = f"{family}-{count}"
                command = [binaries / "stress", "-fixture", world / "fixture.json", "-agents", count, "-warmup", warmup, "-duration", duration, "-out", run / name, "-source-manifest", run / "source-manifest.json"]
                if args.profile:
                    command += ["-cpuprofile", "cpu.pprof", "-memprofile", "heap.pprof"]
                if args.mode == "BakeOnly":
                    command += ["-bake-only"]
                if family == "capacity":
                    command += ["-churn", "0", "-edits-per-sec", "0", "-burst", "0"]
                command += scheduling
                logged(lane, name, command)
        if args.mode == "Full":
            name = "soak-construction-" + str(args.soak_agents)
            logged(lane, name, [binaries / "stress", "-fixture", world / "fixture.json", "-agents", args.soak_agents, "-warmup", "2m", "-duration", "2h", "-out", run / name, "-source-manifest", run / "source-manifest.json"] + scheduling)
        print(f"Evidence: {run}")
        return 2 if gate_failure else 0
    finally:
        after_runtime = verify(lane)
        after_runtime["gate_policy_sha256"] = sha256(Path(__file__).with_name("gate_policy.py"))
        write_new(run / "source-after-runtime.json", after_runtime)
        if not after_runtime["pass"]:
            raise RuntimeError(f"candidate source mismatch after runtime; see {run}")



if __name__ == "__main__":
    sys.exit(main())
