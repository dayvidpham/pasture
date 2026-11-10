#!/usr/bin/env python3
"""Reproduce the W2 rendering-source equivalence record.

Compares two generations of the committed generated trees and classifies every
changed line pair. The audited pair is 5ab7cb7e..c1950bc3. Later commits on
this branch changed generated skill prose, so that pair is not the branch
tree. The listing's first line names the pair it was built from. Do not read
the listing as the current tree.

Usage:
    python3 scripts/epoch_workflow_equivalence.py [BASE] [HEAD] [--listing PATH]

Without arguments it uses the audited refs and writes nothing. With
--listing PATH it writes the per-line TSV listing to PATH. The counts it prints
are derived from the listing; nothing is hand-written.

Classification:
  token-only
      The line differs only by substituting a typed marker's resolved name:
      Skill/skill/invoke_skill, Task|Agent|delegate_assignment tool,
      SendMessage/send_assignment_message, AskUserQuestion/question/request-input.
  required-rewrite:delegation-call-open
      A native call-form opener `Agent({` / `task({` / `delegate_assignment({`
      rewritten to the neutral `delegation: {` form.
  required-rewrite:delegation-call-close
      The matching `})` -> `}` structural closure of that rewrite.
  required-rewrite:article
      The indefinite article that disagreed with the resolved noun rewritten
      to "in the preview the <noun> call shows".
  required-rewrite:retired-mechanism
      TeamCreate/delegation prose and figure wording rewritten by the wave-2
      gate (retired mechanism removal).
  deletion-only:retired-mechanism
      A retired-mechanism comment line with no counterpart in the new tree.

The binary Codex bundle (internal/target/codex/assets/codex-generated.json.gz)
is not line-diffed: it is a gzip of the `.agents/skills` contents that are
already diffed as text, so it carries no independent line pair.
"""

import argparse
import ast
import collections
import difflib
import json
import re
import subprocess
import sys

BASE_DEFAULT = "5ab7cb7e"
HEAD_DEFAULT = "c1950bc3"

# Every committed generated surface. skills/ and .agents/skills/ and
# .opencode/skill/ are the source trees; agents/ and .opencode/agent/ are the
# agent definitions; internal/target/ holds the committed per-harness assets;
# schema.xml is the neutral protocol document.
GENERATED_ROOTS = [
    "skills",
    ".agents/skills",
    ".opencode/skill",
    ".opencode/agent",
    "agents",
    "internal/target",
    "schema.xml",
]

# A generated line pair is token-only when the only change is a name
# substitution. Each rule maps every spelling of one marker's resolved name to
# one placeholder; the surrounding characters must then be identical.
TOKEN_RULES = [
    (re.compile(r"\b(?:Skill|skill|invoke_skill)\("), "NAME("),
    (re.compile(r"\b(?:Task|task|Agent|agent|delegate_assignment) tool\b"), "NAME tool"),
    (re.compile(r"\b(?:SendMessage|send_assignment_message)\b"), "NAME"),
    (re.compile(r"inside (?:AskUserQuestion|question|request-input) option"), "inside NAME option"),
    (re.compile(r"the (?:AskUserQuestion|question|request-input) call shows"), "the NAME call shows"),
]

DELEGATION_OPEN = re.compile(r"^(?:Agent|task|delegate_assignment)\(\{$")
ARTICLE = re.compile(r"\b(?:a|an)\s+(?:AskUserQuestion|question|request-input)\s+preview\b")


def git(*args):
    return subprocess.run(
        ["git", *args], check=True, capture_output=True, text=True
    ).stdout


def canonical(text):
    for pattern, replacement in TOKEN_RULES:
        text = pattern.sub(replacement, text)
    return text


def classify_pair(old, new):
    if canonical(old) == canonical(new):
        return "token-only"
    stripped_old = old.strip()
    if DELEGATION_OPEN.match(stripped_old) and new.strip().startswith("delegation:"):
        return "required-rewrite:delegation-call-open"
    if stripped_old == "})" and new.strip() == "}":
        return "required-rewrite:delegation-call-close"
    if ARTICLE.search(old) and "in the preview the" in new:
        return "required-rewrite:article"
    return "required-rewrite:retired-mechanism"


def changed_text_paths(base, head):
    raw = git("diff", "--name-only", base, head, "--", *GENERATED_ROOTS)
    paths = [line for line in raw.splitlines() if line]
    return [path for path in paths if not path.endswith(".gz")]


def file_at(ref, path):
    return git("show", f"{ref}:{path}")


def collect(base, head):
    rows = []
    for path in changed_text_paths(base, head):
        old_lines = file_at(base, path).splitlines()
        new_lines = file_at(head, path).splitlines()
        matcher = difflib.SequenceMatcher(None, old_lines, new_lines, autojunk=False)
        for tag, i1, i2, j1, j2 in matcher.get_opcodes():
            if tag == "equal":
                continue
            if tag == "replace":
                paired = min(i2 - i1, j2 - j1)
                for offset in range(paired):
                    old = old_lines[i1 + offset]
                    new = new_lines[j1 + offset]
                    rows.append(
                        {
                            "path": path,
                            "old_line": i1 + offset + 1,
                            "new_line": j1 + offset + 1,
                            "classification": classify_pair(old, new),
                            "old": old,
                            "new": new,
                        }
                    )
                for offset in range(paired, i2 - i1):
                    rows.append(
                        {
                            "path": path,
                            "old_line": i1 + offset + 1,
                            "new_line": "",
                            "classification": "deletion-only:retired-mechanism",
                            "old": old_lines[i1 + offset],
                            "new": "",
                        }
                    )
                if j2 - j1 > paired:
                    raise SystemExit(
                        f"unexpected insertion-only line(s) in {path}: "
                        f"{new_lines[j1 + paired : j2]!r}"
                    )
            elif tag == "delete":
                for index in range(i1, i2):
                    rows.append(
                        {
                            "path": path,
                            "old_line": index + 1,
                            "new_line": "",
                            "classification": "deletion-only:retired-mechanism",
                            "old": old_lines[index],
                            "new": "",
                        }
                    )
            elif tag == "insert":
                raise SystemExit(
                    f"unexpected insertion-only line(s) in {path}: {new_lines[j1:j2]!r}"
                )
    return rows


def provenance_line(base, head):
    return (
        f"# provenance: compared {base}..{head}; "
        "this listing is that pair only, not the branch tree\n"
    )


def write_listing(rows, path, base, head):
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(provenance_line(base, head))
        handle.write(
            "path\told_line\tnew_line\tclassification\told\tnew\n"
        )
        for row in rows:
            handle.write(
                "\t".join(
                    [
                        row["path"],
                        str(row["old_line"]),
                        str(row["new_line"]),
                        row["classification"],
                        json.dumps(row["old"], ensure_ascii=False),
                        json.dumps(row["new"], ensure_ascii=False),
                    ]
                )
                + "\n"
            )


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("base", nargs="?", default=BASE_DEFAULT)
    parser.add_argument("head", nargs="?", default=HEAD_DEFAULT)
    parser.add_argument("--listing", help="write the per-line TSV listing here")
    args = parser.parse_args()

    rows = collect(args.base, args.head)
    counts = collections.Counter(row["classification"] for row in rows)

    pairs = [row for row in rows if not row["classification"].startswith("deletion-only")]
    deletions = [row for row in rows if row["classification"].startswith("deletion-only")]
    token_only = counts["token-only"]
    required = len(pairs) - token_only

    if args.listing:
        write_listing(rows, args.listing, args.base, args.head)

    print(f"base {args.base} -> head {args.head}")
    print(f"text files changed: {len(set(row['path'] for row in rows))}")
    print(f"changed line pairs: {len(pairs)}")
    print(f"  token-only: {token_only}")
    print(f"  required-rewrite: {required}")
    for name in (
        "required-rewrite:delegation-call-open",
        "required-rewrite:delegation-call-close",
        "required-rewrite:article",
        "required-rewrite:retired-mechanism",
    ):
        print(f"    {name}: {counts[name]}")
    print(f"deletion-only lines: {len(deletions)}")

    assert token_only + required == len(pairs), "pair split must reconcile"
    assert sum(
        counts[name]
        for name in (
            "required-rewrite:delegation-call-open",
            "required-rewrite:delegation-call-close",
            "required-rewrite:article",
            "required-rewrite:retired-mechanism",
        )
    ) == required, "required-rewrite categories must sum to their total"
    print(
        f"reconciled: {len(pairs)} pairs = {token_only} token-only + {required} "
        f"required-rewrite; {len(rows)} old lines = {len(pairs)} paired + "
        f"{len(deletions)} deletion-only"
    )
    print(f"article copies: {counts['required-rewrite:article']}")


if __name__ == "__main__":
    main()
