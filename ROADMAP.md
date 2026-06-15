# Pasture Roadmap

Pasture is the local DBOS-backed task, audit, and epoch-control runtime. The
current architecture uses one SQLite database (`pasture.db`) as the coordination
point:

- `pastured` hosts long-running DBOS workflows, queue dispatch, recovery, and
  hooks.
- `pasture` CLI commands submit durable workflow starts, cancellations, and
  signals through the DBOS database-backed client.
- Task, audit, migration, status, and query commands continue to read and write
  the shared database directly where no long-running workflow execution is
  required.

## Current Priorities

1. Keep epoch lifecycle execution hosted by `pastured`, not by short-lived CLI
   processes.
2. Preserve replay-stable forensic emission for workflow events and activity
   rows.
3. Keep crash recovery covered by an explicit CI gate.
4. Maintain read-only status/query surfaces that can inspect a cold database
   without launching workflow workers.

## Documentation Status

The non-legacy tree is DBOS-first. Any Python, Temporal, `pasture-msg`, or
`aurad` lifecycle documentation outside `legacy/` should be treated as stale
until it is rewritten against the DBOS host/client boundary above.
