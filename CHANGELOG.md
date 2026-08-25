# Changelog

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

