# Pasture — Agent Coding Standards

This document defines the coding conventions and quality gates for the Pasture
project. All contributors (human and AI) must follow these standards.

## Project Identity

- **Module:** `github.com/dayvidpham/pasture`
- **Binaries:**
  - `pastured` (DBOS engine-host daemon)
  - `pasture` (local task + audit CLI; routes through `protocol.TaskTracker`)
  - `pasture-release` (versioning)
- **Language:** Go 1.26+
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
co-exist in one file. PROPOSAL-2 §7.1 / D11 binds writers to SQLite WAL mode.
The `busy_timeout` that goes with it is taken from the shared timeout profile
(`internal/timeouts`) and is **500 ms** in the production profile; it is never a
literal in a DSN. The cross-subsystem race test in
`internal/tasks/tracker_race_test.go` (BLOCKER B3) exercises this path.

Pre-PROPOSAL-2 deployments used two separate files (`provenance.db` for the
`pasture` CLI, `audit.db` for `pastured`); the current DBOS runtime collapses
both to `pasture.db`. `pastured` accepts `--db`, and the shared fallback remains
`PASTURE_DB_PATH` / `tasks.DefaultDBPath()`. The old `--audit-db-path` alias has
been retired with the Temporal daemon role.

### Timeout tiers (`internal/timeouts`)

Every SQLite busy timeout, and the four deadlines above it, come from one
immutable `timeouts.Profile`. A SQLite lock wait must end before either caller
window can expire; both of those must end before one hook invocation runs out of
time; and that must end before the caller stops waiting for the whole workflow.
`Ingress` and `StartSlice` are siblings: neither is inside the other, and the
constructor does not order them against each other. It refuses any profile that
inverts the rest:

| Tier | Field | Production | Bounds |
|---|---|---|---|
| innermost | `SQLiteBusy` | **500 ms** | one SQLite lock wait inside the driver, set as the DSN `busy_timeout` |
| caller window | `Ingress` | **1 s** | one lifecycle receipt append, including its lock retries |
| caller window | `StartSlice` | **2 s** | how long a slice sub-workflow waits for its `start_slice` signal |
| host window | `HookInvocation` | **5 s** | how long one whole lifecycle hook invocation may take before it reports a fault to its host |
| outermost | `WorkflowResult` | **30 s** | how long a caller waits for a whole workflow to report a result |

The other two profiles keep the same ordering with different budgets:
`TestProfile` (500 ms / 2 s / 3 s / 6 s / 30 s) gives integration runs room for a
serialized writer queue, and
`DeadlineTestProfile` (25 ms / 250 ms / 500 ms / 1 s / 2 s)
is tight on purpose so tests can prove deadline-breach behaviour quickly.

`HookInvocation` is the budget the HOST pays for. A host freezes while it waits
for a lifecycle hook, so the tier sits below the smallest host budget with
headroom for process start: Claude Code allows a hook 10 s, Codex allows far
longer, and the OpenCode plugin awaits the child process with no timeout of its
own. The hook enforces this deadline around its own work rather than only handing
a context down, because the retry ceilings below are longer than any host budget.

The tier bounds the WORK, not the whole process. After the deadline fires, three
things still run outside it: mapping the fault, appending one line to the
lifecycle fault record, and writing the two output streams. That is a fixed
number of local syscalls with no retry and no lock, so on a healthy filesystem
the guarantee holds in practice. If the fault record ever grows a retry or a
lock, it moves inside the bound.

The context does NOT reach the code that waits. `OpenTaskTracker` takes no
context and `audit.Migrate` has none either, so the retry loop below opens with
its own background context and runs to its own ceiling. The loops themselves do
honour a context; they are simply never given one. The goroutine and select at
the hook boundary is therefore the ONLY thing that bounds a lifecycle hook
invocation today. Threading a context through the opener is tracked separately,
and until it lands nothing may claim the deadline bounds the retry loop.

Two longer retry ceilings sit **above** the profile and are not part of it.
Both bound a retry loop, not a single wait, and both are 30 s:
`busyRetryCeiling` (`internal/audit/migrate.go`) bounds retrying a schema
migration that lost the file lock to another process, and `dbosRaceRetryCeiling`
(`internal/engine/dbosinit.go`) bounds re-attempting durable start-up after a
lost schema-bootstrap race.

Rules:

- Production code takes these values from the injected profile. It must not
  write a duration or a `busy_timeout(...)` DSN literal of its own.
- `guard.CheckTimeoutSource` is a narrow check, not a general one. It parses the
  files listed in `internal/lifecycle/guard/timeouts_test.go` (seven today) and
  reports exactly two things: use of the retired `DefaultIngressDeadline`
  identifier, and a string literal carrying the retired five-second
  `busy_timeout` pragma (the exact text it matches is in
  `internal/lifecycle/guard/timeouts.go`). It does not see any other hard-coded
  duration, and it does not look at any file outside that list. Add a file to the
  list when that file starts to carry a timeout.
- A change to a tier is a change to observable behaviour under load. State the
  measurement that justifies it, and keep the ordering strict.

### The lifecycle fault record

A lifecycle hook that cannot evaluate its event appends one JSON line to
`lifecycle-faults.jsonl`, in the same directory as the database the invocation
would have used (`~/.local/share/pasture/` by default). It sits beside the
database and not inside it because the commonest fault is that the database
could not be opened, and evidence that needs the failing store is lost exactly
when it is wanted.

THE LINE IS NOT ALWAYS WRITTEN, and the promise above holds only while the
record can be both PLACED and WRITTEN. The writer has FIVE guarded failures.
FOUR of them are routes a user can reach, and each one loses the record for that
fault. In the order the writer meets them:

1. The resolved store path NAMES NO DIRECTORY, so the record has nothing to sit
   beside (`--db pasture.db`, or `PASTURE_DB_PATH=pasture.db` with no flag).
2. That directory CANNOT BE CREATED — a parent that is a file, or a parent the
   user may not write to.
3. The record file CANNOT BE OPENED although its directory exists — the
   directory is read-only or owned by another user, the filesystem is mounted
   read-only, or something that is not a regular file already stands at that
   name.
4. The line CANNOT BE APPENDED to a file that did open — the filesystem or the
   user's quota is full, or the device reports an I/O error.

The FIFTH failure, a record line that cannot be encoded, is unreachable by
construction: every member of the line is a string or a slice of strings, and
the JSON encoder cannot refuse those. It is counted here because the writer
reports it like the rest, not because a user can meet it.

ROUTES 1 AND 2 ARE ABOUT PLACING THE FILE, ROUTES 3 AND 4 ARE ABOUT WRITING IT,
AND A DIRECTORY THAT EXISTS DOES NOT RULE THE SECOND PAIR OUT. Measured on the
built binary with a store directory that exists and is read-only: exit 0,
nothing at all on standard output, the open failure reported on standard error,
and no record file anywhere. The default path is not exempt from routes 3 and 4
either — `~/.local/share/pasture/` is an ordinary directory on an ordinary
filesystem, so it can be full, read-only, or owned by somebody else.

On every one of those routes there is NO record for that fault anywhere. Each
arm says so on standard error, which is the only channel left to it — and it is
a channel the reader of this paragraph usually does not have, because a
fail-open fault exits 0 and most hosts do not show the standard error of an
exit-0 hook. So the honest statement is this: a fault leaves durable evidence
only where its record can be placed AND written, and on every other route the
operator learns of the loss only if the host shows the hook's standard error.

To get the record back, give `--db` or `PASTURE_DB_PATH` a path whose directory
exists or can be created, IS WRITABLE by the user running the hook, and sits on
a filesystem with space left. A directory that merely exists is not enough:
that is route 3.

A LINE'S `unusableFaultInputs` MEMBER IS ENGLISH, NOT A STABLE KEY. It is an
empty array on every fault pasture could classify, and on the one arm it could
not it carries a sentence per unusable input, worded for a person reading the
line. Those sentences may be reworded, so anything that GROUPS faults by cause
must not key on them. Writing such a reader is the trigger to give the result a
typed member and keep the sentence beside it — the sentence itself must stay
next to the condition, because that adjacency is what stops the refusal and its
explanation from describing different things. This paragraph is the ONLY place
the trigger is written; the doc comment of `hostexit.Fault.UnusableInputs`
points here rather than restating it, and
`TestTheUnusableInputTriggerIsWrittenWhereAParserAuthorMeetsIt` fails if either
end of that pointer is removed, if this paragraph is moved OUT of this section,
or if that test is renamed while these two documents go on citing it.

THE FILE HAS NO RETENTION. One line of roughly 500 bytes is appended per
faulting invocation; nothing rotates, trims or removes a line, and no command
reads or clears it. A hook runs on every wired event, so a database that stays
broken accumulates one line per event, silently, because a fail-open fault exits
0 and most hosts do not show the standard error of an exit-0 hook. Delete the
file to clear it. Retention and reclaim are deferred, and this file is one of
the surfaces that work inherits.

A second unreclaimed surface comes from the same failure class. A lifecycle
invocation abandoned at its deadline can leave a committed payload blob with no
occurrence — one orphan per abandoned invocation, holding that invocation's raw
host payload, bounded by the 1 MiB ingress payload cap. The write order is
deliberate: an orphan blob is reclaimable, while a journal row naming an absent
blob is corruption. `receipt.SQLiteBlobStore.Reclaimable` identifies them and
nothing deletes them yet.

They can now be COUNTED. `pasture hook lifecycle orphans` reports how many
payload blobs no occurrence names, in text or JSON, and it deletes nothing.
`receipt.SQLiteBlobStore.ReclaimableCount` answers it from the same predicate
`Reclaimable` enumerates, so the operator surface and the abandonment invariant
can never describe different sets. The count rebuilds the disposable occurrence
projection from the journal first, exactly as `pasture hook lifecycle list`
does: taken against a projection that was never rebuilt, every blob would look
unnamed.

The number ships with its meaning, and the meaning is the point. Read alone, a
large count invites an operator to hunt for corruption — for the very state the
write order makes impossible. So the report says all three of these, and a test
pins each phrase:

1. **What an orphan is** — a payload blob that no recorded occurrence names,
   left by a hook invocation abandoned between its two durable writes, at most
   one per abandoned invocation.
2. **That it is expected and reclaimable, not damage** — the blob is written
   before the journal row deliberately, because a spare blob can be reclaimed
   later while a journal row naming an absent blob could not be repaired at all.
3. **What a large number means** — not corruption, but repeated abandonment, so
   the thing to investigate is the store contention that caused it.

The count lives on the read surface and NEVER on the hook path. That placement
is load-bearing, not tidiness: on the hook path the count would run inside the
`HookInvocation` deadline, and it reads the store, which is the resource that
contends. It would therefore be slowest under exactly the condition that
produces orphans, and a slow enough count would push the invocation into its
deadline and leave one more orphan behind — making the counter a cause of the
thing it counts. A test asserts that no hook-path source calls it.

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

| Package | Pinned | Purpose |
|---------|--------|---------|
| `github.com/spf13/cobra` | (see `go.mod`) | CLI framework |
| `github.com/spf13/viper` | (see `go.mod`) | Configuration loading (TOML/YAML/env) |
| `github.com/dbos-inc/dbos-transact-golang` | v1.2.0 | Durable-execution substrate (DBOS Transact, SQLite backend) |
| `github.com/dayvidpham/provenance` | v0.0.7 | Task, edge and receipt store; built on the same DBOS version |
| `modernc.org/sqlite` | v1.54.0 | Pure-Go SQLite (audit trail, local state, DBOS system DB) |
| `modernc.org/libc` | v1.75.6 | Indirect, but pinned on purpose: v1.74.3 is retracted upstream, and module resolution selects it unless this floor is held |
| `golang.org/x/term` | (see `go.mod`) | Cross-platform terminal/isatty detection (sync-versions non-TTY guard) |

`go.mod` is the source of truth for every version. The three versions written
above are repeated here because a change to any of them changes behaviour the
operator sees, so a bump must be a deliberate act: the durable runtime version
decides which databases can be opened, and the two modernc versions decide
whether a pure-Go build is possible at all.

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
| 2 | `CategoryConnection` | Connection error (durable runtime unreachable, ACP unreachable, file open failure) |
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

**A seam only a test supplies needs a pin on what production supplies.** An
injected parameter buys parallelism, and it also creates a second supplier of a
value that used to be fixed. When the only other supplier is a test, nothing
fails if production later supplies the parameter DIFFERENTLY, so the seam has to
say what production passes. The lifecycle hook command has two such seams and
one assertion covers both. `handlers.CommitBarrier` names the boundary between
"the lifecycle receipt is durably committed" and "the host is told to continue";
production passes `handlers.PassThroughCommitBarrier{}`, which does nothing, and
one test passes a barrier that HOLDS the invocation at that boundary until the
test releases it, which makes the interleaving deterministic without a clock.
The deadline tier is the second: production passes `timeouts.ProductionProfile()`,
chosen against the smallest host budget, while that same test passes
`timeouts.DeadlineTestProfile()` so the expiry lands inside the held window. Neither is visible in any output, so a wrong wiring
produces no value a table can read — a barrier that ran work between the commit
and the continuation, or a tier that moved the deadline the host-budget claim
rests on, would keep every existing test green. The pin is therefore structural:
`TestTheProductionPathWiresThePassThroughBarrierAndTheProductionTier` parses
every non-test source of `cmd/pasture`, finds each `lifecycleOutcome` call, and
asserts the barrier and tier arguments verbatim, plus that there is exactly ONE
such call, because every guarantee the command makes is stated over one
host-facing path. Its scope is a glob and not a list of file names, so a source
added later is covered the day it is written rather than escaping in silence.

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

## Searching the Codebase

Use the weakest tool that can actually answer the question, and know what each
one cannot answer.

| Question | Tool |
|---|---|
| Structural shape — call sites, struct literals, signatures | `ast-grep` |
| Which type owns this symbol | `go/types` (extend `internal/lifecycle/guard`) |
| **Can I delete this?** | **The compiler.** Delete, build, read errors. |

**Never use `sg`.** On NixOS `sg` resolves to shadow-utils' setgid command, not
ast-grep. Always invoke `ast-grep` by full name.

**A zero result is not an absence proof.** This has produced three false
negatives to date:

- A regex missed a same-package reference (`BackendView`), because the call had
  no package qualifier to match on.
- The pattern `lifecycle\.(EventKind|EventByNativeName|Key)\b` matched none of
  the six real consumers (`BindEvent`, `Origin`, `Semantics`, `NativeEventName`,
  `CanonicalKey`, `ReplayKey`) and wrongly reported two files as deletable.
- `ast-grep -p '$X.ReplayKey()'` returned zero because the real references were
  a struct field and doc comments, not method calls — the pattern *shape* was
  wrong.

`ast-grep` is tree-sitter **syntax**-aware, not **type**-aware: `$X.Key()`
matches every type with a `Key()` method and cannot distinguish receivers. It
fixes multiline and formatting failures; it does not fix wrong-name or
type-resolution failures. Only the compiler does, and it is cheaper than both.

## Parallel Worker Isolation

**One worktree per worker slice.** Workers must never share a worktree.

```bash
# orchestrator: branch each slice off the integration branch
git worktree add -b <worktree-name> <repo-host>/worktree/<worktree-name> <integration-branch>

# orchestrator: after the slice lands and gates pass at the merge point
git worktree remove <path>
```

The repo root is the worktree **host** — never work directly in it. All feature
work lives in `worktree/<worktree-name>`.

A worktree is named `<primary-repo>-<issue#>--<semantic-commit>--<descriptive-name>`,
where the primary repo is the one the change centers on — so `pasture-44--feat--agent-integration`
here, `provenance-12--fix--bounded-apply` in a sibling repo. Branch name and
directory name are always identical. A change spanning several repos reuses the
same worktree name in every participating repo, which is what makes a cross-repo
change greppable after the fact.

Worktrees are cheap here — about 13 MB each, and `GOCACHE`/`GOMODCACHE` are
global, so build artifacts are shared rather than duplicated.

**Why:** workers sharing a tree observe each other's half-finished edits. A
worker can then report a false gate failure, debug someone else's incomplete
code, or conclude its own change is at fault and "fix" something that was never
broken. Verification is only trustworthy on a quiescent tree. The
`codegen-drift` CI job is especially exposed, since it keys off
`git status --porcelain` and any unrelated dirty file reads as drift.

**The tradeoff this creates — read before assuming isolation is free.**
Separate worktrees trade *build interference* for *merge divergence*. Isolation
does not remove cross-slice coupling; it hides it until merge. This has already
bitten: one slice deleted `internal/lifecycle/{event,key}.go` while another
slice's code still consumed six symbols from them. In a shared tree that broke
immediately and visibly. In isolated worktrees it would have surfaced only at
merge, after both slices had built further work on incompatible assumptions.

Therefore isolation is **mandatory but not sufficient**. Pair it with:

- **Declare Layer Integration Points up front** — any type, interface, or file
  one slice exports and another imports. Merge sooner, not later; divergence
  grows with delay.
- **Rebase onto the integration branch at every increment boundary**, not once
  at the end.
- **Run full gates at the merge point**, not only inside the slice worktree.
  Gates passing in isolation prove nothing about the merged tree.

**Merge conflicts are the orchestrator's job, not the worker's.** When a worker
wraps a slice, the orchestrator merges the integration branch into the slice
branch and resolves the conflicts. Ambiguous or confusing design choices are
surfaced to the user rather than settled unilaterally in a merge.

**Generated files are never hand-merged.** On a conflict in generated output
(`internal/lifecycle/registration/*.gen.go`, `internal/lifecycle/ingress/claude/
*.gen.go`, or anything else carrying a `Code generated … DO NOT EDIT` marker):
merge the *source* — the typed host contract the generator reads — keep the
target branch's generator configuration, and re-run `make generate`. The
committed output must be byte-identical to a fresh regeneration, which is
exactly what the `codegen-drift` CI job asserts via `git status --porcelain`.
Verify with a zero-diff regen before pushing; a hand-resolved `.gen.go` will
pass local review and fail that gate.

Commit hygiene is unchanged and still applies: stage only paths your slice owns,
never `git add -A`, and commit with `git agent-commit`.

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

### Work queues

- Queues are rows in the shared database, not per-process state. `RegisterQueue`
  writes the row with the `QueueConflictAlwaysUpdate` policy, so a starting
  process configures the queue every peer then reads. Every worker re-reads the
  row as it polls, about once a second.
- Two queues exist: `pasture-slice-queue` for slice and review sub-workflows,
  and `pasture-control-queue` for epoch control workflows. Slices and reviews
  share one concurrency budget K, because the bound protects one thing: the
  number of writers on the single `pasture.db` file.
- The control queue runs one workflow at a time in each process by design.
  `pasture queue concurrency set control ...` is refused with that reason.
- K can change while the daemon runs: `pasture queue concurrency set slice <n>`
  writes the stored setting, reads it back, and the daemon adopts it at its next
  poll. Work already running is not interrupted. The change lasts until a daemon
  starts again, because a start writes the limit the daemon was configured with.
  For a limit that survives a restart, set `--slice-concurrency` or
  `PASTURE_SLICE_CONCURRENCY`.
- Recovery returns a workflow to the queue it ran on. A recovered slice comes
  back to the slice queue and stays under K; a recovered epoch control workflow
  comes back to the control queue and stays under its limit of one, because
  production starts it by enqueueing it there
  (`dbosController.StartEpoch` in `internal/handlers/controller.go`). Work that
  ran on **no** queue is put on the runtime's own reserved queue instead, which
  pasture does not configure: no concurrency limit, fixed polling cadence.
  Nothing in production reaches that case — only a caller that starts a workflow
  directly with `RunWorkflow`, which today is test code.

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
rewrite repository files. The canonical command emits all supported targets:

- Claude Code: all registered skills under `skills/` and every tool-bearing role
  agent under `agents/`.
- OpenCode: the corresponding generated skills under `.opencode/skill/`, role
  agents under `.opencode/agent/`, plus `opencode.json`.
- Codex: compatible skills under `.agents/skills/`, role agents under
  `.codex/agents/`, and the generated lifecycle package under `.codex/`.
- The hand-authored `protocol` and `install-cli` skills are copied verbatim into
  the applicable non-Claude targets and are intentionally outside the generated-skill
  registry.

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
