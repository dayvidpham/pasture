package definitions_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"sync"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// The bridge checks resolve the legacy metamodel activation through the
// versioned owner codec and the producer operation recipe. All cases use real
// production owners with injected identity and clock; synthetic producer input
// is labelled test data, never a live capture.

const legacyDefinitionKind = "pasture.lifecycle.definition.v1"
const legacyDefinitionStateKind = "pasture.lifecycle.definition-state.v1"

func commitProducer(t *testing.T, service receipt.Service, manifest model.LifecycleMetamodelManifest, body []byte) {
	t.Helper()
	write, err := receipt.NewDefinitionActivation(manifest, body)
	require.NoError(t, err)
	intent, refusal := gate.NewDefinitionActivationIntent(manifest.Content)
	require.Nil(t, refusal)
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal)
	_, err = service.Commit(context.Background(), warrant, write)
	require.NoError(t, err)
}

func journalRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&count))
	return count
}

func TestMetamodelBridgeResolvesActiveProducer(t *testing.T) {
	t.Parallel()
	path, tracker, service := activatedStore(t)
	manifest := metamodel.Active()
	legacy, err := receipt.EnsureActiveMetamodel(context.Background(), service)
	require.NoError(t, err)
	require.Equal(t, manifest.Content, legacy.Definition.Content, "legacy producer identity is not rewritten")
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	before := journalRowCount(t, db)
	ref, snapshot, state, err := receipt.BridgeActiveMetamodel(context.Background(), reader)
	require.NoError(t, err)
	require.Equal(t, journalRowCount(t, db), before, "bridging is read-only")
	// The bridge never relabels the metamodel kind: the resolved kind stays
	// the metamodel kind, never the interpreter kind.
	require.Equal(t, model.DefinitionMetamodel, ref.Definition.Kind)
	require.NotEqual(t, model.DefinitionInterpreter, ref.Definition.Kind)
	require.Equal(t, metamodel.LifecycleMetamodelID, snapshot.ID)
	require.Equal(t, metamodel.LifecycleMetamodelVersion, snapshot.Version)
	require.Equal(t, model.DefinitionCanonicalJSON, snapshot.Codec)
	// Explicit non-equal raw/normalized positive case: the producer content
	// identity hashes the raw body while the persisted evidence digest hashes
	// the journal-normalized bytes, and the new ref carries the latter.
	raw := metamodel.Body()
	normalized, err := definitions.NormalizePayload(legacyDefinitionKind, raw)
	require.NoError(t, err)
	require.NotEqual(t, raw, normalized, "producer raw bytes must differ from journal-normalized bytes for this case to prove anything")
	require.NotEqual(t, manifest.Content, ref.Definition.Content)
	require.Equal(t, model.ContentIdentity(sha256.Sum256(normalized)), ref.Definition.Content)
	require.Equal(t, normalized, snapshot.Body())
	require.Equal(t, ref.Definition.Content, model.ContentIdentity(sha256.Sum256(snapshot.Body())))
	require.Greater(t, int64(state.LatestState), int64(ref.Definition.Definition))
	require.Equal(t, ref.Definition.Definition, state.LatestFact.Definition)
	require.Equal(t, model.DefinitionActivated, state.LatestFact.Event)
	// Close and reopen: the bridge resolves the same rows from real storage.
	require.NoError(t, tracker.Close())
	reopened, err := provenance.OpenSQLite(path)
	require.NoError(t, err)
	defer reopened.Close()
	reopenedReader, err := receipt.NewDefinitionReader(reopened.Journal())
	require.NoError(t, err)
	again, againSnapshot, _, err := receipt.BridgeActiveMetamodel(context.Background(), reopenedReader)
	require.NoError(t, err)
	require.Equal(t, ref, again)
	require.Equal(t, snapshot.Body(), againSnapshot.Body())
	require.NoError(t, reopened.Journal().VerifyIntegrity())
}

func TestMetamodelBridgeRacesClean(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	_, err := receipt.EnsureActiveMetamodel(context.Background(), service)
	require.NoError(t, err)
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	const n = 4
	refs := make([]model.LifecycleMetamodelRef, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			refs[i], _, _, errs[i] = receipt.BridgeActiveMetamodel(context.Background(), reader)
		}()
	}
	close(start)
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i])
		require.Equal(t, refs[0], refs[i])
	}
}

func TestMetamodelBridgeResolvesNonCanonicalProducer(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	// Synthetic historical-style producer: valid owner shape whose raw member
	// order is not journal-canonical, with content distinct from the active
	// producer. Labelled test data, committed through the real legacy writer.
	raw := bytes.ReplaceAll(metamodel.Body(), []byte(`"session"`), []byte(`"session-historical"`))
	require.NotEqual(t, metamodel.Body(), raw)
	rawDigest := sha256.Sum256(raw)
	require.NotEqual(t, metamodel.Active().Content, model.ContentIdentity(rawDigest))
	manifest := model.LifecycleMetamodelManifest{ID: metamodel.LifecycleMetamodelID, Version: metamodel.LifecycleMetamodelVersion, Content: model.ContentIdentity(rawDigest)}
	commitProducer(t, service, manifest, raw)
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	ref, snapshot, _, err := receipt.BridgeMetamodelProducer(reader, manifest)
	require.NoError(t, err)
	require.Equal(t, model.DefinitionMetamodel, ref.Definition.Kind)
	normalized, err := definitions.NormalizePayload(legacyDefinitionKind, raw)
	require.NoError(t, err)
	require.NotEqual(t, raw, normalized)
	require.Equal(t, normalized, snapshot.Body())
	require.Equal(t, model.ContentIdentity(sha256.Sum256(normalized)), ref.Definition.Content)
	require.NotEqual(t, manifest.Content, ref.Definition.Content)
}

func TestMetamodelBridgeResolvesDistinctCanonicalProducer(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	// A second producer whose raw bytes are already canonical: the new ref
	// content equals the producer content here, proving the bridge does not
	// require inequality, it validates whatever the owner evidence shows.
	raw := bytes.ReplaceAll(metamodel.Body(), []byte(`"session"`), []byte(`"session-canonical"`))
	normalized, err := definitions.NormalizePayload(legacyDefinitionKind, raw)
	require.NoError(t, err)
	rawDigest := sha256.Sum256(normalized)
	manifest := model.LifecycleMetamodelManifest{ID: metamodel.LifecycleMetamodelID, Version: metamodel.LifecycleMetamodelVersion, Content: model.ContentIdentity(rawDigest)}
	commitProducer(t, service, manifest, normalized)
	reader, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	ref, snapshot, _, err := receipt.BridgeMetamodelProducer(reader, manifest)
	require.NoError(t, err)
	require.Equal(t, manifest.Content, ref.Definition.Content)
	require.Equal(t, normalized, snapshot.Body())
}

func TestMetamodelBridgeRejects(t *testing.T) {
	t.Parallel()
	t.Run("zero manifest", func(t *testing.T) {
		t.Parallel()
		_, tracker, _ := activatedStore(t)
		reader, err := receipt.NewDefinitionReader(tracker.Journal())
		require.NoError(t, err)
		_, _, _, err = receipt.BridgeMetamodelProducer(reader, model.LifecycleMetamodelManifest{})
		require.Error(t, err)
	})
	t.Run("unknown producer content", func(t *testing.T) {
		t.Parallel()
		_, tracker, service := activatedStore(t)
		_, err := receipt.EnsureActiveMetamodel(context.Background(), service)
		require.NoError(t, err)
		reader, err := receipt.NewDefinitionReader(tracker.Journal())
		require.NoError(t, err)
		manifest := metamodel.Active()
		manifest.Content[0] ^= 1
		_, _, _, err = receipt.BridgeMetamodelProducer(reader, manifest)
		require.Error(t, err, "wrong-manifest content must not resolve")
	})
	t.Run("not activated", func(t *testing.T) {
		t.Parallel()
		_, tracker, _ := activatedStore(t)
		reader, err := receipt.NewDefinitionReader(tracker.Journal())
		require.NoError(t, err)
		_, _, _, err = receipt.BridgeMetamodelProducer(reader, metamodel.Active())
		require.Error(t, err)
	})
	t.Run("nil reader", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := receipt.BridgeMetamodelProducer(nil, metamodel.Active())
		require.Error(t, err)
	})
}

func TestMetamodelBridgeRejectsDamagedRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// damage runs against the open store after activation; target selects
		// the snapshot row (true) or the state row (false) for payload cases.
		damage func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef)
	}{
		{name: "snapshot payload damaged", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET payload='{}' WHERE journal_id=?", snapshotID)
			require.NoError(t, err)
		}},
		{name: "snapshot digest damaged", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET content_digest=x'00' WHERE journal_id=?", snapshotID)
			require.NoError(t, err)
		}},
		{name: "snapshot body swapped with matching digest", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			other := bytes.ReplaceAll(metamodel.Body(), []byte(`"session"`), []byte(`"session-swapped"`))
			normalized, err := definitions.NormalizePayload(legacyDefinitionKind, other)
			require.NoError(t, err)
			sum := sha256.Sum256(normalized)
			_, err = db.Exec("UPDATE journal_evidence SET payload=?,content_digest=? WHERE journal_id=?", string(normalized), sum[:], snapshotID)
			require.NoError(t, err)
		}},
		{name: "snapshot evidence wrong kind", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET evidence_kind='pasture.lifecycle.definition-state.v1' WHERE journal_id=?", snapshotID)
			require.NoError(t, err)
		}},
		{name: "state evidence wrong kind", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET evidence_kind='pasture.lifecycle.definition.v1' WHERE journal_id=?", stateID)
			require.NoError(t, err)
		}},
		{name: "state row missing", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("DELETE FROM journal_evidence WHERE journal_id=?", stateID)
			require.NoError(t, err)
		}},
		{name: "state payload forged with matching digest", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET payload='{\"definition\":\"00\",\"event\":\"retired\"}' WHERE journal_id=?", stateID)
			require.NoError(t, err)
			var payload []byte
			require.NoError(t, db.QueryRow("SELECT payload FROM journal_evidence WHERE journal_id=?", stateID).Scan(&payload))
			normalized, err := definitions.NormalizePayload(legacyDefinitionStateKind, payload)
			require.NoError(t, err)
			sum := sha256.Sum256(normalized)
			_, err = db.Exec("UPDATE journal_evidence SET payload=?,content_digest=? WHERE journal_id=?", string(normalized), sum[:], stateID)
			require.NoError(t, err)
		}},
		{name: "state digest damaged", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_evidence SET content_digest=x'00' WHERE journal_id=?", stateID)
			require.NoError(t, err)
		}},
		{name: "definition result slot missing", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("DELETE FROM journal_operation_result_slots WHERE journal_id=(SELECT journal_id FROM journal_operations WHERE operation_id LIKE 'pasture.lifecycle.definition.%') AND result_slot_id='definition'")
			require.NoError(t, err)
		}},
		{name: "producing operation renamed", damage: func(t *testing.T, db *sql.DB, snapshotID, stateID int64, ref *model.LifecycleMetamodelRef) {
			_, err := db.Exec("UPDATE journal_operations SET operation_id='tampered-operation' WHERE operation_id LIKE 'pasture.lifecycle.definition.%'")
			require.NoError(t, err)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path, tracker, service := activatedStore(t)
			_, err := receipt.EnsureActiveMetamodel(context.Background(), service)
			require.NoError(t, err)
			reader, err := receipt.NewDefinitionReader(tracker.Journal())
			require.NoError(t, err)
			ref, _, state, err := receipt.BridgeActiveMetamodel(context.Background(), reader)
			require.NoError(t, err)
			db, err := sql.Open("sqlite", path)
			require.NoError(t, err)
			defer db.Close()
			tc.damage(t, db, int64(ref.Definition.Definition), int64(state.LatestState), &ref)
			var before int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&before))
			invalid, invalidSnapshot, invalidState, err := receipt.BridgeActiveMetamodel(context.Background(), reader)
			require.Error(t, err)
			require.Equal(t, model.LifecycleMetamodelRef{}, invalid)
			require.Equal(t, model.DefinitionSnapshot{}, invalidSnapshot)
			require.Equal(t, model.DefinitionStateRecord{}, invalidState)
			var structured *pasterrors.StructuredError
			require.True(t, errors.As(err, &structured))
			require.NotEmpty(t, structured.Where)
			require.NotEmpty(t, structured.Why)
			require.NotEmpty(t, structured.Impact)
			require.NotEmpty(t, structured.Fix)
			var after int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&after))
			require.Equal(t, before, after)
		})
	}
}
