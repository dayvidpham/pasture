# Pasture — Agent Coding Standards

This document defines the coding conventions and quality gates for the Pasture
project. All contributors (human and AI) must follow these standards.

## Project Identity

- **Module:** `github.com/dayvidpham/pasture`
- **Binaries:**
  - `pastured` (DBOS engine-host daemon)
  - `pasture` (local task + audit CLI; routes through `protocol.TaskTracker`)
  - `pasture-release` (versioning)
- **Language:** Go 1.25+
- **CGo:** disabled (`CGO_ENABLED=0`) — all dependencies must be pure Go

## Directory Structure

```
pasture/
├── cmd/
│   ├── pasture/         # Local Pasture CLI (task verbs + top-level migrate)
│   ├── pastured/        # DBOS engine-host daemon entry point
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
│   ├── engine/          # DBOS durable engine, projection, queues, recovery
│   └── types/           # Internal aggregate types (not for export)
├── legacy/
│   └── temporal/        # Deprecated nested module preserving old Temporal code
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
`pasture` CLI, `audit.db` for `pastured`); the current DBOS runtime collapses
both to `pasture.db`. `pastured` accepts `--db`, and the shared fallback remains
`PASTURE_DB_PATH` / `tasks.DefaultDBPath()`. The old `--audit-db-path` alias has
been retired with the Temporal daemon role.

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
| `github.com/dbos-inc/dbos-transact-golang` | Durable-execution substrate (DBOS Transact, SQLite backend) |
| `modernc.org/sqlite` | Pure-Go SQLite (audit trail, local state, DBOS system DB) |
| `golang.org/x/term` | Cross-platform terminal/isatty detection (sync-versions non-TTY guard) |

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
// cmd/pasture/epoch.go
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

## References & Internal Identifiers

Project-internal identifiers are meaningless to end users and external
contributors, and they rot over time (tasks close, proposals are superseded,
slices merge). They must never leak into shipped or external-facing artefacts.

**Rule — do NOT place either of the following in source code, user-facing
strings, or any external-facing artefact:**

1. **Beads task identifiers** — `<project>-xxxxx` task IDs, `beads://…` URIs, or
   any bare task reference.
2. **Pasture Protocol process artefacts** — phase/step names (`p3-propose`,
   `s10-review`), `PROPOSAL-N` / `URD` / `URE` / `SLICE-N` / `RATIFIED`,
   schema-section citations (`§7.1`), review labels (`BLOCKER B3`,
   `Scenario 14`), and decision/requirement codes (`D5`, `R13`).

The rule targets **source code** (comments and string literals) and anything an
**end user or downstream consumer** sees: CLI command help (`Use` / `Short` /
`Long`), flag descriptions, error messages (`StructuredError` What/Why/Impact/Fix
— the `Where` field may cite a source location), and log/CLI output.

**When you need to cite a document or decision, reference something durable and
resolvable:**

- an **actual file path** — e.g. `docs/proposals/PROPOSAL-2-pasture-workflow-record.md`,
  `docs/adr/0001-pasture-toolkit-integration-architecture.md`, `internal/tasks/paths.go`;
- or a **GitHub issue / PR URL** — e.g. `https://github.com/dayvidpham/pasture/issues/13`.

Never a bare task ID or a `beads://` URI.

**Exception — the protocol as subject matter.** Referencing the Pasture
Protocol's own vocabulary (phases, roles, constraints, slices) is legitimate
ONLY where the protocol *is* the domain being implemented:

- the code-generation / generation pipeline (`internal/codegen/`,
  `specs_data*.go`, templates, and the generated `skills/**` + `agents/**`);
- the multi-agent orchestration features that implement the protocol (the
  workflow / hooks / signal surfaces that drive epochs);
- **internal contributor & design documentation** — this file (`AGENTS.md`),
  `CONTRIBUTING.md`, `docs/proposals/**`, `docs/adr/**`, and similar. These
  documents exist to explain the system and its protocol, so citing proposals,
  slices, ADRs, decisions, BDD scenarios, and tracking tasks (including bare IDs
  and `beads://` links) is normal design rationale, not leakage. They are read
  by contributors, never shipped to end users.

There, phase and role names are domain terms, not process leakage. Everywhere
else — the local task CLI help, the audit/migrate commands, storage layers,
ordinary code comments — they are leakage and are forbidden.

```go
// Wrong — internal artefact in user-facing help / comment
Long: `…backed by the SQLite database at ~/.local/share/pasture/pasture.db (PROPOSAL-2 §7.1).`
// the daemon prefers --db (SLICE-10 collapsed the two files into one)

// Correct — durable reference, or none at all
Long: `…backed by the SQLite database at ~/.local/share/pasture/pasture.db.`
// the daemon prefers --db; rationale in docs/proposals/PROPOSAL-2-pasture-workflow-record.md
```

## Testing

### Mandatory flags
```bash
go test -race ./...
```
The `-race` flag is mandatory for all test runs.

### Test file conventions
- Test files: `*_test.go` using `package foo_test` (black-box) or `package foo` (white-box).
- Import the actual production package — never a test-only re-export.
- Use dependency injection (interface mocks) for external services (DBOS, SQLite).

### Fixtures (`testdata/` + `testutil.LoadFixtures`)

Table-style test data lives in per-package `testdata/<name>.yaml` files loaded
through `testutil.LoadFixtures` (`internal/testutil/fixtures.go`) rather than
inlined as Go literals. This keeps large scenario tables out of the test body and
lets several tests share one corpus.

- **Typed fixture names.** Fixtures are addressed by the `testutil.FixtureName`
  typed string, never a raw string literal. Each corpus has a named constant
  (`testutil.CLISmoke`, `testutil.ConfigLoading`, `testutil.ContentBlock`, …), so
  a mistyped path fails to compile instead of failing at runtime.
  `LoadFixtures(t, testutil.ConfigLoading, &fixtures)` reads
  `testdata/config_loading.yaml` (relative to the package under test) and
  unmarshals it into `fixtures` — see the real caller `TestResolve_YAMLFixtures`
  in `internal/config/viper_internal_test.go`.
- **Fail-fast on infrastructure errors.** `LoadFixtures` uses `require` (not
  `assert`): a missing or malformed fixture stops the test immediately with an
  actionable message (which file, which working directory) instead of proceeding
  with a zero-value target. Its testable core, `readFixture`, returns the error
  instead of calling `t.FailNow`, so the loader's own error paths are unit-tested
  in-package (`internal/testutil/fixtures_test.go`).
- **Strict decoding.** `readFixture` decodes with `KnownFields(true)`, so an
  unknown or mistyped key in a fixture (e.g. `want_stderr_exclude` instead of
  `want_stderr_excludes`) fails the test loudly instead of silently zero-valuing
  the field — which, for a skip-if-empty assertion field, would otherwise quietly
  disable that check.
- **Location.** Each package owns its `testdata/` directory; `LoadFixtures`
  always resolves `testdata/<name>.yaml` against the test's working directory, so
  fixtures live beside the tests that use them.

### Parallelism via dependency injection (pure core, serial shell)

Prefer `t.Parallel()`. The usual blocker is shared process-global state — chiefly
the environment, which `t.Setenv` mutates and which therefore *forbids*
`t.Parallel()`. The pattern that unlocks parallelism is to split the code under
test into (1) a **pure/injected core** that receives its external inputs as
parameters, and (2) a **thin shell** that reads the real process I/O and calls the
core. Test the core in parallel with fixture inputs; cover the shell with a single
serial test that proves the real wiring.

**Worked example — configuration resolution (`internal/config`).** The OS
environment boundary is injected as a `lookupEnv func(string) (string, bool)`
parameter instead of being read inline:

- **The seam.** `internal/config/viper.go` —
  `bindEnvVar(v *viper.Viper, viperKey, envVar string, lookupEnv func(string) (string, bool))`
  reads each env var through the injected `lookupEnv` rather than calling
  `os.Getenv`/`os.LookupEnv` directly. The unexported
  `resolvePasturedConfigWithFile(cmd, configFile, lookupEnv)` threads it through
  the whole resolution (defaults → config file → env → CLI flag). Ordering is
  load-bearing: `bindEnvVar` runs *before* `bindChangedFlag` so a changed flag
  overwrites the env value, yielding the `CLI > env > YAML > default` precedence.
- **Pure core, parallel (white-box).** `internal/config/viper_internal_test.go`
  is `package config`, so it can reach the unexported seam. Every case supplies a
  fixture map via `envMap(...)` as `lookupEnv` and calls `t.Parallel()`; nothing
  touches the process environment, so the full precedence matrix runs
  concurrently with no `os.Setenv` races.
- **Thin shell, serial (black-box).** `internal/config/viper_test.go` is
  `package config_test`. The single `TestPublicResolvers_ReadProcessEnv` uses
  `t.Setenv` to prove BOTH public entry points (`ResolvePasturedConfig` and
  `ResolvePasturedConfigFromFile`) wire `os.LookupEnv` as their env source.
  Because `t.Setenv` bans `t.Parallel()`, this test stays serial — but it is the
  *only* env-reading test for the resolution seam, so it never bottlenecks the
  suite.

The public entry points (`ResolvePasturedConfig`,
`ResolvePasturedConfigFromFile`) default `lookupEnv` to `os.LookupEnv`, so
production callers never see the seam.

Rule of thumb: if a test needs `t.Setenv` (or any global mutation) it must stay
serial — so push the logic behind an injected parameter, test that in parallel,
and keep exactly one serial test for the real-I/O wiring.

### Quality gates (must pass before every commit)
```bash
make fmt    # gofmt — fails if any file needs formatting
make lint   # go vet ./...
make test   # go test -race ./...
make build  # CGO_ENABLED=0 go build ./...
```

### Smoke tests

The unit/integration suite (`make test`) runs against the DBOS/SQLite runtime
and is the primary quality gate. The old Temporal smoke harness is preserved
only inside `legacy/temporal/` with the deprecated Temporal substrate.

## Build

```bash
make build          # produces bin/pastured, bin/pasture, bin/pasture-release
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

## DBOS Engine Conventions

- Signal/query topic names and payload types live in `pkg/protocol` as typed constants.
- Workflow implementations live in `internal/engine`.
- `pastured` is the long-running DBOS host. It wires `engine.Config` with
  `engine.DefaultExecutorID`, `engine.DefaultAppName`,
  `engine.DefaultApplicationVersion`, `HooksMgr`, tracker/trail, and the
  resolved slice queue concurrency.
- The root module must not require `go.temporal.io/*`. The old Temporal
  substrate is preserved only as the isolated deprecated nested module under
  `legacy/temporal/`.

When debugging "where am I in this workflow?", the layers map cleanly:

| Question | Tool |
|---|---|
| What's the current phase / role / status? | `pasture status --epoch-id <id>` |
| What events have I emitted so far? | `pasture task events --epoch-id <id>` |
| Show the timeline for one task. | `pasture task timeline <task-id>` |
| Inspect durable engine state directly. | SQLite tables in the shared `pasture.db` DBOS/projection/audit store |

### Code generation vs runtime injection

> Pipeline architecture + data-flow diagram: [docs/codegen.md](docs/codegen.md).
> Step-by-step change recipes: [CONTRIBUTING.md](CONTRIBUTING.md).

Pasture's typed Go codegen is the sole authority for generated protocol skills
and agents. Generation is explicit: run `make generate`; ordinary builds do not
rewrite repository files. The canonical command emits both supported harnesses:

- Claude Code: all registered skills under `skills/` and every tool-bearing role
  agent under `agents/`.
- OpenCode: the corresponding generated skills under `.opencode/skill/`, role
  agents under `.opencode/agent/`, plus `opencode.json`.
- The hand-authored `protocol` skill is copied verbatim into the OpenCode target
  and remains outside the generated-skill registry. The canonical `install-cli`
  body is rendered through typed target contexts so harness identity and
  invocation guidance cannot leak between outputs.

The source inventory is deliberately static and explicit:

- `specs_data.go` owns command, role, phase, constraint, figure, checklist, and
  workflow metadata.
- Each generated skill body is declared in exactly one
  `specs_data_body_<skill>.go` file.
- `specs_data_body.go` is the slim `SkillBodySpecs` registry; do not replace it
  with `init()` registration or reflection.
- `harness.go` owns the role and command emitter maps and target routing.

Registry tests require every generated skill directory to have exactly one
`CommandSpecs` metadata owner, one harness emitter, one `SkillBodySpecs` entry,
and one schema-order entry; roles likewise stay aligned with procedure steps.
`TestGeneratedOutputInventory` rejects retired files left behind by in-place
generation. The CI `Codegen Drift` job runs the all-target generator on a clean
checkout and rejects modified or newly created output. A source change that
affects output must therefore commit the regenerated files—and explicitly remove
retired files—in the same change.

At runtime, harnesses load the generated skill matching the current command or
role. Durable workflow state determines where execution is; the generated skill
supplies the instructions for what to do there.

## Nix

A `flake.nix` at the repo root provides:
- `nix build .#pastured` — build the daemon
- `nix build .#pasture` — build the CLI
- `nix build .#pasture-release` — build the release tool
- `nix develop` — dev shell with Go toolchain, gopls, sqlite

## Commit Convention

Use Conventional Commits:
```
feat(pastured): add epoch start workflow
fix(pasture): handle missing --session-id flag gracefully
chore: update go.sum after dependency bump
```

**IMPORTANT:** Workers must use `git agent-commit` instead of `git commit`:
```bash
git agent-commit -m "feat(pastured): add epoch start workflow"
```

## Releasing

Releases are cut by `pasture-release` and **tagged automatically on merge** (a git
tag is the unit of release). The short form:

```bash
git checkout -b chore/release-vX.Y.Z main   # NOT release/* — that pattern is ruleset-protected
pasture-release patch --no-tag              # bump plugin.json + CHANGELOG, commit (no local tag)
# → PR → merge to main → release.yml tags vX.Y.Z, builds the static binaries, publishes the Release
```

The tag-on-merge workflow needs the release GitHub App secrets (`RELEASE_APP_ID`,
`RELEASE_APP_PRIVATE_KEY`, `Contents: write`). After releasing, bump the pasture
entry in the parent `aura-plugins/.claude-plugin/marketplace.json`.

- **Full recipe** (flags, marketplace sync, `workflow_dispatch` recovery,
  troubleshooting the App-token push): [CONTRIBUTING.md](CONTRIBUTING.md#releasing).
- **Versioning policy** (MAJOR/MINOR/PATCH per consumption channel):
  [docs/VERSIONING.md](docs/VERSIONING.md).

## Protocol Evolution

For modifying or extending the protocol — adding or changing constraints, roles,
phases, figures, schema sections, commands, or templates — see
[CONTRIBUTING.md](CONTRIBUTING.md). That guide covers the `specs_data.go` →
`go generate` workflow, file-level dependency graph, and step-by-step recipes
for each operation.

### Repeating a constraint or prose fragment across multiple skills/agents (define once, reference by ID)

When the same rule must appear in more than one role, phase, or skill, **define
it once and reference it by ID** — never copy the text. Duplicated prose drifts:
each copy must be hand-updated and one always gets missed (this caused review
findings **C-MIN-1, C-MIN-2, A-IMP-1** this epoch). Define-once-by-ID keeps a
single source of truth; the `global_ids` parity check and `context_test`
exact-count assertions enforce consistency.

- **Same constraint into more roles/phases** — add its **ID** to the set in
  `internal/codegen/context.go` (`roleConstraints` / `phaseConstraints`). The one
  `ConstraintSpecs` definition then renders into each target's generated
  `skills/<role>/SKILL.md` **and** `agents/<role>.md`. Update
  `testdata/context.yaml` (`exact_count` +1, add to `must_contain`) in lockstep —
  `context_test` asserts exact equality. Do **not** restate the rule as new prose.
  - *Example:* `C-uat-feedback-disposition` attached to `RoleEpoch` (V2-PROP);
    `C-validation-cases` attached to `RoleSupervisor` (V4-PROP).
- **Same prose/behaviour into more skill bodies** — define it once in
  `SharedFragmentSpecs` (`specs_data_fragments.go`) + `AllFragmentIds`, and
  reference it via `fragRef()` / `behaviorRef()` in each consuming body. Never
  copy the text. (Fragments reach skill bodies only; agent definitions render
  only RoleSpec behaviors + attached constraints — use the constraint path for
  those.)
- **Hand-authored `skills/protocol/*.md`** — `CONSTRAINTS.md` is the single
  constraint catalog (one entry per ID); `PROCESS.md` / `CLAUDE.md` / `AGENTS.md`
  / `SKILL.md` **reference** constraints by ID (e.g. "per
  `C-uat-feedback-disposition`"), never restate them.

See the full recipe and worked examples in
[CONTRIBUTING.md](CONTRIBUTING.md#repeating-a-constraint-or-prose-fragment-across-multiple-skillsagents-define-once-reference-by-id).
