#!/usr/bin/env python3
"""Private, fixed-work production-write campaign; run inside nix develop.

No default source: --source is an explicit read-only SQLite backup opt-in.
Each path/concurrency/mode restores the SAME immutable seed. No speedup claim
is made. This trusted developer harness is not an OS security sandbox.
"""
import argparse
import concurrent.futures
from contextlib import closing
import hashlib
import json
import os
import re
import random
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
    with closing(sqlite3.connect(source.resolve().as_uri() + "?mode=ro", uri=True)) as src:
        with closing(sqlite3.connect(dest)) as dst:
            src.backup(dst)
            assert dst.execute("pragma integrity_check").fetchone() == ("ok",)


def counts(db):
    with closing(sqlite3.connect(db.resolve().as_uri() + "?mode=ro", uri=True)) as conn:
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
        return {"signals": [], "status": None}
    signals = []
    if process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGINT)
            signals.append(int(signal.SIGINT))
        except ProcessLookupError:
            pass
    try:
        process.wait(timeout=80)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        signals.append(int(signal.SIGKILL))
        process.wait(timeout=10)
    return {"signals": signals, "status": process.returncode}


def run(cmd, env, cwd, output, payload=None):
    started = time.perf_counter_ns()
    fired = threading.Event()
    termination = {"signals": [], "status": None}
    with output.open("wb") as log:
        process = subprocess.Popen(cmd, env=env, cwd=cwd, stdin=subprocess.PIPE,
                                   stdout=log, stderr=log, start_new_session=True)
        # communicate(timeout=...) uses waitpid polling on Linux and quantizes
        # short requests by tens of milliseconds. A separate watchdog preserves
        # blocking waitpid timing while retaining bounded process cleanup.
        def expire():
            fired.set()
            termination.update(stop(process))
        watchdog = threading.Timer(90, expire)
        watchdog.daemon = True
        watchdog.start()
        try:
            process.communicate(payload)
            elapsed = time.perf_counter_ns() - started
        finally:
            watchdog.cancel()
            watchdog.join()
            if process.poll() is None:
                termination.update(stop(process))
    text = output.read_text()
    fault = "pasture could not evaluate this lifecycle hook event" in text
    category = failure_class(text) if fault or process.returncode != 0 else None
    deadline = fired.is_set() or category in ("append_deadline_before_acquire", "other_deadline")
    success = process.returncode == 0 and not fault and not fired.is_set()
    return {"ns": elapsed, "exit": process.returncode,
            "watchdog_fired": fired.is_set(), "censored": fired.is_set(), "deadline_limited": deadline,
            "termination": termination, "termination_signal": -process.returncode if process.returncode < 0 else None,
            "process_completed": process.returncode is not None, "successful_response": success, "reported_fault": fault,
            "failure_class": category,
            "work_completion": "acknowledged; reconciled by case SQL delta" if success else "not acknowledged; consult case SQL delta",
            "command": cmd, "profile": env.get("PASTURE_WRITE_PROFILE_DIR")}


def distribution(values):
    values = sorted(values)
    if not values:
        return {"n": 0}
    return {"n": len(values), "min_ms": values[0] / 1e6,
            "p50_ms": values[len(values)//2] / 1e6,
            "p95_ms": values[min(len(values)-1, len(values)*95//100)] / 1e6,
            "sum_ms": sum(values) / 1e6}


def failure_class(cause):
    """Classify observed causes, not weak exit-zero hook responses or latency."""
    if "database is locked" in cause or "SQLITE_BUSY" in cause:
        if "lifecycle payload transaction could not start" in cause:
            return "payload_begin_sqlite_busy"
        if "verify its saved genesis authority" in cause:
            return "genesis_replay_sqlite_busy"
        if "Apply: acquire, fold, and commit" in cause:
            return "operation_apply_sqlite_busy"
        return "sqlite_busy_other"
    if "SQLite exhausted its configured writer wait while ingress was still live" in cause:
        return "append_sqlite_busy_ingress_live"
    if "occurrence commit could not acquire the shared database writer before its deadline" in cause:
        return "append_deadline_before_acquire"
    if "context deadline exceeded" in cause or "deadline" in cause:
        return "other_deadline"
    if "operation identity conflict" in cause or "operation-identity" in cause:
        return "operation_conflict"
    if "authority does not govern" in cause or "genesis authority discipline violated" in cause:
        return "authority_refusal"
    return "unclassified"


def classify_case(case, requests):
    classes = {name: 0 for name in ("payload_begin_sqlite_busy", "genesis_replay_sqlite_busy", "operation_apply_sqlite_busy", "sqlite_busy_other",
        "append_sqlite_busy_ingress_live", "append_deadline_before_acquire", "other_deadline", "operation_conflict", "authority_refusal", "unclassified")}
    fault_files = list(case.glob("lifecycle-faults.jsonl*"))
    for f in fault_files:
        for line in f.read_text().splitlines():
            classes[failure_class(json.loads(line)["cause"])] += 1
    # Native hooks may fail open, so their fault record is authoritative. Task
    # creates instead return a nonzero process exit and an actionable cause.
    if not fault_files:
        for i, result in enumerate(requests):
            if result["exit"] != 0:
                classes[failure_class((case/f"request-{i}.log").read_text())] += 1
    return classes


def retained_payload(prior, original_fixture):
    """Recover exact warmed bytes, proving membership in EVERY retained seed.

    Older manifests predate fixture.json. Reconstruct their documented path
    substitution, but accept it only if those exact bytes are present in each
    seed's content-addressed blob store. No best-effort/generated fallback.
    """
    manifest = json.loads((prior/"manifest.json").read_text())
    if (prior/"fixture.json").exists():
        payload = (prior/"fixture.json").read_bytes()
        if manifest.get("payload_sha256") and hashlib.sha256(payload).hexdigest() != manifest["payload_sha256"]:
            raise RuntimeError("retained fixture hash differs; refusing changed payload work")
    else:
        fixture = dict(original_fixture)
        fixture.update(cwd=str(prior/"home"), transcript_path=str(prior/"absent.jsonl"), scratchpad_dir=str(prior/"tmp"))
        payload = json.dumps(fixture).encode()
    digest = "sha256:" + hashlib.sha256(payload).hexdigest()
    for shape in manifest["corpora"]:
        source = prior/("copied.db" if shape == "copy" else "synthetic.db")
        with closing(sqlite3.connect(source.resolve().as_uri()+"?mode=ro", uri=True)) as db:
            row = db.execute("SELECT body FROM lifecycle_payload_blobs WHERE digest=?", (digest,)).fetchone()
        if row is None or bytes(row[0]) != payload:
            raise RuntimeError("no exact warmed payload evidence in retained seed; refusing an insert-vs-update comparison")
    return payload


def classify_existing(work):
    manifest = json.loads((work/"manifest.json").read_text())
    result = {"date": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "command": [sys.executable, "tools/profile-writes.py", "--classify-existing", str(work)],
              "source_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "cases": []}
    for row in manifest["runs"]:
        result["cases"].append({"case": row["case"], "offered": row["offered"], "committed": row["committed"],
            "failed": row["failed"], "failure_classes": classify_case(work/row["case"], row["requests"])})
    (work/"failure-classes.json").write_text(json.dumps(result, indent=2))


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
                "python": sys.version,
                "stage_definitions": {"lane.queue.accumulation.wait": "admission to batch start; excludes same-batch predecessor preparation",
                    "lane.within_batch.prepare_wait": "batch start to own Prepare; excludes queue residence",
                    "lane.admission.to_own_prepare": "inclusive admission to own Prepare; overlaps both component waits",
                    "lane.turn.prepare.apply.settle": "turn elapsed; NOT SQLite lock-held duration"},
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
    build_env = {**os.environ, "CGO_ENABLED": "0", "GOWORK": "off"}
    if args.dependency_worktree:
        dependency = Path(args.dependency_worktree).resolve()
        workspace = work/"go.work"
        workspace.write_text(f'go 1.26.0\nuse (\n"{ROOT}"\n"{dependency}"\n)\n')
        build_env["GOWORK"] = str(workspace)
        manifest["dependency"] = {"path": str(dependency), "revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=dependency, text=True).strip(),
            "diff_sha256": hashlib.sha256(subprocess.check_output(["git", "diff", "HEAD"], cwd=dependency)).hexdigest(), "source_files": {}}
        for source in sorted([*dependency.rglob("*.go"), dependency/"go.mod", dependency/"go.sum"]):
            relative = source.relative_to(dependency)
            saved = work/"source-dependency"/relative
            saved.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, saved)
            manifest["dependency"]["source_files"][str(relative)] = hashlib.sha256(source.read_bytes()).hexdigest()
    manifest["build_environment"] = {k: build_env[k] for k in ("CGO_ENABLED", "GOWORK")}
    bins = {}
    for name in ("pasture", "pastured"):
        binary = work / "bin" / name
        cmd = ["go", "build", "-o", str(binary), "./cmd/" + name]
        if args.binaries_from:
            prior = Path(args.binaries_from).resolve()
            recorded = json.loads((prior/"manifest.json").read_text())[name]["sha256"]
            if hashlib.sha256((prior/"bin"/name).read_bytes()).hexdigest() != recorded:
                raise RuntimeError("baseline binary hash differs from its retained manifest")
            shutil.copyfile(prior/"bin"/name, binary)
            binary.chmod(0o700)
            cmd = ["verified-copy", str(prior/"bin"/name), str(binary)]
        else:
            subprocess.run(cmd, cwd=ROOT, env=build_env, check=True)
        bins[name] = str(binary)
        manifest[name] = {"sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "command": cmd,
                          "build_info": subprocess.check_output(["go", "version", "-m", str(binary)], text=True)}
    fixture = json.loads((ROOT / "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json").read_text())
    if args.seed_campaign:
        payload = retained_payload(Path(args.seed_campaign).resolve(), fixture)
    else:
        fixture["cwd"] = str(work / "home")
        fixture["transcript_path"] = str(work / "absent.jsonl")
        fixture["scratchpad_dir"] = str(work / "tmp")
        payload = json.dumps(fixture).encode()
    (work/"fixture.json").write_bytes(payload)
    manifest["payload_bytes"] = len(payload)
    manifest["payload_sha256"] = hashlib.sha256(payload).hexdigest()
    hook = [bins["pasture"], "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261"]
    seeds = {}
    synthetic = work / "synthetic.db"
    env = environment(work / "seed", synthetic)
    for i in range(0 if args.seed_campaign else args.tasks):
        result = run([bins["pasture"], "--db", str(synthetic), "task", "create", "synthetic", "--namespace", "file://write-profile"], env, work, work / "seed.log")
        if result["exit"]:
            raise RuntimeError("synthetic setup failed; inspect seed.log")
    if args.seed_campaign:
        prior = Path(args.seed_campaign).resolve()
        prior_manifest = json.loads((prior/"manifest.json").read_text())
        for shape, info in prior_manifest["corpora"].items():
            source = prior/("copied.db" if shape == "copy" else "synthetic.db")
            if hashlib.sha256(source.read_bytes()).hexdigest() != info["sha256"]:
                raise RuntimeError("retained seed differs from the originally measured corpus; refusing comparison")
            dest = work/source.name
            backup(source, dest)
            seeds[shape] = dest
    else:
        seeds["synthetic"] = synthetic
    if args.source and not args.seed_campaign:
        copied = work / "copied.db"
        backup(Path(args.source), copied)
        seeds["copy"] = copied
    # Warm contract/identity definitions identically ON THE SEEDS before any
    # forked measurement; cold activation is intentionally not measured.
    for shape, seed in seeds.items():
        env = environment(work / "seed", seed)
        warm = ({"reused_warmed_seed": True, "exit": 0} if args.seed_campaign else
                run([*hook, "--db", str(seed)], env, work, work / (shape + "-warm.log"), payload))
        if warm["exit"]:
            raise RuntimeError("seed hook failed")
        manifest.setdefault("corpora", {})[shape] = {"counts": counts(seed), "bytes": seed.stat().st_size,
            "sha256": hashlib.sha256(seed.read_bytes()).hexdigest(), "warm": warm}
    try:
        for shape, seed in seeds.items():
            for path in ("create", "direct-hook", "lane-hook"):
                for concurrency in (1, 18):
                    schedule = [(repeat, mode) for repeat in range(args.repetitions) for mode in ("baseline", "stages", "full")]
                    random.Random(f"{args.order_seed}:{shape}:{path}:{concurrency}").shuffle(schedule)
                    for order_index, (repeat, mode) in enumerate(schedule):
                        case = work / f"{shape}-{path}-c{concurrency}-{mode}-r{repeat}"
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
                               "repeat": repeat, "order_index": order_index, "order_seed": args.order_seed,
                               "offered": args.requests, "committed": committed, "failed": args.requests - committed,
                               "process_errors": sum(r["exit"] != 0 for r in requests), "faults": faults,
                               "before": before, "after": after, "wall_ns": wall, "latency": distribution([r["ns"] for r in requests]),
                               "requests": requests, "stages": {}}
                        row["latency_definition"] = "all offered processes, including failures and censored observations"
                        row["latency_cohorts"] = {name: distribution([r["ns"] for r in requests if predicate(r)]) for name, predicate in (
                            ("successful_response", lambda r: r["successful_response"]),
                            ("failed_response", lambda r: not r["successful_response"]),
                            ("deadline_limited", lambda r: r["deadline_limited"]),
                            ("watchdog_censored", lambda r: r["censored"]))}
                        row["failure_classes"] = classify_case(case, requests)
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
    parser.add_argument("--seed-campaign", help="reuse a retained campaign's exact already-warmed seed corpora; hash-checked, no setup writes")
    parser.add_argument("--binaries-from", help="use hash-verified retained binaries for the before run rather than rebuilding")
    parser.add_argument("--dependency-worktree", help="explicit unpublished Provenance worktree for private comparison; only a sandbox go.work is created")
    parser.add_argument("--audit-existing", help="audit lane routing on an existing private campaign; performs no writes to any DB")
    parser.add_argument("--classify-existing", help="classify retained failure causes without rerunning workload")
    parser.add_argument("--tasks", type=int, default=60)
    parser.add_argument("--requests", type=int, default=36)
    parser.add_argument("--repetitions", type=int, default=2, help="matched-corpus repetitions per mode; deterministic randomized order")
    parser.add_argument("--order-seed", type=int, default=20261016)
    args = parser.parse_args()
    if args.source and args.seed_campaign:
        parser.error("--source and --seed-campaign are mutually exclusive")
    if args.binaries_from and args.dependency_worktree:
        parser.error("--binaries-from and --dependency-worktree are mutually exclusive")
    if args.tasks < 1 or args.requests < 18 or args.repetitions < 1:
        parser.error("tasks must be positive; requests must be >=18 for the fixed C=18 comparison")
    if args.classify_existing:
        classify_existing(Path(args.classify_existing).resolve())
    elif args.audit_existing:
        audit_existing(Path(args.audit_existing).resolve())
    else:
        campaign(args)
