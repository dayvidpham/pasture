package idmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/provadapter"
)

// These pins cover the cutover behaviours the design names but the first pass
// left as a NAME without a PATH: a real replay at the injection boundary, the
// below-target and no-archive refusal branches, the deferral rules, the seam on
// the production handle, the restore proof, and the WAL archive.

// pinsRetiredAgent is a recognizable retired identity the conversion rewrites, so
// the lifecycle fixture also carries a converted key.
const (
	pinsRetiredAgent      = "file://project--01968a3c-9d4f-7c8a-bc12-feedfacecafe"
	pinsRetiredAgentSlash = "file://project/01968a3c-9d4f-7c8a-bc12-feedfacecafe"
)

// structuredImpact returns the Impact field of a *pasterrors.StructuredError,
// where the scoped P3a refusal states this run's applied catch-up steps.
func structuredImpact(t *testing.T, err error) string {
	t.Helper()
	var se *pasterrors.StructuredError
	require.True(t, stderrors.As(err, &se), "error %T is not a *StructuredError: %v", err, err)
	return se.Impact
}

// checkpointWAL flushes the handle's committed WAL frames into the main database
// file. The conversion runs on the production WAL handle, so a digest taken
// without a checkpoint would read a stale main file and could not tell a
// committed write from no write at all.
func checkpointWAL(db *sql.DB) error {
	var busy, logFrames, checkpointed int
	return db.QueryRowContext(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed)
}

// checkpointedDigest returns the SHA-256 of the store's committed content: the
// handle's WAL is flushed first so the digest covers the main file's committed
// bytes rather than a stale copy.
func checkpointedDigest(t *testing.T, db *sql.DB, path string) string {
	t.Helper()
	require.NoError(t, checkpointWAL(db))
	return fileDigest(t, path)
}

// markerlessStoreOnTheProductionHandle builds the same markerless, at-target
// store as markerlessStore, but opens it through dbconn.OpenSharedDB: the
// production _txlock=immediate / WAL handle the conversion actually runs on. The
// pre-BEGIN seam's contract is about the store in its FINAL journal mode, so the
// seam pins must run on this handle, not the default one.
func markerlessStoreOnTheProductionHandle(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, audit.MigrateContextToCeiling(context.Background(), db, audit.MaxKnownSchemaVersion-1))
	return path, db
}

// lifecycleBearingStore builds a populated, unmarked store that carries a REAL
// lifecycle occurrence (one occurrence with one native binding) and a retired
// identity the conversion rewrites. The journal row is committed through the
// production Provenance journal, so the in-transaction replay has rows to
// re-derive rather than an empty projection: an injection at the replay boundary
// on an empty fixture proves nothing.
func lifecycleBearingStore(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	blobDigest := seedLifecycleOccurrence(t, path)

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	// The audit schema the conversion reads and raises, stopped at the cutover
	// target the way the deferred catch-up leaves it.
	require.NoError(t, audit.MigrateContextToCeiling(ctx, db, audit.MaxKnownSchemaVersion-1))
	// Drop the fence evidence the seed open wrote: the conversion only ever runs
	// on an unmarked store.
	for _, table := range []string{"provenance_id_format", "pasture_id_cutover"} {
		_, err := db.Exec("DROP TABLE IF EXISTS " + table)
		require.NoError(t, err)
	}
	// A retired identity in a pasture-owned column: the conversion rewrites it.
	_, err = db.Exec(`INSERT INTO pasture_agent_categories(agent_id) VALUES(?)`, pinsRetiredAgent)
	require.NoError(t, err)
	// The occurrence names a content-addressed payload blob.
	_, err = db.Exec(`INSERT INTO lifecycle_payload_blobs(digest, body, byte_count, written_at) VALUES(?,?,?,?)`, blobDigest, []byte("body"), len("body"), time.Now().UTC().UnixNano())
	require.NoError(t, err)
	return path, db
}

// seedLifecycleOccurrence commits one lifecycle occurrence with one native
// binding through the production Provenance journal and returns the digest of the
// payload blob it names. It builds the store without internal/tasks so the pin
// stays in package idmigrate.
func seedLifecycleOccurrence(t *testing.T, path string) string {
	t.Helper()
	tr, err := provenance.OpenSQLite(path)
	require.NoError(t, err)
	activated, err := provadapter.ActivatePastureSystem(tr)
	require.NoError(t, err)
	actor := activated.DefaultActorID

	genesis, err := tr.Journal().Apply(provenance.OperationInput{
		OperationID:   "pasture.system.genesis.v1",
		ActorID:       actor,
		CommandDigest: []byte("pasture.system.genesis.command.v1"),
		RecordedAt:    time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:                 provenance.EffectBootstrapAuthority,
			BootstrapLabel:       provadapter.PastureSystemNamespace,
			OperationAuthorityID: provenance.OperationAuthorityID("cutover-pins.bootstrap"),
			ResultSlot:           provenance.ResultSlotID("auth"),
		}},
	})
	require.NoError(t, err)
	var authority provenance.JournalID
	for _, slot := range genesis.ResultSlots {
		if slot.Slot == "auth" {
			authority = slot.ProducedJournalID
		}
	}
	require.NotZero(t, authority, "the genesis operation must produce the bootstrap authority")

	task, err := tr.As(actor, authority).Create("cutover-pins", "occurrence subject", "the task the lifecycle occurrence names", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)

	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, registration.ClaudeCode2_1_261().Contract.String())
	require.NoError(t, err)
	body := []byte("body")
	ref := "sha256:" + fmt.Sprintf("%x", sha256.Sum256(body))
	payload, err := json.Marshal(struct {
		Contract string                      `json:"contract"`
		Event    model.ContractEventKind     `json:"event"`
		Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
		Bindings []model.NativeBinding       `json:"bindings"`
		Capture  model.CaptureDisposition    `json:"capture"`
		Body     string                      `json:"body_digest"`
	}{
		Contract: contract.String(),
		Event:    model.ContractEventKind(1),
		Envelope: model.OccurrenceEnvelopeRef{Runtime: model.RuntimeContractDefinitionRef{Contract: contract}},
		Bindings: []model.NativeBinding{{Kind: 1, NativeName: "session_id", Value: "pins"}},
		Capture:  model.CaptureValid,
		Body:     ref,
	})
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte("pasture.lifecycle.receipt.append/v1\x00"), payload...))
	input := provenance.OperationInput{
		OperationID:        provenance.OperationID("cutover-pins.occurrence.v1"),
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      command[:],
		RecordedAt:         time.Now().UTC().UnixNano(),
		Effects: []provenance.Effect{{
			Sort:          provenance.EffectEvidence,
			TaskID:        task.ID,
			ResultSlot:    provenance.ResultSlotID("occurrence"),
			EvidenceKind:  provenance.EvidenceKind("pasture.lifecycle.occurrence.v1"),
			ContentDigest: sum[:],
			Payload:       append(json.RawMessage(nil), payload...),
		}},
	}
	canonical, err := provenance.Canonicalize(input)
	require.NoError(t, err)
	input.Effects = canonical.NormalizedEffects()
	for i := range input.Effects {
		digestSum := sha256.Sum256(input.Effects[i].Payload)
		input.Effects[i].ContentDigest = append([]byte(nil), digestSum[:]...)
	}
	journal, ok := tr.Journal().(provenance.ContextJournal)
	require.True(t, ok, "the production journal is a context journal")
	_, err = journal.ApplyContext(context.Background(), input)
	require.NoError(t, err)
	require.NoError(t, tr.Close())
	return ref
}

// TestConversionInTransactionInjectionsRollBackARealReplay pins the
// rollback/atomicity claims at the three in-transaction checkpoints the
// pre-BEGIN seam cannot reach, on a fixture with a real replay. The store
// carries one committed lifecycle occurrence with a native binding and a
// retired key, so the replay genuinely writes rows and the conversion
// genuinely rewrites a key before each fault: at every checkpoint the injection
// writes through the live transaction and returns an error, and the test proves
// the marker, the receipt, the deferred ceiling, the replayed projection, the
// converted key and the injection's own write all rolled back together, with the
// committed store digest unchanged from the pre-BEGIN seam.
func TestConversionInTransactionInjectionsRollBackARealReplay(t *testing.T) {
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
			path, db := lifecycleBearingStore(t)
			require.NoError(t, checkpointWAL(db))
			before := fileDigest(t, path)

			var (
				seamSeen        bool
				seamDigest      string
				seamErr         error
				observeErr      error
				replayRows      int
				replayBindings  int
				replayConverted int
			)
			sentinel := conversionError("Injected fault at the "+string(tc.point)+" checkpoint.", "A test injection failed the live conversion transaction.", "The whole conversion transaction rolls back.", "None; this is a test injection.", nil)
			opts := ConvertOptions{StorePath: path}
			opts.beforeBegin = func() {
				seamSeen = true
				// Checkpoint before the digest so a write that landed in the WAL
				// before BEGIN IMMEDIATE is visible: a main-file-only digest would
				// miss it and could not prove nothing wrote pre-BEGIN.
				seamErr = checkpointWAL(db)
				if seamErr == nil {
					seamDigest = fileDigest(t, path)
				}
			}
			opts.injectInTx = func(got InjectPoint, tx *sql.Tx) error {
				if got != tc.point {
					return nil
				}
				if got == injectAfterReplay {
					// The replay runs BEFORE this checkpoint. Observe what it
					// wrote through the live transaction, so the rollback below
					// proves the replay's own writes were undone rather than
					// proving nothing happened on an empty fixture.
					observeErr = stderrors.Join(
						tx.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&replayRows),
						tx.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&replayBindings),
						tx.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, pinsRetiredAgentSlash).Scan(&replayConverted),
					)
					if observeErr != nil {
						return observeErr
					}
				}
				if _, err := tx.Exec(`CREATE TABLE cutover_injection_probe (point TEXT NOT NULL)`); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO cutover_injection_probe(point) VALUES(?)`, string(got)); err != nil {
					return err
				}
				return sentinel
			}

			_, err := Convert(context.Background(), db, opts)
			require.Error(t, err)
			require.NoError(t, seamErr, "the pre-BEGIN checkpoint must succeed")
			require.NoError(t, observeErr, "the replay-boundary observation must read the live transaction")
			require.Equal(t, 5, pasterrors.ExitCode(err), "an in-transaction conversion failure is a storage failure")
			require.Contains(t, err.Error(), "Injected fault")

			require.True(t, seamSeen, "the pre-BEGIN seam must fire")
			require.Equal(t, before, seamDigest, "nothing may write before BEGIN IMMEDIATE")
			require.Equal(t, before, checkpointedDigest(t, db, path), "every conversion and injection write must roll back")

			require.False(t, probeTableExists(t, db), "the injection's own write must roll back")
			state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
			require.NoError(t, stateErr)
			require.False(t, state.MarkerPresent, "the marker must roll back")
			require.False(t, state.ReceiptPresent, "the receipt must roll back")
			require.Equal(t, audit.MaxKnownSchemaVersion-1, state.RecordedVersion, "the deferred ceiling must roll back")

			var occurrences, bindings, converted, retired int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, pinsRetiredAgentSlash).Scan(&converted))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, pinsRetiredAgent).Scan(&retired))
			require.Zero(t, occurrences, "the replayed projection must roll back")
			require.Zero(t, bindings, "the replayed bindings must roll back")
			require.Zero(t, converted, "the converted key must roll back")
			require.Equal(t, 1, retired, "the retired key must survive the rollback")

			if tc.point == injectAfterReplay {
				require.GreaterOrEqual(t, replayRows, 1, "the replay must have written an occurrence before the fault")
				require.GreaterOrEqual(t, replayBindings, 1, "the replay must have written a binding before the fault")
				require.Equal(t, 1, replayConverted, "the conversion must have rewritten the retired key before the fault")
			}
		})
	}
}

// TestBelowTargetRefusalNamesTheAppliedCatchUpAndWritesNothing pins the
// below-target bare-marker variant: on a store BELOW the cutover target, with
// the marker injected BEFORE P1 so this run's archive is taken after the mark,
// the scoped P3a refusal must name the applied catch-up, carry the MARKED-ARCHIVE
// branch, leave no receipt and no raised floor, and leave the store's committed
// content equal to the PRE-P3 content captured at the seam — neither the state
// immediately after the injection nor the pristine fixture.
func TestBelowTargetRefusalNamesTheAppliedCatchUpAndWritesNothing(t *testing.T) {
	path := repoFixture(t)
	pristine := fileDigest(t, path)

	// P0 sees the markerless legacy store; the mark lands before P1's archive.
	insertMarker(t, path)
	afterInjection := fileDigest(t, path)

	catchUp, err := RunCatchUp(context.Background(), CatchUpOptions{StorePath: path, Now: time.Now})
	require.NoError(t, err)
	require.True(t, catchUp.Archive.Marker.Present, "the archive is taken after the injection, so it carries the mark")
	require.Greater(t, catchUp.AppliedCatchUpSteps, 0, "the below-target fixture applies catch-up steps")
	require.NoError(t, catchUp.Trail.Close())

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer db.Close()

	var seamDigest string
	opts := ConvertOptions{
		StorePath:           path,
		Archive:             catchUp.Archive,
		AppliedCatchUpSteps: catchUp.AppliedCatchUpSteps,
	}
	opts.beforeBegin = func() { seamDigest = fileDigest(t, path) }

	_, err = Convert(context.Background(), db, opts)
	require.Error(t, err)
	require.Equal(t, 5, pasterrors.ExitCode(err))
	fix := structuredFix(t, err)
	require.Contains(t, fix, "carries the mark and post-dates it")
	require.Contains(t, fix, "it is NOT the independent pre-marking copy")
	require.Contains(t, structuredImpact(t, err), fmt.Sprintf("%d step(s) applied this run", catchUp.AppliedCatchUpSteps), "the scoped refusal must name the applied catch-up")

	final := fileDigest(t, path)
	require.Equal(t, seamDigest, final, "the refusal's conversion transaction must write nothing")
	require.NotEqual(t, afterInjection, final, "the committed catch-up moved the file off the post-injection state")
	require.NotEqual(t, pristine, final, "the committed catch-up moved the file off the pristine fixture")

	state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, stateErr)
	require.Equal(t, OutcomeRefuseUnbackedMark, state.Outcome)
	require.False(t, state.ReceiptPresent, "the refusal writes no receipt")
	require.Equal(t, audit.MaxKnownSchemaVersion-1, state.RecordedVersion, "the refusal raises no floor")
}

// TestNoArchiveScopedRefusalBranchIsPinned pins the no-archive branch of the
// scoped P3a refusal: when the store did not exist at P1's own check, this run
// made no archive, and a store created at that path afterwards is neither
// archived nor covered by this run. The refusal must say so instead of naming an
// archive it never took.
func TestNoArchiveScopedRefusalBranchIsPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")

	catchUp, err := RunCatchUp(context.Background(), CatchUpOptions{StorePath: path, Now: time.Now})
	require.NoError(t, err)
	require.True(t, catchUp.Archive.Skipped, "P1 skips the archive when the store did not exist")
	require.Empty(t, catchUp.Archive.Path)
	require.NoError(t, catchUp.Trail.Close())

	// A store created at that path AFTER P1's check; a bare mark makes the
	// under-lock verdict refuse.
	insertMarker(t, path)

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer db.Close()

	_, err = Convert(context.Background(), db, ConvertOptions{
		StorePath:           path,
		Archive:             catchUp.Archive,
		AppliedCatchUpSteps: catchUp.AppliedCatchUpSteps,
	})
	require.Error(t, err)
	require.Equal(t, 5, pasterrors.ExitCode(err))
	fix := structuredFix(t, err)
	require.Contains(t, fix, "This run made no archive")
	require.NotContains(t, fix, "is markerless: it was taken before the mark")
	require.NotContains(t, fix, "carries the mark and post-dates it")

	state, stateErr := EvaluateCutoverOnHandle(context.Background(), db, path, true)
	require.NoError(t, stateErr)
	require.Equal(t, OutcomeRefuseUnbackedMark, state.Outcome)
	require.False(t, state.ReceiptPresent)
}

// TestRestoreByReplacementProvesTheReceiptDigestAndPreCutoverKeys pins the
// restore proof the design's recovery procedure depends on. The store is built
// in WAL mode and converted so a REAL receipt records the archive's digest; a
// post-archive write is then left in the WAL. Restore by file replacement must
// (i) reproduce the exact bytes the receipt recorded, (ii) remove both stale
// -wal/-shm siblings so the post-archive write cannot resurface, and (iii) leave
// the pre-cutover retired keys present byte-exact in a valid store.
func TestRestoreByReplacementProvesTheReceiptDigestAndPreCutoverKeys(t *testing.T) {
	path, db := lifecycleBearingStore(t)

	archive, err := ArchiveStore(context.Background(), path, t.TempDir(), time.Now())
	require.NoError(t, err)
	require.False(t, archive.Marker.Present, "the pre-conversion archive is markerless")

	report, err := Convert(context.Background(), db, ConvertOptions{StorePath: path, Archive: archive})
	require.NoError(t, err)
	require.True(t, report.ReceiptWritten)

	var receiptDigest string
	require.NoError(t, db.QueryRow(`SELECT archive_sha256 FROM pasture_id_cutover WHERE singleton_id = 0`).Scan(&receiptDigest))
	require.Equal(t, archive.SHA256, receiptDigest, "the receipt records the archive's digest")

	// A post-archive committed write, left in the WAL (no checkpoint). A reader
	// handle with a PINNED connection stays open so closing the writer does not
	// checkpoint and delete the WAL: the operator's stale-sibling case is exactly
	// a store whose -wal still holds committed bytes.
	holder, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	holderConn, err := holder.Conn(context.Background())
	require.NoError(t, err)
	defer holderConn.Close()
	defer holder.Close()
	_, err = db.Exec(`INSERT INTO pasture_agent_categories(agent_id) VALUES('post-archive')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	for _, sibling := range []string{path + "-wal", path + "-shm"} {
		info, statErr := os.Stat(sibling)
		require.NoError(t, statErr, "the stale sibling %s must exist before the restore", sibling)
		require.Greater(t, info.Size(), int64(0), "the stale sibling %s must carry bytes", sibling)
	}

	// Restore by file replacement, removing both stale siblings.
	restored, err := os.ReadFile(archive.Path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, restored, 0o600))
	for _, sibling := range []string{path + "-wal", path + "-shm"} {
		require.NoError(t, os.Remove(sibling))
	}
	for _, sibling := range []string{path + "-wal", path + "-shm"} {
		_, statErr := os.Stat(sibling)
		require.True(t, os.IsNotExist(statErr), "restore must leave no stale %s sibling", sibling)
	}

	require.Equal(t, receiptDigest, fileDigest(t, path), "the restored bytes must equal the digest the receipt recorded")

	read, err := dbconn.OpenReadOnlyDB(path)
	require.NoError(t, err)
	defer read.Close()
	var integrity string
	require.NoError(t, read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	require.Equal(t, "ok", integrity, "the restored file must be a valid store an older build can open")

	var postArchive, retiredKey int
	require.NoError(t, read.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id='post-archive'`).Scan(&postArchive))
	require.Zero(t, postArchive, "a post-archive row in the stale WAL must not resurface")
	require.NoError(t, read.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, pinsRetiredAgent).Scan(&retiredKey))
	require.Equal(t, 1, retiredKey, "the restored store must carry the pre-cutover retired key byte-exact")

	// The restored store is the PRE-cutover state the previous build wrote: the
	// current state machine must classify it as markerless, not NORMAL.
	state, err := InspectOpenState(context.Background(), path)
	require.NoError(t, err)
	require.NotEqual(t, OutcomeNormal, state.Outcome, "a restored pre-cutover archive is not a cut-over store")
	require.False(t, state.MarkerPresent)
}

// TestArchiveStoreIncludesWALResidentRows pins P1's archive completeness over
// the production journal mode: a row committed into an uncheckpointed WAL must
// be present in the archive. A main-file-only copy cannot see it, which is
// exactly why the archive is taken through a connection (VACUUM INTO) rather
// than by copying bytes.
func TestArchiveStoreIncludesWALResidentRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`PRAGMA wal_autocheckpoint=0`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE wal_marker (id INTEGER PRIMARY KEY, note TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO wal_marker(note) VALUES('wal-resident')`)
	require.NoError(t, err)

	walInfo, err := os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Greater(t, walInfo.Size(), int64(0), "the row must sit in the WAL")

	// A naive copy of the main database file alone does not contain the table.
	mainBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	naive := filepath.Join(t.TempDir(), "naive.db")
	require.NoError(t, os.WriteFile(naive, mainBytes, 0o600))
	require.False(t, mainFileCopySeesTable(t, naive, "wal_marker"), "the row is WAL-resident: a main-file-only copy cannot see it")

	archive, err := ArchiveStore(context.Background(), path, t.TempDir(), time.Now())
	require.NoError(t, err)
	require.False(t, archive.Skipped)
	require.True(t, mainFileCopySeesTable(t, archive.Path, "wal_marker"), "the archive must contain the WAL-resident schema")

	archived, err := dbconn.OpenReadOnlyDB(archive.Path)
	require.NoError(t, err)
	defer archived.Close()
	var note string
	require.NoError(t, archived.QueryRow(`SELECT note FROM wal_marker WHERE id = 1`).Scan(&note))
	require.Equal(t, "wal-resident", note, "the archive must contain the WAL-resident row")
}

// mainFileCopySeesTable reports whether a store file exposes the named table
// through a fresh read-only handle. An unopenable or incomplete copy reports
// false.
func mainFileCopySeesTable(t *testing.T, path, table string) bool {
	t.Helper()
	db, err := dbconn.OpenReadOnlyDB(path)
	if err != nil {
		return false
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		return false
	}
	return count == 1
}
