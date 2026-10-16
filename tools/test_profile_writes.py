import importlib.util
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("profile_writes", Path(__file__).with_name("profile-writes.py"))
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)
render_spec = importlib.util.spec_from_file_location("render_profiles", Path(__file__).with_name("render-write-profiles.py"))
render = importlib.util.module_from_spec(render_spec)
render_spec.loader.exec_module(render)


class ProfileSafety(unittest.TestCase):
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
            with sqlite3.connect(source) as db:
                db.execute("pragma journal_mode=wal")
                db.execute("create table example (n)")
                db.execute("insert into example values (42)")
                db.commit()
                before = source.read_bytes()
                profile.backup(source, dest)
                self.assertEqual(source.read_bytes(), before)
                with sqlite3.connect(dest) as copy:
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
            with sqlite3.connect(db) as conn:
                conn.executescript("create table tasks(n); create table journal(n); create table journal_evidence(evidence_kind); insert into tasks values(1); insert into journal values(1); insert into journal_evidence values('pasture.lifecycle.occurrence.v2');")
            self.assertEqual(profile.counts(db), {"tasks": 1, "journal": 1, "occurrences": 1})
            result = profile.distribution([3_000_000, 1_000_000, 2_000_000])
            self.assertEqual(result["n"], 3)
            self.assertEqual(result["p50_ms"], 2)


if __name__ == "__main__":
    unittest.main()
