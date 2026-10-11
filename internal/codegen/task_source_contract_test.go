package codegen

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type canonicalTaskFixture struct {
	Forbidden []struct {
		Name    string `yaml:"name"`
		Pattern string `yaml:"pattern"`
	} `yaml:"forbidden"`
	Fragments []struct {
		ID        string   `yaml:"id"`
		Contains  []string `yaml:"contains"`
		EveryBody bool     `yaml:"every_body"`
	} `yaml:"fragments"`
}

// This source-owner contract is deliberately narrower than the operational
// classifier: it checks the assembled registries owned by source conversion,
// not historical documents, rendered targets, shell parsing, or publication.
func TestCanonicalTaskSourceContract(t *testing.T) {
	t.Parallel()
	var fixture canonicalTaskFixture
	testutil.LoadFixtures(t, testutil.CanonicalTaskContract, &fixture)
	owners := map[string]any{
		"SkillBodySpecs":       SkillBodySpecs,
		"SharedFragmentSpecs":  SharedFragmentSpecs,
		"RoleSpecs":            RoleSpecs,
		"ConstraintSpecs":      ConstraintSpecs,
		"CommandSpecs":         CommandSpecs,
		"PhaseSpecs":           PhaseSpecs,
		"HandoffSpecs":         HandoffSpecs,
		"FigureSpecs":          FigureSpecs,
		"ChecklistSpecs":       ChecklistSpecs,
		"WorkflowSpecs":        WorkflowSpecs,
		"ReviewAxisSpecs":      ReviewAxisSpecs,
		"ProcedureSteps":       ProcedureSteps,
		"LabelSpecs":           LabelSpecs,
		"CoordinationCommands": CoordinationCommands,
	}
	for _, rule := range fixture.Forbidden {
		t.Run(rule.Name, func(t *testing.T) {
			t.Parallel()
			pattern, err := regexp.Compile(rule.Pattern)
			require.NoError(t, err)
			for owner, value := range owners {
				visitTaskContractStrings(reflect.ValueOf(value), owner, func(path, text string) {
					if match := pattern.FindString(text); match != "" {
						t.Errorf("%s retains unsupported task recipe %q; repair canonical source and regenerate", path, match)
					}
				})
			}
		})
	}
}

func TestWorkerRecipesNeverCloseTasks(t *testing.T) {
	t.Parallel()
	check := func(path, text string) {
		require.NotRegexp(t, `pasture\s+task\s+(?:close|update[^\n]*--status(?:=|\s+)closed)\b`, text, path)
	}
	visitTaskContractStrings(reflect.ValueOf(RoleSpecs[protocol.RoleWorker].Behaviors), "worker behaviors", check)
	visitTaskContractStrings(reflect.ValueOf(GetRoleContext(protocol.RoleWorker).Constraints), "worker constraints", check)
	for owner, body := range SkillBodySpecs {
		if !strings.HasPrefix(owner, "worker") {
			continue
		}
		visitTaskContractStrings(reflect.ValueOf(body), owner, check)
	}
	for id, command := range CoordinationCommands {
		if command.RoleRef == protocol.RoleWorker {
			require.NotContains(t, command.Template, "pasture task close", id)
		}
	}
}

func TestTaskHandoffAndCommitRecipeIdentities(t *testing.T) {
	t.Parallel()
	visitTaskContractStrings(reflect.ValueOf(SkillBodySpecs["architect-handoff"]), "architect-handoff", func(path, text string) {
		require.NotContains(t, text, `pasture task show "${PROPOSAL_ID_URI}"`, path)
	})
	var closures []string
	visitTaskContractStrings(reflect.ValueOf(SkillBodySpecs["supervisor-commit"]), "supervisor-commit", func(path, text string) {
		if strings.Contains(text, `pasture task close "${TASK_A_URI}"`) {
			closures = append(closures, text)
			require.Contains(t, text, `pasture task close "${TASK_B_URI}" --reason="Committed in <commit-hash>"`, path)
		}
	})
	require.Len(t, closures, 1, "the supervisor commit recipe must close both committed tasks")
}

func TestCanonicalTaskRecoveryFragmentReferences(t *testing.T) {
	t.Parallel()
	var fixture canonicalTaskFixture
	testutil.LoadFixtures(t, testutil.CanonicalTaskContract, &fixture)
	refs := FragmentToOwnerRefs()
	for _, tc := range fixture.Fragments {
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()
			id := FragmentId(tc.ID)
			fragment, ok := SharedFragmentSpecs[id]
			require.True(t, ok, "recovery instructions must have one canonical shared owner")
			require.NotNil(t, fragment.Prose)
			for _, text := range tc.Contains {
				require.Contains(t, fragment.Prose.Content, text)
			}
			require.NotEmpty(t, refs[id], "an unreferenced fragment supplies no recovery fallback")
			if tc.EveryBody {
				for owner := range SkillBodySpecs {
					require.Contains(t, refs[id], owner, "body %s must reference shared recovery rather than duplicate it", owner)
				}
			}
		})
	}
}

func visitTaskContractStrings(value reflect.Value, path string, visit func(string, string)) {
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			visitTaskContractStrings(value.Elem(), path, visit)
		}
	case reflect.String:
		visit(path, value.String())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			visitTaskContractStrings(value.Field(i), path+"."+value.Type().Field(i).Name, visit)
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			visitTaskContractStrings(value.MapIndex(key), fmt.Sprintf("%s[%v]", path, key), visit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			visitTaskContractStrings(value.Index(i), fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}
