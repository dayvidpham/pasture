package claudecode_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/target/claudecode"
)

func TestComponentKindParseAndValidity(t *testing.T) {
	for _, name := range []string{"skills", "agents", "hooks"} {
		kind, err := claudecode.ParseComponentKind(name)
		require.NoError(t, err)
		assert.True(t, kind.IsValid())
		assert.Equal(t, name, kind.String())
	}

	_, err := claudecode.ParseComponentKind("plugins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills")

	var zero claudecode.ComponentKind
	assert.False(t, zero.IsValid())
}

func TestComponentIDRejectsEmptyOrPadded(t *testing.T) {
	_, err := claudecode.NewComponentID("")
	require.Error(t, err)

	_, err = claudecode.NewComponentID(" claude-code/skills ")
	require.Error(t, err)

	id, err := claudecode.NewComponentID("claude-code/skills")
	require.NoError(t, err)
	assert.True(t, id.IsValid())
	assert.Equal(t, "claude-code/skills", id.String())
}

func TestNewComponentRejectsEmptyBundle(t *testing.T) {
	id, err := claudecode.NewComponentID("claude-code/skills")
	require.NoError(t, err)

	// The zero Bundle has an empty manifest and must be rejected: a published
	// component must carry real payload bytes.
	_, err = claudecode.NewComponent(claudecode.SkillsKind(), id, artifact.Bundle{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestNewComponentRejectsInvalidKind(t *testing.T) {
	id, err := claudecode.NewComponentID("claude-code/skills")
	require.NoError(t, err)

	bundle := buildTestBundle(t, map[string]string{"file.txt": "x"})
	_, err = claudecode.NewComponent(claudecode.ComponentKind{}, id, bundle, false)
	require.Error(t, err)
}
