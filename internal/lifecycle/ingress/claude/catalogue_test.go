package claude

import (
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestIndependentCatalogueCoversGeneratedManifest(t *testing.T) {
	t.Parallel()
	type row struct {
		Name     string   `yaml:"name"`
		Fields   []string `yaml:"fields"`
		Behavior string   `yaml:"behavior"`
	}
	var catalogue struct {
		Events []row `yaml:"events"`
	}
	raw, err := os.ReadFile("testdata/corpora/claude_2_1_210_catalogue.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &catalogue))
	manifest := registration.ClaudeCode2_1_210()
	require.Len(t, catalogue.Events, len(manifest.Events))
	for i, expected := range catalogue.Events {
		require.Equal(t, expected.Name, manifest.Events[i].NativeName, "independent catalogue order %d", i)
		actualFields := make([]string, 0)
		identityFields := make(map[string]struct{})
		for _, identity := range manifest.Events[i].Identities {
			identityFields[fieldNames[identity.Field]] = struct{}{}
		}
		for _, field := range manifest.Events[i].AllowedFields {
			name := fieldNames[field]
			_, identity := identityFields[name]
			if !isCommonField(name) || (identity && name != "session_id") {
				actualFields = append(actualFields, name)
			}
		}
		sort.Strings(actualFields)
		sort.Strings(expected.Fields)
		require.Equal(t, expected.Fields, actualFields, "independent field catalogue for %s", expected.Name)
		require.Equal(t, expected.Behavior, behaviorName(manifest.Events[i]), "independent behavior catalogue for %s", expected.Name)
	}
}

func isCommonField(name string) bool {
	for _, common := range []string{"session_id", "transcript_path", "cwd", "permission_mode", "hook_event_name", "effort", "agent_id", "agent_type"} {
		if name == common {
			return true
		}
	}
	return false
}

func behaviorName(event registration.Event) string {
	if event.NativeName == "ElicitationResult" {
		return "human-response"
	}
	if event.StopLoop == registration.StopLoopConsultWhenInactive {
		return "stop-gate"
	}
	if event.Blocking == registration.ConditionallyBlocking {
		return "conditional-gate"
	}
	if event.Blocking == registration.NonBlocking {
		return "observation"
	}
	return "gate"
}
