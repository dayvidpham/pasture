package definitions_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/dayvidpham/pasture/internal/acceptance/origin"
	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// The agreement checks prove the committed descriptor literals still match
// their live owners. Each literal below is re-derived from the authority; a
// changed owner fails here with the regeneration and version-bump repair, not
// silently.

func TestSchemaSpecificationAgreesWithCodecOwners(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewLifecycleSchema()
	require.NoError(t, err)
	spec, err := definitions.ParseSchemaSpecification(doc.Body())
	require.NoError(t, err)
	require.Len(t, spec.BindingKinds, 8)
	for _, kind := range spec.BindingKinds {
		parsed, err := model.ParseNativeBindingKind(kind)
		require.NoError(t, err)
		require.True(t, parsed.IsValid())
	}
	for _, source := range spec.HostVersionSources {
		require.NoError(t, model.ValidateHostVersionSource(model.HostVersionSource(source)))
	}
	require.Equal(t, []uint8{1, 2, 3, 4, 5, 6, 7, 8}, spec.CaptureDispositions)
	require.Equal(t, model.CaptureDisposition(1), model.CaptureValid)
	require.Equal(t, model.CaptureDisposition(8), model.CaptureEventMismatch)
	var liveOrigins []string
	for _, candidate := range []origin.CaptureOrigin{origin.OriginAuthenticCapture, origin.OriginPinnedContract, origin.OriginReviewFinding, origin.OriginAuthored, origin.OriginRaw, "bogus-origin"} {
		if candidate.IsValid() {
			liveOrigins = append(liveOrigins, string(candidate))
		}
	}
	require.Equal(t, liveOrigins, spec.CaptureOrigins)
	require.Equal(t, map[string]model.DefinitionKind{
		"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
		"implementation": model.DefinitionEpochImplementation, "retention": model.DefinitionRetentionPolicy,
	}, spec.OccurrenceEnvelope)
	require.Equal(t, map[string]model.DefinitionKind{
		"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
		"metamodel": model.DefinitionMetamodel, "interpreter": model.DefinitionInterpreter,
		"implementation": model.DefinitionEpochImplementation,
	}, spec.SemanticEnvelope)
	require.Equal(t, []string{"runtime", "schema", "implementation"}, spec.SharedEnvelopeSlots)
	require.Equal(t, "sha256", spec.PayloadDigest)
	require.NotEmpty(t, spec.RuntimeContractRule)
}

func TestRetentionSpecificationAgreesWithReclaimPolicy(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewRetentionPolicy()
	require.NoError(t, err)
	spec, err := definitions.ParseRetentionSpecification(doc.Body())
	require.NoError(t, err)
	require.Equal(t, "store-all", spec.Policy)
	// The reclaim budgets are single-sourced in the projection owner; the
	// descriptor repeats them so the committed document names the enforced
	// values, and this check proves the repetition is still exact.
	require.Equal(t, projection.ReclaimCap, spec.ReadCap)
	require.Equal(t, projection.PruneCap, spec.PruneBatchCap)
	require.Contains(t, spec.WindowRule, "WorkflowResult")
	require.Equal(t, "sha256", spec.Digest)
}

func TestEpochSpecificationAgreesWithProtocolAndCodegen(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewEpochImplementation()
	require.NoError(t, err)
	spec, err := definitions.ParseEpochSpecification(doc.Body())
	require.NoError(t, err)
	require.Equal(t, engine.DefaultApplicationVersion, spec.Cohort)
	require.Equal(t, []string{engine.SliceQueueName, engine.ControlQueueName},
		[]string{spec.Queues[0].Name, spec.Queues[1].Name})
	var liveTopics []string
	for _, topic := range protocol.AllSignalTopics {
		liveTopics = append(liveTopics, topic.String())
	}
	require.Equal(t, slices.Sorted(slices.Values(liveTopics)), spec.SignalTopics)
	var liveQueries []string
	for _, name := range protocol.AllQueryNames {
		liveQueries = append(liveQueries, name.String())
	}
	require.Equal(t, slices.Sorted(slices.Values(liveQueries)), spec.QueryNames)
	liveTransitions := make(map[string][]string, len(protocol.PhaseSpecs))
	for from, table := range protocol.PhaseSpecs {
		for _, to := range table.Transitions {
			liveTransitions[string(from)] = append(liveTransitions[string(from)], string(to))
		}
	}
	require.Equal(t, liveTransitions, spec.ProtocolTransitions)
	var livePhases []definitions.EpochPhase
	for _, id := range slices.Sorted(maps.Keys(codegen.PhaseSpecs)) {
		table := codegen.PhaseSpecs[id]
		phase := definitions.EpochPhase{ID: string(id), Number: table.Number}
		for _, role := range table.OwnerRoles {
			phase.OwnerRoles = append(phase.OwnerRoles, string(role))
		}
		for _, transition := range table.Transitions {
			entry := definitions.EpochTransition{To: string(transition.ToPhase), Condition: transition.Condition}
			if transition.Action != "" {
				entry.Action = transition.Action
			}
			phase.Transitions = append(phase.Transitions, entry)
		}
		livePhases = append(livePhases, phase)
	}
	slices.SortFunc(livePhases, func(a, b definitions.EpochPhase) int { return a.Number - b.Number })
	require.Equal(t, livePhases, spec.Phases)
	var liveRoles []definitions.EpochRole
	for _, id := range slices.Sorted(maps.Keys(codegen.RoleSpecs)) {
		liveRoles = append(liveRoles, definitions.EpochRole{ID: string(id)})
	}
	require.Equal(t, liveRoles, spec.Roles)
	var liveWorkflows []definitions.EpochWorkflow
	for _, id := range slices.Sorted(maps.Keys(codegen.WorkflowSpecs)) {
		table := codegen.WorkflowSpecs[id]
		workflow := definitions.EpochWorkflow{ID: id, Role: string(table.RoleRef)}
		for _, stage := range table.Stages {
			entry := definitions.EpochStage{ID: stage.Id, Order: stage.Order, Execution: stage.Execution}
			if stage.PhaseRef != "" {
				entry.Phase = string(stage.PhaseRef)
			}
			workflow.Stages = append(workflow.Stages, entry)
		}
		liveWorkflows = append(liveWorkflows, workflow)
	}
	require.Equal(t, liveWorkflows, spec.Workflows)
}

func TestEpochGateRulesMatchStateMachineBehavior(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewEpochImplementation()
	require.NoError(t, err)
	spec, err := definitions.ParseEpochSpecification(doc.Body())
	require.NoError(t, err)
	contains := func(transitions []protocol.PhaseId, want protocol.PhaseId) bool {
		return slices.Contains(transitions, want)
	}
	// Every consensus-gated pair is refused without consensus and admitted
	// with all-ACCEPT votes through the live state machine.
	for _, pair := range spec.ConsensusGated {
		from, to := protocol.PhaseId(pair[0]), protocol.PhaseId(pair[1])
		bare := protocol.NewEpochStateMachineFromState(&protocol.EpochState{CurrentPhase: from}, nil)
		require.False(t, contains(bare.AvailableTransitions(), to), "gated %q must be unavailable without consensus", pair)
		consensus := protocol.NewEpochStateMachineFromState(&protocol.EpochState{CurrentPhase: from, ReviewVotes: map[protocol.ReviewAxis]protocol.VoteType{
			protocol.AxisCorrectness: protocol.VoteAccept, protocol.AxisTestQuality: protocol.VoteAccept, protocol.AxisElegance: protocol.VoteAccept,
		}}, nil)
		require.True(t, contains(consensus.AvailableTransitions(), to), "gated %q must be available with consensus", pair)
	}
	// Every blocker-gated pair is refused while blockers stand and admitted
	// once they clear, with consensus held so only the blocker gate decides.
	for _, pair := range spec.BlockerGated {
		from, to := protocol.PhaseId(pair[0]), protocol.PhaseId(pair[1])
		votes := map[protocol.ReviewAxis]protocol.VoteType{
			protocol.AxisCorrectness: protocol.VoteAccept, protocol.AxisTestQuality: protocol.VoteAccept, protocol.AxisElegance: protocol.VoteAccept,
		}
		blocked := protocol.NewEpochStateMachineFromState(&protocol.EpochState{CurrentPhase: from, ReviewVotes: votes, BlockerCount: 1}, nil)
		require.False(t, contains(blocked.AvailableTransitions(), to), "gated %q must be unavailable with blockers", pair)
		clear := protocol.NewEpochStateMachineFromState(&protocol.EpochState{CurrentPhase: from, ReviewVotes: votes}, nil)
		require.True(t, contains(clear.AvailableTransitions(), to), "gated %q must be available without blockers", pair)
	}
	// Every revise-drives-back phase offers only its backward transitions
	// while a REVISE vote stands: the live machine must offer exactly the
	// protocol targets that are neither consensus- nor blocker-gated.
	gated := make(map[[2]string]bool)
	for _, pair := range spec.ConsensusGated {
		gated[pair] = true
	}
	for _, pair := range spec.BlockerGated {
		gated[pair] = true
	}
	for _, phase := range spec.ReviseDrivesBack {
		current := protocol.PhaseId(phase)
		revising := protocol.NewEpochStateMachineFromState(&protocol.EpochState{CurrentPhase: current, ReviewVotes: map[protocol.ReviewAxis]protocol.VoteType{
			protocol.AxisCorrectness: protocol.VoteRevise,
		}}, nil)
		var backward []string
		for _, to := range spec.ProtocolTransitions[phase] {
			if !gated[[2]string{phase, to}] {
				backward = append(backward, to)
			}
		}
		var offered []string
		for _, to := range revising.AvailableTransitions() {
			offered = append(offered, string(to))
		}
		require.ElementsMatch(t, backward, offered, "revise at %q must offer only backward transitions", phase)
		require.NotEmpty(t, offered, "revise at %q must still offer a backward transition", phase)
	}
}

func TestInterpreterSpecificationAgreesWithPipelineOwners(t *testing.T) {
	t.Parallel()
	doc, err := definitions.NewInterpreter()
	require.NoError(t, err)
	spec, err := definitions.ParseInterpreterSpecification(doc.Body())
	require.NoError(t, err)
	require.Len(t, spec.Pipeline, 5)
	// The interpretation contract runs end to end on a real verified event:
	// waist verification, legalize transform, middle-end derivation, receipt
	// record, and model record with the producing coordinate stamped.
	binding, err := waist.BindEvent(runtime.ClaudeCode2_1_261Lifecycle(), runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	var identities []waist.Identity
	for index, field := range binding.DeclaredIdentities() {
		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), field.NativeName()+"-descriptor-agreement-"+string(rune('a'+index)))
		require.NoError(t, err)
		identities = append(identities, identity)
	}
	event, err := binding.NewEvent(identities)
	require.NoError(t, err)
	require.True(t, event.IsValid())
	manifest := metamodel.Active()
	derivation, err := middleend.Derive(event, manifest)
	require.NoError(t, err)
	require.True(t, derivation.IsValid())
	var interpreted []byte
	for _, effect := range derivation.Effects() {
		if effect.EvidenceKind == "pasture.lifecycle.interpreted.v2" {
			interpreted = effect.Payload
		}
	}
	require.NotEmpty(t, interpreted, "derivation must emit interpreted.v2 evidence")
	record, err := receipt.DecodeInterpretedV2(1, 2, interpreted)
	require.NoError(t, err)
	coordinate, ok := record.Metamodel()
	require.True(t, ok)
	require.Equal(t, manifest, coordinate)
	require.Equal(t, event.Semantics().Semantic(), record.Semantic())
	require.Equal(t, event.Origin().Contract(), record.Contract())
	// Closed enum literals still match their live owners; the decode path
	// already enforces these, and this pins the committed bytes as well.
	require.Equal(t, []string{"observation", "gate-consultation", "explicit-human-response"}, spec.Semantics)
	require.Equal(t, []string{"pasture.lifecycle.interpreted.v1", "pasture.lifecycle.interpreted.v2", "pasture.lifecycle.interpreted.v3"}, spec.RecordKinds)
	require.NotEmpty(t, spec.VocabularyRule)
}
