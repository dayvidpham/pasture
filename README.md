# Pasture

Go implementation of the Aura Protocol codegen and workflow engine.

## What This Does

Pasture provides the runtime infrastructure for the Aura Protocol: a Temporal workflow engine (`pastured` daemon), CLI for sending protocol messages (`pasture-msg`), a local task + audit CLI (`pasture`), and release tooling (`pasture-release`). The daemon orchestrates agent workflows with constraint validation, phase transitions, and audit trail logging. All task and audit operations route through a single `protocol.TaskTracker` façade against one shared SQLite file at `~/.local/share/pasture/pasture.db`. See `AGENTS.md` for the full architectural overview and `docs/adr/0001-pasture-toolkit-integration-architecture.md` (in the parent repo) for the integration ADR.

## Quick Start

Build and test:
```bash
make build          # produces bin/pastured, bin/pasture-msg, bin/pasture-release
make test           # go test -race ./...
make lint           # go vet ./...
make fmt            # gofmt -w .
```

Or use Nix:
```bash
nix develop         # dev shell with Go, gopls, sqlite, temporal-cli
nix build .#pastured
nix build .#pasture-msg
```

## Project Structure

```
cmd/
  ├── pasture/         # Local task + audit CLI (pasture task + pasture migrate)
  ├── pastured/        # Temporal worker daemon entry point
  ├── pasture-msg/     # CLI for sending protocol messages
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
  ├── temporal/        # Temporal workflow/activity implementations
  └── types/           # Internal aggregate types
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

The skill files (`skills/*/SKILL.md`), agent definitions (`agents/*.md`), and the
protocol `schema.xml` are **generated**, not hand-maintained. The protocol facts
(phases, roles, constraints, commands, figures, skill bodies) are declared once as
typed Go values in `internal/codegen/` and rendered into all three artefacts, so
they can never drift out of sync.

```bash
go generate ./internal/codegen/...   # regenerate schema.xml + skills/ + agents/
go test ./internal/codegen/...       # completeness + sync guards
```

The data flows `specs_data*.go` (source of truth) → `tools/codegen` (4 stages) →
`schema.xml` + `skills/<dir>/SKILL.md` + `agents/<role>.md`. SKILL.md files keep
a hand-authored tail below a `<!-- END GENERATED ... -->` marker that the
generator preserves.

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
