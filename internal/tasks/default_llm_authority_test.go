package tasks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/provadapter"
)

func TestDefaultLLMClaimHasNoAuthorityEvenWithOwnership(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := provadapter.DefaultLLMActorID()
	_, err := store.prov.RegisterFixedSoftwareAgent(provadapter.DefaultLLMActorRegistration())
	require.NoError(t, err)
	task := seedOwnedEpochTask(t, store, actor, "fallback-adversarial-owner")
	gateClaim(t, store, actor, "fallback-session")
	original := store.Journal()
	journal := &gateCountingJournal{Journal: original}
	gateJournal(t, store, journal)

	snapshot := gateSnapshot(t, store, "fallback-session")
	claim, err := snapshot.ResolveSession(gateHarness, "fallback-session")
	require.NoError(t, err)
	require.True(t, claim.Bound)
	require.True(t, claim.Known)
	require.True(t, claim.AttributionOnly)
	require.Equal(t, actor, claim.Actor)
	authority, err := snapshot.Authority(actor)
	require.NoError(t, err)
	require.Equal(t, actor, authority.Actor)
	require.Empty(t, authority.Episodes)
	ownership, other := journal.counts()
	require.Zero(t, ownership)
	require.Zero(t, other)

	// Capability checking itself must be bypassed, not just the API call.
	gateJournal(t, store, gateHiddenJournal{Journal: journal})
	hidden := gateSnapshot(t, store, "fallback-session")
	hiddenClaim, err := hidden.ResolveSession(gateHarness, "fallback-session")
	require.NoError(t, err)
	require.Equal(t, claim, hiddenClaim)
	// Exclusion must preserve contradictory ownership as historical evidence.
	stored, err := original.(provenance.ActorOwnershipQueryAPI).QueryActorOwnership(t.Context(), provenance.ActorOwnershipQuery{Actor: actor})
	require.NoError(t, err)
	require.Len(t, stored.Tasks, 1)
	require.Equal(t, task, stored.Tasks[0].TaskID)
}

func TestDefaultLLMClassificationDoesNotFollowDisplayName(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	lookalike, err := store.prov.RegisterSoftwareAgent("fallback-lookalike", provadapter.DefaultLLMActorName, "1", "test")
	require.NoError(t, err)
	gateClaim(t, store, lookalike.ID, "lookalike-session")
	journal := &gateCountingJournal{Journal: store.Journal()}
	gateJournal(t, store, journal)
	claim, err := gateSnapshot(t, store, "lookalike-session").ResolveSession(gateHarness, "lookalike-session")
	require.NoError(t, err)
	require.True(t, claim.Known)
	require.False(t, claim.AttributionOnly)
	ownership, _ := journal.counts()
	require.Equal(t, 1, ownership)
}

func TestDefaultLLMInferenceNeverQueriesAdversarialOwnership(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := provadapter.DefaultLLMActorID()
	_, err := store.prov.RegisterFixedSoftwareAgent(provadapter.DefaultLLMActorRegistration())
	require.NoError(t, err)
	seedOwnedEpochTask(t, store, actor, "fallback-inference-owner")
	gateClaim(t, store, actor, "fallback-inference-session")
	journal := &gateCountingJournal{Journal: store.Journal()}
	gateJournal(t, store, journal)
	planned, err := planSessionInference(t.Context(), store)
	require.NoError(t, err)
	require.Empty(t, planned)
	ownership, other := journal.counts()
	require.Zero(t, ownership)
	require.Zero(t, other)
	gateJournal(t, store, gateHiddenJournal{Journal: journal})
	planned, err = planSessionInference(t.Context(), store)
	require.NoError(t, err, "fallback-only claims need no ownership capability")
	require.Empty(t, planned)
}

func TestDefaultLLMExcludedBeforeDistinctInferenceActors(t *testing.T) {
	t.Parallel()
	actor := provadapter.DefaultLLMActorID()
	other := provadapter.PastureSystemDefaultActorID()
	claims := []sessionClaimRow{{actor: actor.String()}, {actor: other.String()}, {actor: actor.String()}}
	require.Equal(t, []provenance.ActorID{other}, sessionClaimActors(claims))
}

func TestDefaultLLMInferenceExclusionPreservesExplicitActor(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := provadapter.DefaultLLMActorID()
	_, err := store.prov.RegisterFixedSoftwareAgent(provadapter.DefaultLLMActorRegistration())
	require.NoError(t, err)
	seedOwnedEpochTask(t, store, actor, "fallback-mixed-owner")
	gateClaim(t, store, actor, "fallback-mixed-session")
	explicit := feasibilityActor(t, store, "explicit-mixed-actor")
	epoch := seedOwnedEpochTask(t, store, explicit, "explicit-mixed-owner")
	gateClaim(t, store, explicit, "explicit-mixed-session")
	journal := &gateCountingJournal{Journal: store.Journal()}
	gateJournal(t, store, journal)
	planned, err := planSessionInference(t.Context(), store)
	require.NoError(t, err)
	require.Len(t, planned, 1)
	require.Equal(t, "explicit-mixed-session", planned[0].Session)
	require.Equal(t, epoch.String(), planned[0].EpochID)
	ownership, other := journal.counts()
	require.Equal(t, 1, ownership)
	require.Zero(t, other)
}

func TestGateReaderClassifiesAttributionOnlyByExactDescriptorBeforeOwnership(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "gate_reader.go", nil, 0)
	require.NoError(t, err)
	var guard *ast.IfStmt
	var capability token.Pos
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Snapshot" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if assertion, ok := node.(*ast.TypeAssertExpr); ok && capability == token.NoPos {
				capability = assertion.Pos()
			}
			stmt, ok := node.(*ast.IfStmt)
			if !ok {
				return true
			}
			call, ok := stmt.Cond.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "IsDefaultLLMActor" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			require.True(t, ok)
			require.Equal(t, "provadapter", pkg.Name)
			require.Len(t, call.Args, 1)
			actor, ok := call.Args[0].(*ast.Ident)
			require.True(t, ok)
			require.Equal(t, "actor", actor.Name)
			require.Nil(t, guard, "only one descriptor guard supplies attribution-only facts")
			guard = stmt
			return true
		})
	}
	require.NotNil(t, guard)
	require.NotEqual(t, token.NoPos, capability)
	require.Less(t, guard.Pos(), capability)
	require.Len(t, guard.Body.List, 1)
	_, returns := guard.Body.List[0].(*ast.ReturnStmt)
	require.True(t, returns, "descriptor guard must return before ownership")
	var assignments int
	ast.Inspect(file, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := pair.Key.(*ast.Ident)
		if ok && key.Name == "AttributionOnly" {
			assignments++
			require.Greater(t, pair.Pos(), guard.Pos())
			require.Less(t, pair.End(), guard.End())
			value, ok := pair.Value.(*ast.Ident)
			require.True(t, ok)
			require.Equal(t, "true", value.Name)
		}
		return true
	})
	require.Equal(t, 1, assignments)
}
