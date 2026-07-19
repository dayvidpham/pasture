// Package tasks — atomic legacy audit-row import command (#43 / S3.3 stage b).
//
// legacy_import.go owns the typed, atomic ImportLegacyAuditRow command #43 is the
// exclusive owner of (the issue's import-command section): #14 owns only the
// read-only extraction/adapter plumbing (provadapter.ListLegacyAuditEvents,
// provadapter.RunBaselineMigration). This command turns one raw NON-TASK legacy audit
// row — a row that references tasks by context but was never itself a Pasture task —
// into ONE atomic journal operation that attaches the preserved audit record to every
// task it references.
//
// Atomicity and idempotency. The whole import is a single facade Apply: a stable,
// source-derived OperationID plus a fan-out of one pasture.legacy.audit-imported.v1
// material event per referenced task, in sorted unique task order, folded in one
// all-or-none commit. Because the OperationID is a pure function of the row's source
// identity (source table + legacy row id), re-importing the same row replays
// idempotently through the journal's §9.4 short-circuit (Outcome.Committed.
// ShortCircuited is true) — no duplicate rows. A DISTINCT import that reuses the same
// source identity with a changed fan-out, actor, or payload has a different four-field
// replay identity and is rejected as a compound conflict (Outcome Conflict), writing
// nothing.
//
// Actor-text fallback. A legacy row's RawActor is uninterpreted text. The import
// resolves it to a registered ActorID through a caller-supplied map and falls back to
// a default actor when the text is empty or unmapped; the resolved actor is journaled
// as an actor context and the original text is preserved verbatim in the payload. The
// committing actor of the operation itself is always the pasture-system actor.
//
// Read-only source. The command never mutates the legacy source; #14's extraction is
// read-only and this command only reads the already-extracted LegacyAuditEvent value.

package tasks

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
)

// LegacyAuditImporter is the capability an open task tracker exposes for the atomic
// legacy audit-row import. OpenTaskTracker's concrete tracker satisfies it; a caller
// (the CLI, #40 runtime bindings) type-asserts the returned protocol.TaskTracker to
// this interface to drive the import.
type LegacyAuditImporter interface {
	// ImportLegacyAuditRow imports one raw legacy audit row as a single atomic journal
	// operation. See the method for full semantics.
	ImportLegacyAuditRow(row provadapter.LegacyAuditEvent, resolver LegacyActorResolver) (LegacyImportOutcome, error)
}

// LegacyActorResolver resolves a legacy row's raw actor text to a registered ActorID,
// falling back to Fallback when the text is empty or absent from Map. Fallback must be
// a valid, registered actor because every imported event is attributed to a resolvable
// actor; a zero Fallback is rejected before any journal write.
type LegacyActorResolver struct {
	// Map resolves an exact legacy actor string to a registered ActorID.
	Map map[string]provenance.ActorID
	// Fallback is used when the raw actor text is empty or not present in Map.
	Fallback provenance.ActorID
}

// resolve returns the attributed actor for raw and whether the fallback was applied.
func (r LegacyActorResolver) resolve(raw string) (provenance.ActorID, bool) {
	if raw != "" {
		if id, ok := r.Map[raw]; ok {
			return id, false
		}
	}
	return r.Fallback, true
}

// LegacyImportOutcome reports how one atomic import resolved. Outcome is the facade
// result (Committed — fresh or idempotent replay — or Conflict); OperationID is the
// stable source-derived operation identity; Tasks is the sorted unique fan-out set the
// row was attached to; AttributedActor is the resolved actor and FallbackApplied
// records whether the actor-text fallback was used.
type LegacyImportOutcome struct {
	Outcome         provadapter.Outcome
	OperationID     provenance.OperationID
	Tasks           []provenance.TaskID
	AttributedActor provenance.ActorID
	FallbackApplied bool
}

// ImportLegacyAuditRow imports one raw legacy audit row (from
// provadapter.ListLegacyAuditEvents) as a single atomic journal operation, fanning
// out one preserved audit event per referenced task. It resolves the attributed actor
// with the supplied fallback, derives a stable source-keyed OperationID, and commits
// every fanned-out effect in one all-or-none Apply through the tracker's own
// pasture-system identity and genesis authority.
//
// The fan-out target set is the sorted, de-duplicated set of the row's raw contexts
// that parse as Provenance task identities; a row that references no task cannot be
// attached to the graph and is rejected with an actionable error so the caller keeps
// it in #14's non-task listing. The referenced tasks must already exist in the journal
// (e.g. after a baseline migration); an unknown task fails the whole batch, which
// commits nothing.
func (t *trackerImpl) ImportLegacyAuditRow(row provadapter.LegacyAuditEvent, resolver LegacyActorResolver) (LegacyImportOutcome, error) {
	if err := provadapter.ValidateActorID(resolver.Fallback); err != nil {
		return LegacyImportOutcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pasture can't import a legacy audit row without a fallback actor.",
			Why:      "The actor-text fallback attributes any empty or unmapped legacy actor to a registered fallback actor, and none was supplied.",
			Where:    "Importing a legacy audit row (internal/tasks/legacy_import.go in tasks.ImportLegacyAuditRow).",
			Impact:   "No legacy audit row can be imported.",
			Fix:      "Pass a LegacyActorResolver whose Fallback is a registered actor id.",
			Cause:    err,
		}
	}
	if row.SourceTable == "" || row.LegacyRowID == "" {
		return LegacyImportOutcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pasture can't import a legacy audit row with no source identity.",
			Why:      "The import derives its stable, idempotent operation id from the row's source table and legacy row id, and one of them is empty.",
			Where:    "Importing a legacy audit row (internal/tasks/legacy_import.go in tasks.ImportLegacyAuditRow).",
			Impact:   "The row has no stable identity to key idempotent replay on, so nothing is imported.",
			Fix:      "Supply a LegacyAuditEvent with a non-empty SourceTable and LegacyRowID (as produced by provadapter.ListLegacyAuditEvents).",
		}
	}

	// Fan-out targets: the raw contexts that parse as Provenance task ids, sorted and
	// de-duplicated so replay and audit see one stable order.
	tasks, err := taskFanOut(row)
	if err != nil {
		return LegacyImportOutcome{}, err
	}

	attributed, fallbackApplied := resolver.resolve(row.RawActor)

	// Ensure the pasture-system identity is resolved, then read the committing actor
	// and genesis authority to bind this operation to (the same identity every task
	// mutation commits under).
	if _, err := t.systemSession(); err != nil {
		return LegacyImportOutcome{}, err
	}
	committer, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return LegacyImportOutcome{}, err
	}
	if !found {
		return LegacyImportOutcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't resolve its system identity before importing a legacy audit row.",
			Why:      "The import commits under the pasture-system committing actor and genesis authority, and that identity was not persisted.",
			Where:    "Importing a legacy audit row (internal/tasks/legacy_import.go in tasks.ImportLegacyAuditRow).",
			Impact:   "No legacy audit row can be imported until the system identity is resolved.",
			Fix:      "Run a task mutation once to bootstrap the identity (pasture migrate; then any task command), then retry the import.",
		}
	}

	// Build one preserved material event per referenced task, folded in one operation.
	effects := make([]provenance.Effect, 0, len(tasks))
	for _, task := range tasks {
		eff, err := MapMaterialEvent(LegacyAuditImportedEvent{
			Task:            task,
			SourceTable:     row.SourceTable,
			LegacyRowID:     row.LegacyRowID,
			RawActor:        row.RawActor,
			AttributedActor: attributed,
			RawContexts:     row.RawContexts,
			SourcePayload:   row.Payload,
		})
		if err != nil {
			return LegacyImportOutcome{}, fmt.Errorf(
				"import legacy audit row %s/%s: build event for task %s: %w",
				row.SourceTable, row.LegacyRowID, task, err)
		}
		effects = append(effects, eff)
	}

	mutation, err := legacyImportMutationRef(row)
	if err != nil {
		return LegacyImportOutcome{}, err
	}
	op, err := provadapter.OperationIDFromRef(mutation)
	if err != nil {
		return LegacyImportOutcome{}, err
	}
	command, err := ir.DigestCanonicalCommand(legacyImportCommandBytes(row, tasks, attributed))
	if err != nil {
		return LegacyImportOutcome{}, fmt.Errorf(
			"import legacy audit row %s/%s: digest command: %w", row.SourceTable, row.LegacyRowID, err)
	}

	journal, err := provadapter.NewJournal(t.prov.Journal())
	if err != nil {
		return LegacyImportOutcome{}, err
	}

	// One atomic Apply under the write mutex: the fan-out commits all-or-none and
	// serializes with every other write on the shared connections.
	unlock := t.lockWrite()
	outcome, applyErr := journal.Apply(provadapter.ApplyRequest{
		Mutation:       mutation,
		Actor:          committer,
		Authority:      &authority,
		Command:        command,
		MutationDigest: legacyImportMutationDigest(row, tasks, attributed),
		RecordedAt:     row.RecordedAt.UnixNano(),
		Effects:        effects,
	})
	unlock()

	result := LegacyImportOutcome{
		Outcome:         outcome,
		OperationID:     op,
		Tasks:           tasks,
		AttributedActor: attributed,
		FallbackApplied: fallbackApplied,
	}
	if applyErr != nil {
		// A compound conflict (same source identity reused with a differing operation)
		// is surfaced as a typed rejection carrying the conflict outcome; every other
		// journal error (unknown task, authority scope, …) is returned verbatim.
		return result, fmt.Errorf(
			"import legacy audit row %s/%s (nothing committed): %w",
			row.SourceTable, row.LegacyRowID, applyErr)
	}
	return result, nil
}

// taskFanOut returns the sorted, de-duplicated set of the row's raw contexts that
// parse as Provenance task identities. A row that references no task is rejected.
func taskFanOut(row provadapter.LegacyAuditEvent) ([]provenance.TaskID, error) {
	seen := make(map[string]provenance.TaskID, len(row.RawContexts))
	for _, raw := range row.RawContexts {
		if id, err := provenance.ParseTaskID(legacyContextIdentity(raw)); err == nil {
			seen[id.String()] = id
		}
	}
	if len(seen) == 0 {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Legacy audit row %s/%s references no task, so it can't be attached to the graph.", row.SourceTable, row.LegacyRowID),
			Why:      "An imported audit event is journaled against the tasks it references; this row's contexts contain no Provenance task identity.",
			Where:    "Importing a legacy audit row (internal/tasks/legacy_import.go in tasks.taskFanOut).",
			Impact:   "The row is not imported; it remains a non-task row in provadapter.ListLegacyAuditEvents.",
			Fix:      "Import only rows that reference at least one task; keep genuinely task-free audit rows in #14's non-task listing.",
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]provenance.TaskID, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out, nil
}

// legacyContextIdentity extracts the identity portion of a raw context string. A
// legacy context is stored as an opaque string that may be a bare task id
// ("namespace--uuid") or a kind-qualified "Kind:identity" pair; this returns the
// identity after the last ':' separator, or the whole string when unqualified, so a
// bare task id and a kind-qualified task context both resolve to the same task.
func legacyContextIdentity(raw string) string {
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == ':' {
			return raw[i+1:]
		}
	}
	return raw
}

// legacyImportMutationRef derives the stable, source-keyed idempotency handle for a
// row. The same row always yields the same ref (hence the same OperationID), so a
// re-import replays; the ref is a hash of the source identity so arbitrary legacy row
// ids (which may contain characters a portable ref forbids) always produce a valid
// handle.
func legacyImportMutationRef(row provadapter.LegacyAuditEvent) (portable.MutationRef, error) {
	sum := sha256.Sum256([]byte(row.SourceTable + "\x00" + row.LegacyRowID))
	value := fmt.Sprintf("pasture.legacy-audit-import.%x", sum)
	ref, err := portable.NewMutationRef(value)
	if err != nil {
		return portable.MutationRef{}, fmt.Errorf(
			"import legacy audit row %s/%s: build mutation reference: %w",
			row.SourceTable, row.LegacyRowID, err)
	}
	return ref, nil
}

// legacyImportCommandBytes returns the canonical command bytes the CommandDigest is
// taken over: the fully-resolved import intent (source identity, sorted fan-out, and
// attributed actor). A changed fan-out or actor changes these bytes, so a distinct
// operation reusing the same source identity produces a differing four-field replay
// identity and is rejected as a conflict.
func legacyImportCommandBytes(row provadapter.LegacyAuditEvent, tasks []provenance.TaskID, actor provenance.ActorID) []byte {
	buf := make([]byte, 0, 128)
	buf = append(buf, "import-legacy-audit-row\x1f"...)
	buf = append(buf, row.SourceTable...)
	buf = append(buf, 0x1f)
	buf = append(buf, row.LegacyRowID...)
	buf = append(buf, 0x1f)
	buf = append(buf, actor.String()...)
	for _, task := range tasks {
		buf = append(buf, 0x1e)
		buf = append(buf, task.String()...)
	}
	return buf
}

// legacyImportMutationDigest is the opaque structural mutation digest: a hash over the
// source identity, sorted fan-out, attributed actor, AND the preserved source payload,
// so any structural change to what would be written yields a distinct digest.
func legacyImportMutationDigest(row provadapter.LegacyAuditEvent, tasks []provenance.TaskID, actor provenance.ActorID) []byte {
	h := sha256.New()
	h.Write(legacyImportCommandBytes(row, tasks, actor))
	h.Write([]byte{0x1d})
	h.Write(row.Payload)
	sum := h.Sum(nil)
	return sum
}
