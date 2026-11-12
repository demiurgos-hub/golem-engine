"""Run native Linux x64 physics checks against an immutable Windows source capture.

Invoke through WSL with an explicit Go 1.27.1 executable. This consumes the
source-manifest.json produced by Run-PhysicsStress.ps1; it never rewrites that
capture or claims published module resolution. Timed work must run in isolation.
"""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys

sys.dont_write_bytecode = True


def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def sha(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def verify_sources(workspace, manifest):
    count = 0
    for source in manifest["source"]:
        if source["name"] not in ("engine", "box3d-go", "navmesh-go"):
            raise ValueError("unrecognized source name")
        root = (workspace / source["name"]).resolve(strict=True)
        expected = set()
        for item in source["files"]:
            path = (root / item["path"]).resolve(strict=True)
            if not path.is_relative_to(root) or not path.is_file():
                raise ValueError("source file outside its snapshot")
            if sha(path) != item["sha256"]:
                raise ValueError(f"source checksum mismatch: {source['name']}/{item['path']}")
            expected.add(path)
            count += 1
        # Go commands must not introduce unrecorded source into a later test run.
        for path in root.rglob("*.go"):
            if path.resolve() not in expected:
                raise ValueError(f"unrecorded Go source: {path}")
    return {"files": count, "verifiedUTC": stamp()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("/mnt/d/Demiurgos/tests/physics-validation"))
    parser.add_argument("--source-run", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--go", type=Path, required=True)
    parser.add_argument("--phase", choices=("tests", "workload", "both", "smoke"), default="tests")
    parser.add_argument("--mode", choices=("correctness", "rehearsal", "full"), default="correctness")
    parser.add_argument("--duration", default="2s")
    parser.add_argument("--warmup", default="2s")
    parser.add_argument("--extent", type=int, choices=(64, 256, 768, 2304), default=768)
    parser.add_argument("--physics-workers", type=int, default=8)
    parser.add_argument("--query-workers", type=int, default=6)
    parser.add_argument("--simd", action="store_true")
    parser.add_argument("--profiles", action="store_true")
    parser.add_argument("--debug-capture", action="store_true")
    args = parser.parse_args()
    if sys.platform != "linux" or platform.machine().lower() not in ("x86_64", "amd64"):
        parser.error("this runner requires native Linux x64 (including x64 WSL)")
    for name in (args.source_run, args.run_id):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+", name) or name in (".", ".."):
            parser.error("run IDs must be a single safe path component")
    if not 1 <= args.physics_workers <= 64 or not 1 <= args.query_workers <= 64:
        parser.error("worker counts must be within 1..64")
    if args.phase == "smoke" and args.mode != "correctness":
        parser.error("Linux smoke uses correctness counts, not a second full performance run")
    root = args.root.resolve(strict=True)
    source_manifest = root / "runs" / args.source_run / "source-manifest.json"
    source = json.loads(source_manifest.read_text(encoding="utf-8-sig"))
    workspace = root / "workspaces" / args.source_run
    run = root / "linux" / args.run_id
    run.mkdir()  # Existing or failed evidence is never overwritten.
    go = args.go.resolve(strict=True)
    engine = workspace / "engine"
    modules = {
        "github.com/demiurgos-hub/golem-engine": ("v0.4.0", engine),
        "github.com/demiurgos-hub/golem-engine/golem/collision": ("v0.3.1-0.20261013153120-49b2e72c189a", engine / "golem/collision"),
        "github.com/demiurgos-hub/golem-engine/golem/nav": ("v0.4.0", engine / "golem/nav"),
        "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh": ("v0.1.0", engine / "golem/nav/navmesh"),
        "github.com/demiurgos-hub/golem-engine/golem/physics3d": ("v0.0.0", engine / "golem/physics3d"),
        "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d": ("v0.0.0", engine / "golem/physics3d/box3d"),
        "github.com/gataengine/box3d-go": ("v0.0.0-20261019212002-2a00e926a8d9", workspace / "box3d-go"),
        "github.com/demiurgos-hub/navmesh-go": ("v0.1.0", workspace / "navmesh-go"),
    }
    extras = [engine / value for value in ("golem/collision/cp", "golem/collision/resolv", "golem/nav/kelindar", "golem/nav/pathing", "examples/navigation-server", "examples/navigation-stress", "examples/physics-stress")]
    work = run / "go.work"
    work.write_text("go 1.27.1\nuse (\n" + "".join("  " + json.dumps(str(path)) + "\n" for _, path in modules.values()) + "".join("  " + json.dumps(str(path)) + "\n" for path in extras) + ")\nreplace (\n" + "".join(f"  {name} {version} => {json.dumps(str(path))}\n" for name, (version, path) in modules.items()) + ")\n", encoding="utf-8")
    env = dict(os.environ)
    env.update(GOTOOLCHAIN="local", GOENV="off", GOWORK=str(work), CGO_ENABLED="0", GOEXPERIMENT="simd" if args.simd else "", GOAMD64="v1", GOFLAGS="-mod=readonly")
    # Generated Go consumers must use the same explicit toolchain as their parent.
    env["PATH"] = str(go.parent) + os.pathsep + env.get("PATH", "")
    for key, relative in (("GOCACHE", "linux/cache/go"), ("GOMODCACHE", "linux/cache/mod"), ("GOPATH", "linux/gopath"), ("GOTMPDIR", "linux/tmp")):
        directory = root / relative
        directory.mkdir(parents=True, exist_ok=True)
        env[key] = str(directory)
    env.update(TMPDIR=env["GOTMPDIR"], TMP=env["GOTMPDIR"], TEMP=env["GOTMPDIR"])
    tags = ["-tags", "box3d_simd"] if args.simd else []
    evidence = {"version": 1, "startedUTC": stamp(), "platform": platform.uname()._asdict(), "sourceManifest": str(source_manifest), "sourceManifestSHA256": sha(source_manifest), "runnerSHA256": sha(Path(__file__)), "options": {key: str(value) if isinstance(value, Path) else value for key, value in vars(args).items()}, "environment": {key: env[key] for key in ("GOTOOLCHAIN", "GOWORK", "CGO_ENABLED", "GOEXPERIMENT", "GOAMD64", "GOCACHE", "GOMODCACHE", "GOTMPDIR")}, "checks": [], "publishedResolution": False, "qualified": False}

    def command(name, argv, cwd, settings=None, required=True):
        record = {"name": name, "argv": [str(x) for x in argv], "cwd": str(cwd), "cgo": (settings or env)["CGO_ENABLED"], "startedUTC": stamp()}
        evidence["checks"].append(record)
        with (run / (name + ".log")).open("w", encoding="utf-8") as output:
            result = subprocess.run(record["argv"], cwd=cwd, env=settings or env, stdout=output, stderr=subprocess.STDOUT)
        record.update(exitCode=result.returncode, finishedUTC=stamp())
        save(run / "manifest.json", evidence)
        if "-json" in record["argv"]:
            record["skippedTests"] = []
            for line in (run / (name + ".log")).read_text(encoding="utf-8", errors="replace").splitlines():
                if line.startswith("{"):
                    event = json.loads(line)
                    if event.get("Action") == "skip" and event.get("Test"):
                        record["skippedTests"].append({"package": event.get("Package"), "test": event["Test"]})
        if result.returncode and required:
            raise RuntimeError(f"{name} failed: see {run / (name + '.log')}")
        return result.returncode == 0

    try:
        save(run / "manifest.json", evidence)
        evidence["sourceBefore"] = verify_sources(workspace, source)
        evidence["toolchain"] = subprocess.check_output([str(go), "version"], env=env, text=True).strip()
        if "go1.27.1 linux/amd64" not in evidence["toolchain"]:
            raise ValueError("expected Go 1.27.1 linux/amd64")
        save(run / "cpu.json", {"cpuinfo": Path("/proc/cpuinfo").read_text(), "memory": Path("/proc/meminfo").read_text()})
        if args.phase in ("tests", "both"):
            passed = True
            paths = [path for name, (_, path) in modules.items() if name != "github.com/demiurgos-hub/navmesh-go"] + extras
            for index, path in enumerate(paths):
                settings = dict(env)
                if path == workspace / "box3d-go":
                    settings["GOWORK"] = "off"
                if path == engine:
                    packages = ["./cmd/...", "./schema/...", "./codegen/...", "./golem/...", "./golem-go-client/...", "./internal/..."]
                    passed = command("test-root-server", [go, "test", *tags, "-count=1", "-json", *packages], path, settings, required=False) and passed
                    continue
                passed = command(f"test-{index:02d}-{path.name}", [go, "test", *tags, "-count=1", "-json", "./..."], path, settings, required=False) and passed
            if not passed:
                raise RuntimeError("One or more native module checks failed; see individual logs")
        if args.phase in ("workload", "both", "smoke"):
            binary = run / "physics-stress"
            command("build", [go, "build", *tags, "-trimpath", "-o", binary, "."], engine / "examples/physics-stress")
            evidence["binarySHA256"] = sha(binary)
            flags = ["-mode", args.mode, "-extent", str(args.extent), "-duration", args.duration, "-warmup", args.warmup, "-physics-workers", str(args.physics_workers), "-query-workers", str(args.query_workers), "-source-manifest", str(source_manifest), "-out", str(run / "workload")]
            if args.profiles:
                flags.append("-profiles")
            if args.debug_capture:
                flags.append("-debug-capture")
            if args.phase == "smoke":
                flags.append("-checkpoint")
            command("workload", [binary, *flags], run)
            if args.phase == "smoke":
                from physics_soak import inspect_epoch

                first, first_samples = inspect_epoch(run / "workload", None, sha(source_manifest))
                restart_flags = list(flags)
                restart_flags[restart_flags.index("-out") + 1] = str(run / "restart-smoke")
                restart_flags += ["-resume", str(run / "workload" / "checkpoint")]
                command("restart-smoke", [binary, *restart_flags], run)
                second, second_samples = inspect_epoch(run / "restart-smoke", first["CheckpointID"], sha(source_manifest))
                gates = ("all-moving-every-complete-tick", "awake-dynamic-count", "query-projectile-counts", "accepted-frames", "checkpoint-written")
                for label, result in (("initial", first), ("restart", second)):
                    if any(not result["Gates"].get(gate) for gate in gates):
                        raise RuntimeError(f"{label} Linux smoke omitted required work or failed checkpoint/simulation")
                evidence["smoke"] = {"passed": True, "initialCheckpointID": first["CheckpointID"], "restoredCheckpointID": first["CheckpointID"], "nextCheckpointID": second["CheckpointID"],
                                     "initialMeasuredFrames": len(first_samples), "restartMeasuredFrames": len(second_samples),
                                     "scope": "Native Linux x64 build, simulation, save and fresh-process restore; no full-size latency or long-duration claim."}
        evidence["checksPassed"] = True
    except Exception as error:
        evidence["error"] = str(error)
        raise
    finally:
        try:
            evidence["sourceAfter"] = verify_sources(workspace, source)
        except Exception as error:
            evidence["checksPassed"] = False
            evidence["error"] = str(error)
            raise
        finally:
            evidence["finishedUTC"] = stamp()
            save(run / "manifest.json", evidence)
    print(f"Checks passed; full qualification remains separate: {run}")


if __name__ == "__main__":
    main()
