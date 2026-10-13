package definitions_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type identity struct{ value receipt.Identity }

func (i identity) ResolveLifecycleIdentity(context.Context) (receipt.Identity, error) {
	return i.value, nil
}

type clock struct{}

func (clock) Now() time.Time { return time.Unix(1000, 0).UTC() }

// Real production owners and journal are used here with injected identity and
// clock. No private fields, test-only exports or parallel journal are involved.
func activatedStore(t *testing.T) (string, provenance.Tracker, receipt.Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "definitions.db")
	tracker, err := provenance.OpenSQLite(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	agent, err := tracker.RegisterSoftwareAgent("definition-test", "runtime-owner", "1", "synthetic owner test")
	require.NoError(t, err)
	root, err := tracker.Journal().Apply(provenance.OperationInput{OperationID: "definition-test-bootstrap", ActorID: agent.ID, CommandDigest: []byte("definition-test-bootstrap"), RecordedAt: clock{}.Now().UnixNano(), Effects: []provenance.Effect{{Sort: provenance.EffectBootstrapAuthority, BootstrapLabel: "definition-test", ResultSlot: "authority"}}})
	require.NoError(t, err)
	require.Len(t, root.ResultSlots, 1)
	service := receipt.Service{Identity: identity{receipt.Identity{Actor: agent.ID, Authority: root.ResultSlots[0].ProducedJournalID}}, Clock: clock{}, Appender: receipt.JournalAppender{Journal: tracker.Journal(), Clock: clock{}, Deadline: timeouts.TestProfile().Ingress()}}
	return path, tracker, service
}

func evidenceCount(t *testing.T, reader receipt.DefinitionReader) int {
	t.Helper()
	page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitions.EvidenceKind, "pasture.lifecycle.definition-state.v2"}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	require.NoError(t, err)
	return len(page.Rows)
}

func TestRuntimeActivationReopenAndNoDuplicateEvidence(t *testing.T) {
	t.Parallel()
	for _, contract := range definitions.RuntimeContracts() {
		t.Run(contract.String(), func(t *testing.T) {
			t.Parallel()
			path, tracker, service := activatedStore(t)
			document, err := definitions.NewRuntimeContract(contract)
			require.NoError(t, err)
			ref, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			require.NoError(t, err)
			second, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			require.NoError(t, err)
			require.Equal(t, ref, second)
			reader, err := receipt.NewDefinitionReader(tracker.Journal())
			require.NoError(t, err)
			require.Equal(t, 2, evidenceCount(t, reader))
			require.NoError(t, tracker.Close())
			reopened, err := provenance.OpenSQLite(path)
			require.NoError(t, err)
			defer reopened.Close()
			reader, err = receipt.NewDefinitionReader(reopened.Journal())
			require.NoError(t, err)
			snapshot, state, err := receipt.ResolveDefinition(reader, ref)
			require.NoError(t, err)
			require.Equal(t, document.Body(), snapshot.Body())
			require.Equal(t, document.ID(), snapshot.ID)
			require.Equal(t, document.Version(), snapshot.Version)
			require.Equal(t, model.ContentIdentity(sha256.Sum256(snapshot.Body())), ref.Content)
			require.Greater(t, int64(state.LatestState), int64(ref.Definition))
			require.Equal(t, clock{}.Now(), state.LatestFact.RecordedAt)
			require.Equal(t, ref.Definition, state.LatestFact.Definition)
			body := snapshot.Body()
			body[0] ^= 1
			require.Equal(t, document.Body(), snapshot.Body())
			_, _, err = receipt.ResolveDefinitionAt(reader, ref, provenance.JournalID(ref.Definition))
			require.Error(t, err, "snapshot-only watermark must not see the later activation")
			require.Equal(t, 2, evidenceCount(t, reader), "all resolution is read-only")
			require.NoError(t, reopened.Journal().VerifyIntegrity())
		})
	}
}

func TestRuntimeActivationRacingFirstWriters(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	require.NoError(t, err)
	service.Appender.Deadline = timeouts.TestProfile().WorkflowResult()
	const n = 4
	refs := make([]model.DefinitionRef, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			refs[i], errs[i] = receipt.EnsureActiveDefinition(context.Background(), service, doc)
		}()
	}
	close(start)
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i])
		require.Equal(t, refs[0], refs[i])
	}
	r, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	require.Equal(t, 2, evidenceCount(t, r))
}

func TestRuntimeDefinitionTransactionReaderUsesCallerChanges(t *testing.T) {
	t.Parallel()
	path, tracker, service := activatedStore(t)
	doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	require.NoError(t, err)
	ref, err := receipt.EnsureActiveDefinition(context.Background(), service, doc)
	require.NoError(t, err)
	ordinary, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	reader, err := receipt.NewTransactionDefinitionReader(context.Background(), tx)
	require.NoError(t, err)
	snapshot, _, err := receipt.ResolveDefinition(reader, ref)
	require.NoError(t, err)
	require.True(t, bytes.Equal(snapshot.Body(), doc.Body()))
	write, err := receipt.NewEnvelopeDefinitionActivation(doc)
	require.NoError(t, err)
	result, err := reader.LookupCommitted(write.OperationID())
	require.NoError(t, err)
	require.Equal(t, provenance.CommittedExact, result.Kind)
	_, err = tx.Exec("DELETE FROM journal_operation_result_slots WHERE journal_id=? AND result_slot_id='definition-state'", result.AnchorJournalID)
	require.NoError(t, err)
	_, _, err = receipt.ResolveDefinition(reader, ref)
	require.Error(t, err, "same-transaction lookup must see deleted slot")
	_, _, err = receipt.ResolveDefinition(ordinary, ref)
	require.NoError(t, err, "another connection cannot observe caller's uncommitted slot deletion")
	require.NoError(t, tx.Rollback())
	_, _, err = receipt.ResolveDefinition(ordinary, ref)
	require.NoError(t, err)
}

func TestDefinitionCommitRejectsOtherClassAndZeroWrite(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	intent, refusal := gate.NewLineageIntent(definitions.RuntimeContracts()[0].Harness(), 1)
	require.Nil(t, refusal)
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal)
	otherWrite, err := receipt.NewLineageLinks([]lineage.LinkFact{{Harness: definitions.RuntimeContracts()[0].Harness(), Kind: pastureruntime.IdentitySession, Value: "synthetic-session", From: 1, To: 2}})
	require.NoError(t, err)
	_, err = service.Commit(context.Background(), warrant, otherWrite)
	require.Error(t, err, "a real constructor and its matching non-definition warrant cannot enter definition Commit")
	_, err = service.Commit(context.Background(), warrant, receipt.GatedWrite{})
	require.Error(t, err)
	intent, refusal = gate.NewEnvelopeDefinitionActivationIntent(model.DefinitionRuntimeContract, model.ContentIdentity{1})
	require.Nil(t, refusal)
	warrant, refusal = gate.Legalize(intent)
	require.Nil(t, refusal)
	_, err = service.Commit(context.Background(), warrant, receipt.GatedWrite{})
	require.Error(t, err)
	doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	require.NoError(t, err)
	write, err := receipt.NewEnvelopeDefinitionActivation(doc)
	require.NoError(t, err)
	_, err = service.Commit(context.Background(), gate.Warrant{}, write)
	require.Error(t, err)
	r, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	require.Zero(t, evidenceCount(t, r))
}

func TestDefinitionCorruptionFailsClosedWithoutRepair(t *testing.T) {
	t.Parallel()
	var fixtures struct {
		Cases []struct {
			Name     string `yaml:"name"`
			Change   string `yaml:"change"`
			SQL      string `yaml:"sql"`
			Redigest bool   `yaml:"redigest"`
		} `yaml:"cases"`
	}
	testutil.LoadFixtures(t, testutil.DefinitionCorruption, &fixtures)
	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			path, tracker, service := activatedStore(t)
			doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
			require.NoError(t, err)
			ref, err := receipt.EnsureActiveDefinition(context.Background(), service, doc)
			require.NoError(t, err)
			write, err := receipt.NewEnvelopeDefinitionActivation(doc)
			require.NoError(t, err)
			operation, err := tracker.Journal().LookupCommitted(write.OperationID())
			require.NoError(t, err)
			reader, err := receipt.NewDefinitionReader(tracker.Journal())
			require.NoError(t, err)
			snapshot, state, err := receipt.ResolveDefinition(reader, ref)
			require.NoError(t, err)
			require.Equal(t, doc.Body(), snapshot.Body())
			db, err := sql.Open("sqlite", path)
			require.NoError(t, err)
			defer db.Close()
			var target int64
			switch tc.Change {
			case "zero-ref":
				ref.Definition = 0
			case "dangling-ref":
				ref.Definition += 10000
			case "wrong-ref-kind":
				ref.Kind = model.DefinitionInterpreter
			case "wrong-ref-content":
				ref.Content[0] ^= 1
			case "snapshot":
				target = int64(ref.Definition)
			case "state":
				target = int64(state.LatestState)
			case "operation":
				target = int64(operation.AnchorJournalID)
			default:
				t.Fatalf("unknown corruption fixture %q", tc.Change)
			}
			if tc.SQL != "" {
				_, err = db.Exec(tc.SQL, target)
				require.NoError(t, err)
			}
			if tc.Redigest {
				var payload []byte
				require.NoError(t, db.QueryRow("SELECT payload FROM journal_evidence WHERE journal_id=?", target).Scan(&payload))
				kind := definitions.EvidenceKind
				if tc.Change == "state" {
					kind = "pasture.lifecycle.definition-state.v2"
				}
				normalized, err := definitions.NormalizePayload(kind, payload)
				require.NoError(t, err)
				sum := sha256.Sum256(normalized)
				_, err = db.Exec("UPDATE journal_evidence SET payload=?,content_digest=? WHERE journal_id=?", string(normalized), sum[:], target)
				require.NoError(t, err)
				if tc.Change == "snapshot" {
					ref.Content = model.ContentIdentity(sum)
				}
			}
			var before int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&before))
			invalid, invalidState, err := receipt.ResolveDefinition(reader, ref)
			require.Error(t, err)
			require.Equal(t, model.DefinitionSnapshot{}, invalid)
			require.Equal(t, model.DefinitionStateRecord{}, invalidState)
			var structured *pasterrors.StructuredError
			require.True(t, errors.As(err, &structured))
			require.NotEmpty(t, structured.Where)
			require.NotEmpty(t, structured.Why)
			require.NotEmpty(t, structured.Impact)
			require.NotEmpty(t, structured.Fix)
			if tc.SQL != "" && tc.Name != "producing operation identity damaged" {
				_, err := receipt.EnsureActiveDefinition(context.Background(), service, doc)
				require.Error(t, err, "corrupt committed activation must not be treated as absence")
			}
			var after int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&after))
			require.Equal(t, before, after)
		})
	}
}

func TestExistingMetamodelActivationRecipeRemainsCompatible(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	manifest := metamodel.Active()
	write, err := receipt.NewDefinitionActivation(manifest, metamodel.Body())
	require.NoError(t, err)
	require.Equal(t, provenance.OperationID("pasture.lifecycle.definition."+hex.EncodeToString(manifest.Content[:16])), write.OperationID())
	first, err := receipt.EnsureActiveMetamodel(context.Background(), service)
	require.NoError(t, err)
	second, err := receipt.EnsureActiveMetamodel(context.Background(), service)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, manifest.Content, first.Definition.Content, "legacy producer identity is not rewritten")
	result, err := tracker.Journal().LookupCommitted(write.OperationID())
	require.NoError(t, err)
	require.Len(t, result.ResultSlots, 2)
	require.NoError(t, tracker.Journal().VerifyIntegrity())
}

func TestDefinitionCommitConflictingActorIsNotAReplayException(t *testing.T) {
	t.Parallel()
	_, tracker, service := activatedStore(t)
	doc, err := definitions.NewRuntimeContract(definitions.RuntimeContracts()[0])
	require.NoError(t, err)
	_, err = receipt.EnsureActiveDefinition(context.Background(), service, doc)
	require.NoError(t, err)
	actor, err := tracker.RegisterSoftwareAgent("definition-test", "other-owner", "1", "synthetic conflict control")
	require.NoError(t, err)
	other := service
	current, err := service.Identity.ResolveLifecycleIdentity(context.Background())
	require.NoError(t, err)
	current.Actor = actor.ID
	other.Identity = identity{current}
	write, err := receipt.NewEnvelopeDefinitionActivation(doc)
	require.NoError(t, err)
	intent, refusal := gate.NewEnvelopeDefinitionActivationIntent(doc.Kind(), doc.Content())
	require.Nil(t, refusal)
	warrant, refusal := gate.Legalize(intent)
	require.Nil(t, refusal)
	_, err = other.Commit(context.Background(), warrant, write)
	require.ErrorIs(t, err, provenance.ErrOperationConflict)
	r, err := receipt.NewDefinitionReader(tracker.Journal())
	require.NoError(t, err)
	require.Equal(t, 2, evidenceCount(t, r))
}
