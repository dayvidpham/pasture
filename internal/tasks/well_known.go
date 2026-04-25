// Package tasks — well_known.go
//
// Idempotent automaton-agent registration at `pastured` startup
// (PROPOSAL-2 §7.7.3, BLOCKER A2).
//
// The flow per well-known name:
//
//  1. Lookup-by-name in `pasture_well_known_agents` (O(1) via the UNIQUE index
//     on `name`). If found, recover the AgentID and skip steps 2-3 (idempotent
//     fast path; second-and-subsequent restarts hit only this branch).
//  2. If absent: call `provenance.Tracker.RegisterSoftwareAgent("pasture",
//     name, version, source)` — this mints one fresh UUIDv7 in the
//     Provenance subsystem (separate `*sql.DB` handle on the same file).
//  3. INSERT into `pasture_well_known_agents` (agent_id, name) AND
//     `pasture_agent_categories` (agent_id, automaton_role, pasture_role)
//     under one `BEGIN IMMEDIATE` transaction on the audit `*sql.DB`.
//
// Why a transaction on the audit handle (and not on Provenance's handle)?
// Provenance writes are serialised through its own connection pool; the
// only multi-statement consistency requirement is between the two
// pasture-side INSERTs (well_known + categories), which both live on the
// audit handle. If we wrote without a transaction and the daemon crashed
// after the first INSERT, a re-run would observe a half-registered agent
// (well_known row present but categories row absent) — the next restart's
// step 1 would skip step 2/3 entirely (idempotency triggers off
// well_known) and the categories row would be permanently absent.
//
// Ordering rationale (Provenance call BEFORE audit transaction):
//
//   - We need a real AgentID before we can write the audit-side rows; the
//     ID is what those rows store.
//   - If Provenance succeeds and the audit transaction then fails, the
//     SoftwareAgent is orphaned (mints a UUID with no pasture-side
//     attribution). We re-attempt on next restart: step 1 finds nothing
//     (no `pasture_well_known_agents` row), step 2 mints another fresh
//     UUID, and we move on. The first orphan stays — this is acceptable
//     because (a) duplicate SoftwareAgents in `agents_software` are not a
//     correctness violation, only a hygiene issue; (b) the cleanup cost is
//     bounded by retries-until-success; (c) orphan-detection / cleanup is
//     out of scope for S7.

package tasks

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// auditDBHolder is an unexported interface satisfied by any protocol.TaskTracker
// implementation that exposes its underlying audit *sql.DB handle.
//
// Why an interface instead of a concrete type assertion to *trackerImpl:
// RegisterWellKnownAgents only needs raw *sql.DB access for the per-name
// BEGIN IMMEDIATE transaction. By asserting against this narrow interface we
// decouple the function from the concrete implementation — any test fake or
// future alternative tracker that wires up a *sql.DB and exposes it here can
// be passed to RegisterWellKnownAgents without contributing a *trackerImpl.
//
// *trackerImpl satisfies this interface via its auditDBHandle() method in
// tracker.go. Test fakes satisfy it by embedding or implementing auditDBHandle.
type auditDBHolder interface {
	auditDBHandle() *sql.DB
}

// RegisterWellKnownAgents mints (or recovers) every entry in
// WellKnownAgents() against the supplied tracker and populates cache.
//
// Idempotency contract (Scenario 14): two consecutive calls against the same
// underlying database produce identical row counts in `agents`,
// `agents_software`, `pasture_well_known_agents`, and `pasture_agent_categories`,
// AND identical AgentIDs in `pasture_well_known_agents` (pointwise across
// startups).
//
// tracker MUST implement auditDBHolder (i.e. it must expose an audit *sql.DB
// handle via auditDBHandle). Both the production *trackerImpl and any test
// fake that wires up a real *sql.DB satisfy this. Callers that pass a tracker
// without the method get a CategoryConfig *StructuredError pointing at the
// wiring error.
//
// cache MUST be non-nil. After successful return, cache contains exactly
// WellKnownAgentCount entries; on any error, cache may contain a strict
// subset (entries up to and including the failure point are populated;
// later entries are not). Partial population is safe for restart recovery —
// the missing entries get filled in on the next call because of the
// fast-path on lookup hit.
//
// Errors are *pasterrors.StructuredError categorised as:
//
//   - CategoryConfig (exit 4): tracker is nil, does not implement auditDBHolder,
//     or its auditDBHandle() returns nil; cache is nil.
//   - CategoryStorage (exit 5): SQLite read/write or transaction failure.
//   - CategoryWorkflow (exit 3): Provenance RegisterSoftwareAgent failure.
//
// The function takes ctx for cancellation; long-running registrations
// (15 entries × {1 SELECT + maybe 1 RegisterSoftwareAgent + 2 INSERTs}) take
// well under a second on local SQLite, so context-deadline rejection is
// primarily about clean shutdown if the daemon is killed during startup.
func RegisterWellKnownAgents(ctx context.Context, tracker protocol.TaskTracker, cache *WellKnownAgentCache) error {
	if cache == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "tasks.RegisterWellKnownAgents: cache is nil",
			Why:      "callers must construct a WellKnownAgentCache via NewWellKnownAgentCache before invoking the registrar",
			Impact:   "no AgentIDs would be cached; activities that consult the cache (S8) would all fail with CategoryValidation MustGet errors",
			Fix:      "in cmd/pastured/main.go, instantiate `cache := tasks.NewWellKnownAgentCache()` before the call to RegisterWellKnownAgents",
		}
	}
	if tracker == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "tasks.RegisterWellKnownAgents: tracker is nil",
			Why:      "the unified protocol.TaskTracker must be opened (via tasks.OpenTaskTracker / protocol.OpenTaskTracker) before well-known agents can be registered",
			Impact:   "no agents would be registered and no cache would be populated; pastured cannot start the worker safely",
			Fix:      "open the tracker first (`tracker, err := tasks.OpenTaskTracker(dbPath)`); pass the resulting tracker to RegisterWellKnownAgents",
		}
	}

	dbHolder, ok := tracker.(auditDBHolder)
	if !ok {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     fmt.Sprintf("tasks.RegisterWellKnownAgents: tracker (%T) does not implement auditDBHolder", tracker),
			Why:      "the per-name registration step needs raw access to an audit *sql.DB to run a transaction across `pasture_well_known_agents` and `pasture_agent_categories`; the tracker must expose it via auditDBHandle()",
			Impact:   "well-known agent registration cannot proceed and the daemon cannot guarantee the BCNF integrity of the pasture-side tables",
			Fix:      "construct the tracker via tasks.OpenTaskTracker (or protocol.OpenTaskTracker); test fakes must implement auditDBHandle() *sql.DB on their type",
		}
	}
	auditDB := dbHolder.auditDBHandle()
	if auditDB == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     fmt.Sprintf("tasks.RegisterWellKnownAgents: tracker (%T).auditDBHandle() returned nil", tracker),
			Why:      "the tracker was constructed without an auxiliary *sql.DB; this indicates a programming error in the constructor or test helper",
			Impact:   "well-known agent registration cannot proceed; the daemon should fail fast rather than start without idempotent automaton attribution",
			Fix:      "ensure the tracker is opened via tasks.OpenTaskTracker (which always wires auditDB); test fakes must return a non-nil *sql.DB from auditDBHandle()",
		}
	}

	// Defensive: ensure pasture-side tables exist. tasks.OpenTaskTracker calls
	// this too (post-S2 via the migrator path; pre-S2 via ensurePastureTables);
	// repeating here means RegisterWellKnownAgents is robust to future
	// constructor refactors that drop the defensive call.
	if err := ensurePastureTables(auditDB); err != nil {
		return err
	}

	for _, spec := range WellKnownAgents() {
		if err := ctx.Err(); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryConfig,
				What:     fmt.Sprintf("tasks.RegisterWellKnownAgents: context cancelled after %d/%d entries (next was %q)", cache.Len(), WellKnownAgentCount, spec.Name),
				Why:      err.Error(),
				Impact:   "pastured startup was interrupted; some well-known agents are registered (cached entries) and some are not (will be retried on next start)",
				Fix:      "this is normally a graceful shutdown — restart the daemon to complete registration",
			}
		}

		id, err := ensureWellKnownAgent(ctx, auditDB, tracker, spec)
		if err != nil {
			return err
		}
		cache.set(spec.Name, id)
	}

	if cache.Len() != WellKnownAgentCount {
		// Defensive: the loop should populate exactly WellKnownAgentCount
		// entries on success. If we ever observe a count mismatch, surface
		// it loudly rather than silently let activities miss attribution.
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.RegisterWellKnownAgents: cache populated %d entries; expected %d", cache.Len(), WellKnownAgentCount),
			Why:      "the canonical registry returned a number of specs different from WellKnownAgentCount; this indicates a registry edit that did not update the count constant",
			Impact:   "downstream activities that MustGet on a missing entry would return CategoryValidation; the daemon would not detect the registry/constant drift before workflows began running",
			Fix:      "ensure WellKnownAgents() returns exactly WellKnownAgentCount entries; if the registry intentionally grew, bump the constant in well_known_registry.go",
		}
	}

	return nil
}

// ensureWellKnownAgent implements the lookup-then-register-then-insert flow
// for a single well-known name (PROPOSAL-2 §7.7.3 pseudocode). It returns
// the AgentID for the name (recovered from the database on a hit, freshly
// minted on a miss) and a *StructuredError if any step fails.
//
// auditDB is the audit subsystem's *sql.DB; provTracker is the provenance
// surface (we accept it via the protocol.TaskTracker because the only
// Provenance call we need is RegisterSoftwareAgent, which is in the
// embedded interface). The split keeps this helper unit-testable: tests
// can provide a real auditDB (file-backed t.TempDir()) and a real
// protocol.TaskTracker, no mocks needed.
//
// Error categorisation:
//
//   - CategoryStorage: SELECT, BEGIN, INSERT, COMMIT failures.
//   - CategoryWorkflow: Provenance RegisterSoftwareAgent failures (treated as
//     workflow because Provenance's surface is part of pasture's task-tracker
//     workflow façade).
func ensureWellKnownAgent(
	ctx context.Context,
	auditDB *sql.DB,
	provTracker protocol.TaskTracker,
	spec WellKnownAgentSpec,
) (provenance.AgentID, error) {
	if !spec.Role.IsValid() {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: spec for %q has invalid AutomatonRole %q", spec.Name, spec.Role),
			Why:      "the spec.Role value is not a member of protocol.AllAutomatonRoles; this is a registry-edit error",
			Impact:   "the agent cannot be registered with a valid pasture-side category; downstream JOINs would return an unknown role string",
			Fix:      "fix the WellKnownAgents() entry to use one of protocol.AllAutomatonRoles; AutomatonRoleNone is technically valid but semantically wrong for a well-known automaton — use a concrete role",
		}
	}

	// 1. Fast-path lookup. Run on the auditDB (no transaction needed for a
	//    single SELECT — the UNIQUE index on `name` is the consistency anchor).
	//    If we find a row, the agent is already registered; recover the
	//    AgentID and return without touching Provenance.
	var agentIDStr string
	err := auditDB.QueryRowContext(ctx,
		`SELECT agent_id FROM pasture_well_known_agents WHERE name = ?`,
		spec.Name,
	).Scan(&agentIDStr)
	switch {
	case err == nil:
		// Hit: parse and return.
		agentID, perr := provenance.ParseAgentID(agentIDStr)
		if perr != nil {
			return provenance.AgentID{}, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.ensureWellKnownAgent: pasture_well_known_agents.agent_id %q for name %q does not parse as a Provenance AgentID", agentIDStr, spec.Name),
				Why:      perr.Error(),
				Impact:   "the cached AgentID for this well-known name is unusable; downstream activities will fail to attribute audit events to this automaton",
				Fix:      fmt.Sprintf("inspect the row directly: `sqlite3 <db> 'SELECT * FROM pasture_well_known_agents WHERE name = %q'` — if the agent_id is corrupt, delete the row and restart pastured to re-mint", spec.Name),
			}
		}
		return agentID, nil
	case stderrors.Is(err, sql.ErrNoRows):
		// Miss: fall through to register + insert.
	default:
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: could not check pasture_well_known_agents for name=%q", spec.Name),
			Why:      err.Error(),
			Impact:   "daemon startup cannot guarantee idempotent automaton registration; restart will retry but may flap",
			Fix:      "verify the SQLite file is accessible and the schema is at v3 or higher (run `pasture migrate --dry-run` to inspect the on-disk version)",
		}
	}

	// 2. Mint a fresh SoftwareAgent through Provenance. This call writes to
	//    Provenance's *sql.DB (separate handle from auditDB on the same file).
	//    On success we get an AgentID; on failure we abort without touching
	//    the audit-side tables (so the next startup retries cleanly via the
	//    fast-path miss).
	sa, err := provTracker.RegisterSoftwareAgent(
		WellKnownAgentNamespace,
		spec.Name,
		WellKnownAgentVersion,
		WellKnownAgentSource,
	)
	if err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: provenance.RegisterSoftwareAgent failed for name=%q", spec.Name),
			Why:      err.Error(),
			Impact:   "no Provenance SoftwareAgent was minted for this name; pasture-side mapping rows cannot be inserted; the activity that needs this AgentID will fail with CategoryValidation",
			Fix:      "verify the unified pasture.db's `agents` and `agents_software` tables are writable and not corrupted; check Provenance logs for the underlying SQLite error",
		}
	}

	// 3. Insert mapping rows in ONE transaction on the audit handle. Either
	//    both INSERTs land or neither does. We do not use BEGIN IMMEDIATE
	//    explicitly here because the audit handle has SetMaxOpenConns(1) and
	//    busy_timeout=5000 — the implicit DEFERRED lock upgrades to RESERVED
	//    on first write under the same serialisation guarantees.
	tx, err := auditDB.BeginTx(ctx, nil)
	if err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: cannot begin transaction for name=%q (agent_id=%q)", spec.Name, sa.ID.String()),
			Why:      err.Error(),
			Impact:   "the pasture-side mapping rows cannot be written atomically; a Provenance SoftwareAgent has already been minted (orphan) and will not be referenced by `pasture_well_known_agents`",
			Fix:      "verify the SQLite file is writable; the orphan SoftwareAgent will be re-attempted on next startup and another orphan minted — clean up via a future audit-tool if accumulation becomes a problem",
		}
	}
	// Best-effort rollback on any error path; safe to call after Commit
	// because Tx.Rollback returns sql.ErrTxDone (which we ignore via the
	// blank assignment).
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		sa.ID.String(), spec.Name,
	); err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: INSERT into pasture_well_known_agents failed for name=%q (agent_id=%q)", spec.Name, sa.ID.String()),
			Why:      err.Error(),
			Impact:   "the well-known name is not bound to its AgentID on disk; the in-memory cache for this entry will be missing; downstream activities will fail with CategoryValidation",
			Fix:      "verify the schema is at v3+ and the SQLite file is writable; inspect the row layout via `sqlite3 <db> .schema pasture_well_known_agents`",
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pasture_agent_categories (agent_id, automaton_role, pasture_role)
		 VALUES (?, ?, 'None')`,
		sa.ID.String(), string(spec.Role),
	); err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: INSERT into pasture_agent_categories failed for name=%q (agent_id=%q automaton_role=%q)", spec.Name, sa.ID.String(), spec.Role),
			Why:      err.Error(),
			Impact:   "the agent's pasture-side category is not persisted; the well-known mapping row will be rolled back so the category INSERT can be retried atomically on next startup",
			Fix:      "verify the schema is at v3+; if the row already exists for a different agent_id, this indicates a registry/database mismatch — back up the file before running `pasture migrate` again",
		}
	}

	if err := tx.Commit(); err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.ensureWellKnownAgent: COMMIT failed for name=%q (agent_id=%q)", spec.Name, sa.ID.String()),
			Why:      err.Error(),
			Impact:   "neither pasture_well_known_agents nor pasture_agent_categories rows were persisted; a fresh Provenance SoftwareAgent has been orphaned",
			Fix:      "verify the SQLite file is writable and not held open by another writer; the orphan SoftwareAgent will be re-attempted on next startup",
		}
	}

	return sa.ID, nil
}
