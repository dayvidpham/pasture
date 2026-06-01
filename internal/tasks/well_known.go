// Package tasks — well_known.go
//
// Idempotent automaton-agent registration at `pastured` startup
// (PROPOSAL-2 §7.7.3, BLOCKER A2).
//
// The flow per well-known name:
//
//  1. Lookup-by-name in `pasture_well_known_agents` (O(1) via the UNIQUE index
//     on `name`). If found, recover the AgentId and skip steps 2-3 (idempotent
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
//   - We need a real AgentId before we can write the audit-side rows; the
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
			What:     "Pasture tried to register its built-in agents but the cache is nil.",
			Why: "The code that called the registrar didn't allocate a cache to put the\n" +
				"results into. This is a wiring bug — a real cache must be passed in.",
			Where: "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
			Impact: "The daemon can't remember which agent ids belong to its built-in agents,\n" +
				"so later steps that need them will fail.",
			Fix: "1. In the daemon's startup code, allocate a cache before calling the\n" +
				"   registrar.\n" +
				"2. If you hit this from the CLI rather than from your own code, please\n" +
				"   file a bug — it shouldn't be reachable in normal use.",
		}
	}
	if tracker == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "Pasture tried to register its built-in agents but the task store is nil.",
			Why: "The code that called the registrar didn't open a task store first. We\n" +
				"need a working store before we can register anything in it.",
			Where:  "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
			Impact: "The daemon can't register its built-in agents and isn't safe to run.",
			Fix: "1. Open the task store first, then pass it to the registrar.\n" +
				"2. If you hit this from the CLI rather than from your own code, please\n" +
				"   file a bug — it shouldn't be reachable in normal use.",
		}
	}

	dbHolder, ok := tracker.(auditDBHolder)
	if !ok {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "Pasture got a task store that doesn't expose its database, so built-in agents can't be registered.",
			Why: "The task store passed in doesn't expose the internal hook the registrar\n" +
				"uses to write its bookkeeping rows together in one go. The built-in\n" +
				"task store has this hook; the value passed in doesn't.",
			Where: "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
			Impact: "Built-in agents can't be registered, and the daemon shouldn't start\n" +
				"without them — otherwise actions wouldn't be attributed correctly.",
			Fix: "1. Open the task store through the supported entry point.\n" +
				"2. If this is happening from a test that builds its own task store, make\n" +
				"   that test type expose the database the same way the built-in one does.",
		}
	}
	auditDB := dbHolder.auditDBHandle()
	if auditDB == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "Pasture got a task store with no open database connection, so built-in agents can't be registered.",
			Why: "The task store was constructed without a database connection. This is\n" +
				"a bug in the code that built it.",
			Where: "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
			Impact: "Built-in agents can't be registered, and the daemon shouldn't start\n" +
				"without them.",
			Fix: "1. Open the task store through the supported entry point so the database\n" +
				"   connection is wired up automatically.\n" +
				"2. If this is happening from a test that builds its own task store, make\n" +
				"   sure that store opens a real database file.",
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
				What:     "Pasture stopped registering its built-in agents because the daemon was asked to shut down.",
				Why: fmt.Sprintf(
					"Registration was interrupted after %d of %d agents (the next one was\n"+
						"%q).",
					cache.Len(), WellKnownAgentCount, spec.Name,
				),
				Where: "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
				Impact: "Some built-in agents are registered and some are not. The remaining\n" +
					"ones will be picked up the next time the daemon starts.",
				Fix: "1. If this happened during a normal shutdown, just start the daemon\n" +
					"   again when you're ready — registration will pick up where it left off:\n" +
					"     pastured\n" +
					"2. If you weren't expecting a shutdown, check the logs for the cause\n" +
					"   before restarting.",
				Cause: err,
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
			What:     "Pasture registered a different number of built-in agents than expected.",
			Why: fmt.Sprintf(
				"The list of built-in agents has %d entries, but the expected count is\n"+
					"%d. Someone added or removed an entry without updating the count.",
				cache.Len(), WellKnownAgentCount,
			),
			Where: "Registering built-in agents (internal/tasks/well_known.go in tasks.RegisterWellKnownAgents).",
			Impact: "Pasture can't tell whether the built-in agent registry is correct, so\n" +
				"actions that look up an agent by name might silently fail later.",
			Fix: "1. Decide whether the new total is correct.\n" +
				"2. Update the count constant to match the registry, or fix the registry to\n" +
				"   match the constant. Both live in:\n" +
				"     internal/tasks/well_known_registry.go",
		}
	}

	return nil
}

// ensureWellKnownAgent implements the lookup-then-register-then-insert flow
// for a single well-known name (PROPOSAL-2 §7.7.3 pseudocode). It returns
// the AgentId for the name (recovered from the database on a hit, freshly
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
			What:     fmt.Sprintf("Pasture's built-in agent %q is configured with an unknown role.", spec.Name),
			Why: fmt.Sprintf(
				"The role %q on this agent isn't one of the roles pasture knows about. The\n"+
					"built-in agent registry has a typo or a stale entry.",
				spec.Role,
			),
			Where: "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "This agent can't be registered. Anything that tries to use it later\n" +
				"will fail.",
			Fix: "1. Open the built-in agent list and fix the role on this entry:\n" +
				"     internal/tasks/well_known_registry.go\n" +
				"2. Use one of the named roles (for example,\n" +
				"   constraint-checker or hook-handler).",
		}
	}

	// 1. Fast-path lookup. Run on the auditDB (no transaction needed for a
	//    single SELECT — the UNIQUE index on `name` is the consistency anchor).
	//    If we find a row, the agent is already registered; recover the
	//    AgentId and return without touching Provenance.
	var agentIdStr string
	err := auditDB.QueryRowContext(ctx,
		`SELECT agent_id FROM pasture_well_known_agents WHERE name = ?`,
		spec.Name,
	).Scan(&agentIdStr)
	switch {
	case err == nil:
		// Hit: parse and return.
		agentId, perr := provenance.ParseAgentID(agentIdStr)
		if perr != nil {
			return provenance.AgentID{}, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The saved id for built-in agent %q is corrupted.", spec.Name),
				Why: fmt.Sprintf(
					"Pasture read %q out of the database for this agent, but it doesn't\n"+
						"look like a valid agent id.",
					agentIdStr,
				),
				Where: "Looking up a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
				Impact: "Anything that tries to attribute an action to this agent will fail\n" +
					"until the row is cleaned up.",
				Fix: fmt.Sprintf("1. Look at the broken row directly:\n"+
					"     sqlite3 <db-path> \\\n"+
					"       \"SELECT * FROM pasture_well_known_agents WHERE name = %q\"\n"+
					"2. Remove the broken row and restart pastured so a fresh id is created:\n"+
					"     sqlite3 <db-path> \\\n"+
					"       \"DELETE FROM pasture_well_known_agents WHERE name = %q\"\n"+
					"     pkill -f pastured && pastured\n"+
					"   Removing rows is destructive — back up the database file first.",
					spec.Name, spec.Name),
				Cause: perr,
			}
		}
		return agentId, nil
	case stderrors.Is(err, sql.ErrNoRows):
		// Miss: fall through to register + insert.
	default:
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't check whether the built-in agent %q is already registered.", spec.Name),
			Why:      "Reading from the database to look this agent up failed.",
			Where:    "Looking up a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "The daemon can't safely finish startup. Restarting will retry but may\n" +
				"hit the same error every time.",
			Fix: "1. Confirm the database is reachable and at the latest schema version:\n" +
				"     pasture migrate --dry-run\n" +
				"     pasture migrate\n" +
				"2. Restart the daemon once the database is healthy:\n" +
				"     pkill -f pastured && pastured",
			Cause: err,
		}
	}

	// 2. Mint a fresh SoftwareAgent through Provenance. This call writes to
	//    Provenance's *sql.DB (separate handle from auditDB on the same file).
	//    On success we get an AgentId; on failure we abort without touching
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
			What:     fmt.Sprintf("Pasture couldn't create a fresh agent record for the built-in agent %q.", spec.Name),
			Why:      "Tried to register the built-in agent in the task store but it failed.",
			Where:    "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "This agent isn't registered, and any work attributed to it later will\n" +
				"fail. The daemon shouldn't run without all built-in agents present.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Look for a more specific cause in the daemon's log output for this\n" +
				"   startup attempt — the line just before this error explains why the\n" +
				"   underlying database write failed.\n" +
				"3. Restart the daemon once the database is healthy:\n" +
				"     pkill -f pastured && pastured",
			Cause: err,
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
			What:     fmt.Sprintf("Pasture couldn't start a transaction to register the built-in agent %q.", spec.Name),
			Why: fmt.Sprintf(
				"A fresh agent record (id %q) was just created, but starting the database\n"+
					"transaction that would link the name to that id failed.",
				sa.ID.String(),
			),
			Where: "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "The agent isn't fully registered yet. The daemon will retry on the\n" +
				"next startup and create another fresh agent record, leaving the first\n" +
				"one unused in the database.",
			Fix: "1. Confirm the database file is writable and the disk has free space:\n" +
				"     df -h .\n" +
				"2. Restart the daemon to retry registration:\n" +
				"     pkill -f pastured && pastured\n" +
				"3. The leftover agent record is harmless but accumulates over time. If\n" +
				"   you see many of them, file an issue requesting a cleanup tool.",
			Cause: err,
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
			What:     fmt.Sprintf("Pasture couldn't save the name-to-id mapping for the built-in agent %q.", spec.Name),
			Why: fmt.Sprintf(
				"Tried to write the row binding name %q to id %q but the database refused.",
				spec.Name, sa.ID.String(),
			),
			Where: "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "The name can't be looked up later, so anything that needs this agent\n" +
				"by name will fail. The transaction is being rolled back so the database\n" +
				"is not left half-written.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. If you suspect the database was modified manually, inspect it:\n" +
				"     sqlite3 <db-path> \".schema\"\n" +
				"3. Restart the daemon once the database is healthy:\n" +
				"     pkill -f pastured && pastured",
			Cause: err,
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pasture_agent_categories (agent_id, automaton_role, pasture_role)
		 VALUES (?, ?, 'None')`,
		sa.ID.String(), string(spec.Role),
	); err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't save the role for the built-in agent %q.", spec.Name),
			Why: fmt.Sprintf(
				"Tried to set this agent's role to %q but the database refused.",
				spec.Role,
			),
			Where: "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "The role isn't saved. The transaction is being rolled back so the\n" +
				"name-to-id mapping is removed too, and the daemon can retry from a\n" +
				"clean slate on the next startup.",
			Fix: fmt.Sprintf("1. Confirm the database is at the latest schema version:\n"+
				"     pasture migrate\n"+
				"2. If a different id is already saved for this role, that's a registry vs.\n"+
				"   database mismatch — back up the database before running migrate again:\n"+
				"     cp <db-path> <db-path>.backup\n"+
				"3. Restart the daemon once the database is healthy:\n"+
				"     pkill -f pastured && pastured\n"+
				"   (Built-in agent in question: %q)",
				spec.Name),
			Cause: err,
		}
	}

	if err := tx.Commit(); err != nil {
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't finalise the registration of the built-in agent %q.", spec.Name),
			Why: "The two registration rows (name-to-id and role) were ready, but\n" +
				"committing them together failed.",
			Where: "Registering a built-in agent (internal/tasks/well_known.go in tasks.ensureWellKnownAgent).",
			Impact: "Neither row was saved. The fresh agent record will sit unused in the\n" +
				"database, and the daemon will retry registration on the next startup.",
			Fix: "1. Confirm nothing else is holding the database open exclusively:\n" +
				"     pgrep -af pastured\n" +
				"2. Confirm the database is writable and the disk has free space:\n" +
				"     df -h .\n" +
				"3. Restart the daemon once the database is healthy:\n" +
				"     pkill -f pastured && pastured",
			Cause: err,
		}
	}

	return sa.ID, nil
}
