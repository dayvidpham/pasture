package claudecode_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func TestReviewedNativeCallsDerivedFromContract(t *testing.T) {
	calls, err := claudecode.ReviewedNativeCalls(runtime.ClaudeCode2_1_210())
	require.NoError(t, err)

	// The pinned 2.1.210 profile lowers core operations to exactly these native
	// calls; CollectAssignmentResults is parent-mediated and contributes none.
	assert.ElementsMatch(t,
		[]string{"Agent", "AskUserQuestion", "SendMessage", "Skill", "TaskStop"},
		calls)

	// It names no removed team-lifecycle call.
	assert.NotContains(t, calls, "TeamCreate")
	assert.NotContains(t, calls, "TeamDelete")
}

func TestReviewedNativeCallsRejectsZeroContract(t *testing.T) {
	_, err := claudecode.ReviewedNativeCalls(runtime.RuntimeContract{})
	require.Error(t, err)
}

func TestValidateAgentFidelityAcceptsGeneratedAgents(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	// The real generated agents grant only reviewed native calls (Skill, Agent,
	// SendMessage) plus general host tools (Read, Bash, Task, …).
	require.NoError(t, claudecode.ValidateAgentFidelity(d))
}

func TestValidateAgentFidelityRejectsRemovedTeamLifecycleGrant(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	// Forge an agents bundle that grants a removed team-lifecycle native call.
	forgedAgents := buildTestBundle(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"pasture-agents","version":"0.0.4"}`,
		"agents/rogue.md":            "---\nname: rogue\ntools: Read, Bash, TeamCreate, SendMessage\n---\n\nRogue agent.\n",
	})
	agentsID, err := claudecode.NewComponentID(claudecode.AgentsComponentID)
	require.NoError(t, err)
	agentsComponent, err := claudecode.NewComponent(claudecode.AgentsKind(), agentsID, forgedAgents, false)
	require.NoError(t, err)

	contract := runtime.ClaudeCode2_1_210().ID()
	forged, err := claudecode.NewTargetDescriptor(contract, d.Skills(), agentsComponent, d.Hooks())
	require.NoError(t, err)

	err = claudecode.ValidateAgentFidelity(forged)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TeamCreate")
}

func TestValidateAgentFidelityRejectsZeroDescriptor(t *testing.T) {
	err := claudecode.ValidateAgentFidelity(claudecode.TargetDescriptor{})
	require.Error(t, err)
}
