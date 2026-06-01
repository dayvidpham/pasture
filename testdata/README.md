# pasture/testdata

Checked-in test fixtures for the pasture audit + tracker subsystems.

## `legacy_audit_v1.db`

A SQLite fixture representing the **legacy v1 audit-database shape** from
before PROPOSAL-2 landed (`audit_events(epoch_id, phase, role, event_type,
payload, timestamp)`; **no** `audit_schema_meta` table). Used by Scenario 4
(auto-migration on open), Scenario 11 (crash mid-migration recovery), and
Scenario 15 (explicit `pasture migrate` command) per PROPOSAL-2 §11.

### Composition (verbatim from PROPOSAL-2 §11 Scenario 4)

- **Total rows in `audit_events`:** 1024.
- **Schema:** legacy v1 (no `audit_schema_meta`).
- **Role distribution (sums to 1024):**
  - `architect` — 256
  - `supervisor` — 192
  - `worker` — 192
  - `reviewer` — 192
  - `automaton-checker` — 96
  - `human-david` — 64
  - `unknown-legacy` — 32
  - 7 distinct roles.
- **Phase distribution:**
  - All 12 PhaseId values (`p1-request` … `p12-postmortem`), round-robin.
  - **~100 rows of `phase=''` (empty string, NOT NULL satisfied)** — see
    "Empty-string phase rationale" below.
- **Payload edges:**
  - 64 rows: `{}` (empty object).
  - 32 rows: deeply-nested `{"a":{"b":{"c":{"d":"value"}}}}` (depth 4).
  - 16 rows: embedded UTF-8 (`{"note":"café"}`).
  - 16 rows: payloads >8 KB (large stringified data).
  - 4 rows: top-level arrays (`[1,2,3]`, exercising the JSON unmarshal
    path's tolerance for non-object roots).
- **Epoch-ID shapes:**
  - 768 rows: valid Provenance TaskIDs (`<namespace>--<uuid>`).
  - 192 rows: legacy free strings (`epoch-2026-04-22-mvp-XXX`).
  - 64 rows: `epoch_id` matching another row's (exercises EpochContext
    de-duplication during S4's v3→v4 migration).

### Empty-string phase rationale (TEAM-LEAD BINDING DECISION)

PROPOSAL-2 §11 Scenario 4 calls for `~100 rows of NULL phase` for
free-floating events. The legacy v1 schema declares `phase TEXT NOT NULL`
(see `pasture/internal/audit/sqlite.go` pre-PROPOSAL-2 version), so NULL is
not actually permitted.

The team lead made the binding decision (bd comment on
`aura-plugins-k5g3o`, 2026-04-25) to use `phase=''` (empty string)
instead. Approved rationale:

- Empty string satisfies the `phase TEXT NOT NULL` constraint while still
  exercising the migrator's empty-vs-null branch logic (a real edge case
  worth keeping).
- Future readers MUST NOT "fix" the empty strings to NULL — doing so
  violates the v1 NOT NULL constraint and the fixture build program
  would fail to insert the rows.

This rationale is also documented inline in
`pasture/internal/audit/testdata/build_fixture.go` (the `freeFloatingPhaseCount`
constant + file-level package doc).

### Origin and regeneration

The fixture is hand-curated for diversity of test coverage. The
**canonical artifact** is the `.db` file checked in here; the build program
at `pasture/internal/audit/testdata/build_fixture.go` is committed
alongside as the reproducible recipe (per IMPL_PLAN §5.2).

To regenerate (e.g., after a row-composition change is approved by
re-elicitation):

```bash
cd pasture
go run -tags fixture ./internal/audit/testdata
git diff --stat testdata/legacy_audit_v1.db   # binary diff
git add testdata/legacy_audit_v1.db
```

The build program self-asserts the row counts before exiting; an
assertion failure leaves the half-built `.db` file on disk for inspection
and exits non-zero so the regeneration is detected.

### Maintenance policy

- The fixture is **immutable** once committed. Future schema changes that
  require a different legacy state get their own fixture (e.g.,
  `legacy_audit_v2.db` if a v2-shaped fixture is ever needed).
- Tests **copy** the fixture to `t.TempDir()` before mutating; never
  operate on the checked-in file. See the Scenario 4 test in
  `internal/audit/migrate_v3_backfill_test.go` for the pattern.
- If the fixture's binary content needs to change (e.g., a row-composition
  error is discovered), rerun the build program and check in the new
  `.db` with a commit message documenting the change.

### Consumers

- `internal/audit/migrate_v3_backfill_test.go` — Scenarios 4 + 11 (S3).
- `internal/audit/migrate_v3_v4_test.go` — Scenario 4 v4 invariants (S4,
  not yet landed).
- `cmd/pasture/migrate_test.go` — Scenario 15 dry-run + apply (S6, not
  yet landed).
