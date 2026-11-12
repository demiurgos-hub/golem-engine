"""Summarize recorded tick phases after qualification; never changes raw evidence."""
import argparse
import bisect
import hashlib
import json
import math
import os
from pathlib import Path


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def distribution(samples):
    if not samples:
        return {"count": 0}
    ordered = sorted(samples)
    result = {"count": len(ordered), "max_ms": ordered[-1] / 1e6, "mean_ms": sum(ordered) / len(ordered) / 1e6, "sum_ms": sum(ordered) / 1e6}
    for name, fraction in (("p50", .5), ("p95", .95), ("p99", .99)):
        result[name + "_ms"] = ordered[math.ceil(len(ordered) * fraction) - 1] / 1e6
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("case", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--root", type=Path, default=Path("C:/Demiurgos/navmesh-validation/world-test") if os.name == "nt" else Path("/mnt/c/Demiurgos/navmesh-validation/world-test"))
    args = parser.parse_args()
    case, root = args.case.resolve(), args.root.resolve()
    out = (args.out or case / "per-tick-analysis.json").resolve()
    if not case.is_relative_to(root / "runs") or not out.is_relative_to(root / "runs"):
        parser.error("input case and output must be under qualification runs")
    samples = case / "samples.jsonl"
    summary_path = case / "summary.json"
    summary = json.loads(summary_path.read_text()) if summary_path.exists() else {}
    manifest_path = case / "manifest.json"
    manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
    measurements = summary.get("measurements", {})
    agent_ticks = measurements.get("AgentTicks", 0)
    eligible_ticks = measurements.get("EligibleAgentTicks", 0)
    phases = {name: [] for name in ("navigation", "Prepare", "Movement", "Reconcile", "Rebase")}
    groups = {}
    pending_admissions = {}
    last_revisions = None
    measured_admissions, slow_admissions = 0, 0
    requests_without_tick = 0
    outcomes = {}
    gc_pauses, tick_windows, gc_cpu_samples = [], [], []
    previous_pools, first_pools, final_pools = None, None, None
    pool_maxima = {kind: {name: 0 for name in ("Delayed", "Queued", "Running", "Completions")} for kind in ("Paths", "Builds")}

    def group(name, elapsed, admissions):
        value = groups.setdefault(name, {"navigation": [], "admissions": 0, "admissions_over_100ms": 0})
        value["navigation"].append(elapsed)
        value["admissions"] += admissions[0]
        value["admissions_over_100ms"] += admissions[1]

    def outcome(name, quarantined, elapsed):
        label = "observed_quarantine" if quarantined else "no_observed_quarantine"
        outcomes.setdefault(name, {}).setdefault(label, []).append(elapsed)

    with samples.open() as stream:
        for line in stream:
            record = json.loads(line)
            if "gc_cpu_fraction_since_process_start" in record and record.get("measured"):
                gc_cpu_samples.append(record["gc_cpu_fraction_since_process_start"])
            if record.get("event") == "gc_pause" and record["observation"]["Measured"]:
                gc_pauses.append(record["observation"])
            if record.get("event") == "navigation_tick":
                pools = record["queues_after_movement"]
                if record.get("measured"):
                    if first_pools is None:
                        first_pools = previous_pools or pools
                    final_pools = pools
                    for phase in ("queues_after_prepare", "queues_after_movement"):
                        for kind, values in pool_maxima.items():
                            for name in values:
                                values[name] = max(values[name], record[phase][kind][name])
                previous_pools = pools
            if record.get("event") == "path_admitted":
                observation = record["observation"]
                if observation["Measured"]:
                    outcome("admitted", observation["Quarantined"], observation["Elapsed"])
                if observation["Measured"] and not observation["Quarantined"]:
                    slow = observation["Elapsed"] > 100_000_000
                    measured_admissions += 1
                    slow_admissions += slow
                    counts = pending_admissions.setdefault(observation["Tick"], [0, 0])
                    counts[0] += 1
                    counts[1] += slow
            elif record.get("event") == "request_outcome":
                observation = record["observation"]
                outcome(observation["Outcome"], observation["Quarantined"], observation["Elapsed"])
            elif record.get("event") == "navigation_tick" and record.get("measured"):
                metrics = record["metrics"]
                elapsed = sum(metrics[name] for name in ("Prepare", "Movement", "Reconcile"))
                if "tick_observed_end_unix_nano" in record:
                    end = record["tick_observed_end_unix_nano"]
                    tick_windows.append((end - record["tick_wall_ns"], end, elapsed))
                phases["navigation"].append(elapsed)
                for name in ("Prepare", "Movement", "Reconcile", "Rebase"):
                    phases[name].append(metrics.get(name, 0))
                admissions = pending_admissions.pop(metrics["Tick"], [0, 0])
                attempts = metrics.get("RebaseAttempts", 0)
                cohort = "none" if attempts == 0 else "1-64" if attempts <= 64 else "65-128" if attempts <= 128 else "129-250" if attempts <= 250 else "251-500" if attempts <= 500 else "over500"
                group("rebase_attempts_" + cohort, elapsed, admissions)
                revisions = record["profile_revisions"]
                if last_revisions is not None:
                    changed = sum(before != after for before, after in zip(last_revisions, revisions))
                    group("profiles_published_" + str(changed), elapsed, admissions)
                last_revisions = revisions
                pending = record["queues_after_prepare"]["Paths"]["Completions"]
                group("completion_backlog_after_prepare_" + ("nonzero" if pending else "zero"), elapsed, admissions)
    requests_without_tick = sum(value[0] for value in pending_admissions.values())
    tick_ends = [window[1] for window in tick_windows]
    ticks_with_gc_end = set()
    for pause in gc_pauses:
        index = bisect.bisect_left(tick_ends, pause["EndUnixNano"])
        if index < len(tick_windows) and tick_windows[index][0] <= pause["EndUnixNano"]:
            ticks_with_gc_end.add(index)
    report = {
        "input": str(samples), "input_sha256": digest(samples), "analyzer_sha256": digest(Path(__file__)),
        "interval_clock": manifest.get("interval_clock", {"api": "legacy time.Now/time.Since", "limit": "Historical Windows phase/boundary estimates use the coarse Go clock; other platforms use their Go monotonic clock."}),
        "phase_percentiles": {name: distribution(value) for name, value in phases.items()},
        "cohorts": {name: {"navigation": distribution(value.pop("navigation")), **value} for name, value in sorted(groups.items())},
        "outside_quarantine_admissions": measured_admissions, "outside_quarantine_admissions_over_100ms": slow_admissions,
        "admissions_without_matching_tick_event": requests_without_tick,
        "request_outcomes_by_observed_quarantine": {name: {label: distribution(values) for label, values in cohorts.items()} for name, cohorts in sorted(outcomes.items())},
        "terminal_outcome_evidence_available": "RequestOutcomes" in summary.get("measurements", {}),
        "summary_request_outcomes": summary.get("measurements", {}).get("RequestOutcomes"),
        "physical_displacement": summary.get("physical_displacement", {"available": False, "interpretation": "Historical mode counts do not establish physical displacement."}),
        "observed_cadence": {
            "measured_ticks": measurements.get("MeasuredTicks"),
            "measured_duration_ns": manifest.get("options", {}).get("Duration"),
            "observed_tick_rate_hz": measurements.get("MeasuredTicks", 0) * 1e9 / manifest["options"]["Duration"] if manifest.get("options", {}).get("Duration", 0) > 0 else None,
            "configured_tick_rate_hz": manifest.get("options", {}).get("TickRate"),
            "nominal_minus_observed_ticks": manifest["options"]["Duration"] * manifest["options"]["TickRate"] / 1e9 - measurements.get("MeasuredTicks", 0) if manifest.get("options", {}).get("Duration", 0) > 0 and manifest.get("options", {}).get("TickRate", 0) > 0 else None,
            "full_tick_overruns": measurements.get("Overruns"),
            "full_tick_max_ms": summary.get("tick", {}).get("max_ms"),
            "interpretation": "Per-agent scheduling on every tick does not prove the loop sustained its configured wall-clock cadence. Report actual rate and overruns without inventing a tolerance.",
        },
        "population_modes": {
            "following_or_traversing_agent_ticks": measurements.get("ActiveAgentTicks", 0),
            "all_agent_ticks": agent_ticks,
            "quarantined_agent_ticks": measurements.get("QuarantinedAgentTicks", 0),
            "following_or_traversing_fraction_of_all_agent_ticks": measurements.get("ActiveAgentTicks", 0) / agent_ticks if agent_ticks else None,
            "quarantined_fraction_of_all_agent_ticks": measurements.get("QuarantinedAgentTicks", 0) / agent_ticks if agent_ticks else None,
            "following_or_traversing_fraction_outside_observed_quarantine": measurements.get("ActiveAgentTicks", 0) / eligible_ticks if eligible_ticks else None,
            "interpretation": "Runtime modes are not a count of agents simultaneously moving physically.",
        },
        "garbage_collection": {
            "evidence_available": "GCPauses" in measurements,
            "pause_duration": distribution([pause["PauseNanosecond"] for pause in gc_pauses]),
            "overwritten_pause_events": measurements.get("GCPauseEventsOmitted"),
            "process_lifetime_gc_cpu_fraction_min": min(gc_cpu_samples) if gc_cpu_samples else None,
            "process_lifetime_gc_cpu_fraction_max": max(gc_cpu_samples) if gc_cpu_samples else None,
            "navigation_ticks_with_gc_cycle_end": distribution([window[2] for i, window in enumerate(tick_windows) if i in ticks_with_gc_end]),
            "navigation_ticks_without_gc_cycle_end": distribution([window[2] for i, window in enumerate(tick_windows) if i not in ticks_with_gc_end]),
            "correlation_limit": "Cycle-end timestamps are matched to approximate whole-tick windows observed at OnTickEnd. This is not attribution of individual GC pauses or CPU work to navigation phases. Windows Go1.26.4 runtime GC pause durations remain coarse independently of NavigationNow/QPC.",
        },
        "worker_pool_counters": {kind: {"measured_delta": {name: final_pools[kind][name] - first_pools[kind][name] for name in ("Accepted", "Rejected", "Finished")}, "sampled_maximum": pool_maxima[kind]} for kind in pool_maxima} if final_pools else {},
        "limits": ["Rebase time is a subset of Movement and is not added to total navigation time.", "Publication cohorts count observed profile revision changes, not individual build attempts.", "Completion backlog is sampled after prepare; workers can complete concurrently just after its drain.", "Worker counter deltas use the final pre-measurement tick as baseline when present; maxima are samples at prepare and movement boundaries, not continuous observations.", "Percentiles use exact recorded nanoseconds and nearest rank; displayed runner histograms use conservative bins."],
    }
    with out.open("x") as stream:
        json.dump(report, stream, indent=2)
        stream.write("\n")
    print(out)


if __name__ == "__main__":
    main()
