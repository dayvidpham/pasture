# Pasture

Go implementation of the Aura Protocol codegen and workflow engine.

## What This Does

Pasture provides the runtime infrastructure for the Aura Protocol: a Temporal workflow engine (`pastured` daemon), CLI for sending protocol messages (`pasture-msg`), and release tooling (`pasture-release`). The daemon orchestrates agent workflows with constraint validation, phase transitions, and audit trail logging.

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
  ├── pastured/        # Temporal worker daemon entry point
  ├── pasture-msg/     # CLI for sending protocol messages
  └── pasture-release/ # Release and versioning tool
internal/
  ├── acp/             # Agent Control Protocol client + adapter
  ├── audit/           # Audit trail (SQLite-backed)
  ├── config/          # Viper-based configuration
  ├── errors/          # Actionable error types
  ├── formatters/      # Output formatters (JSON, text, table)
  ├── handlers/        # Cobra RunE → standalone handler functions
  ├── hooks/           # Claude Code hook event handlers
  ├── temporal/        # Temporal workflow/activity implementations
  └── types/           # Internal aggregate types
pkg/
  └── protocol/        # Public aura-protocol types (importable by other modules)
```

## Key Conventions

- **No CGo:** All dependencies pure Go (`CGO_ENABLED=0`). Use `modernc.org/sqlite`, not `mattn/go-sqlite3`.
- **Strongly-typed enums:** Prefer typed constants over bare strings.
- **Actionable errors:** Every error describes what, why, where, when, and how to fix.
- **Test pattern:** `*_test.go` files import actual production code with dependency injection for mocks.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to evolve protocol concepts (adding constraints, roles, phases, etc.).
