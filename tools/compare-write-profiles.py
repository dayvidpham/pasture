#!/usr/bin/env python3
"""Compare private matched-corpus campaigns without hiding failed work."""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import statistics


def grouped(manifest):
    groups = collections.defaultdict(list)
    for row in manifest["runs"]:
        groups[(row["shape"], row["path"], row["concurrency"], row["mode"])].append(row)
    return groups


def summarize(rows):
    requests = [r for row in rows for r in row["requests"]]
    success = [r["ns"] for r in requests if r["successful_response"]]
    categories = collections.Counter()
    for row in rows:
        categories.update(row["failure_classes"])
    return {"repetitions": len(rows), "offered": sum(r["offered"] for r in rows),
            "committed": sum(r["committed"] for r in rows), "failed": sum(r["failed"] for r in rows),
            "censored": sum(r["censored"] for r in requests), "failure_classes": dict(categories),
            "p50_all_ms": statistics.median(r["ns"] for r in requests)/1e6,
            "p50_success_ms": statistics.median(success)/1e6 if success else None,
            "wall_seconds": sum(r["wall_ns"] for r in rows)/1e9,
            "per_repeat_p50_all_ms": [r["latency"]["p50_ms"] for r in rows]}


def compare(before, after):
    for shape, corpus in before["corpora"].items():
        if corpus["sha256"] != after["corpora"][shape]["sha256"] or corpus["counts"] != after["corpora"][shape]["counts"]:
            raise ValueError("starting corpus differs; refusing an equal-work claim")
    if before["payload_sha256"] != after["payload_sha256"]:
        raise ValueError("payload bytes differ; refusing an insert/update comparison")
    a, b = grouped(before), grouped(after)
    if set(a) != set(b):
        raise ValueError("case coverage differs")
    comparisons = []
    for key in a:
        left, right = a[key], b[key]
        order = lambda rows: sorted((r["repeat"], r["order_index"], r["offered"]) for r in rows)
        if order(left) != order(right):
            raise ValueError("repeat/order/offered work differs")
        x, y = summarize(left), summarize(right)
        equal_successful_work = x["offered"] == y["offered"] == x["committed"] == y["committed"] and not x["censored"] and not y["censored"]
        comparisons.append({"shape": key[0], "path": key[1], "concurrency": key[2], "mode": key[3],
            "before": x, "after": y, "equal_successful_work": equal_successful_work,
            "wall_ratio_before_over_after": x["wall_seconds"]/y["wall_seconds"] if equal_successful_work else None})
    return comparisons


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    args = parser.parse_args()
    paths = [p.resolve()/"manifest.json" for p in (args.before, args.after)]
    manifests = [json.loads(p.read_text()) for p in paths]
    result = {"manifest_hashes": [hashlib.sha256(p.read_bytes()).hexdigest() for p in paths],
              "comparisons": compare(*manifests)}
    output = args.after/"comparison.json"
    output.write_text(json.dumps(result, indent=2))
    print(output)
