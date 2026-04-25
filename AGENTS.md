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

Open via the public constructor:

```go
import (
    "github.com/dayvidpham/pasture/pkg/protocol"
    _ "github.com/dayvidpham/pasture/internal/tasks" // wires OpenTaskTracker
)

tracker, err := protocol.OpenTaskTracker("") // empty path → DefaultDBPath()
if err != nil { /* StructuredError with CategoryConnection / CategoryStorage / CategoryValidation */ }
defer tracker.Close()
```

The blank import of `internal/tasks` is required because the constructor body
lives in the internal package (UAT-1 placement binding); `internal/tasks`'s
`init()` calls `protocol.RegisterOpenTaskTracker` to wire the implementation.
`pastured`'s `cmd/pastured/main.go` and the local `pasture` CLI's `cmd/pasture`
both already perform this wiring. External Go consumers (e.g. agent-data-leverage)
must do the same.

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
