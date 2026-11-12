"""Decide whether dependent qualification may continue without rewriting gates."""
import argparse
import hashlib
import json
from pathlib import Path


OBSERVATION_ONLY_5000 = {
    "path_end_to_end_p99_le_100ms_outside_quarantine",
    "every_agent_scheduled_each_tick",
    "unaffected_following_agent_cadence",
    "active_following_or_traversing_share",
}
EXTERNAL_EVIDENCE = {"temporary_scratch_allocations"}
BASE_GATES = {
    "nav_tick_p99_le_5ms", "path_end_to_end_p99_le_100ms_outside_quarantine",
    "every_agent_scheduled_each_tick", "unaffected_following_agent_cadence",
    "temporary_scratch_allocations", "retired_generations_bounded",
    "active_following_or_traversing_share", "no_failed_or_unsupported_agents",
    "host_geometry_activation_resolved", "managed_generations_including_candidate_bounded",
    "complete_evidence_stream", "complete_admission_evidence", "host_traversal_recovery",
    "drained_retired_generations_and_candidates", "live_drain_pipeline_settled",
    "memory_drain_baseline_envelope",
}


def evaluate(summary, agents, family="terrain", duration_ns=0, stable_capacity=False):
    if agents < 1 or not isinstance(summary.get("gates"), dict):
        raise ValueError("agent count and summary gates are required")
    blocking, observations, external = {}, {}, {}
    for name, result in summary["gates"].items():
        if name in EXTERNAL_EVIDENCE and isinstance(result, str) and result.startswith("not_measured"):
            external[name] = result
            continue
        if result == "pass":
            continue
        if name == "path_end_to_end_p99_le_100ms_outside_quarantine" and family == "capacity" and stable_capacity and isinstance(result, str) and result.startswith("not_applicable:"):
            continue
        if name == "nav_tick_p99_le_5ms" and result == "fail":
            observations[name] = result
        elif agents == 5000 and name in OBSERVATION_ONLY_5000 and result == "fail":
            observations[name] = result
        else:
            blocking[name] = result
    expected = set(BASE_GATES)
    if family == "capacity":
        expected.update(("all_agents_move_each_complete_measured_tick", "all_startup_routes_admitted_before_measurement"))
    if family == "construction" and duration_ns >= 60_000_000_000:
        expected.add("completed_and_cancelled_traversals_exercised")
    for required in sorted(expected):
        if required not in summary["gates"]:
            blocking[required] = "missing"
    return {"continue": not blocking, "agents": agents, "expected_gates": sorted(expected), "blocking": blocking, "recorded_observations": observations, "requires_external_evidence": external, "policy": "Preserve every raw gate. The 5ms navigation target is a recorded limit. Only5000-agent path/cadence/activity performance is observation-only; correctness, evidence, recovery, retention and live-drain failures stop dependent work. Unknown or missing required measurements stop; scratch requires independent qualification and only stable-capacity warmed path latency is not applicable."}


def evaluate_files(summary_path, out):
    summary_path, out = Path(summary_path), Path(out)
    manifest = json.loads(summary_path.with_name("manifest.json").read_text())
    options = manifest["options"]
    fixture = json.loads(Path(options["Fixture"]).read_text())
    family = fixture["Config"]["Family"]
    stable_capacity = family == "capacity" and all(options[key] == 0 for key in ("Churn", "Edits", "Burst"))
    result = evaluate(json.loads(summary_path.read_text()), int(options["Agents"]), family, int(options["Duration"]), stable_capacity)
    result.update(summary=str(summary_path), summary_sha256=hashlib.sha256(summary_path.read_bytes()).hexdigest(), policy_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
    with out.open("x", encoding="utf-8") as stream:
        json.dump(result, stream, indent=2)
        stream.write("\n")
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--summary", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    result = evaluate_files(args.summary, args.out)
    print(json.dumps(result, sort_keys=True))
    return 0 if result["continue"] else 3


if __name__ == "__main__":
    raise SystemExit(main())
