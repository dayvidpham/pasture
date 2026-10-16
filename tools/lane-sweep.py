#!/usr/bin/env python3
"""Bounded private lane experiment (stdlib only; never an installed CLI).

Run after integrating the experiment-tagged lane and SQL observers. SQL JSONL
contract: pid/op/method/phase/start_ns/end_ns/outcome; separate file per process.
See dependency docs/experiment-sql-timings.md. Lane JSONL
contract: one object per flush, reason, records, bytes,
max_queue_records, max_queue_bytes (nonnegative integers). Raw observer files are always
retained; incompatible/missing evidence prevents qualification, never guessed.
No percentile or optimum claims from this deliberately small pilot.
"""
import argparse
import concurrent.futures
import ctypes
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import random
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("profile_writes", ROOT / "tools/profile-writes.py")
PW = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PW)
SEED_HASHES = {
    "synthetic": "49b100e9d9f18b3bdc8f2714d03e17d0c55099412aa11d3374bac9cba54848ae",
    "copy": "c4c9731cc07a56388bfa26ad99bb1b45e43ae347baf69ceadd8c3e5f8f1e1d98",
}
LIMITS = {
    "incorrect_ack_or_loss": 0, "hook_completion_fraction": .99,
    "pending": "must not exceed configured record AND byte caps",
    "peer_errors": 0, "peer_p95_ms": "min(500, peer-only baseline p95 + 100)",
    "transaction_p95_ms": 400,
    "authority": "proposed design limits, NOT operator approval",
    "sparse_samples": "n<100: p99 null; p50/p95 descriptive order statistics with n; acceptance indeterminate when peer samples sparse",
}


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def distribution(values):
    values = sorted(values)
    result = {"n": len(values), "max_ms": values[-1] / 1e6 if values else None}
    for p in (50, 95, 99):
        result[f"p{p}_ms"] = values[math.ceil(len(values)*p/100)-1]/1e6 if values and (p != 99 or len(values) >= 100) else None
    return result


def plan(seed=20261016, finalists=None):
    rows = []
    for corpus in SEED_HASHES:
        rows.append(dict(corpus=corpus, load="peer-only", mode="OFF", batch_records=20, flush_ms=25))
        if finalists:
            for load in ("C18", "arrival"):
                for batch in finalists:
                    for repeat in range(2):
                        rows.append(dict(corpus=corpus,load=load,mode="ON",batch_records=batch,flush_ms=25,repeat=repeat))
            continue
        for load in ("C1", "C18", "arrival"):
            for records, flush in [(n, 25) for n in (1, 5, 10, 20, 40)] + [(20, n) for n in (5, 50, 100)]:
                rows.append(dict(corpus=corpus, load=load, mode="ON", batch_records=records, flush_ms=flush))
            # Current settings ON above pair with the same immutable seed OFF.
            rows.append(dict(corpus=corpus, load=load, mode="OFF", batch_records=20, flush_ms=25))
    random.Random(seed).shuffle(rows)
    for i, row in enumerate(rows):
        # Unix socket names include the runtime root: keep case paths short.
        row["case"] = f"{i:03d}"
        row["label"] = f"{row['corpus']}-{row['load']}-{row['mode']}-b{row['batch_records']}-f{row['flush_ms']}"
    return rows


def accounting(requests, peers, before, after, faults):
    """ACK means valid native response, not merely exit zero. SQL is independent."""
    errors = []
    ack = sum(r.get("valid_ack", False) and r.get("successful_response", False) for r in requests)
    delta = {k: after[k]-before[k] for k in ("tasks", "occurrences")}
    peer_ack = sum(r.get("valid_ack", False) and r.get("successful_response", False) for r in peers)
    if ack != len(requests):
        errors.append("invalid/missing hook ACK or client fault; inspect native stdout/stderr and fault records")
    if faults != sum(r.get("reported_fault", False) for r in requests):
        errors.append("missing/unmatched fault evidence; do not infer success from exit zero")
    if delta["occurrences"] != ack or delta["occurrences"] != len(requests):
        errors.append("SQL occurrences do not reconcile with offered hooks and valid ACKs")
    if delta["tasks"] != peer_ack or peer_ack != len(peers):
        errors.append("SQL tasks do not reconcile with offered peers and valid task ACKs")
    return dict(verified=not errors, errors=errors, offered_hooks=len(requests),
                acknowledged_hooks=ack, offered_peers=len(peers), acknowledged_peers=peer_ack,
                 committed_delta_tasks=delta["tasks"], committed_delta_occurrences=delta["occurrences"], faults=faults)


def arrival_evidence(requests, peers, load):
    return {"verified":all(r["start_lateness_ns"]<=100_000_000 for r in peers) and (load!="arrival" or all(r["start_lateness_ns"]<=20_000_000 for r in requests)),
        "definition":"independently scheduled offers, bounded by total request count; hook lateness <=20ms on arrival, peer lateness <=100ms; missed schedules invalidate comparison"}


def observer_evidence(case, budget, expected_batches=None, expected_peers=0):
    errors, reasons, lane, sql = [], {}, [], []
    daemon_log = case/"daemon.log"
    for log in [daemon_log,*case.glob("peer-*.log")]:
        if log.exists():
            for marker in ("EXPERIMENT_METRICS_ERROR","EXPERIMENT_SQL_TIMINGS_ERROR"):
                if marker in log.read_text(): errors.append(f"{marker}: observer output failure in {log.name}; invalidate despite native ACK")
    sql_paths = [case/"sql-daemon.jsonl", *[case/f"sql-peer-{i}.jsonl" for i in range(expected_peers)]]
    for filename, target in [("lane.jsonl", lane), *[(p.name, sql) for p in sql_paths]]:
        path = case/filename
        try:
            decoded = [json.loads(line) for line in path.read_text().splitlines()]
            if target is sql:
                decoded = [dict(event, source=filename) for event in decoded]
            target.extend(decoded)
            if not decoded:
                errors.append(f"empty {filename}")
        except (OSError, ValueError, TypeError) as exc:
            errors.append(f"cannot decode {filename}: {exc}")
    lane = [dict(flush_reason=r.get("reason"), batch_records=r.get("records"), batch_bytes=r.get("bytes"),
                 queue_records=r.get("max_queue_records"), queue_bytes=r.get("max_queue_bytes"),
                 full_condition=r.get("full_condition"), hold_target_ns=r.get("budget",{}).get("HoldTarget")) if isinstance(r,dict) else r for r in lane]
    for row in lane:
        keys = ("batch_records", "batch_bytes", "queue_records", "queue_bytes")
        if not isinstance(row, dict) or row.get("flush_reason") not in ("full","drained","timer","stopped") or any(type(row.get(k)) is not int or row[k] < 0 for k in keys):
            errors.append("lane observer schema mismatch; preserve raw rows and integrate observer contract")
            continue
        reason = row["flush_reason"]
        reasons[reason] = reasons.get(reason, 0)+1
        if "hold_target_ns" in budget and row.get("hold_target_ns") != budget["hold_target_ns"]:
            errors.append("observed HoldTarget differs from recorded configured budget")
        for key in ("batch_records", "batch_bytes", "queue_records", "queue_bytes"):
            if key in budget and row[key] > budget[key]:
                errors.append(f"observed {key} exceeds configured cap")
    durations, operations, phases = [], {}, {}
    for row in sql:
        if (not isinstance(row, dict) or any(type(row.get(k)) is not int for k in ("pid", "op", "start_ns", "end_ns"))
                or row["end_ns"] < row["start_ns"] or row.get("method") not in ("Apply", "ApplyBatchContext")
                or not isinstance(row.get("phase"), str) or not isinstance(row.get("outcome"), str)):
            errors.append("SQL observer schema mismatch; actual transaction timing required")
            continue
        operations.setdefault((row["source"], row["pid"], row["op"]), []).append(row)
        key = (row["method"], row["phase"], row["outcome"])
        phases.setdefault(key, []).append(row["end_ns"]-row["start_ns"])
        if row["source"] == "sql-daemon.jsonl" and row["method"] == "ApplyBatchContext" and row["phase"] == "transaction_scope":
            durations.append(row["end_ns"]-row["start_ns"])
    committed_batches = 0
    peer_commits = set()
    for key, events in operations.items():
        present = [e["phase"] for e in events]
        # The pilot requires successful calls. Missing closure is not zero time.
        required = ("sql_conn", "begin_wait", "transaction_scope", "body", "commit")
        if any(present.count(p) != 1 for p in required) or "begin_attempt" not in present:
            errors.append(f"missing/truncated SQL operation {key}; successful work requires complete timing closure")
            continue
        scope = next(e for e in events if e["phase"] == "transaction_scope")
        commit = next(e for e in events if e["phase"] == "commit")
        if scope["outcome"] != "committed" or commit["outcome"] != "ok":
            errors.append(f"noncommitted SQL timing operation {key}; consult raw outcome")
        elif key[0] == "sql-daemon.jsonl" and scope["method"] == "ApplyBatchContext":
            committed_batches += 1
        elif key[0].startswith("sql-peer-"):
            peer_commits.add(key[0])
    if expected_batches is not None and committed_batches != expected_batches:
        errors.append("successful lane batch count differs from complete committed ApplyBatchContext SQL operations")
    if len(peer_commits) != expected_peers:
        errors.append("missing committed SQL timings for successful peer processes")
    return {"verified": not errors, "errors": errors, "flush_reasons": reasons,
            "actual_transaction": distribution(durations),
            "transaction_definition": "ApplyBatchContext transaction_scope: BEGIN-return to COMMIT/rollback-return reservation proxy ONLY; not strict physical lock duration or fsync attribution",
            "sql_phases": [dict(method=m, phase=p, outcome=o, **distribution(v)) for (m, p, o), v in sorted(phases.items())],
            "sql_committed_batches": committed_batches,
            "full_condition_count": sum(r.get("full_condition") is True for r in lane if isinstance(r,dict)),
            "maxima": {k: max((r[k] for r in lane if isinstance(r, dict) and type(r.get(k)) is int), default=None)
                       for k in ("batch_records", "batch_bytes", "queue_records", "queue_bytes")},
            "raw": [str(case/"lane.jsonl"), *map(str, sql_paths)]}


class Watchdog:
    """Track every child group, including Go build descendants, under one clock."""
    def __init__(self, seconds, interrupt=False):
        # Adopt orphaned descendants so cleanup can reap, not merely signal,
        # a child group whose original build/wrapper leader has already exited.
        if sys.platform != "linux" or ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0) != 0:
            raise RuntimeError("bounded lane sweep requires Linux child-subreaper support; no experiment was started")
        self.deadline = time.monotonic()+seconds
        self.interrupt = interrupt
        self.lock = threading.Lock()
        self.children = []
        self.groups = set()
        self.expired = threading.Event()
        self.timer = threading.Timer(seconds, self.kill)
        self.timer.daemon = True
        self.timer.start()

    def kill(self, expired=True):
        with self.lock:
            if expired:
                self.expired.set()
            for group in list(self.groups):
                try: os.killpg(group,signal.SIGKILL)
                except ProcessLookupError: self.groups.discard(group)
        if expired and self.interrupt:
            # Interrupt Python-side backup/count/setup as well as subprocesses.
            # Finally blocks still reap groups and persist the partial manifest.
            os.kill(os.getpid(), signal.SIGINT)

    def spawn(self, *args, **kwargs):
        with self.lock:
            if self.expired.is_set() or time.monotonic() >= self.deadline:
                raise RuntimeError("lane-sweep hard budget exhausted before child launch; retain manifest and reduce grid")
            child = subprocess.Popen(*args, **kwargs)
            self.children.append(child)
            self.groups.add(child.pid)
            return child

    def close(self):
        self.timer.cancel()
        self.timer.join()
        self.kill(expired=False)
        for child in self.children:
            child.wait(timeout=10)
        deadline=time.monotonic()+5
        while self.groups:
            for group in list(self.groups):
                try:
                    while os.waitpid(-group,os.WNOHANG)[0] != 0: pass
                except ChildProcessError: pass
                try: os.killpg(group,0)
                except ProcessLookupError: self.groups.discard(group)
            if not self.groups: break
            if time.monotonic()>=deadline:
                raise RuntimeError(f"owned process groups remain after teardown: {sorted(self.groups)}; cleanup is NOT verified")
            time.sleep(.01)


def save(work, manifest):
    temp = work/"manifest.pending.json"
    temp.write_text(json.dumps(manifest, indent=2))
    temp.replace(work/"manifest.json")


def summarize(manifest):
    """Evaluate only preregistered limits with sufficient evidence, no ranking."""
    measured = [r for r in manifest["runs"] if r.get("status") == "measured"]
    baselines = {r["corpus"]: r for r in measured if r["load"] == "peer-only"}
    overhead = []
    for row in measured:
        if row["load"] == "peer-only":
            continue
        errors, unknown = [], []
        if not row["accounting"]["verified"] or not row["lane_evidence"]["verified"]:
            errors.append("ACK/SQL/routing reconciliation failed")
        obs = row.get("observers")
        if obs is None:
            unknown.append("OFF has no transaction/queue observers by design")
        else:
            if not obs["verified"]:
                errors.append("observer evidence failed")
            txn = obs["actual_transaction"]["p95_ms"]
            if txn is None:
                unknown.append("transaction sample n<100")
            elif txn > 400:
                errors.append("actual transaction p95 exceeds 400ms")
        baseline = baselines.get(row["corpus"])
        peer_p95 = row["peer_latency"]["p95_ms"]
        baseline_p95 = baseline["peer_latency"]["p95_ms"] if baseline else None
        if baseline is not None and not baseline["accounting"]["verified"]:
            errors.append("peer-only baseline accounting failed")
        if peer_p95 is None or baseline_p95 is None:
            unknown.append("peer or peer-only baseline sample n<100 (no p95 claim)")
        elif peer_p95 > min(500, baseline_p95+100):
            errors.append("peer p95 exceeds proposed design limit")
        if row["peer_latency"]["n"] < 100 or (baseline and baseline["peer_latency"]["n"] < 100):
            unknown.append("peer p95 is a sparse descriptive statistic, not a reliable budget guarantee")
        row["acceptance"] = dict(verdict="REJECT" if errors else "INDETERMINATE" if unknown else "PASS proposed design limits only",
                                 errors=errors, insufficient_evidence=unknown)
        if row["mode"] == "OFF":
            on = next((r for r in measured if r["mode"] == "ON" and r["corpus"] == row["corpus"] and r["load"] == row["load"] and r["batch_records"] == 20 and r["flush_ms"] == 25), None)
            if on:
                off_ns = sum(r["ns"] for r in row["requests"])
                on_ns = sum(r["ns"] for r in on["requests"])
                overhead.append(dict(corpus=row["corpus"], load=row["load"], off_case=row["case"], on_case=on["case"],
                                     off_client_sum_ns=off_ns, on_client_sum_ns=on_ns,
                                     client_sum_ratio=on_ns/off_ns if off_ns else None,
                                     off_wall_ns=row["wall_ns"], on_wall_ns=on["wall_ns"],
                                     qualified_equal_work=all(r["accounting"]["verified"] and r["lane_evidence"]["verified"] for r in (row, on)),
                                     interpretation="single paired pilot observation, no significance or percentile claim"))
    manifest["overhead_pairs"] = overhead
    manifest["untested_cases"] = [r["case"] for r in manifest["runs"] if r.get("status") != "measured"]


def snapshot(source, dest):
    files = {}
    # git-aware inventory excludes sibling worktrees and includes untracked Go.
    names = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard"], cwd=source, text=True).splitlines()
    for name in sorted(set(names)):
        p = source/name
        if p.is_file() and (p.suffix in (".go", ".py") or p.name in ("go.mod", "go.sum")):
            out = dest/name
            out.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(p, out)
            files[name] = sha(p)
    diff = subprocess.check_output(["git", "diff", "HEAD"], cwd=source)
    dest.mkdir(parents=True, exist_ok=True)
    (dest/"source.diff").write_bytes(diff)
    return dict(path=str(source), revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip(),
                diff_sha256=hashlib.sha256(diff).hexdigest(), files=files)


def native_run(cmd, env, case, label, payload=None):
    # profile-writes merges streams. Redirect only native stdout to a private
    # file in a tiny exec wrapper so an exit-zero fault cannot masquerade as ACK.
    out = case/f"{label}.stdout"
    wrapper = "import os,sys; f=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600); os.dup2(f,1); os.close(f); os.execv(sys.argv[2],sys.argv[2:])"
    result = PW.run([sys.executable, "-c", wrapper, str(out), *cmd], env, case, case/f"{label}.log", payload)
    result.update(stdout=str(out), stderr=str(case/f"{label}.log"))
    result["native_command"] = cmd
    text = out.read_text() if out.exists() else ""
    if payload is not None:  # Claude SessionStart proceed is exactly empty.
        result["valid_ack"] = text == ""
    else:
        try:
            response = json.loads(text)
            result["valid_ack"] = isinstance(response.get("id"), str) and bool(response["id"])
        except (ValueError, AttributeError):
            result["valid_ack"] = False
    return result


def run_case(work, row, seed, bins, payload, args, guard):
    case = work/row["case"]
    case.mkdir(mode=0o700)
    db = case/"store.db"
    PW.backup(seed, db)
    env = PW.environment(case, db)
    env.update(PASTURE_WRITE_LANE="auto", PASTURE_EXPERIMENT_SANDBOX=str(case))
    budget = {k: row[k] for k in ("batch_records", "flush_ms")}
    budget.update({k: getattr(args, k) for k in ("batch_bytes", "queue_records", "queue_bytes") if getattr(args, k) is not None})
    denv = dict(env, PASTURE_EXPERIMENT_LANE_BUDGET=json.dumps(budget))
    # Fixed by the unchanged production timeout profile, never an override.
    budget["hold_target_ns"]=400_000_000
    if row["mode"] == "ON":
        # Exclusive case directory, fixed output names, never a reused observer
        # target. The daemon also validates aliases and opens no-follow.
        for name in ("lane.jsonl", "sql-daemon.jsonl", *[f"sql-peer-{i}.jsonl" for i in range(6)]):
            if os.path.lexists(case/name):
                raise RuntimeError(f"observer output {case/name} already exists; use a fresh exclusive case, never truncate retained evidence")
        denv.update(PASTURE_EXPERIMENT_LANE_METRICS=str(case/"lane.jsonl"), PASTURE_EXPERIMENT_SQL_TIMINGS=str(case/"sql-daemon.jsonl"))
        if args.stages:
            denv.update(PASTURE_WRITE_PROFILE_DIR=str(case/"profiles/daemon"), PASTURE_WRITE_PROFILE_MODE="stages")
    (case/"config.yaml").write_text("audit_trail: sqlite\n")
    requests, peers, daemon = [], [], None
    row.update(budget=budget, sandbox=str(case), daemon_environment=denv, client_environment=env,
               raw_paths=dict(db=str(db), daemon=str(case/"daemon.log"), faults=str(case/"lifecycle-faults.jsonl")), status="starting")
    save(work, args.manifest)
    with (case/"daemon.log").open("wb") as log:
        try:
            daemon = guard.spawn([bins["pastured"], "--db", str(db), "--config", str(case/"config.yaml")],
                                 env=denv, cwd=case, stdout=log, stderr=log, start_new_session=True)
            ready_until = min(guard.deadline, time.monotonic()+90)
            while b"lifecycle write lane listening" not in (case/"daemon.log").read_bytes():
                if daemon.poll() is not None or time.monotonic() >= ready_until:
                    raise RuntimeError(f"daemon readiness failed in {case}; inspect daemon.log and experiment integration")
                time.sleep(.02)
            before = PW.counts(db)
            start = time.perf_counter_ns()

            def invoke(i, peer=False):
                spacing = 100_000_000 if peer else (20_000_000 if row["load"] == "arrival" else 0)
                scheduled = start+i*spacing
                delay = (scheduled-time.perf_counter_ns())/1e9
                if delay > 0:
                    time.sleep(delay)
                actual = time.perf_counter_ns()
                cmd = ([bins["pasture"], "--db", str(db), "task", "create", f"peer-{i}", "--namespace", "file://lane-sweep", "--format", "json"] if peer else
                       [bins["pasture"], "--db", str(db), "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261"])
                child_env = dict(env)
                if peer and row["mode"] == "ON":
                    child_env["PASTURE_EXPERIMENT_SQL_TIMINGS"] = str(case/f"sql-peer-{i}.jsonl")
                if args.stages and row["mode"] == "ON" and not peer:
                    child_env.update(PASTURE_WRITE_PROFILE_DIR=str(case/f"profiles/{i}"), PASTURE_WRITE_PROFILE_MODE="stages")
                result = native_run(cmd, child_env, case, f"peer-{i}" if peer else f"request-{i}", None if peer else payload)
                result.update(scheduled_ns=scheduled-start, actual_start_ns=actual-start, start_lateness_ns=actual-scheduled)
                return result

            with concurrent.futures.ThreadPoolExecutor(max_workers=6) as peer_pool:
                peer_futures = [peer_pool.submit(invoke, i, True) for i in range(6)]
                if row["load"] != "peer-only":
                    with concurrent.futures.ThreadPoolExecutor(max_workers=1 if row["load"] == "C1" else args.requests if row["load"] == "arrival" else 18) as pool:
                        requests = list(pool.map(invoke, range(args.requests)))
                peers = [f.result() for f in peer_futures]
            row["wall_ns"] = time.perf_counter_ns()-start
        finally:
            row["daemon_teardown"] = PW.stop(daemon)
    after = PW.counts(db)
    faults = sum(len(p.read_text().splitlines()) for p in case.glob("lifecycle-faults.jsonl*"))
    row.update(before=before, after=after, requests=requests, peers=peers,
               accounting=accounting(requests, peers, before, after, faults),
                hook_latency=distribution([r["ns"] for r in requests]), peer_latency=distribution([r["ns"] for r in peers]),
                successful_hook_latency=distribution([r["ns"] for r in requests if r["successful_response"] and r["valid_ack"]]),
                successful_peer_latency=distribution([r["ns"] for r in peers if r["successful_response"] and r["valid_ack"]]),
                peer_failure_classes={name:sum(r.get("failure_class")==name for r in peers) for name in {r.get("failure_class") for r in peers if r.get("failure_class")}},
                deadline_limited=sum(r["deadline_limited"] for r in requests+peers), censored=sum(r["censored"] for r in requests+peers),
                failure_classes=PW.classify_case(case, requests))
    row["arrival_contract"]=arrival_evidence(requests,peers,row["load"])
    if not row["arrival_contract"]["verified"]:
        row["accounting"]["verified"]=False;row["accounting"]["errors"].append("controlled offer schedule missed its preregistered tolerance")
    for log in [case/"daemon.log",*case.glob("peer-*.log")]:
        for marker in ("EXPERIMENT_METRICS_ERROR","EXPERIMENT_SQL_TIMINGS_ERROR"):
            if marker in log.read_text():
                row["accounting"]["verified"] = False
                row["accounting"]["errors"].append(f"{log.name} reported {marker}; measurements invalid even when ACK and SQL reconcile")
    if requests:
        row["lane_evidence"] = PW.lane_evidence((case/"daemon.log").read_text(),
            [(case/f"request-{i}.log").read_text() for i in range(len(requests))], len(requests), after["occurrences"]-before["occurrences"], daemon.returncode)
    if row["mode"] == "ON":
        batches = row.get("lane_evidence", {}).get("stats", {}).get("batches")
        row["observers"] = observer_evidence(case, budget, expected_batches=batches, expected_peers=6)
        # Observer batches count processed records (including failed ones), so
        # this is a second reconciliation, never a replacement for SQL/ACKs.
        if row["observers"]["verified"]:
            batches = [json.loads(line) for line in (case/"lane.jsonl").read_text().splitlines()]
            if sum(batch["records"] for batch in batches) != len(requests):
                row["observers"]["verified"] = False
                row["observers"]["errors"].append("observer batch records do not equal offered hooks")
    row["status"] = "measured"
    row["stages_raw"] = [str(p) for p in case.glob("profiles/*/stages.json")]
    row["acceptance"] = "indeterminate: pilot sparse samples, peer baseline and configured queue caps require evaluation"
    if not row["accounting"]["verified"] or not row.get("lane_evidence", {"verified": True})["verified"] or not row.get("observers", {"verified": True})["verified"]:
        row["acceptance"] = "REJECT: accounting/routing/observer evidence failed"
    save(work, args.manifest)
    print(row["case"], row["label"], "hook commits", row["accounting"]["committed_delta_occurrences"], "/", len(requests),
          "peer commits", row["accounting"]["committed_delta_tasks"], "/", len(peers),
          "observers", row.get("observers",{}).get("verified"), flush=True)


def campaign(args):
    args.requests=args.requests or (108 if args.finalist_batches else 36)
    os.umask(0o077)
    work = Path(tempfile.mkdtemp(prefix="lane-sweep-", dir="/tmp/opencode"))
    print(work, flush=True)
    guard = Watchdog(args.budget_seconds, interrupt=True)
    # Reuse profile-writes' run/stop and its 90s per-subprocess watchdog, while
    # additionally tracking all children against the campaign's hard ceiling.
    PW.subprocess = SimpleNamespace(Popen=guard.spawn, TimeoutExpired=subprocess.TimeoutExpired)
    manifest = dict(schema="lane-sweep.v1", command=sys.argv, limits=LIMITS, runs=plan(args.order_seed,args.finalist_batches),
                    order_seed=args.order_seed, budget_seconds=args.budget_seconds, max_cases=args.max_cases,
                    requests=args.requests, python=sys.version, status="initializing", cleanup="pending",
                    untested="larger corpora, higher concurrency, repetitions and byte/queue grids deferred: bounded small/current pilot; no optimum or p99 inference")
    args.manifest = manifest
    save(work, manifest)
    try:
        if len(manifest["runs"]) > args.max_cases:
            raise RuntimeError("planned cases exceed --max-cases before build; increase explicit cap (at most 80)")
        prior = args.seed_campaign.resolve()
        original = json.loads((ROOT/"internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_261.json").read_text())
        payload = PW.retained_payload(prior, original)
        (work/"fixture.json").write_bytes(payload)
        manifest["payload_sha256"] = hashlib.sha256(payload).hexdigest()
        seeds = {}
        manifest["corpora"] = {}
        for corpus, expected in SEED_HASHES.items():
            source = prior/("synthetic.db" if corpus == "synthetic" else "copied.db")
            if sha(source) != expected:
                raise RuntimeError(f"seed {source} changed; recover the exact retained corpus, do not substitute work")
            seeds[corpus] = source
            manifest["corpora"][corpus] = dict(path=str(source), sha256=expected, counts=PW.counts(source))
        manifest["source"] = snapshot(ROOT, work/"source")
        dependency = args.dependency_worktree.resolve(strict=True)
        manifest["dependency"] = snapshot(dependency, work/"source-dependency")
        workspace = work/"go.work"
        workspace.write_text(f'go 1.26.0\nuse (\n{json.dumps(str(ROOT))}\n{json.dumps(str(dependency))}\n)\n')
        env = {**os.environ, "CGO_ENABLED": "0", "GOWORK": str(workspace)}
        manifest["build_environment"] = {k: env[k] for k in ("CGO_ENABLED", "GOWORK")}
        bins = {}
        (work/"bin").mkdir()
        for name in ("pasture", "pastured"):
            bins[name] = str(work/"bin"/name)
            cmd = ["go", "build", "-tags=pasture_experiment", "-o", bins[name], "./cmd/"+name]
            result = PW.run(cmd, env, ROOT, work/f"build-{name}.log")
            if not result["successful_response"]:
                raise RuntimeError(f"experiment build failed; inspect {work}/build-{name}.log and integrate experiment-tagged dependencies")
            info = PW.run(["go", "version", "-m", bins[name]], env, ROOT, work/f"build-info-{name}.log")
            manifest[name] = dict(command=cmd, sha256=sha(Path(bins[name])), build_info=str(work/f"build-info-{name}.log"), result=info)
        manifest["status"] = "running"
        for row in manifest["runs"]:
            run_case(work, row, seeds[row["corpus"]], bins, payload, args, guard)
        manifest["status"] = "complete: pilot only, not an optimum or operator approval"
    except BaseException as exc:
        manifest["status"] = "aborted"
        manifest["error"] = repr(exc)
        raise
    finally:
        try:
            guard.close()
            manifest["cleanup"] = dict(children_reaped=not guard.groups, hard_budget_expired=guard.expired.is_set(),
                                       remaining_sockets=[str(p) for p in work.rglob("*.sock")], retained=str(work))
        finally:
            summarize(manifest)
            save(work, manifest)
    if any(isinstance(r.get("acceptance"), dict) and r["acceptance"]["verdict"] == "REJECT" for r in manifest["runs"]):
        raise RuntimeError(f"lane-sweep rejected accounting or proposed limits; inspect {work}/manifest.json, never compare this as equal successful work")
    return work


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--dependency-worktree", required=True, type=Path)
    p.add_argument("--seed-campaign", type=Path, default=Path("/tmp/opencode/wp-v1uzuv8i"))
    p.add_argument("--budget-seconds", type=float, default=900)
    p.add_argument("--max-cases", type=int, default=80)
    p.add_argument("--requests", type=int, default=None)
    p.add_argument("--order-seed", type=int, default=20261016)
    p.add_argument("--stages", action="store_true", help="optional corrected queue timing, ON cases only; never render during measurement")
    p.add_argument("--finalist-batches", type=lambda text:[int(x) for x in text.split(",")], help="two batch-count finalists at 25ms, repeated twice on C18/arrival plus peer baselines; separate bounded campaign")
    # Explicit current production caps, fixed for this experiment; no byte or
    # queue grid is implied. Record these in every daemon's applied JSON budget.
    for key, default in (("batch-bytes", 8 << 20), ("queue-records", 256), ("queue-bytes", 32 << 20)):
        p.add_argument("--"+key, type=int, default=default)
    return p


if __name__ == "__main__":
    p = parser()
    args = p.parse_args()
    args.requests=args.requests or (108 if args.finalist_batches else 36)
    if not 0 < args.budget_seconds <= 900 or not 1 <= args.max_cases <= 80 or args.requests != (108 if args.finalist_batches else 36):
        p.error("each phase budget must be (0,900], max-cases 1..80, offers exactly36pilot/108finalists; at most one pilot plus one finalist phase")
    if any(getattr(args, k) is not None and getattr(args, k) < 1 for k in ("batch_bytes", "queue_records", "queue_bytes")):
        p.error("optional record/byte caps must be positive")
    if args.finalist_batches and (len(args.finalist_batches)!=2 or len(set(args.finalist_batches))!=2 or any(x not in (1,5,10,20,40) for x in args.finalist_batches)):
        p.error("finalists must name two distinct pilot batch counts")
    def interrupted(signum, frame):
        raise KeyboardInterrupt("lane sweep interrupted; retaining evidence and tearing down children")
    signal.signal(signal.SIGTERM, interrupted)
    campaign(args)
