package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortableRefsAreDistinctValidatedDomains(t *testing.T) {
	t.Parallel()

	assignment, err := protocol.NewAssignmentRef("assignment-17")
	require.NoError(t, err)
	task, err := protocol.NewTaskRef("task-17")
	require.NoError(t, err)
	role, err := protocol.NewRoleID("worker")
	require.NoError(t, err)
	mutation, err := protocol.NewMutationRef("mutation-17")
	require.NoError(t, err)
	agent, err := protocol.NewAgentRef("bootstrap-17")
	require.NoError(t, err)

	assert.Equal(t, "assignment-17", assignment.String())
	assert.Equal(t, "task-17", task.String())
	assert.Equal(t, "worker", role.String())
	assert.Equal(t, "mutation-17", mutation.String())
	assert.Equal(t, "bootstrap-17", agent.String())
	assert.True(t, assignment.IsValid())
	assert.True(t, task.IsValid())
	assert.True(t, role.IsValid())
	assert.True(t, mutation.IsValid())
	assert.True(t, agent.IsValid())

	for name, construct := range map[string]func(string) error{
		"assignment": func(value string) error { _, err := protocol.NewAssignmentRef(value); return err },
		"task":       func(value string) error { _, err := protocol.NewTaskRef(value); return err },
		"role":       func(value string) error { _, err := protocol.NewRoleID(value); return err },
		"mutation":   func(value string) error { _, err := protocol.NewMutationRef(value); return err },
		"agent":      func(value string) error { _, err := protocol.NewAgentRef(value); return err },
	} {
		construct := construct
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, invalid := range []string{"", " padded", "padded ", "line\nbreak", string([]byte{0xff})} {
				err := construct(invalid)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "what:")
				assert.Contains(t, err.Error(), "fix:")
			}
		})
	}
}

func TestPortableRefJSONRoundTripRejectsInvalid(t *testing.T) {
	t.Parallel()

	task, err := protocol.NewTaskRef("epoch-root")
	require.NoError(t, err)
	encoded, err := json.Marshal(task)
	require.NoError(t, err)
	assert.JSONEq(t, `"epoch-root"`, string(encoded))

	var decoded protocol.TaskRef
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, task, decoded)

	for _, invalid := range []string{`""`, `" padded"`, `17`, `null`} {
		assert.Error(t, json.Unmarshal([]byte(invalid), &decoded))
	}
}
