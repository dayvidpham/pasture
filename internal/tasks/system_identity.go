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
// Concurrency and crash recovery: Provenance atomically converges the fixed actor,
// and every first-open attempt applies the same genesis OperationID, command, and
// canonical effect. A retry therefore returns the original authority even when a
// prior process committed genesis but crashed before writing the singleton. Racing
// opens persist and re-read that one authority through INSERT OR IGNORE.

package tasks

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/provadapter"
)

const (
	pastureSystemGenesisOperationID provenance.OperationID          = "pasture.system.genesis.v1"
	pastureSystemGenesisAuthorityID provenance.OperationAuthorityID = "pasture.system.genesis.authority.v1"
	pastureSystemGenesisResultSlot  provenance.ResultSlotID         = "auth"
	pastureSystemGenesisCommand                                     = "pasture-system-genesis-command-v1"
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

	expectedActor := provadapter.PastureSystemDefaultActorID()

	// Validate persisted identity before activation so corruption or an intentional
	// different actor cannot be masked by startup writes.
	if actor, authority, found, err := readSystemIdentity(t.auditDB); err != nil {
		return nil, err
	} else if found {
		if actor != expectedActor {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Pasture's saved system actor differs from the fixed default actor.",
				Why: fmt.Sprintf("The pasture_system_identity row names %q, but this build requires %q.",
					actor.String(), expectedActor.String()),
				Where:  "Validating the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
				Impact: "Bootstrap stopped before actor activation or journal mutation so the differing identity remains unchanged for investigation.",
				Fix: "Inspect the pasture_system_identity row and the journal authority it references. " +
					"Restore the expected fixed actor only through an explicit, reviewed data migration; normal startup will not rewrite it.",
			}
		}
		if err := validatePersistedGenesisAuthority(t.prov.Journal(), expectedActor, authority); err != nil {
			return nil, err
		}
		activation, err := provadapter.ActivatePastureSystem(t.prov)
		if err != nil {
			return nil, activationError(err)
		}
		if activation.DefaultActorID != expectedActor {
			return nil, unexpectedActivationActor(activation.DefaultActorID, expectedActor)
		}
		return t.prov.As(expectedActor, authority), nil
	}

	// With no singleton, atomically converge the claim, reserved range, software
	// agent, and manifest entry before establishing the replayable genesis.
	activation, err := provadapter.ActivatePastureSystem(t.prov)
	if err != nil {
		return nil, activationError(err)
	}
	committer := activation.DefaultActorID
	if committer != expectedActor {
		return nil, unexpectedActivationActor(committer, expectedActor)
	}

	authority, err := establishGenesisAuthority(t.prov.Journal(), committer)
	if err != nil {
		return nil, err
	}
	if t.afterGenesisCommit != nil {
		if err := t.afterGenesisCommit(authority); err != nil {
			return nil, err
		}
	}

	if err := writeSystemIdentity(t.auditDB, committer, authority); err != nil {
		return nil, err
	}
	expectedAuthority := authority

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
	if actor != committer || authority != expectedAuthority {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "A concurrent system identity write disagreed with the deterministic genesis result.",
			Why: fmt.Sprintf("The persisted winner is actor %q and authority %d, but bootstrap established actor %q and authority %d.",
				actor.String(), authority, committer.String(), expectedAuthority),
			Where:  "Re-reading the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
			Impact: "Pasture will not start a session under a foreign or ambiguous singleton winner.",
			Fix:    "Inspect pasture_system_identity and the deterministic genesis operation; reconcile them through an explicit reviewed migration before retrying.",
		}
	}
	return t.prov.As(actor, authority), nil
}

func validatePersistedGenesisAuthority(j provenance.JournalAPI, committer provenance.ActorID, authority provenance.JournalID) error {
	committed, err := j.LookupCommitted(pastureSystemGenesisOperationID)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't look up its saved genesis operation.",
			Why:      "Reading the deterministic genesis operation from the journal failed before canonical replay validation could begin.",
			Where:    "Validating the task-backend genesis authority (internal/tasks/system_identity.go in tasks.validatePersistedGenesisAuthority).",
			Impact:   "Bootstrap stopped without changing the saved identity or journal.",
			Fix:      "Verify journal integrity and database readability, then retry. Do not recreate the operation through normal startup while a singleton already exists.",
			Cause:    err,
		}
	}
	if committed.Kind != provenance.CommittedExact {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture's saved system identity references a missing genesis operation.",
			Why:      fmt.Sprintf("The singleton cites authority %d, but deterministic operation %q is absent from the journal.", authority, pastureSystemGenesisOperationID),
			Where:    "Validating the task-backend genesis authority (internal/tasks/system_identity.go in tasks.validatePersistedGenesisAuthority).",
			Impact:   "Bootstrap stopped before canonical replay, actor activation, or task mutation, so startup cannot silently create journal history behind an existing singleton.",
			Fix:      "Inspect the singleton and journal. Reconcile the missing deterministic genesis through an explicit reviewed migration, then retry.",
		}
	}

	result, err := j.Apply(pastureSystemGenesisInput(committer, time.Now().UTC().UnixNano()))
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't verify its saved genesis authority.",
			Why:      "Replaying the complete deterministic genesis identity through the journal failed.",
			Where:    "Validating the task-backend genesis authority (internal/tasks/system_identity.go in tasks.validatePersistedGenesisAuthority).",
			Impact:   "Bootstrap stopped before actor activation or task mutation rather than trusting an operation with a conflicting actor, authority, command, or effect.",
			Fix:      "Inspect the typed journal conflict and the saved singleton. Restore the canonical deterministic genesis only through an explicit reviewed migration, then retry.",
			Cause:    err,
		}
	}
	for _, slot := range result.ResultSlots {
		if result.Kind == provenance.CommittedExact && slot.Slot == pastureSystemGenesisResultSlot &&
			slot.ProducedJournalID == authority {
			return nil
		}
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Pasture's saved genesis authority does not match its deterministic genesis operation.",
		Why:      fmt.Sprintf("The singleton cites authority %d, but operation %q does not return that authority.", authority, pastureSystemGenesisOperationID),
		Where:    "Validating the task-backend genesis authority (internal/tasks/system_identity.go in tasks.validatePersistedGenesisAuthority).",
		Impact:   "Bootstrap stopped without changing the saved identity or journal.",
		Fix:      "Inspect the singleton and genesis journal result; use an explicit reviewed migration for any pre-deterministic development database.",
	}
}

func activationError(err error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Pasture couldn't activate its fixed system actor before recording a task change.",
		Why: "Every journaled task change is committed under the reserved pasture-system\n" +
			"identity, and atomically activating that identity over the task store failed.",
		Where: "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
		Impact: "No task can be created, updated, closed, linked, labelled, or commented on\n" +
			"until system actor activation succeeds.",
		Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
			"     pasture migrate\n" +
			"2. Retry the command once the database is healthy.",
		Cause: err,
	}
}

func unexpectedActivationActor(got, want provenance.ActorID) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Provenance activated an unexpected system actor.",
		Why:      fmt.Sprintf("Activation returned %q instead of manifest actor %q.", got.String(), want.String()),
		Where:    "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.bootstrapSystemSession).",
		Impact:   "Pasture will not bind journal operations to an ambiguous actor.",
		Fix:      "Verify the pinned Provenance fixed-agent contract and the pasture-system manifest before retrying.",
	}
}

// establishGenesisAuthority commits one genesis bootstrap-authority operation (a
// nil-parent EffectBootstrapAuthority) under the committer and returns the produced
// authority's JournalID — the system root every task-governing Session binds to.
func establishGenesisAuthority(j provenance.JournalAPI, committer provenance.ActorID) (provenance.JournalID, error) {
	res, err := j.Apply(pastureSystemGenesisInput(committer, time.Now().UTC().UnixNano()))
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't establish the genesis authority for its task journal.",
			Why: "The task backend commits every change through the ordered journal under a\n" +
				"genesis bootstrap authority, and creating or replaying that authority failed.",
			Where:  "Bootstrapping the task-backend system identity (internal/tasks/system_identity.go in tasks.establishGenesisAuthority).",
			Impact: "No task change can be recorded until the genesis authority exists.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the command once the database is healthy.",
			Cause: err,
		}
	}
	for i := range res.ResultSlots {
		if res.ResultSlots[i].Slot == pastureSystemGenesisResultSlot {
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

func pastureSystemGenesisInput(committer provenance.ActorID, recordedAt int64) provenance.OperationInput {
	return provenance.OperationInput{
		OperationID:   pastureSystemGenesisOperationID,
		ActorID:       committer,
		CommandDigest: []byte(pastureSystemGenesisCommand),
		RecordedAt:    recordedAt,
		Effects: []provenance.Effect{{
			Sort:                 provenance.EffectBootstrapAuthority,
			BootstrapLabel:       provadapter.PastureSystemNamespace,
			OperationAuthorityID: pastureSystemGenesisAuthorityID,
			ResultSlot:           pastureSystemGenesisResultSlot,
		}},
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
