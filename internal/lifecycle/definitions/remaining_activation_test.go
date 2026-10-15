package definitions_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// remainingOwner describes one remaining document owner under test. The
// constructor and generator must be the production owners; the ledger entry
// pins the released bytes.
type remainingOwner struct {
	name      string
	kind      model.DefinitionKind
	id        model.DefinitionID
	version   uint32
	wrongKind model.DefinitionKind
	construct func() (definitions.Document, error)
	generate  func() ([]byte, error)
}

func remainingOwners() []remainingOwner {
	return []remainingOwner{
		{name: "schema", kind: model.DefinitionLifecycleSchema, id: definitions.SchemaDocumentID, version: definitions.SchemaDocumentVersion, wrongKind: model.DefinitionInterpreter,
			construct: definitions.NewLifecycleSchema, generate: definitions.GenerateSchemaBody},
		{name: "retention", kind: model.DefinitionRetentionPolicy, id: definitions.RetentionDocumentID, version: definitions.RetentionDocumentVersion, wrongKind: model.DefinitionInterpreter,
			construct: definitions.NewRetentionPolicy, generate: definitions.GenerateRetentionBody},
		{name: "epoch-implementation", kind: model.DefinitionEpochImplementation, id: definitions.EpochDocumentID, version: definitions.EpochDocumentVersion, wrongKind: model.DefinitionInterpreter,
			construct: definitions.NewEpochImplementation, generate: definitions.GenerateEpochBody},
		{name: "interpreter", kind: model.DefinitionInterpreter, id: definitions.InterpreterDocumentID, version: definitions.InterpreterDocumentVersion, wrongKind: model.DefinitionEpochImplementation,
			construct: definitions.NewInterpreter, generate: definitions.GenerateInterpreterBody},
	}
}

// Real production owners and journal are used here with injected identity and
// clock, exactly as the runtime activation tests do. No private fields,
// test-only exports, or parallel journals are involved.
func TestRemainingKindsActivationReopenAndNoDuplicateEvidence(t *testing.T) {
	t.Parallel()
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			path, tracker, service := activatedStore(t)
			document, err := owner.construct()
			require.NoError(t, err)
			require.True(t, document.IsValid())
			require.Equal(t, owner.kind, document.Kind())
			require.Equal(t, owner.id, document.ID())
			require.Equal(t, owner.version, document.Version())
			ref, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			require.NoError(t, err)
			second, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
			require.NoError(t, err)
			require.Equal(t, ref, second)
			require.Equal(t, owner.kind, ref.Kind)
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
			require.Equal(t, document.Content(), ref.Content)
			require.Greater(t, int64(state.LatestState), int64(ref.Definition))
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

func TestRemainingKindsRacingFirstWriters(t *testing.T) {
	t.Parallel()
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			_, tracker, service := activatedStore(t)
			doc, err := owner.construct()
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
		})
	}
}

func TestRemainingKindsTransactionReaderUsesCallerChanges(t *testing.T) {
	t.Parallel()
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			path, tracker, service := activatedStore(t)
			doc, err := owner.construct()
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
		})
	}
}

func TestRemainingKindsCorruptionFailsClosedWithoutRepair(t *testing.T) {
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
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range fixtures.Cases {
				t.Run(tc.Name, func(t *testing.T) {
					t.Parallel()
					path, tracker, service := activatedStore(t)
					doc, err := owner.construct()
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
					sqlText := tc.SQL
					switch tc.Change {
					case "zero-ref":
						ref.Definition = 0
					case "dangling-ref":
						ref.Definition += 10000
					case "wrong-ref-kind":
						ref.Kind = owner.wrongKind
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
					// The shared fixture mutates the body kind to the
					// interpreter kind; per-kind wrong-kind mutation keeps the
					// control adversarial for every document under test.
					sqlText = strings.Replace(sqlText, "'$.kind',4)", fmt.Sprintf("'$.kind',%d)", owner.wrongKind), 1)
					if sqlText != "" {
						_, err = db.Exec(sqlText, target)
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
					if tc.SQL != "" {
						_, err := receipt.EnsureActiveDefinition(context.Background(), service, doc)
						require.Error(t, err, "corrupt committed activation must not be treated as absence")
					}
					var after int
					require.NoError(t, db.QueryRow("SELECT count(*) FROM journal").Scan(&after))
					require.Equal(t, before, after)
				})
			}
		})
	}
}

func TestRemainingGeneratedBodiesAgreeWithOwners(t *testing.T) {
	t.Parallel()
	var ledger struct {
		Versions []struct {
			ID      string `yaml:"id"`
			Version uint32 `yaml:"version"`
			SHA256  string `yaml:"sha256"`
		} `yaml:"versions"`
	}
	testutil.LoadFixtures(t, testutil.RemainingDocumentVersions, &ledger)
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			doc, err := owner.construct()
			require.NoError(t, err)
			generated, err := owner.generate()
			require.NoError(t, err)
			require.Equal(t, generated, doc.Body(), "compiled owners changed: bump document version and regenerate")
			require.Equal(t, model.ContentIdentity(sha256.Sum256(generated)), doc.Content())
			matched := false
			for _, entry := range ledger.Versions {
				if entry.ID == string(doc.ID()) && entry.Version == doc.Version() {
					content := doc.Content()
					require.Equal(t, entry.SHA256, hex.EncodeToString(content[:]), "a body change requires a document version bump")
					matched = true
				}
			}
			require.True(t, matched, "document coordinate must have a version ledger entry")
			body := doc.Body()
			body[0] ^= 1
			require.Equal(t, generated, doc.Body())
			decoded, err := definitions.Decode(generated)
			require.NoError(t, err)
			require.Equal(t, owner.kind, decoded.Kind())
			_, err = receipt.NewEnvelopeDefinitionActivation(decoded)
			require.Error(t, err, "read-only historical decoding must not provide an arbitrary new-writer JSON escape hatch")
		})
	}
}

func TestRemainingOwnerRejectsUnknownFieldsAndUnsupportedCoordinate(t *testing.T) {
	t.Parallel()
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			doc, err := owner.construct()
			require.NoError(t, err)
			mutations := map[string]func(fields map[string]any){
				"unknown": func(fields map[string]any) { fields["unknown"] = true },
				"version": func(fields map[string]any) { fields["version"] = 999 },
				"codec":   func(fields map[string]any) { fields["codec"] = 255 },
				"kind":    func(fields map[string]any) { fields["kind"] = 3 },
				"empty-specification": func(fields map[string]any) {
					spec, ok := fields["specification"].(map[string]any)
					require.True(t, ok)
					for key := range spec {
						delete(spec, key)
					}
				},
			}
			for name, mutate := range mutations {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					var fields map[string]any
					require.NoError(t, json.Unmarshal(doc.Body(), &fields))
					mutate(fields)
					raw, err := json.Marshal(fields)
					require.NoError(t, err)
					_, err = definitions.Decode(raw)
					require.Error(t, err)
				})
			}
		})
	}
}

func TestActiveForContractReturnsCompleteOwnerSet(t *testing.T) {
	t.Parallel()
	for _, contract := range definitions.RuntimeContracts() {
		t.Run(contract.String(), func(t *testing.T) {
			t.Parallel()
			documents, err := definitions.ActiveForContract(contract)
			require.NoError(t, err)
			require.NoError(t, documents.Validate())
			require.True(t, documents.Runtime.IsValid())
			require.True(t, documents.Schema.IsValid())
			require.True(t, documents.EpochImplementation.IsValid())
			require.True(t, documents.Retention.IsValid())
			require.True(t, documents.Interpreter.IsValid())
			runtime, err := definitions.NewRuntimeContract(contract)
			require.NoError(t, err)
			require.Equal(t, runtime.Body(), documents.Runtime.Body())
			require.Equal(t, model.DefinitionRuntimeContract, documents.Runtime.Kind())
			require.Equal(t, model.DefinitionLifecycleSchema, documents.Schema.Kind())
			require.Equal(t, model.DefinitionEpochImplementation, documents.EpochImplementation.Kind())
			require.Equal(t, model.DefinitionRetentionPolicy, documents.Retention.Kind())
			require.Equal(t, model.DefinitionInterpreter, documents.Interpreter.Kind())
			// Every document in the set activates through the same gated
			// writer and resolves to its owner body.
			_, tracker, service := activatedStore(t)
			for _, document := range []definitions.Document{documents.Runtime, documents.Schema, documents.EpochImplementation, documents.Retention, documents.Interpreter} {
				ref, err := receipt.EnsureActiveDefinition(context.Background(), service, document)
				require.NoError(t, err)
				reader, err := receipt.NewDefinitionReader(tracker.Journal())
				require.NoError(t, err)
				snapshot, _, err := receipt.ResolveDefinition(reader, ref)
				require.NoError(t, err)
				require.Equal(t, document.Body(), snapshot.Body())
			}
			// An unknown contract selects no document set.
			_, err = definitions.ActiveForContract(ir.RuntimeContractID{})
			require.Error(t, err)
		})
	}
}

func TestRemainingCommitRejectsMismatchedWarrant(t *testing.T) {
	t.Parallel()
	for _, owner := range remainingOwners() {
		t.Run(owner.name, func(t *testing.T) {
			t.Parallel()
			_, _, service := activatedStore(t)
			doc, err := owner.construct()
			require.NoError(t, err)
			write, err := receipt.NewEnvelopeDefinitionActivation(doc)
			require.NoError(t, err)
			_, err = service.Commit(context.Background(), gate.Warrant{}, write)
			require.Error(t, err, "zero warrant cannot enter the definition commit surface")
			other, refusal := gate.NewLineageIntent(definitions.RuntimeContracts()[0].Harness(), 1)
			require.Nil(t, refusal)
			otherWarrant, refusal := gate.Legalize(other)
			require.Nil(t, refusal)
			_, err = service.Commit(context.Background(), otherWarrant, write)
			require.Error(t, err, "a warrant for another write class cannot enter the definition commit surface")
		})
	}
}
