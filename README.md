# Pasture

Go implementation of the Aura Protocol codegen and workflow engine.

## What This Does

Pasture provides the runtime infrastructure for the Aura Protocol: a DBOS-backed durable engine (`pastured` daemon), a unified local CLI (`pasture`) for task management, epoch lifecycle, signals, and queries, and release tooling (`pasture-release`). The daemon orchestrates agent workflows with constraint validation, phase transitions, queue recovery, and audit trail logging. All task and audit operations route through a single `protocol.TaskTracker` facade against one shared SQLite file at `~/.local/share/pasture/pasture.db`. See `AGENTS.md` for the full architectural overview and `docs/adr/0001-pasture-toolkit-integration-architecture.md` (in the parent repo) for the integration ADR.

## Quick Start

Build and test:
```bash
make build          # produces bin/pastured, bin/pasture, bin/pasture-release
make test           # go test -race ./...
make lint           # go vet ./...
make fmt            # gofmt -w .
```

Or use Nix:
```bash
nix develop         # dev shell with Go, gopls, sqlite
nix build .#pastured
nix build .#pasture
```

## Running With The DBOS Backend

Pasture now uses DBOS Transact over the local SQLite file instead of a Temporal
server. There is no workflow server to install or manage. For durable background
work, run `pastured` as the DBOS engine host and point both `pastured` and
`pasture` at the same `pasture.db` file.

`pastured` currently requires a readable YAML config file. The DBOS defaults are
enough for local use, so a minimal durable config is:

```yaml
audit_trail: sqlite
```

Example:

```bash
$ printf 'audit_trail: sqlite\n' > /tmp/pasture-demo-config.yaml
$ export PASTURE_DB_PATH=/tmp/pasture-dbos-demo.db
$ pastured --config /tmp/pasture-demo-config.yaml --db "$PASTURE_DB_PATH" --slice-concurrency 4
2026/06/12 17:26:14 INFO pastured starting version=v0.1.0 dbPath=/tmp/pasture-dbos-demo.db auditTrail=sqlite sliceConcurrency=4
2026/06/12 17:26:15 INFO Initializing DBOS context app_name=pasture dbos_version=v0.16.0
2026/06/12 17:26:15 INFO Using custom SQLite system database handle
2026/06/12 17:26:15 INFO daemon runtime ready dbPath=/tmp/pasture-dbos-demo.db wellKnownAgents=15 hookRecorders=1 hasTracker=true
2026/06/12 17:26:15 INFO DBOS launched app_version=1 executor_id=pasture
2026/06/12 17:26:15 INFO DBOS engine launched, waiting for shutdown dbPath=/tmp/pasture-dbos-demo.db sliceConcurrency=4 hookRecorders=1
```

In another terminal, use the CLI against the same database:

```bash
$ pasture --db "$PASTURE_DB_PATH" task create "Demo DBOS epoch" --type task --priority high --phase request --format json
{
  "id": "https://github.com/dayvidpham/pasture--019ebe5f-8047-7f26-b00a-89a1ce877392",
  "title": "Demo DBOS epoch",
  "status": "open",
  "priority": "high",
  "type": "task",
  "phase": "request",
  "createdAt": "2026-06-13T00:26:30Z",
  "updatedAt": "2026-06-13T00:26:30Z"
}

$ pasture --db "$PASTURE_DB_PATH" epoch start --epoch-id https://github.com/dayvidpham/pasture--019ebe5f-8047-7f26-b00a-89a1ce877392
2026/06/12 17:26:35 INFO Initializing DBOS context app_name=dbos-client dbos_version=v0.16.0
2026/06/12 17:26:35 INFO Using custom SQLite system database handle
Started epoch: workflow_id=https://github.com/dayvidpham/pasture--019ebe5f-8047-7f26-b00a-89a1ce877392

$ pasture --db "$PASTURE_DB_PATH" phase advance --epoch-id https://github.com/dayvidpham/pasture--019ebe5f-8047-7f26-b00a-89a1ce877392 --to elicit --triggered-by worker --condition "request classified"
2026/06/12 17:26:37 INFO Initializing DBOS context app_name=dbos-client dbos_version=v0.16.0
2026/06/12 17:26:37 INFO Using custom SQLite system database handle
Signal delivered successfully

$ pasture --db "$PASTURE_DB_PATH" query current --epoch-id https://github.com/dayvidpham/pasture--019ebe5f-8047-7f26-b00a-89a1ce877392
Phase: elicit
Role:  user
```

Operational note: task, audit, migration, and read-only status/query commands
use the unified DBOS-backed SQLite file directly. Epoch lifecycle commands use a
lightweight DBOS client: `epoch start` enqueues the control workflow on
`pasture-control-queue`, and signal/cancel verbs write durable DBOS records for
the target workflow ID. `pastured` remains the long-running host that dequeues
and executes epoch control, slice/review queues, hooks, and recovery work.

## Project Structure

```
cmd/
  ├── pasture/         # Unified CLI: task management, epoch lifecycle, signals, queries, migrate
  ├── pastured/        # DBOS engine-host daemon entry point
  └── pasture-release/ # Release and versioning tool
internal/
  ├── acp/             # Agent Control Protocol client + adapter
  ├── audit/           # Audit trail + schema migrator (SQLite-backed)
  ├── config/          # Viper-based configuration
  ├── errors/          # Actionable error types
  ├── formatters/      # Output formatters (JSON, text, table)
  ├── handlers/        # Cobra RunE → standalone handler functions
  ├── hooks/           # Claude Code hook event handlers
  ├── tasks/           # protocol.TaskTracker implementation + well-known agent registry
  ├── engine/          # DBOS durable engine, projection, queues, recovery
  └── types/           # Internal aggregate types
legacy/
  └── temporal/        # Deprecated nested module preserving the old Temporal substrate
pkg/
  └── protocol/        # Public aura-protocol types — including protocol.TaskTracker
```

## CLI Surface (`pasture`)

The local `pasture` CLI hosts task verbs (`task create / show / update / close / list`,
`task ready`, `task blocked`, `task dep add|tree`, `task label add|remove`,
`task comment add`, `task comments`) and event/audit verbs:

| Subcommand | Purpose |
|---|---|
| `pasture task events [--epoch-id <id>] [--phase <p>] [--role <r>]` | Query audit events |
| `pasture task timeline TASK-ID` | Show all events attached to a task |
| `pasture task contexts EVENT-ID` | List context_edges attached to an event |
| `pasture task agents [list\|show]` | List or inspect registered agents |
| `pasture migrate [--dry-run]` | Run pending audit-database schema migrations (top-level — NOT under `pasture task`) |

## Key Conventions

- **No CGo:** All dependencies pure Go (`CGO_ENABLED=0`). Use `modernc.org/sqlite`, not `mattn/go-sqlite3`.
- **Strongly-typed enums:** Prefer typed constants over bare strings.
- **Actionable errors:** Every error describes what, why, where, when, and how to fix.
- **Test pattern:** `*_test.go` files import actual production code with dependency injection for mocks.

## Code Generation

The protocol `schema.xml`, registered skills, and tool-bearing role agents are
**generated**, not hand-maintained. Protocol facts (phases, roles, constraints,
commands, figures, and skill bodies) are declared once as typed Go values in
`internal/codegen/` and rendered for both Claude Code and OpenCode. The
hand-authored `protocol` and `install-cli` skills sit outside that generated
registry and are copied verbatim into the OpenCode target.

```bash
make generate                        # regenerate every committed target
go test ./internal/codegen/...       # completeness, parity, and sync guards
```

The data flows from `specs_data*.go` through `tools/codegen` to `schema.xml`,
the Claude Code trees (`skills/`, `agents/`), and the OpenCode trees
(`.opencode/skill/`, `.opencode/agent/`, `opencode.json`). For registered Claude
Code skills, the generator owns the complete content through the END marker;
maintained body prose belongs in `specs_data_body_<skill>.go`, not below that
marker. CI regenerates all targets on a clean checkout and fails on any resulting
worktree change; an exact output-inventory test also rejects retired files that
in-place generation cannot remove.

- **Architecture + data-flow diagram:** [docs/codegen.md](docs/codegen.md)
- **How to add a constraint / role / phase / section / command:** [CONTRIBUTING.md](CONTRIBUTING.md)

## Releasing

Releases are cut with `pasture-release` and tagged automatically on merge.

```bash
# On a non-`release/*` branch (the `release/**` pattern is ruleset-protected):
pasture-release patch --no-tag      # bump plugin.json + CHANGELOG, commit (no tag)
# → open a PR → merge to main → release.yml tags vX.Y.Z, builds the static
#   binaries (linux/darwin × amd64/arm64), and publishes the GitHub Release.
```

The tag-on-merge workflow needs the release GitHub App secrets (`RELEASE_APP_ID`,
`RELEASE_APP_PRIVATE_KEY`, with `Contents: write`). Full recipe (bump levels,
`--plugin` marketplace sync, troubleshooting) in
[CONTRIBUTING.md](CONTRIBUTING.md#releasing); versioning policy (what is
MAJOR/MINOR/PATCH per channel) in [docs/VERSIONING.md](docs/VERSIONING.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to evolve protocol concepts (adding
constraints, roles, phases, etc.) and how to release. See
[docs/codegen.md](docs/codegen.md) for the codegen pipeline architecture.
