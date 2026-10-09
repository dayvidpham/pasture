package handlers_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// registeredAgentJSON mirrors the formatter's single-agent wire shape.
type registeredAgentJSON struct {
	AgentID string `json:"agentId"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

// TestTaskAgentsRegister_AllKindsThenCommentAdd proves the end-to-end reason
// registration exists: a fresh user registers an author, then uses the printed
// ID to add a comment. Every kind must yield a usable author ID.
func TestTaskAgentsRegister_AllKindsThenCommentAdd(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	model := provenance.DefaultModelRegistry().Models()[0]

	register := func(in handlers.TaskAgentRegisterInput) registeredAgentJSON {
		t.Helper()
		var out bytes.Buffer
		code, err := handlers.TaskAgentsRegister(&out, in, types.OutputJSON)
		require.NoError(t, err, "stderr-free registration")
		require.Zero(t, code)
		var got registeredAgentJSON
		require.NoError(t, json.Unmarshal(out.Bytes(), &got))
		require.NotEmpty(t, got.AgentID)
		return got
	}

	human := register(handlers.TaskAgentRegisterInput{
		DBPath:    path,
		Namespace: "acme",
		Kind:      handlers.AgentRegisterHuman,
		Name:      "Ada Lovelace",
		Contact:   "ada@example.com",
	})
	require.Equal(t, "human", human.Kind)
	require.Equal(t, "Ada Lovelace", human.Name)
	require.True(t, strings.HasPrefix(human.AgentID, "acme--"), "namespace must be honoured: %q", human.AgentID)

	software := register(handlers.TaskAgentRegisterInput{
		DBPath:    path,
		Namespace: "acme",
		Kind:      handlers.AgentRegisterSoftware,
		Name:      "pasture-cli",
		Version:   "0.0.13",
		Source:    "github.com/dayvidpham/pasture",
	})
	require.Equal(t, "software", software.Kind)
	require.Equal(t, "pasture-cli", software.Name)

	ml := register(handlers.TaskAgentRegisterInput{
		DBPath:    path,
		Namespace: "acme",
		Kind:      handlers.AgentRegisterML,
		Role:      "worker",
		Provider:  string(model.Provider),
		Model:     string(model.Name),
	})
	require.Equal(t, "machine_learning", ml.Kind)
	require.Equal(t, "worker / "+string(model.Name), ml.Name)

	// Every printed ID must be accepted as a comment author.
	taskID := createTask(t, path, "authored work")
	for _, author := range []string{human.AgentID, software.AgentID, ml.AgentID} {
		var out bytes.Buffer
		code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
			DBPath:   path,
			IdStr:    taskID,
			AuthorId: author,
			Body:     "signed by " + author,
		}, types.OutputJSON)
		require.NoError(t, err, "comment authored by %s", author)
		require.Zero(t, code, "comment authored by %s", author)
	}
}

// TestTaskAgentsRegister_TextOutputCarriesID ensures the human-readable output
// still prints the ID (the whole point of the verb).
func TestTaskAgentsRegister_TextOutputCarriesID(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	var out bytes.Buffer
	code, err := handlers.TaskAgentsRegister(&out, handlers.TaskAgentRegisterInput{
		DBPath:    path,
		Namespace: "acme",
		Kind:      handlers.AgentRegisterHuman,
		Name:      "Grace Hopper",
	}, types.OutputText)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Contains(t, out.String(), "AgentId:")
	require.Contains(t, out.String(), "acme--")
	require.Contains(t, out.String(), "Grace Hopper")
}

// TestTaskAgentsRegister_ValidationErrorsRejectBeforeStoreOpen is table-driven:
// every malformed registration must fail with an actionable validation error
// and must not create the database file (validation happens before open).
func TestTaskAgentsRegister_ValidationErrorsRejectBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	model := provenance.DefaultModelRegistry().Models()[0]

	cases := []struct {
		name        string
		in          handlers.TaskAgentRegisterInput
		wantFixHas  string
		wantWhatHas string
	}{
		{
			name:       "unknown kind",
			in:         handlers.TaskAgentRegisterInput{Kind: "robot", Name: "x"},
			wantFixHas: "register human",
		},
		{
			name:       "human missing name",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterHuman},
			wantFixHas: "register human",
		},
		{
			name:       "software missing name",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterSoftware, Version: "1"},
			wantFixHas: "register software",
		},
		{
			name:       "software missing version",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterSoftware, Name: "cli"},
			wantFixHas: "register software",
		},
		{
			name:       "ml missing role",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Provider: "anthropic", Model: "x"},
			wantFixHas: "register ml",
		},
		{
			name:       "ml unknown role",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Role: "wizard", Provider: "anthropic", Model: "x"},
			wantFixHas: "architect",
		},
		{
			name:       "ml missing provider",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Role: "worker", Model: "x"},
			wantFixHas: "provider",
		},
		{
			name:       "ml unknown provider",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Role: "worker", Provider: "not-a-provider", Model: "x"},
			wantFixHas: "provider",
		},
		{
			name:       "ml missing model",
			in:         handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Role: "worker", Provider: string(model.Provider)},
			wantFixHas: "--model",
		},
		{
			name:        "ml unknown model",
			in:          handlers.TaskAgentRegisterInput{Kind: handlers.AgentRegisterML, Role: "worker", Provider: string(model.Provider), Model: "not-a-model"},
			wantFixHas:  "pasture task agents register ml",
			wantWhatHas: "not-a-model",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			absent := filepath.Join(t.TempDir(), "never-created.db")
			tc.in.DBPath = absent
			tc.in.Namespace = "acme"

			var out bytes.Buffer
			code, err := handlers.TaskAgentsRegister(&out, tc.in, types.OutputJSON)
			require.Equal(t, 1, code)
			var se *pasterrors.StructuredError
			require.ErrorAs(t, err, &se)
			require.Equal(t, pasterrors.CategoryValidation, se.Category)
			require.NotEmpty(t, se.What)
			require.NotEmpty(t, se.Why)
			require.NotEmpty(t, se.Impact)
			require.Contains(t, se.Fix, tc.wantFixHas)
			require.Contains(t, se.Fix, "pasture task agents register")
			if tc.wantWhatHas != "" {
				require.Contains(t, se.What, tc.wantWhatHas)
			}
			require.Empty(t, out.String(), "no output on failure")

			require.NoFileExists(t, absent, "validation must reject before opening the store")
		})
	}
}
