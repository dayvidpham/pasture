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


if __name__=="__main__":unittest.main()
