# Canonical task recipe conversion

Source baseline: `3d73caeb` (origin/main, 2026-10-10). Scope: canonical
`internal/codegen/specs_data*.go` registries and their direct generated
consequences. Schema/context and hand-authored protocol documents are a later
increment; hook transport and payload retirement are separate work.

## Inventory and migration method

The bounded, non-production migration parsed Go syntax, evaluated constant
string concatenations, and replaced complete constant expressions. A one-shot
operand migration handled positional titles, actual JSON `id` capture, one
label per subsequent mutation, explicit authors, supported statuses, full URI
input bindings, and dependency direction. Raw versus interpreted strings were
decoded before migration and re-encoded as Go literals afterwards. There is no
render-time rewrite, compatibility parser, or production migration utility.

These are lexical occurrence counts in decoded canonical strings, including
prose command references and explicitly incorrect examples, not counts of
independently executable programs. The after column includes newly explicit
label/metadata operations and the two new shared fragments. Every source owner
was reviewed; non-task code samples and unrelated native calls were retained.

| Owner (`internal/codegen/`) | Before `bd` command references | After task command references |
|---|---:|---:|
| specs_data.go | 57 | 63 |
| specs_data_body.go | 0 | 0 |
| specs_data_body_architect.go | 20 | 30 |
| specs_data_body_architect_handoff.go | 14 | 14 |
| specs_data_body_architect_propose_plan.go | 4 | 6 |
| specs_data_body_architect_ratify.go | 7 | 7 |
| specs_data_body_architect_request_review.go | 4 | 4 |
| specs_data_body_epoch.go | 26 | 33 |
| specs_data_body_explore.go | 2 | 2 |
| specs_data_body_impl_review.go | 25 | 31 |
| specs_data_body_impl_slice.go | 15 | 17 |
| specs_data_body_research.go | 2 | 2 |
| specs_data_body_reviewer.go | 4 | 4 |
| specs_data_body_reviewer_comment.go | 4 | 4 |
| specs_data_body_reviewer_review_code.go | 22 | 29 |
| specs_data_body_reviewer_review_plan.go | 6 | 7 |
| specs_data_body_reviewer_vote.go | 2 | 2 |
| specs_data_body_status.go | 11 | 11 |
| specs_data_body_supervisor.go | 49 | 58 |
| specs_data_body_supervisor_commit.go | 2 | 3 |
| specs_data_body_supervisor_plan_tasks.go | 9 | 13 |
| specs_data_body_supervisor_spawn_worker.go | 14 | 14 |
| specs_data_body_supervisor_track_progress.go | 15 | 15 |
| specs_data_body_swarm.go | 0 | 0 |
| specs_data_body_user_elicit.go | 6 | 10 |
| specs_data_body_user_request.go | 9 | 13 |
| specs_data_body_user_uat.go | 10 | 12 |
| specs_data_body_worker.go | 10 | 9 |
| specs_data_body_worker_blocked.go | 4 | 3 |
| specs_data_body_worker_complete.go | 6 | 5 |
| specs_data_body_worker_implement.go | 5 | 5 |
| specs_data_fragments.go | 8 | 24 |

Before: 372 lexical command references; after: 450. The original vocabulary comprised
ready/blocked/list/show/create/update/close/comments/label/dep/stats/task/mol/sync.
After: registered commands only; stats is a scoped JSON-list summary, the one
native task mention is a show operation, and molecule/sync instructions are
removed. No recovery/diagnostic/editor command was invented. Generic assignments
are not fabricated: only an existing owner-responsibility transfer is shown.
Design and requested-role metadata are retained through supported notes/body
fields rather than unsupported create flags. Audit labels/comments remain
append-only. Worker completion is evidence, never slice closure.

Shared recovery/attribution exports: `FragTaskRecovery` (`frag--task-recovery`)
and `FragTaskAuthor` (`frag--task-author`) in `SharedFragmentSpecs`. All 29 skill
bodies reference both IDs. Recovery reads live state and names store/namespace
precedence, author registration, input bindings, non-atomic create/label retry,
frontmatter references, blockers, closure authority, and Git safety. Hook
composition should import the recovery fragment's exact `Prose.Content` bytes.

The native scanner still finds 55 classified candidates. Only four content
windows changed (architect handoff and supervisor skill invocation context);
ordinals, dispositions, notes, and all unrelated entries remain unchanged.
`owners.json` is unchanged. In particular, the native TeamCreate/SendMessage
windows were preserved after correcting Markdown list indentation.

## Verification

Passed focused commands:

```sh
go test -race ./cmd/pasture -run 'TestCLI_(GeneratedTaskRecipes|TaskMapped)' -count=1
go test -race ./internal/codegen/...
make fmt
make lint
make build
```

Contract fixtures exercise real registered CLI commands against isolated stores
in both repository namespaces: positional titles, JSON URI binding, labels,
authored chronological comments, filters, status updates, typed dependencies,
and readiness after child closure. Unsupported commands/flags fail before store
opening. Five shell recipes are extracted verbatim from generated skill files
and executed: shared and reviewer severity graph creation, request capture plus
metadata, and both supervisor plan heredocs with bound slice URIs.
Production behavior is the built CLI -> Cobra registrations -> handlers ->
unified task store; no test-only command implementation is used.

An independent shell syntax pass checked all 142 generated bash/sh fences
(dedenting Markdown list fences): zero `bash -n` failures. An independent scan
of the 165 changed plain-text generated/embedded files found zero command-shaped
`bd` matches. The remaining changed file is the compressed Codex asset bundle;
after decompression its only matches are unchanged protocol documents owned by
the completeness increment, not converted recipes. The canonical
`specs_data*.go` source-tree digest (sorted relative
path, NUL, contents, NUL; excluding tests) at this check was
`8a0f147d24e481b78bda9c232c7b0d1daff388145644439809796e0044769df6`.

The initial full race run rejected the two new CLI test filenames under the
package's exhaustive lifecycle test-owner inventory. Their declarations were
relocated into the existing task-test owners (`task_residual_verbs_test.go` and
`task_workflow_test.go`), leaving all lifecycle guards and their coverage scope
unchanged. The two ownership sweep tests and the relocated contract tests then
passed together with `-race`; the final full-suite result is reported separately.
`make -B build` forced fresh binaries despite the Makefile's timestamp-only
binary targets.

The initial repaired full race suite passed. A subsequent source review found
residual handoff wording and alias/binding issues; those were repaired, worker
behavior/constraint closure checks and alias fixtures were extended, and all
five emitted recipes and `go test -race ./internal/codegen/...` passed. The
final revised full-suite result is reported in the tracking task. Reconciliation
of the changed supervisor classification used the existing pre-generation
output scanner: generate against the old output window, update its exact window,
then regenerate successfully. No gate was bypassed or weakened; all 55 native
classifications remain, with exactly four changed content windows.

The last budgeted review found two remaining minor issues (handoff aliases and
the second committed-task closure). The user explicitly authorized another
repair/review round; both were fixed and pinned by regression tests. The revised
full-suite run had been terminated by the tool's 120-second command ceiling,
not a test assertion. Its partial log is retained; the final run uses no shell
ceiling and the project's normal 30-minute per-package test timeout.

Generation uses `make generate`, including all three harness outputs and target
asset generators. A full race suite and post-commit twice-generated clean-tree
proof are recorded in the tracking task; this document does not claim results
before those operations finish.

Independent broad scan command:

```sh
rg -n '\bbd[[:space:]]+(ready|blocked|list|show|create|q|update|close|comments?|label|dep|stats|count|search|task|prime|sync|edit|mol|doctor)\b' \
  skills agents hooks .opencode .agents .codex internal/target
```

At this increment it reports 851 lines, all in untouched protocol documents
and their copies (755), or unchanged `bd-prime.md`/`git-discipline.sh` payloads
and their embedded copies (96). None are in the converted owner projections.
Historical exceptions in converted projections: **0**. This is deliberately not
an all-program recipe-free claim: remaining owners must be converted/classified
by their assigned increments, then the combined tree must pass strict scanning.
The scan is a conservative independent lexical report, not the future strict
executable-context classifier. The old context injection remains wired here.

No task CLI surface, lifecycle transport, Git hooks, installed user settings,
archive, other repository, or well-known actor registration was changed.
