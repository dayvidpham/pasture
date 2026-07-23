// Package tasks — journaled task-backend system identity (#43).
//
// system_identity.go resolves the (committing actor, governing authority) pair
// the task backend binds every journaled mutation to. Since the provenance main
// upgrade (journal architecture amendment), task mutations no longer flow through
// a direct-write Tracker verb; they flow through a Session obtained from
// Tracker.As(actor, authority), and every Session verb commits one logical
// operation through the ordered journal under that actor and authority.
//
// The task backend needs a stable system identity to commit under. This file
// bootstraps it once and persists it in the pasture-side singleton
// pasture_system_identity so every later open reuses the same pair:
//
//  1. Atomically activate the reserved pasture-system actor namespace, ordinal
//     range [0, 1023], and manifest-v1 ordinal-zero software agent through
//     provadapter.ActivatePastureSystem.
//  2. Use that deterministic pasture-system/default ActorID as the committer.
//  3. Establish the genesis bootstrap authority (one EffectBootstrapAuthority
//     operation under a nil parent authority) and bind Tracker.As to its JournalID.
//
// Concurrency: Provenance atomically converges the fixed actor. The singleton is
// the authority serialization point: racing first opens may each establish a
// genesis authority, but only one row wins INSERT OR IGNORE and every caller
// re-reads that persisted winner.

package tasks

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/provadapter"
)

// systemSession returns the Session bound to the pasture-system committing actor
// and the genesis bootstrap authority, bootstrapping and persisting that identity
// on first use. The result is cached for the lifetime of the tracker so every
// mutation on this handle commits under the same (actor, authority) pair. Safe for
// concurrent callers (guarded by sysOnce).
func (t *trackerImpl) systemSession() (*provenance.Session, error) {
	t.sysOnce.Do(func() {
		t.sysSession, t.sysErr = t.bootstrapSystemSession()
	})
	return t.sysSession, t.sysErr
}

// bootstrapSystemSession is the one-time resolution behind systemSession.
func (t *trackerImpl) bootstrapSystemSession() (*provenance.Session, error) {
	if err := t.ensurePastureTablesOnce(); err != nil {
		return nil, err
	}

	// Atomically converge the claim, reserved range, software agent, and manifest
	// entry before consulting the persisted session identity. This also repairs a
	// claim-only store created before fixed-agent activation was available.
	activation, err := provadapter.ActivatePastureSystem(t.prov)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't reserve its system actor namespace before recording a task change.",
			Why: "Every journaled task change is committed under the reserved pasture-system\n" +
				"identity, and reserving that namespace over the task store failed.",
			Where: "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
			Impact: "No task can be created, updated, closed, linked, labelled, or commented on\n" +
				"until the namespace reservation succeeds.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the command once the database is healthy.",
			Cause: err,
		}
	}

	committer := activation.DefaultActorID

	// Fast path: preserve an existing genesis authority, but replace an obsolete
	// random fallback committer with the deterministic ordinal-zero actor.
	if actor, authority, found, err := readSystemIdentity(t.auditDB); err != nil {
		return nil, err
	} else if found {
		if actor != committer {
			if err := updateSystemIdentityActor(t.auditDB, committer); err != nil {
				return nil, err
			}
		}
		return t.prov.As(committer, authority), nil
	}

	authority, err := establishGenesisAuthority(t.prov.Journal(), committer)
	if err != nil {
		return nil, err
	}

	if err := writeSystemIdentity(t.auditDB, committer, authority); err != nil {
		return nil, err
	}

	// Re-read so a concurrent first-open race resolves to the single persisted
	// winner rather than this call's (possibly losing) local values.
	actor, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture wrote its system identity but couldn't read it back.",
			Why: "The pasture_system_identity singleton was just written, yet the follow-up\n" +
				"read returned no row. This points at a storage-consistency problem.",
			Where:  "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
			Impact: "The task backend has no identity to commit under, so no task change can proceed.",
			Fix: "1. Confirm the database file is not being truncated or replaced by another process.\n" +
				"2. Re-open the database and retry the command.",
		}
	}
	return t.prov.As(actor, authority), nil
}

// establishGenesisAuthority commits one genesis bootstrap-authority operation (a
// nil-parent EffectBootstrapAuthority) under the committer and returns the produced
// authority's JournalID — the system root every task-governing Session binds to.
func establishGenesisAuthority(j provenance.JournalAPI, committer provenance.ActorID) (provenance.JournalID, error) {
	res, err := j.Apply(provenance.OperationInput{
		OperationID:   provenance.OperationID("pasture.system.genesis." + uuid.Must(uuid.NewV7()).String()),
		ActorID:       committer,
		CommandDigest: []byte("pasture-system-genesis-command"),
		RecordedAt:    time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:           provenance.EffectBootstrapAuthority,
			BootstrapLabel: provadapter.PastureSystemNamespace,
			ResultSlot:     "auth",
		}},
	})
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't establish the genesis authority for its task journal.",
			Why: "The task backend commits every change through the ordered journal under a\n" +
				"genesis bootstrap authority, and creating that authority failed.",
			Where:  "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.establishGenesisAuthority).",
			Impact: "No task change can be recorded until the genesis authority exists.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the command once the database is healthy.",
			Cause: err,
		}
	}
	for i := range res.ResultSlots {
		if string(res.ResultSlots[i].Slot) == "auth" {
			return res.ResultSlots[i].ProducedJournalID, nil
		}
	}
	return 0, &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Pasture's genesis operation produced no authority.",
		Why: "The genesis bootstrap-authority operation committed but returned no \"auth\"\n" +
			"result slot, so the produced authority's journal position is unknown.",
		Where:  "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.establishGenesisAuthority).",
		Impact: "The task backend cannot bind to a governing authority, so no task change can proceed.",
		Fix: "This indicates an incompatible provenance journal build; re-pin the provenance\n" +
			"dependency to a version whose bootstrap authority exposes an \"auth\" result slot.",
	}
}

// readSystemIdentity reads the persisted (committer actor, genesis authority) pair.
// found is false (with a nil error) when the singleton has not been written yet.
func readSystemIdentity(db *sql.DB) (actor provenance.ActorID, authority provenance.JournalID, found bool, err error) {
	var actorStr string
	var authInt int64
	scanErr := db.QueryRow(
		`SELECT committer_actor_id, genesis_authority_journal_id
		 FROM pasture_system_identity WHERE singleton_id = 0`,
	).Scan(&actorStr, &authInt)
	if stderrors.Is(scanErr, sql.ErrNoRows) {
		return provenance.ActorID{}, 0, false, nil
	}
	if scanErr != nil {
		return provenance.ActorID{}, 0, false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't read its saved system identity.",
			Why:      "Tried to read the pasture_system_identity row but the read failed.",
			Where:    "Reading the task-backend system identity (internal/tasks/system_identity.go in tasks.readSystemIdentity).",
			Impact:   "The task backend can't resolve the identity to commit changes under.",
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the command once the database is healthy.",
			Cause: scanErr,
		}
	}
	parsed, parseErr := provenance.ParseActorID(actorStr)
	if parseErr != nil {
		return provenance.ActorID{}, 0, false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture's saved system identity %q is not a valid actor id.", actorStr),
			Why:      "The stored committer_actor_id could not be parsed back into an actor identity.",
			Where:    "Reading the task-backend system identity (internal/tasks/system_identity.go in tasks.readSystemIdentity).",
			Impact:   "The task backend can't commit changes under a corrupted identity.",
			Fix: "The pasture_system_identity row is corrupted. Investigate how it was written;\n" +
				"a clean re-initialisation of the system identity may be required.",
			Cause: parseErr,
		}
	}
	return parsed, provenance.JournalID(authInt), true, nil
}

// writeSystemIdentity persists the resolved identity as the singleton, idempotently
// (INSERT OR IGNORE keeps the first writer's row on a race).
func writeSystemIdentity(db *sql.DB, actor provenance.ActorID, authority provenance.JournalID) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO pasture_system_identity
		 (singleton_id, committer_actor_id, genesis_authority_journal_id)
		 VALUES (0, ?, ?)`,
		actor.String(), int64(authority),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't save its system identity.",
			Why:      "Tried to persist the pasture_system_identity row but the write failed.",
			Where:    "Persisting the task-backend system identity (internal/tasks/system_identity.go in tasks.writeSystemIdentity).",
			Impact: "The identity would have to be re-resolved on every open, and concurrent\n" +
				"opens could diverge on which identity governs task changes.",
			Fix: "1. Confirm the database is writable and the disk has free space:\n" +
				"     df -h .\n" +
				"2. Retry the command once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// updateSystemIdentityActor replaces only the persisted committer while retaining
// the existing genesis authority. It upgrades stores initialized with the former
// random fallback actor after fixed ordinal-zero activation became available.
func updateSystemIdentityActor(db *sql.DB, actor provenance.ActorID) error {
	result, err := db.Exec(
		`UPDATE pasture_system_identity SET committer_actor_id = ? WHERE singleton_id = 0`,
		actor.String(),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't upgrade its saved system identity to the fixed default actor.",
			Why:      "The ordinal-zero actor was activated, but updating pasture_system_identity failed.",
			Where:    "Upgrading the task-backend system identity (internal/tasks/system_identity.go in tasks.updateSystemIdentityActor).",
			Impact:   "The task backend will not mix the obsolete random committer with the fixed system identity.",
			Fix: "1. Confirm the database is writable and the disk has free space:\n" +
				"     df -h .\n" +
				"2. Retry the command once the database is healthy.",
			Cause: err,
		}
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't confirm its system identity upgrade.",
			Why:      fmt.Sprintf("Expected to update one pasture_system_identity row, but updated %d.", rows),
			Where:    "Upgrading the task-backend system identity (internal/tasks/system_identity.go in tasks.updateSystemIdentityActor).",
			Impact:   "The committing actor is unknown, so no task mutation will proceed.",
			Fix:      "Confirm the singleton row exists exactly once, then reopen the database and retry.",
			Cause:    err,
		}
	}
	return nil
}
