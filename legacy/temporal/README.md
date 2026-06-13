# Legacy Temporal Substrate

This module is an archived, experimental copy of Pasture's pre-DBOS Temporal
worker implementation. It is preserved for reference and possible future
revival only.

Status:

- Deprecated: the root Pasture module now uses DBOS with SQLite.
- Not wired into root builds, release packages, tests, or `go mod tidy`.
- Not supported as the default runtime.
- Requires an explicit opt-in by changing into this directory and running Go
  commands against this nested module.

The active install path is the root module's `pastured` binary, which hosts the
DBOS engine and does not require a Temporal server.
