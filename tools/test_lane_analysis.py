import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC=importlib.util.spec_from_file_location("lane_analysis",Path(__file__).with_name("analyze-lane-sweep.py"))
ANALYSIS=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ANALYSIS)


class AnalysisTests(unittest.TestCase):
    def manifest(self):
        request=dict(successful_response=True,valid_ack=True,ns=1000000,censored=False,deadline_limited=False,start_lateness_ns=0)
        row=dict(status="measured",corpus="copy",load="C18",mode="OFF",batch_records=20,flush_ms=25,
            budget=dict(batch_records=20,flush_ms=25,batch_bytes=8<<20,queue_records=256,queue_bytes=32<<20,hold_target_ns=400000000),
            requests=[request],peers=[],failure_classes={},wall_ns=1000000,
            accounting=dict(committed_delta_occurrences=1,committed_delta_tasks=0,verified=True),acceptance="unobserved")
        return dict(payload_sha256="same-payload",corpora={"copy":{"sha256":"same-seed"}},runs=[row])

    def test_pool_matched_cases_not_failed_latencies(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);manifest=self.manifest()
            manifest["runs"][0]["requests"].append(dict(manifest["runs"][0]["requests"][0],successful_response=False,ns=1))
            (root/"manifest.json").write_text(json.dumps(manifest))
            result=ANALYSIS.analyze([root])["groups"][0]
            self.assertEqual(result["offered_hooks"],2)
            self.assertEqual(result["failed_hooks"],1)
            self.assertEqual(result["successful_hook"]["n"],1)
            self.assertEqual(result["successful_hook"]["p50_ms"],1)
            self.assertIsNone(result["successful_hook"]["p99_ms"])

    def test_changed_seed_refuses_pooling(self):
        with tempfile.TemporaryDirectory() as directory:
            roots=[Path(directory)/name for name in ("before","after")]
            for root in roots:root.mkdir();(root/"manifest.json").write_text(json.dumps(self.manifest()))
            changed=self.manifest();changed["payload_sha256"]="changed"
            (roots[1]/"manifest.json").write_text(json.dumps(changed))
            with self.assertRaisesRegex(ValueError,"changed seed/payload"):ANALYSIS.analyze(roots)

    def test_durable_fault_responses_are_not_successful_acks(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);manifest=self.manifest();row=manifest["runs"][0]
            row["requests"].append(dict(row["requests"][0],successful_response=False,deadline_limited=True))
            row["accounting"]["committed_delta_occurrences"]=2;row["accounting"]["verified"]=False
            (root/"manifest.json").write_text(json.dumps(manifest))
            group=ANALYSIS.analyze([root])["groups"][0]
            self.assertEqual(group["failed_hooks"],0)
            self.assertEqual(group["failed_hook_responses"],1)
            self.assertEqual(group["committed_minus_acknowledged"],1)
            self.assertEqual(group["successful_hook"]["n"],1)
            self.assertFalse(group["accounting_verified"])

    def test_different_configured_caps_never_pool(self):
        for cap in ("batch_bytes","queue_records","queue_bytes","hold_target_ns"):
            with self.subTest(cap=cap), tempfile.TemporaryDirectory() as directory:
                root=Path(directory);manifest=self.manifest()
                changed=self.manifest()["runs"][0]
                changed["budget"][cap]//=2
                manifest["runs"].append(changed)
                (root/"manifest.json").write_text(json.dumps(manifest))
                groups=ANALYSIS.analyze([root])["groups"]
                self.assertEqual(len(groups),2)
                self.assertEqual({g["budget"][cap] for g in groups},{r["budget"][cap] for r in manifest["runs"]})
                self.assertTrue(all(g["repetitions"]==1 for g in groups))

    def test_budget_key_order_does_not_split_identical_work(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);manifest=self.manifest()
            same=self.manifest()["runs"][0]
            same["budget"]=dict(reversed(list(same["budget"].items())))
            manifest["runs"].append(same)
            (root/"manifest.json").write_text(json.dumps(manifest))
            groups=ANALYSIS.analyze([root])["groups"]
            self.assertEqual(len(groups),1)
            self.assertEqual(groups[0]["repetitions"],2)

    def test_missing_budget_cannot_inherit_implicit_caps(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);manifest=self.manifest()
            del manifest["runs"][0]["budget"]["queue_bytes"]
            (root/"manifest.json").write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError,"full configured lane budget"):ANALYSIS.analyze([root])


if __name__=="__main__":unittest.main()
