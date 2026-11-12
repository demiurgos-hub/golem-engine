"""Summarize the four selected physics delivery gates without running them.

Every gate must identify the exact selected source-manifest bytes and build
settings. This is an evidence reader, not published-dependency qualification.
Omitted evidence remains MISSING; excluded matrices and soaks are not gates.
"""

import argparse
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import sys

sys.dont_write_bytecode = True

NATIVE_TESTS = (
    "TestLocalJointPreparationUsesLocalPosesAndRetainsEveryFamily",
    "TestLocalJointPreparationOrdinaryArithmetic",
    "TestLocalJointMotorAndHingeUseOneIntervalBudget",
    "TestLocalJointSpringEquationDoesNotRestart",
    "TestLocalJointWeldTransfersImpactWithoutWarmStart",
    "TestNativeContinuationSupportedPlate",
    "TestNativeContinuationMomentumRestitution",
    "TestNativeContinuationForceDuration",
    "TestNativeContinuationMotorSupports",
    "TestNativeContinuationLifecycle",
)
INTEGRATION_CHECKS = (
    "root-backend", "golem-collision", "golem-collision-cp", "golem-collision-resolv",
    "golem-nav", "golem-nav-kelindar", "golem-nav-pathing", "golem-nav-navmesh",
    "golem-physics3d", "golem-physics3d-box3d", "examples-navigation-server",
    "examples-navigation-stress",
)
UNITY_CASES = {"bounce", "force", "freefall", "hinge", "impulse", "push-heavy",
               "push-light", "stack-0", "stack-1", "stack-2", "stack-3", "stack-4", "torque"}
COUNTS = {"Characters": 1000, "DynamicBodies": 1000, "Projectiles": 500,
          "StaticColliders": 100000, "Triangles": 5000000, "Queries": 10000}
EXCLUDED = ["extended race/geometry/joint/mass/concurrency matrices", "two-hour soak",
            "duplicate full-size Linux performance", "ARM64", "Unity2021", "Ebiten/graphics",
            "exhaustive catalog/collision parity"]


class Evidence:
    def __init__(self):
        self.artifacts = {}

    def bytes(self, path):
        path = path.resolve(strict=True)
        data = path.read_bytes()
        digest = hashlib.sha256(data).hexdigest()
        previous = self.artifacts.setdefault(str(path), digest)
        if digest != previous:
            raise ValueError(f"evidence changed while reading: {path}")
        return data

    def digest(self, path):
        self.bytes(path)
        return self.artifacts[str(path.resolve())]

    def read(self, path):
        return json.loads(self.bytes(path).decode("utf-8-sig"))

    def events(self, path):
        return [json.loads(line) for line in self.bytes(path).decode("utf-8-sig").splitlines()
                if line.startswith("{")]


def check(gate, condition, message):
    if not condition:
        gate["problems"].append(message)


def same_hash(left, right):
    return isinstance(left, str) and isinstance(right, str) and left.lower() == right.lower()


def tags_from_command(command):
    for index, value in enumerate(command):
        if value == "-tags":
            return command[index + 1].split(",")
        if value.startswith("-tags="):
            return value[6:].split(",")
    return []


def recorded_path(value, base):
    # Evidence may move between the Windows host and its native x64 WSL runner.
    value = str(value).replace("\\", "/")
    if os.name == "nt" and value.startswith("/mnt/") and len(value) > 7:
        value = value[5].upper() + ":/" + value[7:]
    elif os.name != "nt" and len(value) > 2 and value[1:3] == ":/":
        value = "/mnt/" + value[0].lower() + "/" + value[3:]
    path = Path(value)
    return path if path.is_absolute() else base / path


def test_passes(events):
    return {item["Test"] for item in events if item.get("Action") == "pass" and item.get("Test")}


def validate_config(gate, tags, environment, expected_tags, experiment):
    check(gate, sorted(tags) == expected_tags, "build tags differ from the selected source runner")
    check(gate, environment.get("CGO_ENABLED") == "0", "CGO is not explicitly disabled")
    check(gate, environment.get("GOEXPERIMENT", "") == experiment, "GOEXPERIMENT differs from the selected source runner")


def focused(gate, directory, evidence, source, digest, tags, experiment, required):
    result = evidence.read(directory / "result.json")
    invocation = evidence.read(directory / "invocation.json")
    check(gate, same_hash(evidence.digest(directory / "source-manifest.json"), digest), "copied source manifest differs")
    for record in (result, invocation):
        check(gate, same_hash(record.get("sourceManifestSHA256"), digest), "native checks used another source snapshot")
        check(gate, record.get("nativeCommit") == source["box3d-go"]["commit"], "native commit differs")
        validate_config(gate, tags_from_command(record["command"]), record["environment"], tags, experiment)
    before_path, after_path = directory / "native-before.json", directory / "native-after.json"
    before, after = evidence.read(before_path), evidence.read(after_path)
    expected = {item["path"]: item["sha256"].lower() for item in source["box3d-go"]["files"]}
    check(gate, before == after == expected, "native before/after inventory differs from the selected source files")
    check(gate, same_hash(evidence.digest(before_path), result.get("beforeSHA256")) and
          same_hash(evidence.digest(after_path), result.get("afterSHA256")), "native inventory checksum differs")
    events = evidence.events(directory / "tests.jsonl")
    check(gate, same_hash(evidence.digest(directory / "tests.jsonl"), result.get("testLogSHA256")), "native test log checksum differs")
    passed = test_passes(events)
    check(gate, result.get("exitCode") == 0 and result.get("sourceUnchanged") is True, "native execution failed or changed source")
    check(gate, result.get("toolchain", "").endswith("go1.27.1 windows/amd64"), "native toolchain differs from Windows x64 Go 1.27.1")
    check(gate, not any(item.get("Action") in ("fail", "skip") for item in events), "native log contains a failure or skip")
    check(gate, set(required) <= passed, "required native tests did not pass: " + ", ".join(sorted(set(required) - passed)))
    gate.update(requiredTests=required, passedTests=len(passed), toolchain=result.get("toolchain"))


def integration(gate, path, evidence, digest, tags, experiment):
    record = evidence.read(path)
    check(gate, same_hash(record.get("sourceManifestSHA256"), digest), "integration used another source snapshot")
    validate_config(gate, record["buildTags"], record["environment"], tags, experiment)
    check(gate, record.get("integrationPassed") is True and not record.get("error") and not record.get("sourceIntegrityError"), "integration failed")
    check(gate, record.get("toolchain", "").endswith("go1.27.1 windows/amd64"), "integration toolchain differs from Windows x64 Go 1.27.1")
    check(gate, record.get("sourceFilesBefore", 0) > 0 and record.get("sourceFilesBefore") == record.get("sourceFilesAfter"), "before/after source verification is incomplete")
    checks = {item["name"]: item for item in record["checks"]}
    check(gate, set(INTEGRATION_CHECKS) <= checks.keys(), "one or more selected integration modules are missing")
    for name, item in checks.items():
        log = recorded_path(item["log"], path.parent)
        events = evidence.events(log)
        check(gate, same_hash(evidence.digest(log), item.get("logSHA256")), f"{name}: test log checksum missing or different")
        check(gate, item.get("exitCode") == 0 and bool(test_passes(events)) and
              not any(event.get("Action") == "fail" for event in events), f"{name}: failed or executed no real tests")
        check(gate, sorted(tags_from_command(item["arguments"])) == tags, f"{name}: build tags differ")
        if name == "golem-physics3d-box3d":
            check(gate, "TestUnityDynamicsReference" in test_passes(events), "required Unity replay test did not pass")
    harness = record["reusedHarness"]
    log = recorded_path(harness["log"], path.parent)
    events = evidence.events(log)
    check(gate, same_hash(evidence.digest(log), harness["sha256"]) and bool(test_passes(events)) and
          not any(event.get("Action") == "fail" for event in events), "reused frozen harness checks are invalid")
    comparison_path = path.parent / "dynamics/dynamics-go-comparison.json"
    comparison = evidence.read(comparison_path)
    check(gate, same_hash(evidence.digest(comparison_path), record["unityComparison"]["sha256"]), "Unity comparison checksum differs")
    cases = comparison["Comparisons"]
    check(gate, comparison.get("Passed") is True and same_hash(comparison.get("SourceManifestSHA256"), digest)
          and len(cases) == 13 and {item["Owner"] for item in cases} == UNITY_CASES and all(item["Passed"] for item in cases), "the thirteen saved Unity cases did not all pass on this source")
    for item, field in zip(record["unityReference"], ("SourceHash", "ReferenceHash"), strict=True):
        actual = evidence.digest(recorded_path(item["path"], path.parent))
        check(gate, same_hash(actual, item["sha256"]) and same_hash(actual, comparison[field]), "saved Unity input bytes differ")
    gate.update(checks=len(checks), unityCases=len(cases), runnerSHA256=record.get("scriptSHA256"))


def windows(gate, directory, evidence, digest, tags, experiment):
    manifest = evidence.read(directory / "manifest.json")
    result = evidence.read(directory / "results.json")
    scene = evidence.read(directory / "scene.json")
    samples = [item for item in evidence.events(directory / "samples.jsonl") if item.get("Measured")]
    check(gate, same_hash(manifest.get("sourceManifestSHA256"), digest), "Windows workload used another source snapshot")
    settings = {item["Key"]: item["Value"] for item in manifest["buildInfo"]["Settings"]}
    validate_config(gate, list(filter(None, settings.get("-tags", "").split(","))), settings, tags, experiment)
    check(gate, manifest.get("goos") == "windows" and manifest.get("goarch") == "amd64", "workload is not Windows x64")
    check(gate, manifest["buildInfo"].get("GoVersion") in ("go1.27.1", "go1.27.1-X:simd"), "workload toolchain differs from Go 1.27.1")
    check(gate, result.get("Mode") == "full" and all(result["Workload"].get(key) == value for key, value in COUNTS.items()), "required full workload counts differ")
    check(gate, scene.get("actualStaticColliders") == 100000 and scene.get("actualStaticTriangles") == 5000000 and
          scene.get("uniqueMeshAssets", 0) > 0, "full resident geometry is missing")
    check(gate, bool(samples) and len(samples) == result.get("MeasuredSamples") == result["FullTick"].get("samples"), "complete measured sample counts differ or are empty")
    check(gate, not result.get("Failure") and result.get("RejectedFrames") == 0, "workload failed or rejected a frame")
    required_counts = {"Scheduled": 1000, "Following": 1000, "Moving": 1000, "AwakeDynamic": 1000, "Projectiles": 500, "Queries": 10000}
    check(gate, all(item.get("Accepted") is True and all(item.get(key) == value for key, value in required_counts.items()) for item in samples), "one or more measured frames omitted required movement/physics/query work")
    times = sorted(item["FullTickNS"] for item in samples)
    raw_p99 = times[math.ceil(len(times) * .99) - 1] if times else None
    check(gate, bool(times) and times[0] > 0 and raw_p99 < 50_000_000 and result["FullTick"]["p99NS"] < 50_000_000, "full-tick p99 did not meet the strict 50 ms target")
    check(gate, result.get("MeasurementWallNS", 0) >= manifest["options"]["Duration"] > 0, "requested measurement window did not complete")
    for name in ("full-counts", "all-moving-every-complete-tick", "awake-dynamic-count", "query-projectile-counts", "accepted-frames", "full-tick-p99-below-50ms"):
        check(gate, result["Gates"].get(name) is True, f"workload gate failed: {name}")
    gate.update(measuredFrames=len(samples), p50NS=result["FullTick"]["p50NS"], p99NS=result["FullTick"]["p99NS"],
                rawP99NS=raw_p99, worstNS=max(times) if times else None, cadence=result.get("Cadence"),
                measurementWallNS=result.get("MeasurementWallNS"), profiles=manifest["options"].get("Profiles"),
                isolation="External timing-isolation evidence remains a separate runner/operator record.")


def linux(gate, path, evidence, digest, tags, experiment):
    record = evidence.read(path)
    check(gate, same_hash(record.get("sourceManifestSHA256"), digest), "Linux smoke used another source snapshot")
    validate_config(gate, record["buildTags"], record["environment"], tags, experiment)
    check(gate, record["platform"].get("system") == "Linux" and record["platform"].get("machine") in ("x86_64", "amd64"), "smoke was not native Linux x64")
    check(gate, record.get("toolchain", "").endswith("go1.27.1 linux/amd64"), "smoke toolchain differs from Linux x64 Go 1.27.1")
    check(gate, record["options"].get("phase") == "smoke" and record["options"].get("mode") == "correctness", "expected the selected build/simulation/restart smoke")
    check(gate, record.get("checksPassed") is True and not record.get("error"), "Linux smoke failed")
    check(gate, record["sourceBefore"].get("files", 0) > 0 and record["sourceBefore"]["files"] == record["sourceAfter"]["files"], "Linux source verification is incomplete")
    checks = {item["name"]: item for item in record["checks"]}
    check(gate, {"build", "workload", "restart-smoke"} <= checks.keys() and all(item.get("exitCode") == 0 for item in checks.values()), "Linux build/simulate/restart commands are missing or failed")
    smoke = record["smoke"]
    check(gate, smoke.get("passed") is True and bool(smoke.get("initialCheckpointID")) and
          smoke["initialCheckpointID"] == smoke.get("restoredCheckpointID") and bool(smoke.get("nextCheckpointID")) and
          smoke.get("initialMeasuredFrames", 0) > 0 and smoke.get("restartMeasuredFrames", 0) > 0, "save/fresh-process restore identity or measured work is missing")
    gate.update(smoke=smoke, runnerSHA256=record.get("runnerSHA256"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-manifest", type=Path, required=True)
    parser.add_argument("--focused", type=Path, help="directory containing native invocation/result/inventory/test evidence")
    parser.add_argument("--integration", type=Path, help="Run-PhysicsIntegration manifest.json")
    parser.add_argument("--windows", type=Path, help="full Windows workload directory")
    parser.add_argument("--linux", type=Path, help="physics_linux --phase smoke manifest.json")
    parser.add_argument("--required-native-test", action="append", default=[], help="additional exact required native test name; existing focused families remain required")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    root = Path("D:/Demiurgos/tests/physics-validation" if os.name == "nt" else "/mnt/d/Demiurgos/tests/physics-validation").resolve(strict=True)
    output = args.out.resolve()
    if not output.is_relative_to(root) or output.exists():
        parser.error("--out must be a new evidence file under the physics validation workspace")
    evidence = Evidence()
    manifest = evidence.read(args.source_manifest)
    source = {item["name"]: item for item in manifest["source"]}
    if len(manifest["source"]) != 3 or set(source) != {"engine", "box3d-go", "navmesh-go"} or not all(item.get("committedSources") for item in source.values()):
        parser.error("requires exactly three committed engine/Box3D/navigation source snapshots")
    digest = evidence.digest(args.source_manifest)
    runner = evidence.read(args.source_manifest.with_name("runner-manifest.json"))
    if runner.get("cgo") != "0" or runner.get("goamd64") != "v1":
        parser.error("requires the frozen CGO-disabled GOAMD64=v1 build configuration")
    tags = sorted(runner["buildTags"])
    experiment = runner["goexperiment"]
    if len(tags) != len(set(tags)) or set(tags) - {"box3d_simd", "box3d_noassert"} or experiment not in ("", "simd") or ("box3d_simd" in tags) != (experiment == "simd") or ("box3d_noassert" in tags) != bool(runner.get("nativeReleaseRequested")):
        parser.error("source runner has unsupported or inconsistent build settings")
    required = sorted(set(NATIVE_TESTS) | set(args.required_native_test))
    report = {"version": 1, "createdUTC": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "sourceManifestSHA256": digest, "sourceCommits": {key: item["commit"] for key, item in source.items()},
              "buildTags": tags, "goexperiment": experiment, "gates": {}, "excluded": EXCLUDED,
              "qualified": False, "publishedResolution": False, "scope": "Selected four-gate delivery only; unchanged query accuracy, no broader platform or duration claim."}
    calls = (("focusedCollision", args.focused, focused, (source, digest, tags, experiment, required)),
             ("engineIntegration", args.integration, integration, (digest, tags, experiment)),
             ("windowsCapacity", args.windows, windows, (digest, tags, experiment)),
             ("linuxSmoke", args.linux, linux, (digest, tags, experiment)))
    for name, path, function, tail in calls:
        gate = {"status": "MISSING", "problems": []}
        report["gates"][name] = gate
        if path is None:
            gate["problems"].append("no evidence supplied")
            continue
        gate["input"] = str(path)
        try:
            function(gate, path, evidence, *tail)
        except (OSError, ValueError, KeyError, TypeError, IndexError) as error:
            gate["problems"].append(f"incomplete or invalid evidence: {error}")
        gate["status"] = "FAIL" if gate["problems"] else "PASS"
    report["selectedDeliveryPassed"] = all(gate["status"] == "PASS" for gate in report["gates"].values())
    report["artifacts"] = evidence.artifacts
    report["summaryScriptSHA256"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("x", encoding="utf-8") as stream:
        json.dump(report, stream, indent=2)
        stream.write("\n")
    print("; ".join(f"{name}: {gate['status']}" for name, gate in report["gates"].items()))
    return 0 if report["selectedDeliveryPassed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
