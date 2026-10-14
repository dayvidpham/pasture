// Package handlers — migrate_ids.go
//
// Handler for `pasture migrate ids [--dry-run]`: the one-time OFFLINE
// identity-format cutover, the command frontend of the shared pipeline. It is a
// database-level operation under the existing `pasture migrate` surface, not a
// `task` verb, because it rewrites keys across the whole store.
//
// The pipeline phases are:
//
//	P0 read-only state check (the dry run stops here); a NORMAL store is a
//	   front-loaded no-op with no archive, no catch-up and no writes.
//	P1 archive: a consistent logical copy through a read-only source, verified,
//	   with the archive's own format marker recorded.
//	P2 audit catch-up with the DEFERRED ceiling.
//	P3 the conversion transaction (idmigrate.Convert).
//	P4 reopen proof: the real production tracker plus an explicit owner
//	   integrity verifier on the committed store.
//
// The dry run is strictly read-only: it opens the file read-only, reports the
// store path, format, the recorded version and pending catch-up, the
// affected rows/fields, runtime-state blockers, the archive path it would use,
// and the irreversible cost, and it leaves the file's bytes (and SHA-256)
// unchanged.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/idmigrate"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
)

// MigrateIDsInput captures the inputs for `pasture migrate ids`.
type MigrateIDsInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// DryRun reports what conversion would do without writing anything.
	DryRun bool
	// BackupDir overrides where the consistent archive is written. Empty uses
	// the store's own directory.
	BackupDir string
}

type migrateIDsReport struct {
	StorePath           string         `json:"store_path"`
	Format              string         `json:"format"`
	DryRun              bool           `json:"dry_run"`
	AlreadyCurrent      bool           `json:"already_current"`
	NothingToConvert    bool           `json:"nothing_to_convert"`
	WouldChange         bool           `json:"would_change,omitempty"`
	Columns             map[string]int `json:"columns,omitempty"`
	EpochProjectionRows int            `json:"epoch_projection_rows,omitempty"`
	AuditPayloadRows    int            `json:"audit_payload_rows,omitempty"`
	RecordedVersion     int            `json:"recorded_version,omitempty"`
	PendingCatchUpSteps int            `json:"pending_catchup_steps,omitempty"`
	EpochIDLowerBound   int            `json:"epoch_id_lower_bound,omitempty"`
	NoScan              bool           `json:"no_scan,omitempty"`
	Blockers            []string       `json:"blockers,omitempty"`
	BackupPath          string         `json:"backup_path,omitempty"`
	BackupSHA256        string         `json:"backup_sha256,omitempty"`
	CutoverAt           int64          `json:"cutover_at,omitempty"`
	ChangedFields       int            `json:"changed_fields,omitempty"`
	ChangedMutations    int            `json:"changed_mutations,omitempty"`
	ChangedAllocations  int            `json:"changed_allocations,omitempty"`
	PastureRows         int            `json:"pasture_rows,omitempty"`
	Reclaimed           int            `json:"reclaimed"`
	ReopenProof         *reopenProof   `json:"reopen_proof,omitempty"`
}

type reopenProof struct {
	TaskID        string `json:"task_id,omitempty"`
	CommentAuthor string `json:"comment_author,omitempty"`
	FixedAgents   bool   `json:"fixed_agents_and_genesis_unchanged"`
}

// MigrateIDs runs the identity-format cutover command.
func MigrateIDs(w io.Writer, in MigrateIDsInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't prepare the folder for the pasture database at %q.", dbPath),
			Why:      "Creating the parent directory failed.",
			Where:    "Converting the store identity format (internal/handlers/migrate_ids.go in handlers.MigrateIDs).",
			Impact:   "The conversion can't run because the database file's folder doesn't exist and can't be created.",
			Fix: fmt.Sprintf("1. Create the folder yourself and retry:\n     mkdir -p %q\n"+
				"     pasture migrate ids\n2. Or pick a different database location:\n"+
				"     pasture migrate ids --db <path>", filepath.Dir(dbPath)),
			Cause: err,
		}
		return pasterrors.ExitCode(se), se
	}

	if in.DryRun {
		return runMigrateIDsDryRun(context.Background(), w, dbPath, in.BackupDir, format)
	}
	return runMigrateIDsApply(context.Background(), w, dbPath, in.BackupDir, format)
}

func runMigrateIDsDryRun(ctx context.Context, w io.Writer, dbPath, backupDir string, format types.OutputFormat) (int, error) {
	if !idmigrate.StatStore(dbPath) {
		// A store that does not exist yet is created and cut over by its first
		// open; there is nothing to convert.
		return emitMigrateIDsReport(w, format, migrateIDsReport{StorePath: dbPath, Format: "empty", DryRun: true, NothingToConvert: true})
	}
	db, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer db.Close()

	state, err := idmigrate.EvaluateCutoverOnHandle(ctx, db, dbPath, true)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	switch state.Outcome {
	case idmigrate.OutcomeRefuseUnbackedMark:
		se := idmigrate.UnbackedMarkRefusal(dbPath)
		return pasterrors.ExitCode(se), se
	case idmigrate.OutcomeNewerSchema:
		se := audit.NewerSchemaError(state.RecordedVersion, audit.MaxKnownSchemaVersion)
		return pasterrors.ExitCode(se), se
	}

	preview, err := idmigrate.DryRun(ctx, db, dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	report := migrateIDsReport{
		StorePath:           dbPath,
		Format:              string(state.Format),
		DryRun:              true,
		AlreadyCurrent:      state.Outcome == idmigrate.OutcomeNormal,
		WouldChange:         preview.WouldChange(),
		Columns:             preview.Columns,
		EpochProjectionRows: preview.EpochProjectionRows,
		AuditPayloadRows:    preview.AuditPayloadRows,
		RecordedVersion:     preview.RecordedVersion,
		PendingCatchUpSteps: preview.PendingCatchUpSteps,
		EpochIDLowerBound:   preview.EpochIDLowerBound,
		Blockers:            preview.Blockers,
		BackupPath:          backupPreview(dbPath, backupDir),
	}
	if state.Outcome == idmigrate.OutcomeNormal {
		report.NoScan = true
		report.CutoverAt = state.Receipt.CutoverAt
		report.BackupPath = state.Receipt.ArchivePath
		report.BackupSHA256 = state.Receipt.ArchiveSHA256
	}
	return emitMigrateIDsReport(w, format, report)
}

// backupPreview names the directory the apply would archive into without
// creating anything, so a dry run can report the archive location.
func backupPreview(dbPath, backupDir string) string {
	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(dbPath)
	}
	return filepath.Join(dir, filepath.Base(dbPath)+".id-backup-<UTC timestamp>")
}

func runMigrateIDsApply(ctx context.Context, w io.Writer, dbPath, backupDir string, format types.OutputFormat) (int, error) {
	if !idmigrate.StatStore(dbPath) {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("No store at %q; nothing to convert.", dbPath),
			Why:      "The database file does not exist.",
			Where:    "Converting the store identity format (internal/handlers/migrate_ids.go in handlers.runMigrateIDsApply).",
			Impact:   "Nothing was changed and no file was created.",
			Fix:      "The first open creates the store and performs its cutover under the fence; run a normal pasture command instead.",
		}
		return pasterrors.ExitCode(se), se
	}

	// P0: read-only state check. A NORMAL store is a front-loaded no-op with no
	// archive, no catch-up and no writes.
	stateDB, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	state, err := idmigrate.EvaluateCutoverOnHandle(ctx, stateDB, dbPath, true)
	_ = stateDB.Close()
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	switch state.Outcome {
	case idmigrate.OutcomeNormal:
		return emitMigrateIDsReport(w, format, migrateIDsReport{
			StorePath:       dbPath,
			Format:          string(state.Format),
			AlreadyCurrent:  true,
			NoScan:          true,
			BackupPath:      state.Receipt.ArchivePath,
			BackupSHA256:    state.Receipt.ArchiveSHA256,
			CutoverAt:       state.Receipt.CutoverAt,
			RecordedVersion: state.RecordedVersion,
		})
	case idmigrate.OutcomeRefuseUnbackedMark:
		se := idmigrate.UnbackedMarkRefusal(dbPath)
		return pasterrors.ExitCode(se), se
	case idmigrate.OutcomeNewerSchema:
		se := audit.NewerSchemaError(state.RecordedVersion, audit.MaxKnownSchemaVersion)
		return pasterrors.ExitCode(se), se
	}
	// CUTOVER or REFUSE-POPULATED: the command converts both.

	// P1: archive.
	archive, err := idmigrate.ArchiveStore(ctx, dbPath, backupDir, time.Now())
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// P2: audit catch-up with the deferred ceiling.
	beforeVersion := idmigrate.RecordedSchemaVersionPath(dbPath)
	trail, err := audit.NewSqliteAuditTrailContextWithOptions(ctx, dbPath, audit.WithDeferredCeiling(audit.MaxKnownSchemaVersion-1))
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if err := trail.Close(); err != nil {
		return pasterrors.ExitCode(err), err
	}
	applied := idmigrate.RecordedSchemaVersionPath(dbPath) - beforeVersion
	if applied < 0 {
		applied = 0
	}

	// The count baseline is captured AFTER catch-up and BEFORE the conversion
	// transaction, so the reopen comparison proves conversion adds and removes
	// no identity rows (fixed actors and genesis are not duplicated).
	baseline, err := identityCountsPath(ctx, dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// P3: the conversion transaction.
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	report, convErr := idmigrate.Convert(ctx, db, idmigrate.ConvertOptions{
		StorePath:           dbPath,
		Archive:             archive,
		AppliedCatchUpSteps: applied,
		Now:                 time.Now,
	})
	closeErr := db.Close()
	if convErr != nil {
		return pasterrors.ExitCode(convErr), convErr
	}
	if closeErr != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The conversion finished, but pasture couldn't release its handle on %q.", dbPath),
			Why:      "Closing the conversion database handle failed.",
			Where:    "Converting the store identity format (internal/handlers/migrate_ids.go in handlers.runMigrateIDsApply).",
			Impact:   "The conversion is committed, but the next process may briefly see a locked store.",
			Fix:      "Wait a few seconds and retry the next command.",
			Cause:    closeErr,
		}
		return pasterrors.ExitCode(se), se
	}

	out := migrateIDsReport{
		StorePath:          dbPath,
		Format:             "namespace-slash-uuid.v1",
		AlreadyCurrent:     report.AlreadyCurrent,
		BackupPath:         report.ArchivePath,
		BackupSHA256:       report.ArchiveSHA256,
		ChangedFields:      report.ChangedFields,
		ChangedMutations:   report.ChangedMutations,
		ChangedAllocations: report.ChangedAllocations,
		PastureRows:        report.PastureRows,
		Reclaimed:          report.Reclaimed,
	}
	if report.AlreadyCurrent {
		out.Format = string(state.Format)
		out.NoScan = true
		return emitMigrateIDsReport(w, format, out)
	}

	// P4: real reopen + explicit owner integrity verification + count compare.
	proof, proofErr := reopenAndVerify(ctx, dbPath, baseline)
	if proofErr != nil {
		return pasterrors.ExitCode(proofErr), proofErr
	}
	out.ReopenProof = proof
	return emitMigrateIDsReport(w, format, out)
}

// identityCountsPath captures the identity-bearing row counts on a committed
// store through a fresh read handle. It never runs inside the conversion
// transaction.
func identityCountsPath(ctx context.Context, dbPath string) (map[string]int64, error) {
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return identityCountsQuery(ctx, db)
}

// reopenAndVerify opens the converted store through the production tracker,
// runs the dependency's owner integrity verifier on the committed file in an
// explicit transaction that is ALWAYS rolled back, and confirms the
// identity-bearing row counts did not change (fixed actors and genesis are not
// duplicated).
func reopenAndVerify(ctx context.Context, dbPath string, before map[string]int64) (*reopenProof, error) {
	verifyDB, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return nil, err
	}
	tx, err := verifyDB.BeginTx(ctx, nil)
	if err != nil {
		_ = verifyDB.Close()
		return nil, err
	}
	verifyErr := provenance.VerifyIDConversionInTx(ctx, tx)
	_ = tx.Rollback()
	if verifyErr != nil {
		_ = verifyDB.Close()
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The converted store at %q failed explicit owner integrity verification after reopen.", dbPath),
			Why:      verifyErr.Error(),
			Where:    "Reopening and verifying the converted store (internal/handlers/migrate_ids.go in handlers.reopenAndVerify).",
			Impact:   "The committed store is not trustworthy. Restore the consistent archive the conversion made and do not use this file.",
			Fix: "1. Restore the archive:\n     cp <archive> <db>\n" +
				"2. Report the verification failure with the store's source revision before retrying.",
			Cause: verifyErr,
		}
	}
	after, err := identityCountsQuery(ctx, verifyDB)
	_ = verifyDB.Close()
	if err != nil {
		return nil, err
	}
	if err := compareIdentityCounts(before, after); err != nil {
		return nil, err
	}

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return nil, err
	}
	defer tracker.Close()

	proof := &reopenProof{FixedAgents: true}
	all, err := tracker.List(provenance.ListFilter{})
	if err != nil {
		return nil, err
	}
	if len(all) > 0 {
		proof.TaskID = all[0].ID.String()
		comments, err := tracker.Comments(all[0].ID)
		if err != nil {
			return nil, err
		}
		if len(comments) > 0 {
			proof.CommentAuthor = comments[0].AuthorID.String()
		}
	}
	return proof, nil
}

func identityCountsQuery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (map[string]int64, error) {
	tables := []string{
		"agents", "agents_software", "agents_human", "agents_ml", "activities",
		"comments", "tasks", "edges", "labels", "task_attributions",
		"journal", "journal_operations", "governed_allocation_operations",
		"actor_namespace_claims", "fixed_actor_manifest_entries",
		"pasture_well_known_agents", "pasture_agent_categories",
		"audit_events", "context_edges", "lifecycle_occurrences", "lifecycle_payload_blobs",
	}
	counts := map[string]int64{}
	for _, table := range tables {
		var exists bool
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var count int64
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}

func compareIdentityCounts(before, after map[string]int64) error {
	for table, want := range before {
		if got, ok := after[table]; !ok || got != want {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The converted store's %q row count changed.", table),
				Why:      fmt.Sprintf("The table had %d row(s) before conversion and %d after reopen.", want, after[table]),
				Where:    "Reopening and verifying the converted store (internal/handlers/migrate_ids.go in handlers.compareIdentityCounts).",
				Impact:   "Conversion must not add or remove identity rows. The committed store may be unusable.",
				Fix:      "Restore the archive the conversion made and report the count change with the store's source revision.",
			}
		}
	}
	return nil
}

func emitMigrateIDsReport(w io.Writer, format types.OutputFormat, report migrateIDsReport) (int, error) {
	if format == types.OutputJSON {
		blob, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			se := &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: "The conversion report could not be encoded as JSON.", Why: err.Error(), Where: "Rendering the conversion report (internal/handlers/migrate_ids.go).", Impact: "The report could not be printed.", Fix: "Re-run the command with text output."}
			return pasterrors.ExitCode(se), se
		}
		fmt.Fprintln(w, string(blob))
		return 0, nil
	}
	fmt.Fprintln(w, renderMigrateIDsText(report))
	return 0, nil
}

// renderMigrateIDsText renders the plain-language operator report.
func renderMigrateIDsText(report migrateIDsReport) string {
	var b []byte
	b = append(b, fmt.Sprintf("store: %s\n", report.StorePath)...)
	b = append(b, fmt.Sprintf("format: %s\n", report.Format)...)
	if report.DryRun {
		b = append(b, "mode: dry run (nothing was written; the file is unchanged)\n"...)
	}
	if report.AlreadyCurrent {
		b = append(b, "already current: the store's fence evidence is complete; no changes were made\n"...)
		if report.NoScan {
			b = append(b, "no identity scan is performed on a marked store; this verdict is fence evidence, not a content check\n"...)
		}
		if report.BackupPath != "" {
			b = append(b, fmt.Sprintf("archive: %s (sha256 %s)\n", report.BackupPath, report.BackupSHA256)...)
		}
		return string(b)
	}
	if report.NothingToConvert {
		b = append(b, "nothing to convert: no store exists yet; the first open creates and cuts it over\n"...)
		return string(b)
	}
	if report.DryRun {
		if report.WouldChange {
			b = append(b, "would convert:\n"...)
		} else {
			b = append(b, "would convert: nothing recognizable; no identity fields need rewriting\n"...)
		}
		for _, key := range sortedKeys(report.Columns) {
			b = append(b, fmt.Sprintf("  - %s: %d distinct value(s)\n", key, report.Columns[key])...)
		}
		if report.EpochProjectionRows > 0 {
			b = append(b, fmt.Sprintf("  - epoch_state_projection.state_json: %d row(s)\n", report.EpochProjectionRows)...)
		}
		if report.AuditPayloadRows > 0 {
			b = append(b, fmt.Sprintf("  - audit_events.payload: %d row(s)\n", report.AuditPayloadRows)...)
		}
		if report.RecordedVersion > 0 {
			b = append(b, fmt.Sprintf("recorded audit schema version: %d; pending catch-up steps: %d\n", report.RecordedVersion, report.PendingCatchUpSteps)...)
		}
		if report.EpochIDLowerBound > 0 {
			b = append(b, fmt.Sprintf("lower-bound work the catch-up exposes (may undercount; not a verdict): %d retired epoch attachment(s)\n", report.EpochIDLowerBound)...)
		}
		if len(report.Blockers) > 0 {
			b = append(b, "blockers (conversion will refuse until these are drained):\n"...)
			for _, blocker := range report.Blockers {
				b = append(b, fmt.Sprintf("  - %s\n", blocker)...)
			}
		}
		b = append(b, fmt.Sprintf("archive would be written to: %s\n", report.BackupPath)...)
		b = append(b, "irreversible: after conversion an older pasture binary refuses this file for every command, "+
			"and old two-dash task IDs no longer resolve. The archive keeps the original bytes.\n"...)
		b = append(b, "next: stop all writers, then run `pasture migrate ids` (no --dry-run)\n"...)
		return string(b)
	}
	b = append(b, fmt.Sprintf("archive: %s (sha256 %s)\n", report.BackupPath, report.BackupSHA256)...)
	b = append(b, fmt.Sprintf("changed: %d typed field(s), %d canonical mutation(s), %d allocation receipt(s), %d pasture row(s)\n",
		report.ChangedFields, report.ChangedMutations, report.ChangedAllocations, report.PastureRows)...)
	b = append(b, fmt.Sprintf("reclaimed: %d raw payload blob(s) (conversion never reclaims)\n", report.Reclaimed)...)
	if report.ReopenProof != nil {
		b = append(b, "reopen proof: production open succeeded; owner integrity verified; fixed actors and genesis unchanged\n"...)
		if report.ReopenProof.TaskID != "" {
			b = append(b, fmt.Sprintf("  - task: %s\n", report.ReopenProof.TaskID)...)
		}
		if report.ReopenProof.CommentAuthor != "" {
			b = append(b, fmt.Sprintf("  - comment author: %s\n", report.ReopenProof.CommentAuthor)...)
		}
	}
	b = append(b, "next: restart only upgraded binaries\n"...)
	return string(b)
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
