#!/usr/bin/env python3
"""Symbolize private campaign profiles and render dependency-free SVG flames.

Run under nix develop: python3 tools/render-write-profiles.py /tmp/opencode/wp-...
Widths are sampled CPU/bytes/objects/delay, NEVER elapsed time or call counts.
"""
import hashlib
import html
import json
from pathlib import Path
import re
import subprocess
import sys
import time


def decode_raw(raw, metric):
    locations, samples = {}, []
    section, current, index = "", None, None
    for line in raw.splitlines():
        if line == "Samples:":
            section = "samples"
            continue
        if line == "Locations":
            section = "locations"
            continue
        if line == "Mappings":
            section = "mappings"
        if section == "samples":
            if "/" in line and ":" not in line:
                fields = line.split()
                index = next((i for i, v in enumerate(fields) if v.split("/")[0] == metric), None)
            match = re.match(r"\s*([\d\s-]+):\s*([\d\s]*)$", line)
            if match and index is not None:
                values = list(map(int, match[1].split()))
                samples.append((values[index], list(map(int, match[2].split()))))
        if section == "locations":
            match = re.match(r"\s*(\d+):\s+0x[\da-f]+(?:\s+M=\d+)?\s+(.*)", line)
            if match:
                current = int(match[1])
                locations[current] = [match[2].split()[0]] if match[2].strip() else ["unknown"]
            elif current is not None and line.startswith("             ") and line.strip():
                locations[current].append(line.split()[0])
    stacks = []
    for weight, ids in samples:
        frames = [frame for ident in ids for frame in locations.get(ident, ["unknown"])]
        stacks.append((weight, list(reversed(frames)) or ["[unknown]"]))
    return stacks


def flame(stacks, metric):
    tree = {"weight": 0, "children": {}}
    for weight, frames in stacks:
        if weight <= 0:
            continue
        tree["weight"] += weight
        node = tree
        for frame in frames:
            node = node["children"].setdefault(frame, {"weight": 0, "children": {}})
            node["weight"] += weight
    total = tree["weight"]
    boxes, depth = [], 0
    def walk(node, x, width, level):
        nonlocal depth
        depth = max(depth, level)
        offset = x
        for name, child in sorted(node["children"].items()):
            w = width * child["weight"] / node["weight"] if node["weight"] else 0
            digest = hashlib.sha256(name.encode()).digest()
            color = f"rgb({200+digest[0]%55},{90+digest[1]%100},{50+digest[2]%80})"
            title = html.escape(f"{name}: {child['weight']} {metric}; {100*child['weight']/total:.2f}% of total")
            label = html.escape(name[:max(0, int(w/7)-1)])
            boxes.append(f'<g><title>{title}</title><rect x="{offset:.3f}" y="{40+level*18}" width="{w:.3f}" height="17" fill="{color}"/><text x="{offset+2:.3f}" y="{53+level*18}" font-size="11">{label}</text></g>')
            walk(child, offset, w, level+1)
            offset += w
    walk(tree, 0, 1200, 0)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="{80+depth*18}"><rect width="100%" height="100%" fill="white"/>'
            f'<text x="5" y="20">Stack flame (root at top): {html.escape(metric)}; total {total}; width is NOT wall time or call count</text>' + "".join(boxes) + '</svg>')


def cpu_metadata(raw, stacks):
    match = re.search(r"^Period:\s+(\d+)\s*$", raw, re.MULTILINE)
    period = int(match[1]) if match else 0
    return (period, sum(w for w, _ in stacks)//period) if period > 0 else (None, None)


def render(work, from_raw=False):
    source = Path(__file__).read_bytes()
    (work/"render-source.py").write_bytes(source)
    (work/"render-provenance.json").write_text(json.dumps({"date": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "source_sha256": hashlib.sha256(source).hexdigest(), "command": [sys.executable, str(Path(__file__).resolve()), str(work), *(["--from-raw"] if from_raw else [])],
        "go": subprocess.check_output(["go", "version"], text=True).strip()}, indent=2))
    manifest = json.loads((work/"manifest.json").read_text())
    index = []
    for row in manifest["runs"]:
        if row["mode"] != "full":
            continue
        case = work/row["case"]
        for process in ("client", "daemon"):
            dirs = sorted(p for p in (case/"profiles").iterdir() if p.is_dir() and ((p.name == "daemon") == (process == "daemon")))
            if not dirs:
                continue
            binary = work/"bin"/("pastured" if process == "daemon" else "pasture")
            output = case/("views-"+process)
            output.mkdir(exist_ok=True)
            for file, metric in (("cpu", "cpu"), ("heap", "inuse_space"), ("allocs", "alloc_space"), ("allocs", "alloc_objects"), ("block", "delay"), ("mutex", "delay")):
                profiles = [str(p/(file+".pprof")) for p in dirs]
                cmd = ["go", "tool", "pprof", "-sample_index="+metric]
                for flag, extension in (("-raw", "raw.txt"), ("-top", "top.txt"), ("-top -cum", "cum.txt"), ("-traces", "stacks.txt")):
                    command = cmd + flag.split() + [str(binary)] + profiles
                    if not from_raw:
                        result = subprocess.run(command, capture_output=True, check=True)
                        (output/(file+"-"+metric+"."+extension)).write_bytes(result.stdout)
                raw = (output/(file+"-"+metric+".raw.txt")).read_text()
                stacks = decode_raw(raw, metric)
                svg = flame(stacks, metric)
                dest = output/(file+"-"+metric+".svg")
                dest.write_text(svg)
                dest.with_suffix(".html").write_text('<!doctype html><meta charset="utf-8"><title>Private stack profile</title>'+svg)
                cpu_period, sample_count = cpu_metadata(raw, stacks) if metric == "cpu" else (None, None)
                index.append({"case": row["case"], "process": process, "profile": file, "metric": metric,
                              "sample_rows": len(stacks), "weight": sum(w for w, _ in stacks),
                              "cpu_period_ns": cpu_period,
                              "cpu_sample_count": sample_count,
                              "svg": str(dest), "command": cmd + [str(binary)] + profiles})
            if from_raw:
                continue
            representative = dirs[0]
            validations = []
            for name in ("sched", "sync", "syscall"):
                cmd = ["go", "tool", "trace", "-pprof="+name, str(representative/"trace.out")]
                result = subprocess.run(cmd, capture_output=True, check=True)
                (output/("trace-"+name+".pprof")).write_bytes(result.stdout)
                validations.append({"command": cmd, "exit": result.returncode})
            # These extracts use Go's full trace decoder. Avoid -d=parsed:
            # repeated stack expansion can turn a small trace into >10 GB of
            # text and contaminate the experiment with renderer memory load.
            (output/"trace-validation.json").write_text(json.dumps(validations, indent=2))
    (work/"artifact-index.json").write_text(json.dumps(index, indent=2))
    print(work/"artifact-index.json")


if __name__ == "__main__":
    render(Path(sys.argv[1]).resolve(), "--from-raw" in sys.argv[2:])
