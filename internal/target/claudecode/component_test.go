package claudecode_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func TestNewComponentUsesCanonicalIdentity(t *testing.T) {
	t.Parallel()
	bundle := buildTestBundle(t, map[string]string{"file.txt": "x"})
	id, err := artifact.NewComponentID(artifact.HarnessClaudeCode, artifact.ExtensionSkills)
	require.NoError(t, err)
	component, err := claudecode.NewComponent(id, bundle, false)
	require.NoError(t, err)
	assert.Equal(t, id, component.ID())
	assert.Equal(t, artifact.ExtensionSkills, component.Extension())
}

func TestNewComponentRejectsZeroCrossHarnessAndEmptyBundle(t *testing.T) {
	t.Parallel()
	bundle := buildTestBundle(t, map[string]string{"file.txt": "x"})
	if component, err := claudecode.NewComponent(artifact.ComponentID{}, bundle, false); err == nil || component.IsValid() {
		t.Fatal("zero ID accepted")
	}
	for _, harness := range []artifact.Harness{artifact.HarnessOpenCode, artifact.HarnessCodex} {
		id, _ := artifact.NewComponentID(harness, artifact.ExtensionSkills)
		if component, err := claudecode.NewComponent(id, bundle, false); err == nil || component.IsValid() {
			t.Fatalf("cross-harness ID %s accepted", id)
		}
	}
	id, _ := artifact.NewComponentID(artifact.HarnessClaudeCode, artifact.ExtensionSkills)
	if component, err := claudecode.NewComponent(id, artifact.Bundle{}, false); err == nil || component.IsValid() {
		t.Fatal("empty bundle accepted")
	}
}
