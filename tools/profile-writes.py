#!/usr/bin/env python3
"""Private, fixed-work production-write campaign; run inside nix develop.

No default source: --source is an explicit read-only SQLite backup opt-in.
Each path/concurrency/mode restores the SAME immutable seed. No speedup claim
is made. This trusted developer harness is not an OS security sandbox.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
import re
from pathlib import Path
import signal
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parent.parent


def backup(source, dest):
    with sqlite3.connect(source.resolve().as_uri() + "?mode=ro", uri=True) as src:
        with sqlite3.connect(dest) as dst:
            src.backup(dst)
            assert dst.execute("pragma integrity_check").fetchone() == ("ok",)


def counts(db):
    with sqlite3.connect(db.resolve().as_uri() + "?mode=ro", uri=True) as conn:
        return {"tasks": conn.execute("select count(*) from tasks").fetchone()[0],
                "occurrences": conn.execute("select count(*) from journal_evidence where evidence_kind in ('pasture.lifecycle.occurrence.v1','pasture.lifecycle.occurrence.v2')").fetchone()[0],
                "journal": conn.execute("select count(*) from journal").fetchone()[0]}


def environment(work, db):
    # Never inherit operator PASTURE_*, harness, config, daemon or capture knobs.
    env = {"PATH": os.environ["PATH"], "HOME": str(work / "home"),
           "XDG_CONFIG_HOME": str(work / "config"), "XDG_DATA_HOME": str(work / "data"),
           "XDG_STATE_HOME": str(work / "state"), "XDG_RUNTIME_DIR": str(work / "r"),
           "TMPDIR": str(work / "tmp"), "PASTURE_DB_PATH": str(db),
           "PASTURE_WRITE_LANE": "direct"}
    for key in ("HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"):
        Path(env[key]).mkdir(mode=0o700, parents=True, exist_ok=True)
    return env


def stop(process):
    if process is None:
        return
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGINT)
    try:
        process.wait(timeout=80)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=10)


def run(cmd, env, cwd, output, payload=None):
    started = time.perf_counter_ns()
    with output.open("wb") as log:
        process = subprocess.Popen(cmd, env=env, cwd=cwd, stdin=subprocess.PIPE,
                                   stdout=log, stderr=log, start_new_session=True)
        # communicate(timeout=...) uses waitpid polling on Linux and quantizes
        # short requests by tens of milliseconds. A separate watchdog preserves
        # blocking waitpid timing while retaining bounded process cleanup.
        watchdog = threading.Timer(90, stop, args=(process,))
        watchdog.daemon = True
        watchdog.start()
        try:
            process.communicate(payload)
        finally:
            watchdog.cancel()
            if process.poll() is None:
                stop(process)
    return {"ns": time.perf_counter_ns() - started, "exit": process.returncode,
            "command": cmd, "profile": env.get("PASTURE_WRITE_PROFILE_DIR")}


def distribution(values):
    values = sorted(values)
    return {"n": len(values), "min_ms": values[0] / 1e6,
            "p50_ms": values[len(values)//2] / 1e6,
            "p95_ms": values[min(len(values)-1, len(values)*95//100)] / 1e6,
            "sum_ms": sum(values) / 1e6}


def lane_evidence(daemon_log, request_logs, offered, committed, daemon_exit):
    """Prove all successful writes used the lane, not auto-mode direct fallback.

    The production log calls Records 'committedRecords', but the lane increments
    it for settled failures too. SQL deltas must independently equal offered.
    """
    matches = re.findall(r"committedRecords=(\d+) batches=(\d+) maxBatchRecords=(\d+) maxHold=(\S+) holdExceeded=(\d+)", daemon_log)
    errors = []
    if daemon_exit != 0 or "pastured stopped cleanly" not in daemon_log or "signal=interrupt" not in daemon_log:
        errors.append("daemon did not complete the requested orderly shutdown")
    if len(matches) != 1:
        errors.append("missing or ambiguous lane shutdown counters")
        stats = {}
    else:
        records, batches, maximum, hold, exceeded = matches[0]
        stats = {"records_processed": int(records), "batches": int(batches), "max_batch_records": int(maximum),
                 "max_hold": hold, "hold_exceeded": int(exceeded)}
        if int(records) != offered or committed != offered or int(batches) < 1:
            errors.append("lane records and SQL occurrence delta do not both equal offered work; direct fallback or failed lane work cannot qualify")
    if any("this hook writes directly" in log for log in request_logs):
        errors.append("a hook reported direct fallback")
    return {"verified": not errors, "errors": errors, "stats": stats}


def audit_existing(work):
    manifest = json.loads((work/"manifest.json").read_text())
    result = {"date": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "command": [sys.executable, "tools/profile-writes.py", "--audit-existing", str(work)],
              "source_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "cases": []}
    (work/"audit-source.py").write_bytes(Path(__file__).read_bytes())
    for row in manifest["runs"]:
        if row["path"] != "lane-hook":
            continue
        case = work/row["case"]
        proof = lane_evidence((case/"daemon.log").read_text(), [p.read_text() for p in case.glob("request-*.log")],
                              row["offered"], row["committed"], row.get("daemon", {}).get("exit"))
        result["cases"].append({"case": row["case"], **proof})
    (work/"lane-audit.json").write_text(json.dumps(result, indent=2))
    if not result["cases"] or not all(c["verified"] for c in result["cases"]):
        raise RuntimeError("lane execution audit failed; inspect lane-audit.json rather than calling auto-mode writes lane measurements")


def campaign(args):
    # Exclusive mkdtemp under a fixed private scratch parent; no caller-supplied
    # reusable output path, symlink, hardlink or cleanup target.
    os.umask(0o077)
    work = Path(tempfile.mkdtemp(prefix="wp-", dir="/tmp/opencode"))
    print(work, flush=True)
    manifest = {"date": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "command": ["python3", "tools/profile-writes.py", *sys.argv[1:]],
                "revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                "diff_sha256": hashlib.sha256(subprocess.check_output(["git", "diff", "HEAD"], cwd=ROOT)).hexdigest(),
                "build": "CGO_ENABLED=0 go build (optimized, no race); identical binaries baseline/stages/full",
                "runtime": subprocess.check_output(["go", "version"], text=True).strip(),
                "arguments": vars(args), "runs": [], "cleanup": "incomplete"}
    (work / "source.diff").write_bytes(subprocess.check_output(["git", "diff", "HEAD"], cwd=ROOT))
    manifest["source_files"] = {}
    for source in sorted([*ROOT.rglob("*.go"), ROOT/"go.mod", ROOT/"go.sum", Path(__file__).resolve(), ROOT/"tools/render-write-profiles.py"]):
        relative = source.relative_to(ROOT)
        # Snapshot source, including the new opt-in package not yet tracked by
        # git diff, so dirty-tree measurements remain exactly reproducible.
        saved = work/"source"/relative
        saved.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, saved)
        manifest["source_files"][str(relative)] = hashlib.sha256(source.read_bytes()).hexdigest()
    (work / "bin").mkdir()
    bins = {}
    for name in ("pasture", "pastured"):
        binary = work / "bin" / name
        cmd = ["go", "build", "-o", str(binary), "./cmd/" + name]
        subprocess.run(cmd, cwd=ROOT, env={**os.environ, "CGO_ENABLED": "0"}, check=True)
        bins[name] = str(binary)
        manifest[name] = {"sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "command": cmd,
                          "build_info": subprocess.check_output(["go", "version", "-m", str(binary)], text=True)}
    fixture = json.loads((ROOT / "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json").read_text())
    fixture["cwd"] = str(work / "home")
    fixture["transcript_path"] = str(work / "absent.jsonl")
    fixture["scratchpad_dir"] = str(work / "tmp")
    payload = json.dumps(fixture).encode()
    hook = [bins["pasture"], "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261"]
    seeds = {}
    synthetic = work / "synthetic.db"
    env = environment(work / "seed", synthetic)
    for i in range(args.tasks):
        result = run([bins["pasture"], "--db", str(synthetic), "task", "create", "synthetic", "--namespace", "file://write-profile"], env, work, work / "seed.log")
        if result["exit"]:
            raise RuntimeError("synthetic setup failed; inspect seed.log")
    seeds["synthetic"] = synthetic
    if args.source:
        copied = work / "copied.db"
        backup(Path(args.source), copied)
        seeds["copy"] = copied
    # Warm contract/identity definitions identically ON THE SEEDS before any
    # forked measurement; cold activation is intentionally not measured.
    for shape, seed in seeds.items():
        env = environment(work / "seed", seed)
        warm = run([*hook, "--db", str(seed)], env, work, work / (shape + "-warm.log"), payload)
        if warm["exit"]:
            raise RuntimeError("seed hook failed")
        manifest.setdefault("corpora", {})[shape] = {"counts": counts(seed), "bytes": seed.stat().st_size,
            "sha256": hashlib.sha256(seed.read_bytes()).hexdigest(), "warm": warm}
    try:
        for shape, seed in seeds.items():
            for path in ("create", "direct-hook", "lane-hook"):
                for concurrency in (1, 18):
                    for mode in ("baseline", "stages", "full"):
                        case = work / f"{shape}-{path}-c{concurrency}-{mode}"
                        case.mkdir()
                        db = case / "store.db"
                        backup(seed, db)
                        env = environment(work / "env", db)
                        env["PASTURE_WRITE_LANE"] = "auto" if path == "lane-hook" else "direct"
                        profiles = case / "profiles"
                        profiles.mkdir()
                        daemon = None
                        daemon_log = None
                        try:
                            if path == "lane-hook":
                                config = case / "config.yaml"
                                config.write_text("audit_trail: sqlite\n")
                                denv = dict(env)
                                if mode != "baseline":
                                    denv.update(PASTURE_WRITE_PROFILE_DIR=str(profiles / "daemon"), PASTURE_WRITE_PROFILE_MODE=mode)
                                daemon_log = (case / "daemon.log").open("wb")
                                daemon = subprocess.Popen([bins["pastured"], "--db", str(db), "--config", str(config)],
                                    env=denv, cwd=work, stdout=daemon_log, stderr=daemon_log, start_new_session=True)
                                deadline = time.monotonic() + 90
                                while b"lifecycle write lane listening" not in (case / "daemon.log").read_bytes():
                                    if daemon.poll() is not None or time.monotonic() > deadline:
                                        raise RuntimeError("daemon failed to become ready; inspect daemon.log")
                                    time.sleep(.02)
                            before = counts(db)
                            def request(i):
                                child_env = dict(env)
                                if mode != "baseline":
                                    child_env.update(PASTURE_WRITE_PROFILE_DIR=str(profiles / str(i)), PASTURE_WRITE_PROFILE_MODE=mode)
                                cmd = ([bins["pasture"], "--db", str(db), "task", "create", "measured", "--namespace", "file://write-profile"]
                                       if path == "create" else [*hook, "--db", str(db)])
                                return run(cmd, child_env, work, case / f"request-{i}.log", None if path == "create" else payload)
                            started = time.perf_counter_ns()
                            with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
                                requests = list(pool.map(request, range(args.requests)))
                            wall = time.perf_counter_ns() - started
                        finally:
                            stop(daemon)
                            if daemon_log:
                                daemon_log.close()
                        after = counts(db)
                        committed = after["tasks" if path == "create" else "occurrences"] - before["tasks" if path == "create" else "occurrences"]
                        faults = sum(1 for p in case.glob("lifecycle-faults.jsonl*") for _ in p.open())
                        row = {"case": case.name, "shape": shape, "path": path, "concurrency": concurrency, "mode": mode,
                               "offered": args.requests, "committed": committed, "failed": args.requests - committed,
                               "process_errors": sum(r["exit"] != 0 for r in requests), "faults": faults,
                               "before": before, "after": after, "wall_ns": wall, "latency": distribution([r["ns"] for r in requests]),
                               "requests": requests, "stages": {}}
                        if daemon is not None:
                            row["daemon"] = {"command": daemon.args, "pid": daemon.pid, "exit": daemon.returncode}
                            row["lane_evidence"] = lane_evidence((case/"daemon.log").read_text(),
                                [p.read_text() for p in case.glob("request-*.log")], args.requests, committed, daemon.returncode)
                        stages = {}
                        for f in profiles.glob("*/stages.json"):
                            for event in json.loads(f.read_text())["events"] or []:
                                stages.setdefault(event["stage"], []).append(event["nanoseconds"])
                        row["stages"] = {k: distribution(v) for k, v in stages.items()}
                        manifest["runs"].append(row)
                        (work / "manifest.json").write_text(json.dumps(manifest, indent=2))
                        print(case.name, "offered/committed/failed", args.requests, committed, args.requests-committed, "p50_ms", round(row["latency"]["p50_ms"], 2), flush=True)
        sockets = list((work/"env"/"r").rglob("*.sock"))
        manifest["cleanup"] = {"process_groups_reaped": True, "remaining_sockets": [str(p) for p in sockets],
                               "disposable_stores": "retained privately"}
        if sockets:
            raise RuntimeError("daemon left sandbox sockets behind; inspect cleanup evidence")
    finally:
        (work / "manifest.json").write_text(json.dumps(manifest, indent=2))
    summary = [{k: v for k, v in row.items() if k not in ("requests",)} for row in manifest["runs"]]
    (work / "summary.json").write_text(json.dumps(summary, indent=2))
    # Render only AFTER measured work and process teardown: symbolization must
    # not compete with the baseline or the instrumented request windows.
    subprocess.run([sys.executable, str(ROOT/"tools/render-write-profiles.py"), str(work)], check=True, cwd=ROOT)
    audit_existing(work)
    if any(r["failed"] or r["process_errors"] or r["faults"] or not r.get("lane_evidence", {"verified": True})["verified"] for r in summary):
        raise RuntimeError("campaign accounting detected failures; retain evidence and do not compare as equal successful work")
    return work


if __name__ == "__main__":
    def terminate(signum, frame):
        raise KeyboardInterrupt("profiling interrupted; stopping isolated process groups")
    signal.signal(signal.SIGTERM, terminate)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", help="explicit source for consistent READ-ONLY SQLite backup (never opened by Pasture)")
    parser.add_argument("--audit-existing", help="audit lane routing on an existing private campaign; performs no writes to any DB")
    parser.add_argument("--tasks", type=int, default=60)
    parser.add_argument("--requests", type=int, default=36)
    args = parser.parse_args()
    if args.tasks < 1 or args.requests < 18:
        parser.error("tasks must be positive; requests must be >=18 for the fixed C=18 comparison")
    if args.audit_existing:
        audit_existing(Path(args.audit_existing).resolve())
    else:
        campaign(args)
