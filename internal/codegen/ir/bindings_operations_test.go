package ir_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypedBindingsCaptureAndLexicalScope(t *testing.T) {
	t.Parallel()

	root, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	child, err := ir.NewChildScope(root, "child")
	require.NoError(t, err)
	sibling, err := ir.NewChildScope(root, "sibling")
	require.NoError(t, err)
	taskKey, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.task")
	require.NoError(t, err)
	taskRef, err := ir.InputValueRef(taskKey, child)
	require.NoError(t, err)
	bindings := ir.NewRuntimeBindings()
	bindings, err = ir.BindRuntimeValue(bindings, taskKey, child, mustTaskRef(t, "created-task"))
	require.NoError(t, err)

	resolved, err := ir.ResolveRuntimeValue(bindings, taskRef, child)
	require.NoError(t, err)
	assert.Equal(t, "created-task", resolved.String())
	_, err = ir.ResolveRuntimeValue(bindings, taskRef, sibling)
	assert.Error(t, err, "sibling scope must not consume a child's result")
	_, err = ir.ResolveRuntimeValue(bindings, taskRef, root)
	assert.Error(t, err, "ancestor scope must not consume a descendant's result")

	assignmentKey, err := ir.NewBindingKey[portable.AssignmentRef]("pasture.binding.task")
	require.NoError(t, err)
	wrongDomain, err := ir.InputValueRef(assignmentKey, child)
	require.NoError(t, err)
	_, err = ir.ResolveRuntimeValue(bindings, wrongDomain, child)
	assert.Error(t, err, "same spelling in a different portable domain must fail")

	_, err = ir.BindRuntimeValue(bindings, taskKey, child, mustTaskRef(t, "duplicate"))
	assert.Error(t, err)

	codec, err := ir.NewJSONCodec[portable.AssignmentRef]("pasture.binding.assignment-result/v1", func(value portable.AssignmentRef) error {
		if !value.IsValid() {
			return assert.AnError
		}
		return nil
	})
	require.NoError(t, err)
	resultKey, err := ir.NewBindingKey[portable.AssignmentRef]("pasture.binding.assignment-result")
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(resultKey, child, codec)
	require.NoError(t, err)
	resultRef, err := ir.ResultValueRef(slot)
	require.NoError(t, err)
	bindings, err = ir.CaptureRuntimeResult(bindings, slot, []byte(`"assignment-created"`))
	require.NoError(t, err)
	assignment, err := ir.ResolveRuntimeValue(bindings, resultRef, child)
	require.NoError(t, err)
	assert.Equal(t, "assignment-created", assignment.String())
	_, err = ir.CaptureRuntimeResult(bindings, slot, []byte(`"assignment-duplicate"`))
	assert.Error(t, err)
	_, err = ir.CaptureRuntimeResult(ir.NewRuntimeBindings(), slot, []byte(`" padded"`))
	assert.Error(t, err)
}

func TestMutationContinuationRetainsExactRequestAuthorityResultsAndScope(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("parent")
	require.NoError(t, err)
	key, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.gate-task")
	require.NoError(t, err)
	bindings, err := ir.BindRuntimeValue(ir.NewRuntimeBindings(), key, scope, mustTaskRef(t, "gate-task"))
	require.NoError(t, err)
	snapshot, err := ir.SnapshotBindings(bindings, scope)
	require.NoError(t, err)
	operationID, err := ir.NewSemanticOperationID("pasture.task.update/v1")
	require.NoError(t, err)
	request, err := ir.NewResolvedMutationRequest(operationID, "pasture.task.update-request/v1", []byte(` { "z": 2, "a": 1 } `))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"z":2}`, string(request.CanonicalBytes()))
	digest, err := ir.DigestCanonicalCommand([]byte(`{"command":"task-update","version":1}`))
	require.NoError(t, err)
	parsedDigest, err := ir.ParseCanonicalCommandDigest(digest.String())
	require.NoError(t, err)
	assert.True(t, digest.Equal(parsedDigest))

	resultCodec, err := ir.NewJSONCodec[portable.TaskRef]("pasture.task.update-result/v1", nil)
	require.NoError(t, err)
	resultKey, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.updated-task")
	require.NoError(t, err)
	resultSlot, err := ir.NewResultSlot(resultKey, scope, resultCodec)
	require.NoError(t, err)
	declaration, err := ir.DeclareResultSlot(resultSlot)
	require.NoError(t, err)
	authority, err := ir.InitiatingAssignment(mustAssignmentRef(t, "assignment-owner"))
	require.NoError(t, err)
	mutationRef, err := portable.NewMutationRef("mutation-logical-1")
	require.NoError(t, err)
	continuation, err := ir.NewMutationContinuation(
		mutationRef, authority, request, digest, []ir.ResultSlotDeclaration{declaration}, snapshot,
	)
	require.NoError(t, err)
	assert.True(t, continuation.IsValid())
	assert.Equal(t, mutationRef, continuation.Ref())
	assert.Equal(t, request.CanonicalBytes(), continuation.ReconstructRequest())
	assert.True(t, continuation.Scope().Equal(snapshot))
	assert.Equal(t, declaration.Key(), continuation.Results()[0].Key())

	reconstructed := continuation.ReconstructRequest()
	reconstructed[0] = '!'
	assert.Equal(t, byte('{'), continuation.ReconstructRequest()[0])
	results := continuation.Results()
	results[0] = ir.ResultSlotDeclaration{}
	assert.True(t, continuation.Results()[0].IsValid())

	secondRef, err := portable.NewMutationRef("mutation-logical-2")
	require.NoError(t, err)
	second, err := ir.NewMutationContinuation(secondRef, authority, request, digest, nil, snapshot)
	require.NoError(t, err)
	assert.NotEqual(t, continuation.Ref(), second.Ref(), "distinct invocations must retain distinct refs")

	context := assignmentContext(t, bindings, []ir.MutationContinuation{continuation})
	assert.True(t, context.IsValid())
	assert.Equal(t, continuation.Ref(), context.Mutations()[0].Ref())
	_, err = ir.NewAssignmentContext(
		context.Role(), context.Assignment(), context.Task(), context.Worktree(),
		context.Evidence(), context.Decisions(), context.Outstanding(),
		[]ir.MutationContinuation{continuation, continuation}, context.Bindings(),
	)
	assert.Error(t, err)
}

func TestClosedOrchestrationVariantsAreNeutralAndComplete(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("orchestration")
	require.NoError(t, err)
	context := assignmentContext(t, ir.NewRuntimeBindings(), nil)
	schedule, err := ir.BoundedParallel(2)
	require.NoError(t, err)
	skill, err := ir.NewSkillID("pasture.skill.worker-implement/v1")
	require.NoError(t, err)
	invoke, err := ir.NewInvokeSkill(skill, nil, scope)
	require.NoError(t, err)
	delegate, err := ir.NewDelegateAssignment([]ir.AssignmentContext{context}, schedule, scope)
	require.NoError(t, err)
	continuation, err := ir.NewContinueAssignment(context, scope)
	require.NoError(t, err)
	message, err := ir.NewSendAssignmentMessage(context.Assignment(), "Status requested", scope)
	require.NoError(t, err)
	collect, err := ir.NewCollectAssignmentResults([]portable.AssignmentRef{context.Assignment()}, ir.DependencyOrdered(), scope)
	require.NoError(t, err)
	stop, err := ir.NewStopAssignment([]portable.AssignmentRef{context.Assignment()}, "work is superseded", scope)
	require.NoError(t, err)
	decision := decisionRequest(t, ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "What changed?"}})

	operations := []struct {
		operation ir.SemanticOperation
		kind      ir.OperationKind
	}{
		{invoke, ir.OperationInvokeSkill},
		{delegate, ir.OperationDelegateAssignment},
		{continuation, ir.OperationContinueAssignment},
		{message, ir.OperationSendAssignmentMessage},
		{collect, ir.OperationCollectAssignmentResults},
		{stop, ir.OperationStopAssignment},
		{decision, ir.OperationRequestUserDecision},
	}
	assert.Len(t, operations, len(ir.AllOperationKinds()))
	for _, test := range operations {
		kind, err := ir.SemanticOperationKind(test.operation)
		require.NoError(t, err)
		assert.Equal(t, test.kind, kind)
		identity, err := ir.SemanticOperationIdentity(test.operation)
		require.NoError(t, err)
		assert.True(t, identity.IsValid())
		canonical, err := ir.CanonicalSemanticOperation(test.operation)
		require.NoError(t, err)
		assert.True(t, json.Valid(canonical))
		assert.NotContains(t, string(canonical), "TeamCreate")
		assert.NotContains(t, string(canonical), "SendMessage")
		assert.NotContains(t, string(canonical), "functions.")
	}

	assert.Equal(t, "Status requested", message.Message())
	assert.Equal(t, context.Assignment(), message.Assignment())
	assert.Equal(t, "work is superseded", stop.Reason())
	_, err = ir.BoundedParallel(0)
	assert.Error(t, err)
	_, err = ir.NewSendAssignmentMessage(portable.AssignmentRef{}, "message", scope)
	assert.Error(t, err)
}

func assignmentContext(t testing.TB, bindings ir.RuntimeBindings, mutations []ir.MutationContinuation) ir.AssignmentContext {
	t.Helper()
	role, err := portable.NewRoleID("worker")
	require.NoError(t, err)
	worktree, err := ir.NewWorktreeRef("worktree/worker")
	require.NoError(t, err)
	evidence, err := ir.NewEvidenceRef("evidence-1")
	require.NoError(t, err)
	decision, err := ir.NewDecisionRef("decision-1")
	require.NoError(t, err)
	work, err := ir.NewWorkItemRef("work-1")
	require.NoError(t, err)
	context, err := ir.NewAssignmentContext(
		role, mustAssignmentRef(t, "assignment-1"), mustTaskRef(t, "task-1"), worktree,
		[]ir.EvidenceRef{evidence}, []ir.DecisionRef{decision}, []ir.WorkItemRef{work}, mutations, bindings,
	)
	require.NoError(t, err)
	return context
}

func TestCanonicalRequestRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	operationID, err := ir.NewSemanticOperationID("pasture.test.operation/v1")
	require.NoError(t, err)
	_, err = ir.NewResolvedMutationRequest(operationID, "pasture.test.request/v1", []byte(`{} {}`))
	assert.Error(t, err)
	_, err = ir.ParseCanonicalCommandDigest("sha256:ABC")
	assert.Error(t, err)

	digest, err := ir.DigestCanonicalCommand([]byte("command"))
	require.NoError(t, err)
	upper := []byte(digest.String())
	upper = bytes.ToUpper(upper)
	_, err = ir.ParseCanonicalCommandDigest(string(upper))
	assert.Error(t, err)
}
