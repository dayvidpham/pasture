"""Local tests: no builds, live stores, campaign, tracker or daemon required."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("lane_sweep", Path(__file__).with_name("lane-sweep.py"))
SWEEP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SWEEP)


class SweepTests(unittest.TestCase):
    def test_pilot_grid_and_pairs(self):
        plan = SWEEP.plan()
        self.assertEqual(plan, SWEEP.plan())
        self.assertNotEqual(plan, SWEEP.plan(1))
        self.assertEqual(len(plan), 56)
        self.assertEqual(len({r["case"] for r in plan}), 56)
        measured = [r for r in plan if r["mode"] == "ON"]
        self.assertEqual(len(measured), 48)
        for corpus in SWEEP.SEED_HASHES:
            for load in ("C1", "C18", "arrival"):
                rows = [r for r in plan if r["corpus"] == corpus and r["load"] == load]
                self.assertEqual(len(rows), 9)
                self.assertEqual(sum(r["batch_records"] == 20 and r["flush_ms"] == 25 for r in rows), 2)

    def test_sparse_tails_are_explicitly_null(self):
        for n in (0, 1, 36, 99):
            result = SWEEP.distribution(list(range(n)))
            self.assertEqual(result["n"], n)
            self.assertIsNone(result["p99_ms"])
            if n: self.assertIsNotNone(result["p50_ms"])
            self.assertEqual(result["max_ms"], (n-1)/1e6 if n else None)
        result = SWEEP.distribution(list(range(1, 101)))
        self.assertEqual(result["p95_ms"], 95/1e6)
        self.assertEqual(result["p99_ms"], 99/1e6)

    def test_finalists_are_bounded_repeated_real_shapes(self):
        rows=SWEEP.plan(finalists=[10,20])
        self.assertEqual(len(rows),18)
        for corpus in SWEEP.SEED_HASHES:
            measured=[r for r in rows if r["corpus"]==corpus and r["mode"]=="ON"]
            self.assertEqual(len(measured),8)
            self.assertEqual({r["repeat"] for r in measured},{0,1})
            self.assertEqual({r["load"] for r in measured},{"C18","arrival"})

    def result(self, **kwargs):
        return dict(valid_ack=True, successful_response=True, reported_fault=False, **kwargs)

    def test_accounting_accepts_only_reconciled_work(self):
        before = {"tasks": 60, "occurrences": 1}
        after = {"tasks": 61, "occurrences": 2}
        result = SWEEP.accounting([self.result()], [self.result()], before, after, 0)
        self.assertTrue(result["verified"])
        self.assertEqual(result["committed_delta_tasks"], 1)
        self.assertEqual(result["committed_delta_occurrences"], 1)

    def test_invalid_ack_missing_fault_missing_commits_rejected(self):
        before = {"tasks": 0, "occurrences": 0}
        after = {"tasks": 1, "occurrences": 1}
        variants = [
            ([dict(self.result(), valid_ack=False)], after, 0),
            ([dict(self.result(), reported_fault=True, successful_response=False)], after, 0),
            ([self.result()], dict(after, occurrences=0), 0),
            ([self.result()], dict(after, tasks=0), 0),
            ([self.result()], after, 1),
        ]
        for requests, counts, faults in variants:
            with self.subTest(requests=requests, counts=counts, faults=faults):
                self.assertFalse(SWEEP.accounting(requests, [self.result()], before, counts, faults)["verified"])

    def test_peer_failures_rejected_even_with_sql_task_delta(self):
        before = {"tasks": 0, "occurrences": 0}
        after = {"tasks": 1, "occurrences": 0}
        result = SWEEP.accounting([], [dict(self.result(), successful_response=False)], before, after, 0)
        self.assertFalse(result["verified"])

    def test_observers_retain_actual_values_and_caps(self):
        with tempfile.TemporaryDirectory() as directory:
            case = Path(directory)
            lane = dict(reason="full", records=20, bytes=800, max_queue_records=36, max_queue_bytes=1440, full_condition=True)
            (case/"lane.jsonl").write_text(json.dumps(lane)+"\n")
            events = [dict(pid=123, op=1, method="ApplyBatchContext", phase=phase, start_ns=1, end_ns=10_000_001,
                           outcome="committed" if phase == "transaction_scope" else "ok")
                      for phase in ("sql_conn", "begin_attempt", "begin_wait", "transaction_scope", "body", "commit")]
            (case/"sql-daemon.jsonl").write_text("".join(json.dumps(e)+"\n" for e in events))
            budget = dict(batch_records=20, queue_records=36, queue_bytes=1440)
            result = SWEEP.observer_evidence(case, budget)
            self.assertTrue(result["verified"])
            self.assertEqual(result["flush_reasons"], {"full": 1})
            self.assertEqual(result["maxima"]["batch_bytes"], 800)
            self.assertEqual(result["actual_transaction"]["max_ms"], 10)
            self.assertEqual(result["sql_committed_batches"], 1)
            self.assertEqual(len(result["sql_phases"]), 6)
            self.assertTrue(SWEEP.observer_evidence(case, budget, expected_batches=1)["verified"])
            self.assertFalse(SWEEP.observer_evidence(case, budget, expected_batches=2)["verified"])
            self.assertFalse(SWEEP.observer_evidence(case, budget, expected_batches=1, expected_peers=1)["verified"])
            self.assertFalse(SWEEP.observer_evidence(case, dict(budget, queue_bytes=1))["verified"])
            (case/"daemon.log").write_text("EXPERIMENT_METRICS_ERROR close failure; ACK unchanged\n")
            result = SWEEP.observer_evidence(case, budget)
            self.assertFalse(result["verified"])
            self.assertTrue(any("EXPERIMENT_METRICS_ERROR" in e for e in result["errors"]))
            (case/"daemon.log").write_text("")
            (case/"sql-daemon.jsonl").write_text("".join(json.dumps(e)+"\n" for e in events[:-1]))
            self.assertFalse(SWEEP.observer_evidence(case, budget)["verified"])

    def test_missing_or_incompatible_observers_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            case = Path(directory)
            self.assertFalse(SWEEP.observer_evidence(case, {})["verified"])
            (case/"lane.jsonl").write_text('{"batch_records":true}\n')
            (case/"sql-daemon.jsonl").write_text('{"elapsed":300}\n')
            self.assertFalse(SWEEP.observer_evidence(case, {})["verified"])

    def test_native_stdout_not_merged_into_ack(self):
        with tempfile.TemporaryDirectory() as directory:
            case = Path(directory)
            env = {"PATH": os.environ["PATH"]}
            cmd = [sys.executable, "-c", "import sys; print('diagnostic', file=sys.stderr)"]
            result = SWEEP.native_run(cmd, env, case, "hook", b"{}")
            self.assertTrue(result["valid_ack"])
            self.assertIn("diagnostic", (case/"hook.log").read_text())
            cmd = [sys.executable, "-c", "print('not a native proceed')"]
            result = SWEEP.native_run(cmd, env, case, "bad", b"{}")
            self.assertFalse(result["valid_ack"])
            self.assertTrue(result["successful_response"])  # exit zero alone insufficient

    def test_native_fail_open_is_not_success(self):
        with tempfile.TemporaryDirectory() as directory:
            cmd = [sys.executable, "-c", "import sys; print('pasture could not evaluate this lifecycle hook event', file=sys.stderr)"]
            result = SWEEP.native_run(cmd, {"PATH": os.environ["PATH"]}, Path(directory), "fault", b"{}")
            self.assertTrue(result["valid_ack"])
            self.assertTrue(result["reported_fault"])
            self.assertFalse(result["successful_response"])

    def test_task_ack_requires_json_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            for output, expected in (("{}", False), ('{"id":"file://test/uuid"}', True), ("ok", False)):
                cmd = [sys.executable, "-c", f"print({output!r})"]
                self.assertEqual(SWEEP.native_run(cmd, {"PATH": os.environ["PATH"]}, Path(directory), "peer")["valid_ack"], expected)

    def test_global_watchdog_kills_and_reaps_child(self):
        guard = SWEEP.Watchdog(.1)
        try:
            child = guard.spawn([sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True)
            self.assertEqual(child.wait(timeout=5), -9)
            self.assertTrue(guard.expired.is_set())
            with self.assertRaisesRegex(RuntimeError, "hard budget exhausted"):
                guard.spawn([sys.executable, "-c", "pass"], start_new_session=True)
        finally:
            guard.close()

    def test_cleanup_is_not_reported_as_budget_expiry(self):
        guard = SWEEP.Watchdog(10)
        guard.close()
        self.assertFalse(guard.expired.is_set())

    def test_summary_does_not_pass_sparse_pilot_or_failed_work(self):
        base = dict(corpus="synthetic", load="peer-only", status="measured", case="base",
                    peer_latency=SWEEP.distribution([1]*6), accounting={"verified": True})
        row = dict(corpus="synthetic", load="C1", status="measured", case="on", mode="ON", batch_records=20, flush_ms=25,
                   peer_latency=SWEEP.distribution([1]*6), accounting={"verified": True}, lane_evidence={"verified": True},
                   observers=dict(verified=True, actual_transaction=SWEEP.distribution([1]*36)))
        manifest = {"runs": [base, row]}
        SWEEP.summarize(manifest)
        self.assertEqual(row["acceptance"]["verdict"], "INDETERMINATE")
        row["observers"]["actual_transaction"] = SWEEP.distribution([500_000_000]*100)
        SWEEP.summarize(manifest)
        self.assertEqual(row["acceptance"]["verdict"], "REJECT")
        self.assertIn("actual transaction p95 exceeds 400ms", row["acceptance"]["errors"])

    def test_current_caps_explicit_and_socket_paths_short(self):
        args = SWEEP.parser().parse_args(["--dependency-worktree", "/not-used"])
        self.assertEqual((args.batch_bytes, args.queue_records, args.queue_bytes), (8 << 20, 256, 32 << 20))
        for row in SWEEP.plan():
            runtime = Path("/tmp/opencode/lane-sweep-abcdefgh")/row["case"]/"r/pasture/lane-0123456789ab.sock"
            self.assertLess(len(str(runtime)), 108)

    def test_environment_and_teardown_wired_without_build_or_daemon(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            args = SWEEP.parser().parse_args(["--dependency-worktree", str(work)])
            args.manifest = {"runs": []}
            row = dict(SWEEP.plan()[0], load="C1", mode="OFF")
            # Deliberate readiness failure exercises finally without a campaign.
            daemon = unittest.mock.Mock()
            daemon.poll.return_value = 1
            guard = unittest.mock.Mock()
            guard.deadline = time.monotonic()+10
            guard.spawn.return_value = daemon
            with patch.object(SWEEP.PW, "backup"), patch.object(SWEEP.PW, "stop", return_value={"status": 1}) as stop:
                with self.assertRaisesRegex(RuntimeError, "readiness"):
                    SWEEP.run_case(work, row, work/"seed", {"pastured": "not-run"}, b"{}", args, guard)
                stop.assert_called_once_with(daemon)
            client = row["client_environment"]
            denv = row["daemon_environment"]
            self.assertEqual(client["PASTURE_WRITE_LANE"], "auto")
            self.assertNotIn("PASTURE_EXPERIMENT_LANE_BUDGET", client)
            self.assertEqual(set(k for k in client if k.startswith("PASTURE_EXPERIMENT_")), {"PASTURE_EXPERIMENT_SANDBOX"})
            self.assertNotIn("PASTURE_EXPERIMENT_LANE_METRICS", denv)
            self.assertNotIn("PASTURE_EXPERIMENT_SQL_TIMINGS", denv)
            self.assertIn("PASTURE_EXPERIMENT_LANE_BUDGET", denv)
            self.assertEqual((work/row["case"]).stat().st_mode & 0o777, 0o700)


if __name__ == "__main__":
    unittest.main()
