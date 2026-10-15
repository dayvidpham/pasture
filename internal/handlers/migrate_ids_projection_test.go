package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

const (
	retiredProjectionAgent      = "file://project--01968a3c-9d4f-7c8a-bc12-feedfacecafe"
	retiredProjectionAgentSlash = "file://project/01968a3c-9d4f-7c8a-bc12-feedfacecafe"
)

// buildRetiredStoreWithStaleProjection builds a genuine retired store whose
// disposable lifecycle projection must be rebuilt: one committed lifecycle
// occurrence with one native binding lives in the journal, while
// lifecycle_occurrences and lifecycle_occurrence_bindings are empty. The store
// is markerless and populated with a retired identity, so the offline command
// converts it through the full pipeline (archive, catch-up, conversion,
// reopen verification) rather than short-circuiting as already current.
func buildRetiredStoreWithStaleProjection(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	blobDigest := seedProjectionOccurrence(t, path)

	db, err := dbconn.OpenSharedDB(path)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, audit.MigrateContextToCeiling(ctx, db, audit.MaxKnownSchemaVersion-1))
	for _, table := range []string{"provenance_id_format", "pasture_id_cutover"} {
		_, err := db.Exec("DROP TABLE IF EXISTS " + table)
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO pasture_agent_categories(agent_id) VALUES(?)`, retiredProjectionAgent)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO lifecycle_payload_blobs(digest, body, byte_count, written_at) VALUES(?,?,?,?)`,
		blobDigest, []byte("body"), len("body"), time.Now().UTC().UnixNano())
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// The projection is stale by construction: the journal carries the
	// occurrence but no rebuild has run, so both projection tables are empty.
	// This is the state that made the old count check fail 0 to 1.
	probe, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer probe.Close()
	var occurrences, bindings int
	require.NoError(t, probe.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, probe.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.Zero(t, occurrences, "the fixture projection must start empty so the replay repopulates it")
	require.Zero(t, bindings, "the fixture bindings must start empty so the replay repopulates them")
	return path
}

// seedProjectionOccurrence commits one lifecycle occurrence with one native
// binding through the production Provenance journal and returns the digest of
// the payload blob it names. It mirrors the conversion-pipeline replay fixture
// so the end-to-end command has journal truth to re-derive.
func seedProjectionOccurrence(t *testing.T, path string) string {
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
			OperationAuthorityID: provenance.OperationAuthorityID("projection-e2e.bootstrap"),
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

	task, err := tr.As(actor, authority).Create("projection-e2e", "occurrence subject", "the task the lifecycle occurrence names", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
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
		Bindings: []model.NativeBinding{{Kind: 1, NativeName: "session_id", Value: "projection-e2e"}},
		Capture:  model.CaptureValid,
		Body:     ref,
	})
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := sha256.Sum256(append([]byte("pasture.lifecycle.receipt.append/v1\x00"), payload...))
	input := provenance.OperationInput{
		OperationID:        provenance.OperationID("projection-e2e.occurrence.v1"),
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

// TestMigrateIDsRetiredStoreWithStaleProjectionEndToEnd converts a retired
// store whose disposable projection must be rebuilt from empty to journal
// truth, through the whole command including the reopen verification. On the
// old count check this failed as a false positive (lifecycle_occurrences 0
// to 1); after the fix the conversion, the semantic projection proof and the
// identity-count proof all succeed.
func TestMigrateIDsRetiredStoreWithStaleProjectionEndToEnd(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	path := buildRetiredStoreWithStaleProjection(t)

	var out bytes.Buffer
	code, err := handlers.MigrateIDs(&out, handlers.MigrateIDsInput{DBPath: path, BackupDir: storeDir}, types.OutputText)
	require.NoError(t, err, "a retired store with a stale projection must convert including verification")
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "reopen proof:")

	require.Equal(t, 1, countArchives(t, storeDir), "the conversion makes exactly one archive")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	var occurrences, bindings, converted, retired int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&occurrences))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrence_bindings`).Scan(&bindings))
	require.GreaterOrEqual(t, occurrences, 1, "the replay must repopulate the projection from empty")
	require.GreaterOrEqual(t, bindings, 1, "the replay must repopulate the bindings from empty")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, retiredProjectionAgentSlash).Scan(&converted))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pasture_agent_categories WHERE agent_id=?`, retiredProjectionAgent).Scan(&retired))
	require.Equal(t, 1, converted, "the retired identity must convert to slash form")
	require.Zero(t, retired, "no retired two-dash form may survive in the converted column")

	// The converted store reopens through the production tracker, and a second
	// run is the front-loaded no-op with no second archive.
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	var second bytes.Buffer
	code, err = handlers.MigrateIDs(&second, handlers.MigrateIDsInput{DBPath: path, BackupDir: storeDir}, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, second.String(), "already current")
	require.Equal(t, 1, countArchives(t, storeDir), "a second run must not make another archive")
}
