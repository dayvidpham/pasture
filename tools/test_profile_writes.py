import importlib.util
import copy
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("profile_writes", Path(__file__).with_name("profile-writes.py"))
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)
render_spec = importlib.util.spec_from_file_location("render_profiles", Path(__file__).with_name("render-write-profiles.py"))
render = importlib.util.module_from_spec(render_spec)
render_spec.loader.exec_module(render)
compare_spec = importlib.util.spec_from_file_location("compare_profiles", Path(__file__).with_name("compare-write-profiles.py"))
comparison = importlib.util.module_from_spec(compare_spec)
compare_spec.loader.exec_module(comparison)


class ProfileSafety(unittest.TestCase):
    def test_comparison_refuses_changed_inputs_or_failed_work_speedups(self):
        row = {"shape": "synthetic", "path": "create", "concurrency": 1, "mode": "baseline", "repeat": 0, "order_index": 0,
               "offered": 2, "committed": 2, "failed": 0, "wall_ns": 3_000_000, "failure_classes": {}, "latency": {"p50_ms": 2},
               "requests": [{"ns": n, "successful_response": True, "censored": False} for n in (1_000_000, 2_000_000)]}
        before = {"corpora": {"synthetic": {"sha256": "same", "counts": {"tasks": 60}}}, "payload_sha256": "same", "runs": [row]}
        after = copy.deepcopy(before)
        self.assertTrue(comparison.compare(before, after)[0]["equal_successful_work"])
        after["runs"][0]["committed"] = 1
        after["runs"][0]["failed"] = 1
        result = comparison.compare(before, after)[0]
        self.assertFalse(result["equal_successful_work"])
        self.assertIsNone(result["wall_ratio_before_over_after"])
        after["payload_sha256"] = "different"
        with self.assertRaises(ValueError): comparison.compare(before, after)
        after = copy.deepcopy(before)
        after["corpora"]["synthetic"]["sha256"] = "different"
        with self.assertRaises(ValueError): comparison.compare(before, after)

    def test_reused_payload_is_exact_warmed_bytes_without_another_hook(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            prior = Path(tmp)
            body = b'{"cwd":"/tmp/opencode/old-private-root","unchanged":"original bytes"}'
            (prior/"fixture.json").write_bytes(body)
            (prior/"manifest.json").write_text(json.dumps({"corpora": {"synthetic": {}}, "payload_sha256": hashlib.sha256(body).hexdigest()}))
            with closing(sqlite3.connect(prior/"synthetic.db")) as db, db:
                db.execute("create table lifecycle_payload_blobs(digest,body)")
                db.execute("insert into lifecycle_payload_blobs values(?,?)", ("sha256:"+hashlib.sha256(body).hexdigest(), body))
            with mock.patch.object(profile, "run", side_effect=AssertionError("must not warm again")):
                self.assertEqual(profile.retained_payload(prior, {"different": "new fixture"}), body)
            (prior/"fixture.json").write_bytes(body+b" ")
            with self.assertRaises(RuntimeError): profile.retained_payload(prior, {})
            (prior/"fixture.json").write_bytes(body)
            with closing(sqlite3.connect(prior/"synthetic.db")) as db, db: db.execute("delete from lifecycle_payload_blobs")
            with self.assertRaises(RuntimeError): profile.retained_payload(prior, {})

    def test_task_and_hook_deadlines_both_enter_deadline_cohort(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            root = Path(tmp)
            env = profile.environment(root, root/"store.db")
            task = profile.run(["sh", "-c", "echo 'context deadline exceeded'; exit 3"], env, root, root/"task.log")
            hook = profile.run(["sh", "-c", "echo 'pasture could not evaluate this lifecycle hook event: context deadline exceeded'"], env, root, root/"hook.log")
            success = profile.run(["sh", "-c", "exit 0"], env, root, root/"ok.log")
            self.assertTrue(task["deadline_limited"])
            self.assertTrue(hook["deadline_limited"])
            self.assertFalse(success["deadline_limited"])
            self.assertTrue(success["successful_response"])
            self.assertIsNone(success["failure_class"])

    def test_watchdog_censor_fields_and_termination_are_explicit(self):
        class ImmediateWatchdog:
            def __init__(self, interval, callback): self.callback = callback
            def start(self): self.callback()
            def cancel(self): pass
            def join(self): pass
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            root = Path(tmp)
            with mock.patch.object(profile.threading, "Timer", ImmediateWatchdog):
                result = profile.run(["sleep", "60"], profile.environment(root, root/"store.db"), root, root/"censored.log")
            self.assertTrue(result["watchdog_fired"])
            self.assertTrue(result["censored"])
            self.assertTrue(result["deadline_limited"])
            self.assertFalse(result["successful_response"])
            self.assertTrue(result["process_completed"])
            self.assertIn(2, result["termination"]["signals"])

    def test_failure_classes_do_not_conflate_deadline_authority_or_conflict(self):
        self.assertEqual(profile.failure_class("The lifecycle payload transaction could not start. database is locked (5) (SQLITE_BUSY)"), "payload_begin_sqlite_busy")
        self.assertEqual(profile.failure_class("SQLite exhausted its configured writer wait while ingress was still live"), "append_sqlite_busy_ingress_live")
        self.assertEqual(profile.failure_class("the lifecycle occurrence commit could not acquire the shared database writer before its deadline"), "append_deadline_before_acquire")
        self.assertEqual(profile.failure_class("provenance: authority does not govern the effect's task"), "authority_refusal")
        self.assertEqual(profile.failure_class("provenance: operation identity conflict"), "operation_conflict")
        self.assertEqual(profile.failure_class("Apply: acquire, fold, and commit pinned SQLite write transaction: database is locked"), "operation_apply_sqlite_busy")
        self.assertEqual(profile.failure_class("unexpected failure"), "unclassified")

    def test_lane_proof_rejects_successful_direct_fallback_after_daemon_loss(self):
        log = "signal=interrupt\ncommittedRecords=0 batches=0 maxBatchRecords=0 maxHold=0s holdExceeded=0\npastured stopped cleanly\n"
        # Auto-mode may silently commit directly when the socket disappeared.
        # Exit-zero hooks plus SQL success cannot pass as measured lane work.
        proof = profile.lane_evidence(log, [""], 36, 36, 0)
        self.assertFalse(proof["verified"])
        healthy = log.replace("committedRecords=0 batches=0 maxBatchRecords=0", "committedRecords=36 batches=2 maxBatchRecords=18")
        self.assertTrue(profile.lane_evidence(healthy, [""], 36, 36, 0)["verified"])
        self.assertFalse(profile.lane_evidence(healthy, ["this hook writes directly"], 36, 36, 0)["verified"])
        self.assertFalse(profile.lane_evidence(healthy, [""], 36, 36, -9)["verified"])
        self.assertFalse(profile.lane_evidence(healthy, [""], 36, 35, 0)["verified"])

    def test_flames_preserve_sample_weights_and_escape_function_names(self):
        self.assertEqual(render.cpu_metadata("Period: 20000000\n", [(40000000, ["root"])]), (20000000, 2))
        self.assertEqual(render.cpu_metadata("", []), (None, None))
        raw = """Samples:
samples/count cpu/nanoseconds
 2 20000000: 1 2
 1 10000000: 3 2
Locations
 1: 0x123 M=1 leaf<&> /source.go:1:0 s=1
 2: 0x456 M=1 root /source.go:2:0 s=2
 3: 0x789 M=1 other /source.go:3:0 s=3
Mappings
"""
        stacks = render.decode_raw(raw, "cpu")
        self.assertEqual(stacks, [(20000000, ["root", "leaf<&>"]), (10000000, ["root", "other"])])
        svg = render.flame(stacks, "cpu")
        self.assertIn("total 30000000", svg)
        self.assertIn("leaf&lt;&amp;&gt;", svg)
        self.assertIn('width="800.000"', svg)
        self.assertNotIn("leaf<&>", svg)
        # Samples with unavailable stacks still contribute width and sample
        # counts; silently dropping them would disagree with pprof totals.
        unknown = render.decode_raw("Samples:\nsamples/count cpu/nanoseconds\n 1 10000000: \nLocations\nMappings\n", "cpu")
        self.assertEqual(unknown, [(10000000, ["[unknown]"])])

    def test_backup_includes_wal_and_never_changes_source(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            source, dest = Path(tmp)/"source.db", Path(tmp)/"copy.db"
            with closing(sqlite3.connect(source)) as db, db:
                db.execute("pragma journal_mode=wal")
                db.execute("create table example (n)")
                db.execute("insert into example values (42)")
                db.commit()
                before = source.read_bytes()
                profile.backup(source, dest)
                self.assertEqual(source.read_bytes(), before)
                with closing(sqlite3.connect(dest)) as copy:
                    self.assertEqual(copy.execute("select n from example").fetchall(), [(42,)])

    def test_environment_drops_operator_knobs_and_overrides_every_root(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            root = Path(tmp)
            env = profile.environment(root, root/"store.db")
            self.assertEqual(env["PASTURE_DB_PATH"], str(root/"store.db"))
            self.assertEqual({k for k in env if k.startswith("PASTURE_")}, {"PASTURE_DB_PATH", "PASTURE_WRITE_LANE"})
            for key in ("HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"):
                self.assertTrue(Path(env[key]).is_relative_to(root))
            self.assertLess(len(env["XDG_RUNTIME_DIR"] + "/pasture/lane-123456789012.sock"), 108)

    def test_stop_reaps_process_group(self):
        process = subprocess.Popen(["sleep", "60"], start_new_session=True)
        profile.stop(process)
        self.assertIsNotNone(process.poll())
        profile.stop(process)
        with self.assertRaises(ProcessLookupError):
            os.kill(process.pid, 0)

    def test_failed_child_preserves_exit_and_closes_log(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            root = Path(tmp)
            env = profile.environment(root, root/"store.db")
            result = profile.run(["sh", "-c", "exit 7"], env, root, root/"failure.log")
            self.assertEqual(result["exit"], 7)
            self.assertGreater(result["ns"], 0)
            self.assertIsNone(result["profile"])

    def test_counts_and_distribution(self):
        with tempfile.TemporaryDirectory(dir="/tmp/opencode") as tmp:
            db = Path(tmp)/"store.db"
            with closing(sqlite3.connect(db)) as conn, conn:
                conn.executescript("create table tasks(n); create table journal(n); create table journal_evidence(evidence_kind); insert into tasks values(1); insert into journal values(1); insert into journal_evidence values('pasture.lifecycle.occurrence.v2');")
            self.assertEqual(profile.counts(db), {"tasks": 1, "journal": 1, "occurrences": 1})
            result = profile.distribution([3_000_000, 1_000_000, 2_000_000])
            self.assertEqual(result["n"], 3)
            self.assertEqual(result["p50_ms"], 2)


if __name__ == "__main__":
    unittest.main()
