# Epoch workflow — rendering-source literal mapping (W2)

Work list for the de-litteralling slice (PROPOSAL-4 W2). It names every literal
that reaches generated skill or agent prose, the typed surface that replaces it,
and what each target resolves that surface to. The source of truth is the code;
this file records the mapping that was reviewed before the edits.

Scope: every authored string that the generators render into a generated skill,
sub-skill, or agent file. That is the 28 `internal/codegen/specs_data_body_*.go`
files plus the non-body rendering sources: `SharedFragmentSpecs`, role behaviour
and constraint prose, checklists, coordination commands, procedure steps,
workflows, review axes, command titles/descriptions, and figure content.
`schema.xml` is generated too; it resolves markers to the target-neutral
spelling because it names no single harness. Generated trees are never edited by
hand; `make generate` produces them from these specs.

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

`ResolveBodyMarkers` resolves them per generation target through
`internal/runtime/profiles.go`; an unknown payload fails generation. It runs
over the whole rendered document in `renderSkill`, `renderSubSkill`,
`renderAgentForTarget`, and the OpenCode lifecycle plugin, so every rendering
corpus goes through the one resolver. `renderedSources` in
`internal/codegen/body_refs.go` enumerates every authored rendering input; the
guards in `body_refs_test.go` iterate that one list, so a new corpus cannot be
added blind.

`TestRenderingSourcesCarryNoRawHarnessLiterals` fails the build when a raw call
prefix, harness phrase (`Task tool`, `Agent tool`, `SendMessage`, `TeamCreate`)
or tracker command prefix is re-added. `TestRenderingSourcesAvoidArticleBeforeMarker`
fails when an indefinite article precedes a marker: no single article agrees
with every target's resolved noun.

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

The counts below are the original body-slice census. They are historical, not a
current invariant: the fix pass also maps the same classes in non-body rendering
sources, so the live count lives in the guard, not this table.

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
| `aura-swarm`, `beads`, and the retired Beads command | retired mechanism names, no IR kind | contradiction sweep (W7) |
| `Read`/`Write`/`Grep` as English verbs | not tool literals | — |

`TeamCreate` needed no IR kind: deleting the retired mechanism from the
rendering sources was the fix. It is removed from every generated role and
sub-skill. The hand-authored `skills/protocol/` documents are copied verbatim,
not rendered, and still name it; their cleanup stays with W7.

The Codex `request-input` binding id is cited at
`internal/runtime/profiles.go` (Codex contract): the harness-tools research note
records that Codex 0.167.0's model-facing tool is `request_user_input` and that
`request-input` is the contract's internal binding id. Correcting the emitted
name is a contract change, not a rendering change, and is not made here.

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

### 4.2 Non-body rendering sources

Counts are intentionally omitted (they change with every wording edit). Each
source below now authors typed markers and is covered by `renderedSources`:

| Source | Rendered into |
|---|---|
| `SharedFragmentSpecs` (`frag--task-recovery`, `frag--task-author`, `frag--sup-*`) | every skill body, plus the OpenCode lifecycle plugin recovery text |
| `RoleSpecs[].Introduction` / `.OwnershipNarrative` / `.Behaviors` | role skills and agent definitions |
| `ConstraintSpecs[].Given/When/Then/ShouldNot/Command/Examples` | role skills, agent definitions, `schema.xml` |
| `CommandSpecs[].Title/Description` | sub-skill headings and frontmatter descriptions |
| `ChecklistSpecs`, `CoordinationCommands`, `WorkflowSpecs`, `ReviewAxisSpecs`, `ProcedureSteps` | role skills and agent definitions |
| `FigureSpecs` content (`skills/protocol/figures/*.yaml`) | workflow figures in role skills |
| `schema.go` section data and directives | target-neutral `schema.xml` |

## 5. Equivalence

Method (strict): generate the base tree and the changed tree, then diff every
generated file under `skills/`, `.agents/skills/`, `.opencode/skill/`,
`agents/`, `.codex/`, `schema.xml`, the committed target assets and
`internal/inventory/`. Each changed line pair is canonicalised by replacing the
old and new resolved token with one placeholder. A line pair that differs by any
other character is UNEXPLAINED. Article, number and grammar differences are not
normalised: an article that changes because a resolved noun changed is a defect
in the source, not a legitimate repair.

Result over this fix pass (base `5ab7cb7e`): 56 generated files changed, 304
changed line pairs. 91 line pairs are token-only and explained by the
resolution table in §2. 218 line pairs are unexplained by resolution, in 64
unique shapes, and every one is an explicit source rewrite required by the
wave-2 gate:

- `TeamCreate` deletion and the delegation-prose rewrite (62 shapes across the
  5 committed copies: `skills/`, `.agents/skills/`, `.opencode/skill/`, and the
  Claude/OpenCode target assets).
- The article rewrite at `user-elicit` (3 copies).
- Five `replace block` changes are the deleted `TeamCreate` code block.

No unexplained line pair is an accidental wording change caused by marker
resolution. Resolution alone changed only tokens. `schema.xml`, agents, and the
OpenCode lifecycle plugin are now resolved too: `schema.xml` uses the neutral
spelling, and the plugin embeds OpenCode-resolved recovery prose.

The classification manifest
(`internal/codegen/scan/manifest/classifications.json`) is updated only where a
changed source removed or renamed a classified section (`TeamCreate` blocks and
the two renamed sections). No new candidate is added.

Still out of scope and owned by the contradiction sweep (W7): the hand-authored
`skills/protocol/` tree is copied verbatim rather than rendered, so its
`TeamCreate` and `Task tool` prose is unchanged here.
