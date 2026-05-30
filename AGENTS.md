# Pasture — Agent Coding Standards

This document defines the coding conventions and quality gates for the Pasture
project. All contributors (human and AI) must follow these standards.

## Project Identity

- **Module:** `github.com/dayvidpham/pasture`
- **Binaries:**
  - `pastured` (Temporal worker daemon)
  - `pasture-msg` (Temporal-control CLI)
  - `pasture` (local task + audit CLI; routes through `protocol.TaskTracker`)
  - `pasture-release` (versioning)
- **Language:** Go 1.23+
- **CGo:** disabled (`CGO_ENABLED=0`) — all dependencies must be pure Go

## Directory Structure

```
pasture/
├── cmd/
│   ├── pasture/         # Local Pasture CLI (task verbs + top-level migrate)
│   ├── pastured/        # Temporal worker daemon entry point
│   ├── pasture-msg/     # CLI for sending protocol messages
│   └── pasture-release/ # Release and versioning tool
├── internal/
│   ├── acp/             # Agent Control Protocol client + adapter
│   ├── audit/           # Audit trail + schema migrator (SQLite-backed)
│   ├── config/          # Viper-based configuration
│   ├── errors/          # Actionable error types
│   ├── formatters/      # Output formatters (JSON, text, table)
│   ├── handlers/        # Cobra RunE → standalone handler functions
│   ├── hooks/           # Claude Code hook event handlers
│   ├── release/         # pasture-release internals
│   ├── tasks/           # protocol.TaskTracker implementation +
│   │                    #   well-known agent registry + free-floating recorders
│   ├── temporal/        # Temporal workflow/activity implementations
│   └── types/           # Internal aggregate types (not for export)
├── pkg/
│   └── protocol/        # Public aura-protocol types — including the
│                        #   protocol.TaskTracker façade interface
└── skills/
    └── install-cli/     # Claude Code skill installer script
```

## Task Tracker (Unified Façade)

`protocol.TaskTracker` (defined in `pkg/protocol/tasktracker.go`) is the
canonical entry point for all task and audit operations across the toolkit.
PROPOSAL-2 (`docs/proposals/PROPOSAL-2-pasture-workflow-record.md`) and ADR
0001 (`docs/adr/0001-pasture-toolkit-integration-architecture.md`) describe
the rationale; this section documents the implemented surface.

The interface composes three method families on a single shared SQLite file:

1. **Embedded `provenance.Tracker`** (28 methods, upstream library, unchanged
   per URD R1) — task CRUD, edges, labels, comments, agents (Human/ML/Software),
   activities.
2. **Inline audit-trail methods** (4 method signatures matching `audit.Trail`
   exactly: `RecordEvent`, `RecordEventReturningID`, `QueryEvents`,
   `RecordSessionEntries`/`QuerySessionEntries`) — declared inline rather than
   embedded to avoid a `pkg/protocol → internal/audit` import cycle. Any
   `audit.Trail` implementation satisfies them automatically.
3. **6 pasture-only methods** — `SetAgentCategories` / `AgentCategories` (R8),
   `AttachContext` / `EventContexts` / `Timeline` (R9), and `Close` (lifecycle).

**In-tree callers** (all `internal/handlers` packages, `cmd/pastured`, and
transitively `cmd/pasture`) already import `internal/tasks` directly and call
`tasks.OpenTaskTracker` — the idiomatic Go way:

```go
import "github.com/dayvidpham/pasture/internal/tasks"

tracker, err := tasks.OpenTaskTracker("") // empty path → DefaultDBPath()
if err != nil { /* StructuredError with CategoryConnection / CategoryStorage / CategoryValidation */ }
defer tracker.Close()
```

**New in-tree main packages** that do NOT go through `internal/handlers` should
follow the same pattern: import `internal/tasks` directly.

If you ever need to call `protocol.OpenTaskTracker` (the façade form) from a
new main package or integration test, add the blank import AND a startup guard:

```go
import (
    "github.com/dayvidpham/pasture/pkg/protocol"
    _ "github.com/dayvidpham/pasture/internal/tasks" // wires OpenTaskTracker via init()
)

func init() { protocol.MustHaveImpl() } // panics immediately if the blank import was forgotten

tracker, err := protocol.OpenTaskTracker("") // empty path → DefaultDBPath()
if err != nil { /* StructuredError with CategoryConnection / CategoryStorage / CategoryValidation */ }
defer tracker.Close()
```

The `MustHaveImpl()` guard catches a forgotten blank import at process startup
rather than at the first `OpenTaskTracker` call. The blank import is required
because the constructor body lives in `internal/tasks` (UAT-1 placement
binding per PROPOSAL-2 §7.4); `internal/tasks`'s `init()` calls
`protocol.RegisterOpenTaskTracker` to wire the implementation. The indirection
is necessary because `pkg/protocol` cannot import `internal/tasks` directly
(that would create an import cycle: `internal/tasks` already imports
`pkg/protocol` for the `TaskTracker` type).

`Close` is safe to call multiple times and closes both wrapped subsystems
(the `provenance.Tracker` and the audit `*sql.DB`) exactly once.

### Unified database file (`pasture.db`)

The single shared SQLite file lives at:

| Resolution step | Path |
|---|---|
| 1. `$PASTURE_DB_PATH` | (user override) |
| 2. `$XDG_DATA_HOME/pasture/pasture.db` | (XDG layout) |
| 3. `$HOME/.local/share/pasture/pasture.db` | **default** |
| 4. `.pasture/pasture.db` | last-resort relative fallback |

See `internal/tasks/paths.go` (`DBPathEnv`, `DefaultDBFilename`,
`DefaultDBPath`).

Both subsystems open the same file: the Provenance tables (`tasks`, `edges`,
`labels`, `comments`, `agents`, `agents_software`, `agents_human`, `agents_ml`,
`activities`) and the audit tables (`audit_events`, `context_edges`, `sessions`,
`pasture_well_known_agents`, `pasture_agent_categories`, `audit_schema_meta`)
co-exist in one file. PROPOSAL-2 §7.1 / D11 binds writers to SQLite WAL mode
with `busy_timeout=5000`; the cross-subsystem race test in
`internal/tasks/tracker_race_test.go` (BLOCKER B3) exercises this path.

Pre-PROPOSAL-2 deployments used two separate files (`provenance.db` for the
`pasture` CLI, `audit.db` for `pastured`); SLICE-10 collapses both to
`pasture.db`. The `pastured --audit-db-path` flag and `PASTURE_AUDIT_DB_PATH`
environment variable are preserved as **deprecated aliases** for `--db` and
`PASTURE_DB_PATH`. If both `--db` and `--audit-db-path` are set with different
values, the daemon prefers `--db` and emits a deprecation warning (see
`resolveDBPath` in `cmd/pastured/main.go`).

### Schema migration (`pasture migrate`)

`pasture migrate [--dry-run]` is a top-level CLI command (NOT under
`pasture task`) because migration is a database-level operation. It opens the
unified file via the same audit subsystem `OpenTaskTracker` uses, runs
`audit.Migrate`, and prints `migrated <db-path> from v<from> to v<to>`. With
`--dry-run` it prints the planned migrations and exits 0 without modifying the
file (the file's SHA-256 is unchanged before and after). Already-current
databases are a no-op: a second invocation prints
`migrated <db-path> from v<n> to v<n>`.

Auto-on-open is preserved: `OpenTaskTracker` runs the migrator at every open
(PROPOSAL-2 §7.10). Both paths share one migrator implementation
(`internal/audit/migrate.go` + the `migrate_v*.go` step files); the explicit
command exists for ops convenience and for the BDD Scenario 15 surface.

### Well-known automaton agents (15 entries, registered at `pastured` startup)

`pastured` registers 15 well-known software agents at startup
(PROPOSAL-2 §7.7.2; implementation in `internal/tasks/well_known_registry.go`,
`well_known.go`, `well_known_cache.go`). Registration is idempotent: two
consecutive startups produce identical row counts in `agents`,
`agents_software`, `pasture_well_known_agents`, and `pasture_agent_categories`
(BDD Scenario 14). The breakdown:

| Count | `protocol.AutomatonRole` | Naming convention |
|---|---|---|
| 1 | `ConstraintChecker` | `pasture/automaton/check-constraints` |
| 3 | `TransitionGate` | `pasture/automaton/transition-gate/{consensus,vote-threshold,exit-condition}` |
| 9 | `HookHandler` | `pasture/automaton/hook/<ClaudeCodeHookEvent>` (one per Claude Code hook event) |
| 1 | `ConsensusReached` | `pasture/automaton/consensus-reached` (UAT-1 first-class) |
| 1 | `CreateFollowup` | `pasture/automaton/create-followup` (UAT-1 first-class) |

Total: 15 (`tasks.WellKnownAgentCount`). The 9 Claude Code hook event names
are: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`,
`Notification`, `Stop`, `SubagentStop`, `PreCompact`, `SessionEnd`. List the
registered agents with `pasture task agents list`.

### `pastured --idle-after-migrate <duration>`

A test-mode flag on `pastured` that, after migration + well-known agent
registration completes, idles the daemon for the given duration before
starting the Temporal worker. Default `0` disables the idle window
(production behaviour). Used by the S3 Scenario 12 concurrent-migrator race
test to widen the window during which a second migrator can race the first.
Not for production use.

### `pasture task` subcommands (added by PROPOSAL-2)

| Subcommand | Purpose |
|---|---|
| `pasture task events` | Query audit events with optional filters (`--epoch-id`, `--phase`, `--role`). |
| `pasture task timeline TASK-ID` | Show all events attached to a task in chronological order. |
| `pasture task contexts EVENT-ID` | List all `context_edges` rows attached to an audit event. |
| `pasture task agents [list\|show]` | List or inspect registered agents and their pasture-side categories. |

Existing `pasture task` verbs (`create`, `show`, `update`, `close`, `list`,
`ready`, `blocked`, `dep add`/`tree`, `label add`/`remove`, `comment add`,
`comments`) are unchanged in shape but now route through
`protocol.TaskTracker` rather than importing `provenance` directly (SLICE-10).

## Dependencies (Approved)

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Configuration loading (TOML/YAML/env) |
| `go.temporal.io/sdk` | Temporal workflow orchestration |
| `modernc.org/sqlite` | Pure-Go SQLite (audit trail, local state) |

No other external dependencies may be added without supervisor approval.

## Go Conventions

### No CGo
```go
// build constraint at top of any file that must remain CGo-free
//go:build !cgo
```
All SQLite usage MUST use `modernc.org/sqlite` (pure Go), never `mattn/go-sqlite3`.

### Strongly-Typed Enums
Prefer typed constants over bare strings:
```go
// Correct
type ExitCode int
const (
    ExitSuccess    ExitCode = 0
    ExitValidation ExitCode = 1
    ExitConnection ExitCode = 2
    ExitWorkflow   ExitCode = 3
)

// Wrong
os.Exit(1) // magic number with no name
```

### Exit Codes
| Code | `errors.Category` | Meaning |
|------|-------------------|---------|
| 0 | (none) | Success |
| 1 | `CategoryValidation` | Validation error (bad input, missing flags) |
| 2 | `CategoryConnection` | Connection error (Temporal unreachable, ACP unreachable, file open failure) |
| 3 | `CategoryWorkflow` | Workflow error (execution failure, signal rejected) |
| 4 | `CategoryConfig` | Configuration error (bad YAML, invalid env var) |
| 5 | `CategoryStorage` | Storage error (SQLite open, schema migration failure, schema-version mismatch) |

`CategoryStorage` was added in PROPOSAL-2 §7.10.5 to give migration / open /
version-mismatch failures a distinct exit code separate from connection or
configuration errors. See `internal/errors/errors.go` and the `ExitCode()`
mapping.

### Actionable Errors
Every error must describe: what went wrong, why, where, when, and how to fix it.
```go
// Correct
fmt.Errorf("config: failed to load %q: %w — ensure the file exists and is valid TOML", path, err)

// Wrong
fmt.Errorf("invalid input")
```

### Command Pattern (Cobra + Handlers)
Use the hybrid pattern: Cobra `RunE` delegates to a standalone handler function.
This keeps `RunE` thin and makes handlers independently testable.

```go
// cmd/pasture-msg/start.go
var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start a new agent session",
    RunE:  runStart,
}

// handlers/start.go (testable independently)
func runStart(cmd *cobra.Command, args []string) error {
    cfg := mustLoadConfig(cmd)
    return handlers.Start(cfg, args)
}
```

### Package Imports
- `pkg/protocol` is the public API — import it directly; do NOT create aliases in `internal/types/`.
- `internal/` packages are private; only importable within the module.

## Testing

### Mandatory flags
```bash
go test -race ./...
```
The `-race` flag is mandatory for all test runs.

### Test file conventions
- Test files: `*_test.go` using `package foo_test` (black-box) or `package foo` (white-box).
- Import the actual production package — never a test-only re-export.
- Use dependency injection (interface mocks) for external services (Temporal, SQLite).

### Quality gates (must pass before every commit)
```bash
make fmt    # gofmt — fails if any file needs formatting
make lint   # go vet ./...
make test   # go test -race ./...
make build  # CGO_ENABLED=0 go build ./...
```

### Smoke tests

The unit/integration suite (`make test`) runs in-process against mocked or
in-memory backends. The smoke tests below exercise the production-shape path
(real Temporal server, real SQLite file, real CLI invocations) and surface
wiring bugs the test suite cannot see.

```bash
nix develop                          # devShell provides temporal-cli, sqlite3, jq
make smoke-temporal                  # Temporal E2E smoke (aura-plugins-cn5ax)
```

`make smoke-temporal` boots a local Temporal dev server on ports 17233/18233
(non-default so it doesn't collide with a long-running personal Temporal),
starts `pastured` against a fresh sqlite db, creates a REQUEST task, kicks off
an EpochWorkflow, advances one phase, and asserts:

- `tasks` row exists for the REQUEST
- `audit_events` rows recorded
- `context_edges` rows link events to the epoch with `kind=EpochContext`
- Temporal search attributes `PastureEpochId` / `PasturePhase` upserted on the workflow

Set `KEEP_WORKDIR=1` to preserve the tempdir (db + logs) after the run for
debugging. Override `TEMPORAL_PORT` / `TEMPORAL_UI_PORT` if 17233/18233 are
taken. See `scripts/smoke/temporal-e2e.sh` for the full script.

## Build

```bash
make build          # produces bin/pastured, bin/pasture-msg, bin/pasture-release
make test           # go test -race ./...
make lint           # go vet ./...
make fmt            # gofmt -w .
make clean          # rm -rf bin/
```

Cross-compilation (all platforms):
```bash
GOOS=linux   GOARCH=amd64  CGO_ENABLED=0 go build ./cmd/pastured
GOOS=darwin  GOARCH=arm64  CGO_ENABLED=0 go build ./cmd/pastured
GOOS=windows GOARCH=amd64  CGO_ENABLED=0 go build ./cmd/pastured
```

## Temporal Conventions

- Signal and query names live in `internal/temporal/constants.go` as typed constants.
- Never hardcode signal/query name strings at call sites — always use the constants.
- Workflow and activity implementations live in `internal/temporal/`.

### Why Temporal: observability + introspection

Temporal was chosen as the workflow substrate specifically because it
*provides* the observability and introspection surface the toolkit needs.
There is no separate Pasture-side "introspection layer" to build — Temporal
already gives:

- **Live state** — `pasture-msg query state --epoch-id <id>` queries the
  running workflow's current `EpochState` via Temporal's workflow-query API.
- **Filterable cross-workflow listing** — six search attributes upserted by
  every workflow (`PastureEpochId`, `PasturePhase`, `PastureRole`, `PastureStatus`,
  `PastureDomain`, `PastureLastEventType`) make any open epoch greppable, e.g.
  `temporal workflow list -q "PasturePhase = 'elicit'"`. The SA wire names are
  declared in `internal/temporal/search_attributes.go`.
- **UI + history replay** — the Temporal UI on port 8233 (and
  `temporal workflow show`) provide per-workflow timelines, event histories,
  and replay tooling without any code on our side.
- **Durable substrate** — `pasture.db` `audit_events` + `context_edges` hold
  the canonical historical record outside of Temporal's retention window.

The **join key** that makes these views coherent is the D5/R13 binding from
PROPOSAL-2: `epoch-id = Provenance TaskID = Temporal workflow ID =
audit_events context key`. A single string flows through the whole stack
without translation. That's why §7.12 validation rejects malformed epoch-ids
at workflow start — losing the alignment would break the introspection
surface.

When debugging "where am I in this workflow?", the layers map cleanly:

| Question | Tool |
|---|---|
| What's the current phase / role / status? | `pasture-msg query state` (live, via Temporal query) |
| Which workflows are currently in phase X? | `temporal workflow list -q "PasturePhase = 'X'"` |
| What events have I emitted so far? | `pasture task events --epoch-id <id>` (durable, via `pasture.db`) |
| Show the timeline for one task. | `pasture task timeline <task-id>` |
| Visualise everything for one workflow. | Temporal UI at `localhost:8233` (or wherever `pastured --address` points) |

A unified status command that joins all of these in one call is tracked as
[`aura-plugins-punit`](beads://aura-plugins-punit); not yet built but not
load-bearing — today's two-CLI path is functional.

### Code generation vs runtime injection

The skill bodies in `skills/*/SKILL.md` are *generated at build time* from
the protocol schema. The generators live in two places:

- `scripts/aura_protocol/gen_skills.py` (the original Python generator, in
  the parent `aura-plugins/` repo) — **frozen / deprecated**
- `pasture/internal/codegen/skills.go` (the Go port) — **canonical / authoritative**

#### SKILL.md generation authority (audit `aura-plugins-5wbhm` — verdict: `qualified`)

**Verdict:** Go (`pasture/internal/codegen/skills.go`) is the authoritative
SKILL.md generation pipeline — *qualified* because that authority has been
verified only across the **8 overlapping skills**; the remaining **29
Python-only skills** are not yet ported (tracked by
[`aura-plugins-x5071`](beads://aura-plugins-x5071)).

**Verified-8 (Go authoritative, content-current):**

Diff lines are from the 2026-05-24 migration-doc inventory (both generators run
on a clean tree). Three buckets map to the "ahead-of or at-parity" predicate:

| Skill | Diff lines (2026-05-24) | Content currency | Nature |
|---|---:|---|---|
| `architect` | 56 | at-parity (structural) | Sort order, heading text, label placement — structural template difference; each side current w.r.t. its own template |
| `impl-review` | 25 | Go genuinely ahead | Go has full schema-driven body; Python frozen at 2026-02-23 hand-authored version (no template on Python side) |
| `reviewer` | 30 | at-parity (structural) | Sort order, heading patterns — structural template difference |
| `supervisor` | 208 | Go genuinely ahead | Go embeds Stage-3 ASCII flow diagram in generated block; Python retains a hand-authored `## Ride the Wave (Rewritten)` tail outside `END GENERATED` (decision pending per migration doc) |
| `supervisor-plan-tasks` | 27 | at-parity (structural) | Heading order, marker position — structural template difference |
| `supervisor-spawn-worker` | 33 | at-parity (structural) | Same shape as supervisor-plan-tasks |
| `worker` | 49 | Go genuinely ahead | Go has expanded verify step (`B-worker-verify-production` bullet), current `worker-slices` phase IDs; Python has hand-authored tail outside `END GENERATED` (Planning Backwards / TDD sections); structural drift in sort order |
| `protocol` | 0 | exact parity | In sync — 0-diff still counts as verified per audit UAT |

Key evidence: the 2026-05-24 regenerator run (both generators run on a clean
tree) produced **0 changes** for Go regen (`pasture/skills/` already in sync
with Go template output), confirming `pasture/skills/` is the canonical
current output. Python regen modified only `supervisor` (4 lines — wording
change), leaving all other Python skills behind the Go output.

**Note (7→8 off-by-one):** The user's original reworded claim said "7 skills";
the Phase-5 UAT resolved this as an off-by-one: `protocol` (0-diff/in-sync)
is the 8th overlapping skill and counts as verified. Residual
[`aura-plugins-acroy`](beads://aura-plugins-acroy) tracks the doc/ROADMAP
phrasing correction.

**29 Python-only SKILL.md-bearing skills (not yet ported — `x5071` scope):**

`architect-handoff`, `architect-propose-plan`, `architect-ratify`,
`architect-request-review`, `epoch`, `explore`, `feedback`, `impl-slice`,
`msg-ack`, `msg-broadcast`, `msg-receive`, `msg-send`, `plan`, `research`,
`reviewer-comment`, `reviewer-review-code`, `reviewer-review-plan`,
`reviewer-vote`, `status`, `supervisor-commit`, `supervisor-track-progress`,
`swarm`, `test`, `user-elicit`, `user-request`, `user-uat`, `worker-blocked`,
`worker-complete`, `worker-implement`.

These 29 exist only under `aura-plugins/skills/` and continue to use the
Python pipeline as reference; no immediate action required until ported.

**Count reconciliation (29, not 31 or 30):**
- `aura-plugins/skills/` has 38 directories; 37 have a SKILL.md (`templates`
  is an assets dir with no SKILL.md).
- Overlapping (both homes have SKILL.md): 8.
- Python-only with SKILL.md: 37 − 8 = **29**.
- The PROPOSAL used "31" (= 38 − 7), computed before `protocol` was recognized
  as the 8th overlapping skill. Correcting 7→8 necessarily corrects 31→30, and
  excluding `templates` (no SKILL.md) corrects 30→**29**.
- `x5071` description says "~30" and its enumerated list contains 30 entries,
  but includes `protocol` (now in the overlapping-8, already ported) and omits
  `templates` (no SKILL.md). `x5071`'s enumerated list is stale by one entry
  (`protocol` should be removed); do NOT refile — note the discrepancy here and
  let `acroy` absorb the doc-count fix.
- CI check for the 8 overlapping skills: tracked by
  [`aura-plugins-g8egz`](beads://aura-plugins-g8egz) (not yet filed as a
  working CI rule; filed as a follow-up task).

**Pasture-only skill (1):** `pasture/skills/install-cli/` — the Claude Code
skill installer. No Python counterpart.

The runtime equivalent — "load the right phase-context into a Claude session
when the workflow is at phase X" — is **not** a separate Go layer. The
context is loaded implicitly: the user (or a future SessionStart hook —
tracked as [`aura-plugins-oo359`](beads://aura-plugins-oo359)) invokes the
matching `/aura:*` skill for the current phase; Claude Code loads the
generated SKILL.md into the session context. Temporal answers the *where*
(via SAs); the skill system supplies the *what to do here*.

## Nix

A `flake.nix` at the repo root provides:
- `nix build .#pastured` — build the daemon
- `nix build .#pasture-msg` — build the CLI
- `nix build .#pasture-release` — build the release tool
- `nix develop` — dev shell with Go toolchain, gopls, sqlite, temporal-cli

## Commit Convention

Use Conventional Commits:
```
feat(pastured): add epoch start workflow
fix(pasture-msg): handle missing --session-id flag gracefully
chore: update go.sum after dependency bump
```

**IMPORTANT:** Workers must use `git agent-commit` instead of `git commit`:
```bash
git agent-commit -m "feat(pastured): add epoch start workflow"
```

## Protocol Evolution

For modifying or extending the protocol — adding or changing constraints, roles,
phases, figures, schema sections, commands, or templates — see
[CONTRIBUTING.md](CONTRIBUTING.md). That guide covers the `specs_data.go` →
`go generate` workflow, file-level dependency graph, and step-by-step recipes
for each operation.
