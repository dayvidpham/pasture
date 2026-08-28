# Changelog

## [Unreleased]

### Changed
- `pastured` now chooses its exit code from the kind of failure instead of
  reporting 1 for everything: 1 for bad input or an unclassified failure, 2
  when the database cannot be opened, 3 when a stop does not finish inside its
  budget, 4 for a configuration problem, and 5 for a storage or schema
  failure. A stop that the operator asked for still exits 0, at any point in
  the daemon's life. Scripts and service units that treated any non-zero exit
  as the same fault need no change; those that test for exactly 1 must be
  updated.
- `pastured` reports a stop that did not finish in time. The message names the
  parts of the durable engine that were still running, and the process exits 3
  instead of 0, so a service manager sees that the stop was not orderly. Work
  that was cut off is left pending rather than cancelled, so the next start
  finishes it.

### Fixed
- `pastured` answers a stop signal that arrives while it is still starting.
  The signal was previously held until startup finished, so a daemon blocked
  on a slow database could be ended only by a kill.

## [0.0.7] - 2026-08-28

### Changed
- Provenance dependency updated to v0.0.6 (#112): a deadline-expired contended
  write always carries its SQLite busy evidence in the error chain (the busy
  error observed by any earlier acquisition attempt is joined into the deadline
  return, with a post-expiry zero-budget probe covering an interrupted first
  attempt). The receipt appender's contention-versus-deadline classification is
  therefore deterministic under load.

## [0.0.6] - 2026-08-26

### Changed
- Provenance dependency updated to v0.0.5 (#110): caller deadlines are now a
  real bound on contended SQLite writes (busy acquisition is retried until the
  deadline actually expires, with the busy detail preserved in the error
  chain), reference-data seeding runs through prepared statements, and fresh
  databases create the operation journal in its completed shape without an
  immediate rebuild.
- The governed-allocation composed request and result types carry their final
  names (`GovernedAllocationComposedRequest` / `...ComposedResult`); the
  transitional `ComposedBatch` names are gone (#109).

### Fixed
- Writer contention during lifecycle occurrence commits is classified from the
  error chain rather than from a timer race: a commit that stays contended
  until its ingress deadline reports the typed contention error (with the
  SQLite cause attached), and only a deadline that expires without contention
  reports the deadline error (#110).

## [0.0.5] - 2026-08-25

### Added
- Global multi-harness installer: `pasture install` / `pasture uninstall` for Claude Code, OpenCode, and Codex skills/agents/hooks, with a shared registry, per-cell reporting, exact preservation of external installs, and the Claude v0.0.4 monolith migration (#99)
- `pasture install status --json` and `pasture install plan` read-only surfaces; hidden `apply-selection` / `apply-cell` for Home Manager and automation (#99)
- `pasture bundle export`: canonical nine-cell release component archives, digests, and bundle IDs in a deterministic, documented format for the aggregate release pipeline (#101)
- Immutable release catalog: exact-tag GitHub release selection with checksum verification and redirect trust validation (#99)
- Production-CLI integration validation suite for the installer (`checks.installer`) (#100)

### Fixed
- `apply-selection` / `apply-cell` now exit non-zero when any cell fails, matching the human verbs (#100)
- Apply text rows print the live observation, so an untouched cell after a first-failure stop no longer reads as a success (#105)
- Durable-execution start-up retries when it loses the schema-bootstrap race against a concurrent process (#102)
- The provenance dependency pins the activation write-lock fix, removing an instant-SQLITE_BUSY failure under concurrent starts (#102, #103)
- `pasture --version` and `pastured --version` report the real release tag; unstamped builds report `devel` instead of a fictional version (#106)

## [0.0.4] - 2026-06-07

### Added
- feat(hook): pasture hook record — fail-hard gather, event id, --format json, repo+remotes (#18)
- feat(pasture): 6l5yo — graduate git_recorder.go via 'pasture hook record' (Manager path) (#17)

### Fixed
- fix(audit): resolve closed-but-unfixed review residuals (#16)

### Documentation
- docs: References & Internal Identifiers standard + scrub user-facing help text (#19)

### Other
- test(hooks): make NonBlocking dispatch test event-based (fix y8r75 flakiness) (#12)
- ci(release): tag-on-merge — auto-tag + publish when a version bump lands on main (#10)

## [0.0.3] - 2026-06-05

### Other
- registry sync-versions: newest-wins marketplace reconciliation + interactive confirm (#7)

## [0.0.2] - 2026-06-04

### Other
- Epoch-protocol improvements I1–I10 + Impl-UAT-2 refinements (#6)

