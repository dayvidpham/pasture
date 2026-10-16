#!/usr/bin/env python3
"""Privacy-safe aggregate reader; never opens a database or launches children."""
import argparse
import collections
import hashlib
import importlib.util
import json
from pathlib import Path

SPEC=importlib.util.spec_from_file_location("sweep",Path(__file__).with_name("lane-sweep.py"))
SWEEP=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SWEEP)


def analyze(roots):
    groups=collections.defaultdict(list)
    seeds=None
    for root in roots:
        manifest=json.loads((root/"manifest.json").read_text())
        identity=(manifest["payload_sha256"],{k:v["sha256"] for k,v in manifest["corpora"].items()})
        if seeds is not None and seeds!=identity: raise ValueError("changed seed/payload identity: do not pool these experiments")
        seeds=identity
        for row in manifest["runs"]:
            if row.get("status")!="measured": continue
            key=(row["corpus"],row["load"],row["mode"],row["batch_records"],row["flush_ms"],len(row["requests"]))
            groups[key].append((root,row))
    output=[]
    for (corpus,load,mode,batch,flush,offers),entries in sorted(groups.items()):
        requests=[r for _,row in entries for r in row["requests"]]
        peers=[r for _,row in entries for r in row["peers"]]
        phases=collections.defaultdict(list);stages=collections.defaultdict(list)
        reasons=collections.Counter(); failures=collections.Counter();peer_failures=collections.Counter();maxima={}
        for root,row in entries:
            failures.update(row["failure_classes"]);peer_failures.update(row.get("peer_failure_classes",{}))
            if mode=="ON":
                reasons.update(row["observers"]["flush_reasons"])
                for key,value in row["observers"]["maxima"].items():
                    if value is not None: maxima[key]=max(maxima.get(key,0),value)
                # Raw intervals, not averages of per-case percentiles.
                for filename in (root/row["case"]).glob("sql-*.jsonl"):
                    for line in filename.read_text().splitlines():
                        event=json.loads(line)
                        owner="peer" if filename.name.startswith("sql-peer-") else "daemon"
                        phases[owner+":"+event["method"]+":"+event["phase"]+":"+event["outcome"]].append(event["end_ns"]-event["start_ns"])
                for filename in (root/row["case"]).glob("profiles/*/stages.json"):
                    for event in json.loads(filename.read_text())["events"] or []: stages[event["stage"]].append(event["nanoseconds"])
        wall=sum(row["wall_ns"] for _,row in entries)/1e9
        committed=sum(row["accounting"]["committed_delta_occurrences"] for _,row in entries)
        success=lambda r:r["successful_response"] and r["valid_ack"]
        output.append(dict(corpus=corpus,load=load,mode=mode,batch_records=batch,flush_ms=flush,offers_per_case=offers,repetitions=len(entries),
            offered_hooks=len(requests),committed_hooks=committed,failed_hooks=len(requests)-committed,
            offered_peers=len(peers),committed_peers=sum(row["accounting"]["committed_delta_tasks"] for _,row in entries),
            failure_classes=dict(failures),peer_failure_classes=dict(peer_failures),
            censored=sum(r["censored"] for r in requests+peers),deadline_limited=sum(r["deadline_limited"] for r in requests+peers),
            successful_hook=SWEEP.distribution([r["ns"] for r in requests if success(r)]),
            successful_peer=SWEEP.distribution([r["ns"] for r in peers if success(r)]),
            arrival_lateness=SWEEP.distribution([r["start_lateness_ns"] for r in requests]),
            wall_seconds=wall,offered_hooks_per_second=len(requests)/wall,committed_hooks_per_second=committed/wall,
            flush_reasons=dict(reasons),maxima=maxima,phases={k:SWEEP.distribution(v) for k,v in phases.items()},
            stages={k:SWEEP.distribution(v) for k,v in stages.items()},
            accounting_verified=all(row["accounting"]["verified"] for _,row in entries),
            observer_verified=all(row.get("observers",{"verified":True})["verified"] for _,row in entries),
            acceptance=[row["acceptance"] for _,row in entries]))
    return {"schema":"lane-sweep-aggregate.v1","source_sha256":hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            "definitions":"p50/p95 descriptive order statistics; p99 only n>=100; do not infer reliability or physical lock duration; peer-only throughput includes no hook offers",
            "groups":output}


if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("roots",type=Path,nargs="+")
    args=parser.parse_args()
    print(json.dumps(analyze(args.roots),indent=2))
