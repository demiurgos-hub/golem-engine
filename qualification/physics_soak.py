"""Run one recorded physics binary through repeated process/checkpoint boundaries.

This records the churn/restart component. It does not replace capacity, authored
content, independent-platform or published-dependency qualification.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import subprocess
import sys
import time


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, value):
    with Path(path).open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")


def read_json(path):
    with Path(path).open(encoding="utf-8-sig") as stream:
        return json.load(stream)


def inspect_epoch(directory, expected_checkpoint, source_hash):
    result = read_json(directory / "results.json")
    if result.get("Failure"):
        raise RuntimeError(result["Failure"])
    manifest = read_json(directory / "manifest.json")
    if manifest.get("sourceManifestSHA256") != source_hash:
        raise RuntimeError("epoch source identity differs from the selected snapshot")
    if manifest.get("goarch") != "amd64" or platform.machine().lower() not in ("amd64", "x86_64") or manifest.get("goos") != platform.system().lower():
        raise RuntimeError("native Windows/Linux x64 qualification is required")
    if "complete overrun" not in manifest.get("measurement", ""):
        raise RuntimeError("binary does not retain complete terminal measurement frames")
    settings = {entry["Key"]: entry["Value"] for entry in manifest["buildInfo"].get("Settings", [])}
    if settings.get("CGO_ENABLED") != "0":
        raise RuntimeError("physics production binary must record CGO_ENABLED=0")
    samples = []
    with (directory / "samples.jsonl").open(encoding="utf-8") as stream:
        for line in stream:
            sample = json.loads(line)
            if sample["Measured"]:
                samples.append(sample)
    if not samples or len(samples) != result["MeasuredSamples"]:
        raise RuntimeError("measured sample evidence is incomplete")
    if expected_checkpoint is not None:
        restored = read_json(directory / "restart.json")
        if restored["checkpointID"] != expected_checkpoint or not restored["ready"]:
            raise RuntimeError("process restart did not consume the preceding checkpoint")
    checkpoint = read_json(directory / "checkpoint" / "physics-checkpoint.json")
    if checkpoint["CheckpointID"] != result["CheckpointID"]:
        raise RuntimeError("checkpoint manifest is not paired to the completed epoch")
    data = directory / "checkpoint" / checkpoint["File"]
    if data.parent != directory / "checkpoint" or data.name != checkpoint["SHA256"] + ".physics.json" or digest(data) != checkpoint["SHA256"]:
        raise RuntimeError("checkpoint data identity mismatch")
    return result, samples


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--source-manifest", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--mode", choices=["correctness", "rehearsal", "full"], default="full")
    parser.add_argument("--extent", type=int, choices=[64, 256, 768, 2304], default=768)
    parser.add_argument("--seconds", type=float, default=7200)
    parser.add_argument("--epoch-seconds", type=float, default=600)
    parser.add_argument("--warmup-seconds", type=float, default=2)
    parser.add_argument("--restart-probe-seconds", type=float, default=30)
    parser.add_argument("--churn-ticks", type=int, default=100)
    parser.add_argument("--physics-workers", type=int, default=8)
    parser.add_argument("--query-workers", type=int, default=6)
    parser.add_argument("--timing-isolated", action="store_true", help="attest no concurrent fuzzing, benchmarks, builds or heavy tests")
    args = parser.parse_args()
    if any(not math.isfinite(v) or v <= 0 for v in (args.seconds, args.epoch_seconds, args.restart_probe_seconds)) or not math.isfinite(args.warmup_seconds) or args.warmup_seconds < 0 or args.churn_ticks < 1:
        parser.error("durations and churn interval must be finite and positive")
    if math.ceil(args.seconds / args.epoch_seconds) > 10000:
        parser.error("a run is limited to 10,000 preserved epochs")
    if not 1 <= args.physics_workers <= 64 or not 1 <= args.query_workers <= 64:
        parser.error("workers must be between 1 and 64")
    args.binary, args.source_manifest, args.out = (p.resolve() for p in (args.binary, args.source_manifest, args.out))
    binary_hash, source_hash = digest(args.binary), digest(args.source_manifest)
    args.out.mkdir(parents=True, exist_ok=False)
    report = {"version": 1, "qualified": False, "runnerSHA256": digest(__file__), "binary": str(args.binary), "binarySHA256": binary_hash,
              "sourceManifest": str(args.source_manifest), "sourceManifestSHA256": source_hash,
              "platform": platform.platform(), "machine": platform.machine(), "timingIsolated": args.timing_isolated,
              "startedUTC": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "epochs": [], "gates": {},
              "missing": ["independent capacity and movement cadence gates", "representative content and full scenario matrix", "independent platform qualification", "published dependency resolution"]}
    previous_checkpoint = None
    previous_directory = None
    remaining = args.seconds
    durations = []
    while remaining > 0:
        duration = min(args.epoch_seconds, remaining)
        durations.append(duration)
        remaining -= duration
    # Always demonstrate that the last measured state can start another process.
    durations.append(args.restart_probe_seconds)
    measured = []
    try:
        for index, duration in enumerate(durations):
            directory = args.out / f"epoch-{index:04d}"
            command = [str(args.binary), "-mode", args.mode, "-extent", str(args.extent), "-duration", f"{duration}s", "-warmup", f"{args.warmup_seconds}s",
                       "-physics-workers", str(args.physics_workers), "-query-workers", str(args.query_workers), "-churn-ticks", str(args.churn_ticks),
                       "-checkpoint", "-source-manifest", str(args.source_manifest), "-out", str(directory)]
            if previous_directory is not None:
                command += ["-resume", str(previous_directory / "checkpoint")]
            epoch = {"index": index, "probe": index == len(durations)-1, "requestedSeconds": duration, "command": command, "startedNS": time.time_ns()}
            report["epochs"].append(epoch)
            write_json(args.out / "report.json", report)
            if digest(args.binary) != binary_hash or digest(args.source_manifest) != source_hash:
                raise RuntimeError("binary or source manifest changed during qualification")
            with (args.out / f"epoch-{index:04d}.log").open("w", encoding="utf-8") as output:
                process = subprocess.run(command, cwd=args.out, stdout=output, stderr=subprocess.STDOUT)
            epoch["exitCode"], epoch["finishedNS"] = process.returncode, time.time_ns()
            if process.returncode:
                raise RuntimeError(f"epoch {index} failed; evidence preserved")
            result, samples = inspect_epoch(directory, previous_checkpoint, source_hash)
            epoch["results"] = result
            previous_checkpoint, previous_directory = result["CheckpointID"], directory
            if not epoch["probe"]:
                measured.extend(samples)
            print(f"epoch {index}: {len(samples)} frames, checkpoint {previous_checkpoint}", flush=True)
        elapsed = sum(e["results"]["MeasurementWallNS"] for e in report["epochs"] if not e["probe"])
        latency = sorted(s["FullTickNS"] for s in measured)
        p99 = latency[math.ceil(len(latency)*.99)-1]
        epochs = [e["results"] for e in report["epochs"]]
        report["measurementNS"], report["measuredFrames"], report["p99NS"], report["worstNS"] = elapsed, len(measured), p99, max(latency)
        report["gates"] = {
            "full-counts": args.mode == "full" and all(e["Gates"]["full-counts"] for e in epochs),
            "two-hours-measured": args.seconds >= 7200 and elapsed >= 7200*1e9,
            "process-restart-chain": len(epochs) >= 2,
            "repeated-geometry-changes": all(e["Churn"]["Committed"] >= 3 and e["Churn"]["Committed"] == e["Churn"]["Submitted"] and e["Churn"]["Unloads"] > 0 and not e["Churn"]["PendingGeneration"] for e in epochs[:-1]),
            "accepted-frames": all(e["Gates"]["accepted-frames"] for e in epochs),
            "awake-query-projectile-counts": all(e["Gates"]["awake-dynamic-count"] and e["Gates"]["query-projectile-counts"] for e in epochs),
            "no-query-overflow": all(e["QueryOverflows"] == 0 for e in epochs),
            "p99-below-50ms": p99 < 50_000_000,
            "timing-isolated": args.timing_isolated,
        }
        report["churnRestartComponentPassed"] = all(report["gates"].values())
    except BaseException as error:
        report["error"] = str(error)
        raise
    finally:
        report["finishedUTC"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        write_json(args.out / "report.json", report)


if __name__ == "__main__":
    main()
