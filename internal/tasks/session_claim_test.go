package tasks_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/provadapter"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

type claimClock struct{}

func (claimClock) Now() time.Time { return time.Unix(1700000000, 0).UTC() }

type claimOperation string

func (id claimOperation) NewOperationID() (string, error) { return string(id), nil }

func claimStore(t *testing.T) (protocol.TaskTracker, *sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })
	_, err = tracker.Create("file://claim-test", "bootstrap", "initialize identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return tracker, db, path
}

// RED: change the claim insert to replace a conflicting row, or remove the
// duplicate refusal phrase. A second claim must not replace the first actor.
func TestSessionClaimIsWrittenOnceAndNamesTheDuplicate(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "one"}}
	claim := func(actor tasks.ActorClaim) error {
		return tasks.RecordLifecycleSessionClaim(t.Context(), tracker, ir.HarnessClaudeCode, registration.EventSessionStart, bindings, actor, claimClock{})
	}
	require.NoError(t, claim("first"))
	err := claim("second")
	require.ErrorContains(t, err, "already has an actor claim")
	require.ErrorContains(t, claim("first"), "already has an actor claim", "even the same actor cannot claim the session twice")
	for _, phrase := range []string{"session_claim.go", "session-start", "unchanged", "start a new session"} {
		require.Contains(t, err.Error(), phrase)
	}
	var count int
	var actor string
	var at int64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*), actor, claimed_at FROM pasture_session_claim WHERE harness = ? AND session = ?`, string(ir.HarnessClaudeCode), "one").Scan(&count, &actor, &at))
	require.Equal(t, 1, count, "duplicate claim must leave exactly one row")
	require.Equal(t, "first", actor, "duplicate claim must leave the first actor unchanged")
	require.Equal(t, claimClock{}.Now().UnixNano(), at)
	bindings[0].Value = "two"
	require.NoError(t, claim("second"))
	require.NoError(t, tasks.RecordLifecycleSessionClaim(t.Context(), tracker, ir.HarnessCodex, registration.EventCodexSessionStart, bindings, "third", claimClock{}))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_session_claim`).Scan(&count))
	require.Equal(t, 3, count, "different sessions and harnesses have separate claims")
}

// RED: remove the zero-claim or non-start early return. Neither path may write
// or require a database, clock, or identity binding.
func TestSessionClaimZeroAndNonStartDoNotWrite(t *testing.T) {
	t.Parallel()
	require.NoError(t, tasks.RecordLifecycleSessionClaim(t.Context(), nil, ir.HarnessClaudeCode, registration.EventSessionStart, nil, "", nil))
	require.NoError(t, tasks.RecordLifecycleSessionClaim(t.Context(), nil, ir.HarnessClaudeCode, registration.EventPreToolUse, nil, "actor", nil))
}

// RED: drop the handler claim call, omit a start-event arm, or use the claimed
// actor as receipt author. These are real receipt writes through the handler.
func TestSessionStartStoresClaimAndKeepsSystemReceiptAuthor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		harness                      ir.HarnessID
		event, version, dir, fixture string
	}{
		{ir.HarnessClaudeCode, "SessionStart", "2.1.261", "claude", "session_start_2_1_261.json"},
		{ir.HarnessCodex, "SessionStart", "0.153.0", "codex", "session_start_0_153_0.json"},
		{ir.HarnessOpenCode, "session.created", "1.18.29", "opencode", "session_created_1_18_29.json"},
	}
	for _, tc := range cases {
		t.Run(string(tc.harness), func(t *testing.T) {
			t.Parallel()
			tracker, db, path := claimStore(t)
			claimedActor, err := tracker.RegisterHumanAgent("claim-holder", "Claim Holder", "claim-holder@example.test")
			require.NoError(t, err)
			raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", tc.dir, "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), timeouts.TestProfile().WorkflowResult())
			defer cancel()
			invoke := func(id string) (hostexit.Outcome, error) {
				return handlers.HookLifecycleNative(ctx, handlers.HookLifecycleInput{
					DBPath:      path,
					Harness:     tc.harness,
					Event:       tc.event,
					HostVersion: tc.version,
					Input:       bytes.NewReader(raw),
					Clock:       claimClock{},
					Operations:  claimOperation(id),
					ActorClaim:  tasks.ActorClaim(claimedActor.ID.String()),
				})
			}
			firstOutcome, err := invoke("claim.first")
			require.NoError(t, err)
			require.Equal(t, hostexit.ExitContinue, firstOutcome.Exit)
			require.Empty(t, firstOutcome.Stderr)
			var count int
			var actor string
			require.NoError(t, db.QueryRow(`SELECT COUNT(*), actor FROM pasture_session_claim WHERE harness = ?`, string(tc.harness)).Scan(&count, &actor))
			require.Equal(t, 1, count, "session-start handler must store one claim")
			require.Equal(t, claimedActor.ID.String(), actor)
			// This second receipt is appended after the claim already exists.
			secondOutcome, err := invoke("claim.second")
			require.ErrorContains(t, err, "already has an actor claim")
			require.ErrorIs(t, err, handlers.ErrLifecycleCommittedWithoutContinuation)
			require.ErrorContains(t, err, "this claim failure is not a policy decision")
			require.Equal(t, firstOutcome, secondOutcome,
				"the duplicate remains an error without replacing the committed observation Outcome")
			require.NoError(t, db.QueryRow(`SELECT COUNT(*), actor FROM pasture_session_claim WHERE harness = ?`, string(tc.harness)).Scan(&count, &actor))
			require.Equal(t, 1, count, "duplicate refusal must not add a claim")
			require.Equal(t, claimedActor.ID.String(), actor, "duplicate refusal must not overwrite the original actor")
			page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}}, Kinds: []provenance.EvidenceKind{"pasture.lifecycle.occurrence.v1"}, Page: provenance.FactPageRequest{Limit: 10}})
			require.NoError(t, err)
			require.Len(t, page.Rows, 2, "both receipt operations must exist")
			for _, row := range page.Rows {
				require.Equal(t, provadapter.PastureSystemDefaultActorID(), row.EffectiveActorID, fmt.Sprintf("receipt %s must retain system attribution after a claim", row.ProducingOperationID))
			}
		})
	}
}

// RED: remove ActorClaim from the command's input literal, duplicate the claim
// call, or put that call in a loop. This check reads all non-test Go files in
// cmd/pasture and internal/handlers, not transports or the later gate reader.
func TestSessionClaimCommandWiringAndSingleCall(t *testing.T) {
	fset := token.NewFileSet()
	inputs, calls := 0, 0
	for _, dir := range []string{filepath.Join("..", "..", "cmd", "pasture"), filepath.Join("..", "handlers")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, files, "source population must not be empty")
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			require.NoError(t, err)
			ast.Inspect(file, func(n ast.Node) bool {
				if lit, ok := n.(*ast.CompositeLit); ok {
					if name, ok := lit.Type.(*ast.SelectorExpr); ok && name.Sel.Name == "HookLifecycleInput" {
						inputs++
						found := false
						for _, entry := range lit.Elts {
							kv, ok := entry.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							key, ok := kv.Key.(*ast.Ident)
							if !ok || key.Name != "ActorClaim" {
								continue
							}
							var text bytes.Buffer
							require.NoError(t, printer.Fprint(&text, fset, kv.Value))
							require.Equal(t, "tasks.ActorClaim(env.ActorClaim)", text.String(), "command must forward the environment claim")
							found = true
						}
						require.True(t, found, "command input must carry ActorClaim")
					}
				}
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "RecordLifecycleSessionClaim" {
						calls++
					}
				}
				switch n.(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					ast.Inspect(n, func(child ast.Node) bool {
						if sel, ok := child.(*ast.SelectorExpr); ok {
							require.NotEqual(t, "RecordLifecycleSessionClaim", sel.Sel.Name, "claim write must not run in a loop")
						}
						return true
					})
				}
				return true
			})
		}
	}
	require.Equal(t, 1, inputs, "one production command input must be checked")
	require.Equal(t, 1, calls, "one claim API call per native receipt path")
}

// RED: drop the v2 session-created arm from the claim writer. A v2 gate would
// then seal UNBOUND and no v2 policy denial could consult a gate.
func TestSessionClaimV2SessionCreatedWritesClaim(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "sessionID", Value: "v2-session"}}
	require.NoError(t, tasks.RecordLifecycleSessionClaim(t.Context(), tracker, ir.HarnessOpenCode, registration.EventOpenCode2SessionCreated, bindings, "v2-actor", claimClock{}))
	var count int
	var actor string
	require.NoError(t, db.QueryRow(`SELECT COUNT(*), actor FROM pasture_session_claim WHERE harness = ? AND session = ?`, string(ir.HarnessOpenCode), "v2-session").Scan(&count, &actor))
	require.Equal(t, 1, count, "the v2 session-created coordinate must write the session claim")
	require.Equal(t, "v2-actor", actor)
	require.NoError(t, tasks.RecordLifecycleSessionClaim(t.Context(), tracker, ir.HarnessOpenCode, registration.EventOpenCode2SessionPrompt, bindings, "other", claimClock{}))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_session_claim`).Scan(&count))
	require.Equal(t, 1, count, "a non-start v2 coordinate must not write a claim")
}
