package claudecode_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func TestDescriptorPublishesPinnedClaudeIdentity(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)
	require.True(t, d.IsValid())

	assert.Equal(t, ir.HarnessClaudeCode, d.Harness())

	wantContract := runtime.ClaudeCode2_1_210().ID()
	assert.Equal(t, wantContract, d.RuntimeContractID(),
		"the target must publish the exact contract identity it was compiled under")
	assert.Equal(t, "claude-code/claude-code@2.1.210", d.RuntimeContractID().String())
	assert.Equal(t, ir.HarnessClaudeCode, d.RuntimeContractID().Harness())
}

func TestDescriptorPublishesThreeComponentsInCanonicalOrder(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	components := d.Components()
	require.Len(t, components, 3)
	assert.Equal(t, claudecode.SkillsKind(), components[0].Kind())
	assert.Equal(t, claudecode.AgentsKind(), components[1].Kind())
	assert.Equal(t, claudecode.HooksKind(), components[2].Kind())

	assert.Equal(t, claudecode.SkillsComponentID, d.Skills().ID().String())
	assert.Equal(t, claudecode.AgentsComponentID, d.Agents().ID().String())
	assert.Equal(t, claudecode.HooksComponentID, d.Hooks().ID().String())

	for _, c := range components {
		assert.True(t, c.IsValid())
		assert.Positive(t, c.Bundle().Manifest().Len(),
			"every published component must carry a non-empty bundle")
	}
}

func TestDescriptorHooksAreDefaultOff(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	assert.False(t, d.Hooks().DefaultEnabled(),
		"hooks run session-lifecycle commands and enforce git discipline; they must be default-off")
	// The whole Claude selection defaults off: no component activates without an
	// explicit selection.
	assert.False(t, d.Skills().DefaultEnabled())
	assert.False(t, d.Agents().DefaultEnabled())
}

func TestDescriptorComponentLookupByKind(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	skills, err := d.Component(claudecode.SkillsKind())
	require.NoError(t, err)
	assert.Equal(t, d.Skills().ID(), skills.ID())

	_, err = d.Component(claudecode.ComponentKind{})
	require.Error(t, err, "a zero component kind must be rejected actionably")
}

func TestNewTargetDescriptorRejectsNonClaudeContract(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	openCodeContract := runtime.OpenCode1_17_18().ID()
	_, err = claudecode.NewTargetDescriptor(openCodeContract, d.Skills(), d.Agents(), d.Hooks())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude-code")
}

func TestNewTargetDescriptorRejectsWrongKindInSlot(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	contract := runtime.ClaudeCode2_1_210().ID()
	// Agents component placed in the skills slot must be rejected: each slot
	// holds exactly its own kind.
	_, err = claudecode.NewTargetDescriptor(contract, d.Agents(), d.Agents(), d.Hooks())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills")
}

func TestNewTargetDescriptorRejectsZeroComponent(t *testing.T) {
	d, err := claudecode.Descriptor()
	require.NoError(t, err)

	contract := runtime.ClaudeCode2_1_210().ID()
	_, err = claudecode.NewTargetDescriptor(contract, claudecode.Component{}, d.Agents(), d.Hooks())
	require.Error(t, err)
}

func TestZeroTargetDescriptorIsInvalid(t *testing.T) {
	var zero claudecode.TargetDescriptor
	assert.False(t, zero.IsValid())
}
