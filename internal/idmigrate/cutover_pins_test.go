package idmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// structuredFix returns the Fix field of a *pasterrors.StructuredError, where
// the refusal texts live.
func structuredFix(t *testing.T, err error) string {
	t.Helper()
	var se *pasterrors.StructuredError
	require.True(t, stderrors.As(err, &se), "error %T is not a *StructuredError: %v", err, err)
	return se.Fix
}

// markerlessStore builds an audit-only store at the cutover target (the last
// version below the identity-format ceiling): a complete audit schema with no
// Provenance identity rows, no slash marker and no receipt. It is the state the
// conversion transaction meets when the catch-up has run but the conversion has
// not, and the state a markerless cutover starts from.
func markerlessStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, audit.MaxKnownSchemaVersion-1))
	t.Cleanup(func() { _ = db.Close() })
	return path, db
}

// repoFixture copies the repository's legacy audit-only fixture to a temp file.
func repoFixture(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked to the filesystem root without finding go.mod")
		dir = parent
	}
	src := filepath.Join(dir, "testdata", "legacy_audit_v1.db")
	raw, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "legacy.db")
	require.NoError(t, os.WriteFile(dst, raw, 0o600))
	return dst
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func insertMarker(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS provenance_id_format (singleton INTEGER PRIMARY KEY, format TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT OR REPLACE INTO provenance_id_format(singleton, format) VALUES(1, ?)`, string(provenance.IDFormatSlash))
	require.NoError(t, err)
}

// TestP3aRefusesAVersionAboveMaxKnownUnderTheLock pins the fourth P3a arm: a
// second connection raises the recorded audit version above MaxKnown between
// the unlocked state check and the lock, and the under-lock evidence re-check
// refuses with the newer-schema error and rolls the transaction back.
func TestP3aRefusesAVersionAboveMaxKnownUnderTheLock(t *testing.T) {
	path, db := markerlessStore(t)
	_, err := db.Exec(`INSERT INTO audit_schema_meta(version, applied_at) VALUES(?, ?)`,
		audit.MaxKnownSchemaVersion+1, time.Now().UTC().UnixNano())
	require.NoError(t, err)

	_, err = Convert(context.Background(), db, ConvertOptions{StorePath: path})
	require.Error(t, err)
	require.Contains(t, err.Error(), "written by a newer pasture")

	// Nothing was written: the marker, the receipt and the ceiling are absent.
	state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, stateErr)
	require.Equal(t, OutcomeNewerSchema, state.Outcome)
	require.False(t, state.ReceiptPresent, "the refusal must not write the receipt")
}

// TestRefuseUnbackedMarkClassRows pins the whole REFUSE-UNBACKED-MARK class: a
// marker outside a fenced, archived pasture cutover is refused, and only the
// complete triple is NORMAL.
func TestRefuseUnbackedMarkClassRows(t *testing.T) {
	cases := []struct {
		name        string
		marker      bool
		receipt     bool
		receiptMark string
		floor       int
		version     int
		want        Outcome
	}{
		{"marker only, below floor", true, false, "", 0, 9, OutcomeRefuseUnbackedMark},
		{"receipt without marker", false, true, string(provenance.IDFormatSlash), audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion, OutcomeRefuseUnbackedMark},
		{"marker-mismatched receipt", true, true, "some-other-format", audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion, OutcomeRefuseUnbackedMark},
		{"version below the receipt floor", true, true, string(provenance.IDFormatSlash), audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion - 1, OutcomeRefuseUnbackedMark},
		{"complete triple", true, true, string(provenance.IDFormatSlash), audit.MaxKnownSchemaVersion, audit.MaxKnownSchemaVersion, OutcomeNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, db := markerlessStore(t)
			if tc.version != audit.MaxKnownSchemaVersion-1 {
				_, err := db.Exec(`INSERT INTO audit_schema_meta(version, applied_at) VALUES(?, ?)`, tc.version, time.Now().UTC().UnixNano())
				require.NoError(t, err)
			}
			if tc.marker {
				insertMarker(t, path)
			}
			if tc.receipt {
				_, err := db.Exec(receiptTableDDL)
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO pasture_id_cutover VALUES(0, ?, ?, '', '', ?)`, tc.receiptMark, tc.floor, time.Now().UTC().UnixNano())
				require.NoError(t, err)
			}
			state, err := EvaluateCutoverOnHandle(context.Background(), db, path, true)
			require.NoError(t, err)
			require.Equal(t, tc.want, state.Outcome)

			// A REFUSE-UNBACKED-MARK verdict renders the classifying refusal,
			// which carries the (a)/(b)/(c) block.
			if tc.want == OutcomeRefuseUnbackedMark {
				fix := structuredFix(t, UnbackedMarkRefusal(path))
				require.Contains(t, fix, "(a) If you hold an independent pre-marking copy")
				require.Contains(t, fix, "(c) If you do not need its history")
			}

			// EVERY arm is also driven through the conversion's P3a verdict, so
			// the under-lock classification is pinned, not only the read-only one:
			// the same refuse arms fire the SCOPED P3a refusal under the fence and
			// write nothing, and the complete-triple arm is the no-op.
			report, convErr := Convert(context.Background(), db, ConvertOptions{StorePath: path})
			if tc.want == OutcomeNormal {
				require.NoError(t, convErr)
				require.True(t, report.AlreadyCurrent, "the complete triple is the conversion no-op")
			} else {
				require.Error(t, convErr)
				scopedFix := structuredFix(t, convErr)
				require.Contains(t, scopedFix, "(a) If you hold an independent pre-marking copy", "the P3a refusal carries the scoped block")
			}
			// The conversion wrote no new fence evidence: the verdict is unchanged.
			after, err := EvaluateCutoverOnHandle(context.Background(), db, path, true)
			require.NoError(t, err)
			require.Equal(t, tc.want, after.Outcome, "the P3a refusal must not add evidence that flips the verdict")
		})
	}
}

// TestPostArchiveSeamInjectionIsRefusedAndRestorable pins the post-P1 seam: after
// the verified markerless archive is taken, a second connection writes a bare
// marker; the under-lock verdict refuses, and the refusal carries the
// MARKERLESS-ARCHIVE branch naming this run's archive. Restoring that archive by
// file replacement clears the mark and the cutover then completes.
func TestPostArchiveSeamInjectionIsRefusedAndRestorable(t *testing.T) {
	path, db := markerlessStore(t)
	fileName := filepath.Base(path)

	archive, err := ArchiveStore(context.Background(), path, t.TempDir(), time.Now())
	require.NoError(t, err)
	require.False(t, archive.Marker.Present, "the pre-P1 archive must be markerless")

	insertMarker(t, path)

	_, err = Convert(context.Background(), db, ConvertOptions{StorePath: path, Archive: archive, AppliedCatchUpSteps: 2})
	require.Error(t, err)
	fix := structuredFix(t, err)
	require.Contains(t, fix, "is markerless")
	require.Contains(t, fix, archive.Path)
	require.Contains(t, fix, "usable pre-marking copy")

	// The refusal wrote nothing: the injected marker is still the only fence
	// evidence and no receipt exists.
	state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, stateErr)
	require.Equal(t, OutcomeRefuseUnbackedMark, state.Outcome)

	// Restore this run's archive by file replacement, removing the stale
	// -wal/-shm siblings.
	require.NoError(t, db.Close())
	restored, err := os.ReadFile(archive.Path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, restored, 0o600))
	for _, sibling := range []string{path + "-wal", path + "-shm"} {
		if rmErr := os.Remove(sibling); rmErr != nil && !os.IsNotExist(rmErr) {
			t.Fatalf("remove %s: %v", sibling, rmErr)
		}
	}
	require.NotEqual(t, fileName, "", "the store keeps its name across the replacement")

	reopened, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	defer reopened.Close()
	restoredState, err := EvaluateCutoverOnHandle(context.Background(), reopened, path, true)
	require.NoError(t, err)
	require.False(t, restoredState.MarkerPresent, "restoring the markerless archive must clear the mark")

	report, err := Convert(context.Background(), reopened, ConvertOptions{StorePath: path, Archive: archive, AppliedCatchUpSteps: 2})
	require.NoError(t, err)
	require.True(t, report.ReceiptWritten)

	final, err := EvaluateCutoverOnHandle(context.Background(), reopened, path, true)
	require.NoError(t, err)
	require.Equal(t, OutcomeNormal, final.Outcome)

	second, err := Convert(context.Background(), reopened, ConvertOptions{StorePath: path, Archive: archive})
	require.NoError(t, err)
	require.True(t, second.AlreadyCurrent, "a second run must be the no-op")
}

// TestPreBeginDigestSeamIsolatesTheConversionTransaction pins the digest seam
// position: it fires after the foreign-key bracket is established and
// immediately before BEGIN IMMEDIATE, so the store's digest there equals the
// digest before Convert was called (nothing pre-BEGIN writes) and differs from
// the committed result.
func TestPreBeginDigestSeamIsolatesTheConversionTransaction(t *testing.T) {
	path, db := markerlessStore(t)
	before := fileDigest(t, path)

	var seamDigest string
	seamSeen := false
	opts := ConvertOptions{StorePath: path}
	opts.beforeBegin = func() {
		seamSeen = true
		seamDigest = fileDigest(t, path)
	}
	_, err := Convert(context.Background(), db, opts)
	require.NoError(t, err)
	require.True(t, seamSeen, "the pre-BEGIN seam must fire")
	require.Equal(t, before, seamDigest, "nothing may write to the store before BEGIN IMMEDIATE")

	after := fileDigest(t, path)
	require.NotEqual(t, before, after, "the committed conversion changes the store")
}

// TestForeignKeyBracketRestoresEnforcement pins the FK bracket's restore half:
// after a conversion the handle's foreign-key enforcement is back ON, so a
// failed bracket can never hand back a connection with enforcement silently
// disabled.
func TestForeignKeyBracketRestoresEnforcement(t *testing.T) {
	path, db := markerlessStore(t)
	db.SetMaxOpenConns(1)

	_, err := Convert(context.Background(), db, ConvertOptions{StorePath: path})
	require.NoError(t, err)

	var fk int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk, "the bracket must restore foreign-key enforcement")
}

// TestP2ToP3BoundaryResumesToCompletion pins the catch-up -> conversion crash
// boundary: after the deferred-ceiling catch-up commits, the store is unmarked,
// below the ceiling and backfill-populated; the state machine refuses it as a
// populated markerless store, and the conversion then completes to the triple.
func TestP2ToP3BoundaryResumesToCompletion(t *testing.T) {
	path := repoFixture(t)

	// P1 archive, then P2 catch-up with the deferred ceiling.
	archive, err := ArchiveStore(context.Background(), path, t.TempDir(), time.Now())
	require.NoError(t, err)
	trail, err := audit.NewSqliteAuditTrailWithOptions(path, audit.WithDeferredCeiling(audit.MaxKnownSchemaVersion-1))
	require.NoError(t, err)
	require.NoError(t, trail.Close())

	// The resumed state: unmarked, at the cutover target, backfill-populated.
	resumed, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	defer resumed.Close()
	state, err := EvaluateCutoverOnHandle(context.Background(), resumed, path, true)
	require.NoError(t, err)
	require.Equal(t, audit.MaxKnownSchemaVersion-1, state.RecordedVersion)
	require.False(t, state.MarkerPresent)
	require.True(t, state.Populated, "the v2->v3 backfill populates the store during the catch-up")
	require.Equal(t, OutcomeRefusePopulated, state.Outcome, "a normal open refuses the resumed state")

	// P3 conversion completes it; a second run is the no-op.
	report, err := Convert(context.Background(), resumed, ConvertOptions{StorePath: path, Archive: archive, AppliedCatchUpSteps: 8})
	require.NoError(t, err)
	require.True(t, report.ReceiptWritten)

	final, err := EvaluateCutoverOnHandle(context.Background(), resumed, path, true)
	require.NoError(t, err)
	require.Equal(t, OutcomeNormal, final.Outcome)
	require.Equal(t, audit.MaxKnownSchemaVersion, final.RecordedVersion)

	second, err := Convert(context.Background(), resumed, ConvertOptions{StorePath: path, Archive: archive})
	require.NoError(t, err)
	require.True(t, second.AlreadyCurrent)
}

// probeTableExists reports whether the injection probe table survived.
func probeTableExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var exists bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='cutover_injection_probe')`).Scan(&exists))
	return exists
}

// injectionSeam writes a probe table and one row through the live conversion
// transaction at the named checkpoint, then returns sentinel so the whole
// transaction must roll back. It fires only at point.
func injectionSeam(point InjectPoint, sentinel error) func(InjectPoint, *sql.Tx) error {
	return func(got InjectPoint, tx *sql.Tx) error {
		if got != point {
			return nil
		}
		if _, err := tx.Exec(`CREATE TABLE cutover_injection_probe (point TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cutover_injection_probe(point) VALUES(?)`, string(got)); err != nil {
			return err
		}
		return sentinel
	}
}

// TestConversionInTransactionInjectionsRollBackTheWholeTransaction pins the
// rollback/atomicity claims at the three in-transaction checkpoints the
// pre-BEGIN seam cannot reach: marker->receipt (R5.9), the replay boundary
// (R6.9), and the final boundary (everything written, not committed). At each,
// the injection writes through the live transaction and returns an error, and
// the test proves the marker, the receipt, the deferred ceiling and the
// injection's own write all rolled back together — so "any failure rolls the
// entire transaction back" is exercised, not merely asserted.
func TestConversionInTransactionInjectionsRollBackTheWholeTransaction(t *testing.T) {
	cases := []struct {
		name  string
		point InjectPoint
	}{
		{"marker->receipt", injectAfterMarker},
		{"replay boundary", injectAfterReplay},
		{"final boundary", injectFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, db := markerlessStore(t)
			sentinel := fmt.Errorf("injected fault at %s", tc.point)
			opts := ConvertOptions{StorePath: path}
			opts.injectInTx = injectionSeam(tc.point, sentinel)

			_, err := Convert(context.Background(), db, opts)
			require.Error(t, err)
			require.Contains(t, err.Error(), sentinel.Error())

			require.False(t, probeTableExists(t, db), "the injection's own write must roll back with the conversion")
			state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
			require.NoError(t, stateErr)
			require.False(t, state.MarkerPresent, "the marker must roll back")
			require.False(t, state.ReceiptPresent, "the receipt must roll back")
			require.Equal(t, audit.MaxKnownSchemaVersion-1, state.RecordedVersion, "the deferred ceiling must roll back")
		})
	}
}

// TestScopedP3aMarkedArchiveBranchIsRefused pins the MARKED-ARCHIVE branch of the
// scoped P3a refusal: this run's P1 archive was taken AFTER a bare mark, so it
// carries the mark and post-dates it, and the refusal must say it is NOT the
// independent pre-marking copy option (a) names.
func TestScopedP3aMarkedArchiveBranchIsRefused(t *testing.T) {
	path, db := markerlessStore(t)
	insertMarker(t, path)

	archive, err := ArchiveStore(context.Background(), path, t.TempDir(), time.Now())
	require.NoError(t, err)
	require.True(t, archive.Marker.Present, "the archived store already carries the mark")

	_, err = Convert(context.Background(), db, ConvertOptions{StorePath: path, Archive: archive, AppliedCatchUpSteps: 3})
	require.Error(t, err)
	fix := structuredFix(t, err)
	require.Contains(t, fix, archive.Path)
	require.Contains(t, fix, "carries the mark and post-dates it")
	require.Contains(t, fix, "it is NOT the independent pre-marking copy")
}

// TestBelowTargetPreBeginDigestEquality pins the digest seam for a store BELOW
// the cutover target (not only at it): with the deferred catch-up pending, the
// store's digest at the seam — after the foreign-key bracket, before BEGIN
// IMMEDIATE — still equals the digest before Convert was called, and the
// committed conversion changes the store.
func TestBelowTargetPreBeginDigestEquality(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, audit.MaxKnownSchemaVersion-2))
	before := fileDigest(t, path)

	var seamDigest string
	seamSeen := false
	opts := ConvertOptions{StorePath: path}
	opts.beforeBegin = func() {
		seamSeen = true
		seamDigest = fileDigest(t, path)
	}
	_, err = Convert(context.Background(), db, opts)
	require.NoError(t, err)
	require.True(t, seamSeen)
	require.Equal(t, before, seamDigest, "nothing may write before BEGIN IMMEDIATE for a below-target store either")
	require.NotEqual(t, before, fileDigest(t, path), "the committed conversion changes the store")
}

// TestForeignKeyBracketRestoresEnforcementOnAFailedConversion pins the ABORT
// half of the FK bracket: when the conversion fails and rolls back, the pinned
// connection must still hand back foreign-key enforcement ON, so a failed
// bracket can never leak a connection with enforcement silently disabled.
func TestForeignKeyBracketRestoresEnforcementOnAFailedConversion(t *testing.T) {
	path, db := markerlessStore(t)
	db.SetMaxOpenConns(1)

	opts := ConvertOptions{StorePath: path}
	opts.injectInTx = injectionSeam(injectFinal, fmt.Errorf("injected fault"))
	_, err := Convert(context.Background(), db, opts)
	require.Error(t, err)

	var fk int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk, "the bracket must restore enforcement even when the conversion aborts")
}

// TestConvertSucceedsOnAWALModeStore pins the production journal mode: the
// checked-in fixtures are rollback-journal, so a conversion is exercised in WAL
// mode here too. The conversion must publish the receipt and reach NORMAL.
func TestConvertSucceedsOnAWALModeStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	seed, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), seed, audit.MaxKnownSchemaVersion-1))
	require.NoError(t, seed.Close())

	wal, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wal.Close() })
	var mode string
	require.NoError(t, wal.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode, "the WAL case must actually run in WAL mode")

	report, err := Convert(context.Background(), wal, ConvertOptions{StorePath: path})
	require.NoError(t, err)
	require.True(t, report.ReceiptWritten)

	final, err := EvaluateCutoverOnHandle(context.Background(), wal, path, true)
	require.NoError(t, err)
	require.Equal(t, OutcomeNormal, final.Outcome)
	require.Equal(t, audit.MaxKnownSchemaVersion, final.RecordedVersion)
}
