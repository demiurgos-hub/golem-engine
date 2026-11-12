import unittest

from gate_policy import BASE_GATES, evaluate


class GatePolicyTests(unittest.TestCase):
    def summary(self):
        return {"gates": {name: "pass" for name in BASE_GATES}}

    def test_waiver_keeps_raw_failure_and_external_scratch_requirement(self):
        summary = self.summary()
        summary["gates"].update(nav_tick_p99_le_5ms="fail", temporary_scratch_allocations="not_measured")
        result = evaluate(summary, 1000)
        self.assertTrue(result["continue"])
        self.assertEqual(result["recorded_observations"], {"nav_tick_p99_le_5ms": "fail"})
        self.assertEqual(result["requires_external_evidence"], {"temporary_scratch_allocations": "not_measured"})
        self.assertEqual(summary["gates"]["nav_tick_p99_le_5ms"], "fail")

    def test_1000_required_path_and_memory_failures_stop(self):
        for gate in ("path_end_to_end_p99_le_100ms_outside_quarantine", "memory_drain_baseline_envelope", "unaffected_following_agent_cadence", "all_agents_move_each_complete_measured_tick"):
            summary = self.summary()
            summary["gates"][gate] = "fail"
            self.assertFalse(evaluate(summary, 1000)["continue"], gate)

    def test_5000_performance_does_not_exempt_integrity(self):
        summary = self.summary()
        summary["gates"]["path_end_to_end_p99_le_100ms_outside_quarantine"] = "fail"
        summary["gates"]["every_agent_scheduled_each_tick"] = "fail"
        self.assertTrue(evaluate(summary, 5000)["continue"])
        for gate in ("no_failed_or_unsupported_agents", "host_traversal_recovery", "complete_evidence_stream", "managed_generations_including_candidate_bounded", "live_drain_pipeline_settled"):
            broken = {"gates": dict(summary["gates"], **{gate: "fail"})}
            self.assertFalse(evaluate(broken, 5000)["continue"], gate)

    def test_missing_or_unknown_required_measurement_stops(self):
        summary = self.summary()
        summary["gates"]["every_agent_scheduled_each_tick"] = "unknown"
        self.assertFalse(evaluate(summary, 1000)["continue"])
        del summary["gates"]["no_failed_or_unsupported_agents"]
        self.assertEqual(evaluate(summary, 1000)["blocking"]["no_failed_or_unsupported_agents"], "missing")

    def test_only_stable_capacity_path_observation_can_be_not_applicable(self):
        summary = self.summary()
        summary["gates"].update(path_end_to_end_p99_le_100ms_outside_quarantine="not_applicable: no measured requests", all_agents_move_each_complete_measured_tick="pass", all_startup_routes_admitted_before_measurement="pass")
        self.assertTrue(evaluate(summary, 1000, "capacity", stable_capacity=True)["continue"])
        self.assertFalse(evaluate(summary, 1000, "capacity", stable_capacity=False)["continue"])
        self.assertFalse(evaluate(summary, 1000, "terrain")["continue"])
        summary["gates"]["retired_generations_bounded"] = "not_applicable: missing observation"
        self.assertFalse(evaluate(summary, 1000, "capacity", stable_capacity=True)["continue"])


if __name__ == "__main__":
    unittest.main()
