# Epoch workflow — body literal mapping (W2)

Work list for the de-litteralling slice (PROPOSAL-4 W2). It names every literal
that appeared in a skill body spec file, the typed surface that replaces it, and
what each target resolves that surface to. The source of truth is the code; this
file records the mapping that was reviewed before the edits.

Scope: the 28 `internal/codegen/specs_data_body_*.go` files. Generated trees are
never edited by hand; `make generate` produces them from these specs.

Related tenets: tenet 4 (name it once), tenet 6 (fail the build), tenet 3 (write
the operation, not the tool). Requirements: R20 (no unrendered harness tool
name), R32 (one schema, correct names on all three harnesses).

## 1. Marker grammar

A body string carries one of three markers. A marker payload is a closed-set
value, never free prose.

| Marker | Payload | Meaning |
|---|---|---|
| `[[tool:<ir.HarnessTool>]]` | one member of `ir.AllHarnessTools()` | native file or shell capability |
| `[[op:<ir.OperationKind>]]` | one member of `ir.AllOperationKinds()` | orchestration intent |
| `[[rec]]` | none | the tracker entry point |

`ToolRef`, `OpRef` and `TrackerRef` in `internal/codegen/body_refs.go` construct
the markers from typed values. `ResolveBodyMarkers` resolves them per generation
target through `internal/runtime/profiles.go`; an unknown payload fails
generation. `TestBodySpecsCarryNoRawHarnessLiterals` fails the build when a raw
call prefix or command prefix is re-added.

## 2. Resolution table

The authored body is identical for every target. Only the resolved token differs.

| Marker | Claude Code | OpenCode | Codex |
|---|---|---|---|
| `[[tool:read]]` | `Read` | `read` | `read` (neutral; no per-agent tool list) |
| `[[tool:glob]]` | `Glob` | `glob` | `glob` |
| `[[tool:grep]]` | `Grep` | `grep` | `grep` |
| `[[tool:bash]]` | `Bash` | `bash` | `bash` |
| `[[op:invoke_skill]]` | `Skill` | `skill` | `invoke_skill` (semantic) |
| `[[op:delegate_assignment]]` | `Agent` | `task` | `delegate_assignment` (parent-mediated) |
| `[[op:send_assignment_message]]` | `SendMessage` | `send_assignment_message` (semantic) | `send_assignment_message` (parent-mediated) |
| `[[op:request_user_decision]]` | `AskUserQuestion` | `question` | `request-input` |
| `[[rec]]` | `pasture task` | `pasture task` | `pasture task` |

A non-native operation resolves to the neutral kind spelling. That names the
intent, not another harness's tool, so it cannot be mistaken for a name the host
exposes. The per-target call *shape* (arguments, option limits, degradation
prose) belongs to the harness-binding and writing waves; W2 fixes the name.

The tracker entry point resolves to the one `trackerEntryPoint` constant. The
tracker-contract wave replaces that constant's value with the runtime adapter
entry point; no body changes when it does.

## 3. Literal-to-kind mapping

### 3.1 Tracker commands

| Literal | Typed surface | Count |
|---|---|---|
| `pasture task <verb>` (create, label, dep, comment, show, list, blocked, ready, close, update, comments, timeline, agents, assignment) | `[[rec]] <verb>` | 363 |

The two non-command mentions of the product name (`the REQUEST pasture task ID`)
are prose, not commands, and are left as written. They do not name a command.

### 3.2 Native harness tools

| Literal | Typed surface | Count |
|---|---|---|
| `Read` in a tool list | `[[tool:read]]` | 5 |
| `Glob` in a tool list | `[[tool:glob]]` | 7 |
| `Grep` in a tool list | `[[tool:grep]]` | 7 |
| `Bash` in a tool list | `[[tool:bash]]` | 2 |
| `Edit`, `Write`, `WebFetch` | none in body specs | 0 |

A native name is replaced only where it names the capability (a tool list, a
tool reference). Ordinary English uses of the same word are left alone: "Read
the URD", "Write the tests", "Grep for keywords" are verbs, not tool literals.

### 3.3 Orchestration

| Literal | Typed surface | Count |
|---|---|---|
| `Skill(` call prefix | `[[op:invoke_skill]](` | 29 |
| `Task(` call prefix, `Task tool` | `[[op:delegate_assignment]]` | 27 |
| `SendMessage` | `[[op:send_assignment_message]]` | 14 |
| `AskUserQuestion` | `[[op:request_user_decision]]` | 9 |
| `Agent tool` | `[[op:delegate_assignment]]` | 2 |

`Task` resolves to `Agent` on Claude Code. The pinned contract records the
rename (`Agent` is the current tool; `Task` is the legacy alias), so the
resolved text is the current name, not a paraphrase.

### 3.4 Not mapped

| Literal | Why no mapping | Owner |
|---|---|---|
| `TeamCreate` | no honest IR kind: the closed orchestration set models delegation, not a team object, and the retired mechanism is dropped | contradiction sweep (W7); reported as an unmapped literal in the slice evidence |
| `aura-swarm`, `beads`, and the retired Beads command | retired mechanism names, no IR kind | contradiction sweep (W7) |
| `Read`/`Write`/`Grep` as English verbs | not tool literals | — |

Inventing an IR kind for `TeamCreate` would violate amendment A-3 (zero new
kinds) and tenet 2 (a closed set is not extended at a use site). Leaving it in
place until W7 deletes the retired mechanism is the honest option.

## 4. Work list by file

`rec` counts tracker entry-point markers; `tool` and `op` count typed markers.

| File | rec | tool | op |
|---|---|---|---|
| `specs_data_body_architect.go` | 30 | 0 | 8 |
| `specs_data_body_architect_handoff.go` | 14 | 0 | 17 |
| `specs_data_body_architect_propose_plan.go` | 6 | 0 | 0 |
| `specs_data_body_architect_ratify.go` | 7 | 0 | 0 |
| `specs_data_body_architect_request_review.go` | 4 | 0 | 3 |
| `specs_data_body_epoch.go` | 33 | 0 | 10 |
| `specs_data_body_explore.go` | 2 | 9 | 0 |
| `specs_data_body_impl_review.go` | 31 | 0 | 4 |
| `specs_data_body_impl_slice.go` | 17 | 0 | 0 |
| `specs_data_body_research.go` | 2 | 3 | 0 |
| `specs_data_body_reviewer.go` | 4 | 0 | 0 |
| `specs_data_body_reviewer_comment.go` | 4 | 0 | 0 |
| `specs_data_body_reviewer_review_code.go` | 29 | 0 | 0 |
| `specs_data_body_reviewer_review_plan.go` | 7 | 0 | 0 |
| `specs_data_body_reviewer_vote.go` | 2 | 0 | 0 |
| `specs_data_body_status.go` | 11 | 0 | 0 |
| `specs_data_body_supervisor.go` | 58 | 0 | 16 |
| `specs_data_body_supervisor_commit.go` | 3 | 0 | 0 |
| `specs_data_body_supervisor_plan_tasks.go` | 13 | 0 | 0 |
| `specs_data_body_supervisor_spawn_worker.go` | 14 | 0 | 12 |
| `specs_data_body_supervisor_track_progress.go` | 15 | 0 | 0 |
| `specs_data_body_user_elicit.go` | 10 | 0 | 4 |
| `specs_data_body_user_request.go` | 13 | 9 | 3 |
| `specs_data_body_user_uat.go` | 12 | 0 | 5 |
| `specs_data_body_worker.go` | 9 | 0 | 0 |
| `specs_data_body_worker_blocked.go` | 3 | 0 | 0 |
| `specs_data_body_worker_complete.go` | 5 | 0 | 0 |
| `specs_data_body_worker_implement.go` | 5 | 0 | 0 |
| **Total** | **363** | **21** | **82** |

## 5. Equivalence

Method: `make generate` was run at the W1 commit to establish the old output,
then again after this change. Every generated file under `skills/`,
`.opencode/skill/`, `.agents/skills/`, `agents/`, `.codex/`, and the committed
target assets was diffed. Each changed line is classified against the
resolution table in §2: after replacing the old and new token with one
placeholder, the two lines must be identical. A line that differs by any other
text is a wording change and fails the check.

Result: 312 changed generated lines, 0 unexplained differences. Every changed
line canonicalises to its old form after the resolution table in §2 is applied
to both sides.

- Claude Code: 7 of 31 skill files changed. Only the `Task` → `Agent`
  resolutions changed; `Skill`, `SendMessage`, `AskUserQuestion` and
  `pasture task` resolve to their existing spellings, so those lines are byte
  identical.
- OpenCode: the same lines with `skill`, `task`, `question`; non-native
  operations keep the neutral kind.
- Codex: the same lines with neutral capability and operation names.
- 20 of the 312 lines are the same four source strings appearing in each
  target tree (and its committed asset copy), where the article or preposition
  had to change because the resolved noun changed ("spawn the supervisor as a
  Task tool subagent" → "spawn the supervisor with the Agent tool"). These are
  resolution-induced grammar repairs, not free rewrites; the checker normalises
  the two shapes and reports no other wording change.

The classification manifest
(`internal/codegen/scan/manifest/classifications.json`) is updated only where a
resolved token changed a classified content window (`Task tool` → `Agent tool`).
No classification entry is added or removed.

Out of this slice's file scope, and therefore not de-litteralled here: agent
definitions (`agents/`, rendered from `RoleSpec.Behaviors` and
`ConstraintSpecs`) and the hand-authored `skills/protocol/` tree. They are not
`specs_data_body_*.go` files. Their tool-name prose belongs to the contradiction
sweep and the harness-binding waves.
