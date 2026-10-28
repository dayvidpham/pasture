package execution_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/testutil"
)

type fixture struct {
	Name         string                     `yaml:"name"`
	Permutations [][3]int                   `yaml:"permutations"`
	Models       [3]*string                 `yaml:"models"`
	Providers    [3]*string                 `yaml:"providers"`
	WantModel    execution.AttributionValue `yaml:"want_model"`
	WantProvider execution.AttributionValue `yaml:"want_provider"`
}

func input(models, providers [3]*string) execution.Inputs {
	var d [3]execution.Declaration
	mm := [3]execution.Member{execution.HostModelID, execution.EnvironmentModel, execution.RegistryModel}
	pm := [3]execution.Member{execution.HostProvider, execution.EnvironmentProvider, execution.RegistryProvider}
	for i := range d {
		if models[i] != nil {
			d[i].Model = execution.Raw{Present: true, Member: mm[i], Value: *models[i]}
		}
		if providers[i] != nil {
			d[i].Provider = execution.Raw{Present: true, Member: pm[i], Value: *providers[i]}
		}
	}
	return execution.Inputs{Kind: execution.Primary, Host: d[0], Environment: d[1], ActorRegistry: d[2]}
}

func TestNormalizationFixtures(t *testing.T) {
	t.Parallel()
	var cases []fixture
	testutil.LoadFixtures(t, testutil.ExecutionNormalization, &cases)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			got, err := execution.Normalize(input(tc.Models, tc.Providers))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Model, tc.WantModel) {
				t.Errorf("model: got %#v want %#v", got.Model, tc.WantModel)
			}
			if !reflect.DeepEqual(got.Provider, tc.WantProvider) {
				t.Errorf("provider: got %#v want %#v", got.Provider, tc.WantProvider)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip execution.Metadata
			if err := json.Unmarshal(encoded, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, roundtrip) {
				t.Fatal("JSON changed normalized evidence")
			}
			for _, permutation := range tc.Permutations {
				var models, providers [3]*string
				for old, next := range permutation {
					models[next], providers[next] = tc.Models[old], tc.Providers[old]
				}
				permuted, err := execution.Normalize(input(models, providers))
				if err != nil {
					t.Fatal(err)
				}
				wantModel := permuteExpectation(tc.WantModel, permutation, tc.Models, false)
				wantProvider := permuteExpectation(tc.WantProvider, permutation, tc.Providers, true)
				if !reflect.DeepEqual(permuted.Model, wantModel) || !reflect.DeepEqual(permuted.Provider, wantProvider) {
					t.Fatalf("source permutation %v: got %#v/%#v want %#v/%#v", permutation, permuted.Model, permuted.Provider, wantModel, wantProvider)
				}
			}
		})
	}
}

// Permute literal expected evidence, not the normalization decision table.
// Fixtures enabling permutations have one invalid source at most. All observed
// provider inputs in these fixtures are paired to the same observed model.
func permuteExpectation(value execution.AttributionValue, permutation [3]int, raw [3]*string, provider bool) execution.AttributionValue {
	sources := [3]execution.Source{execution.Host, execution.Environment, execution.ActorRegistry}
	members := [3]execution.Member{execution.HostModelID, execution.EnvironmentModel, execution.RegistryModel}
	if provider {
		members = [3]execution.Member{execution.HostProvider, execution.EnvironmentProvider, execution.RegistryProvider}
	}
	index := func(source execution.Source) int {
		for i, s := range sources {
			if source == s {
				return i
			}
		}
		panic("fixture has unsupported source")
	}
	if value.Source != execution.None {
		next := permutation[index(value.Source)]
		if value.Status == execution.Observed {
			for i, r := range raw {
				if r != nil && *r == value.Value && permutation[i] < next {
					next = permutation[i]
				}
			}
		}
		value.Source, value.Field = sources[next], members[next]
	}
	if value.Candidates != nil {
		value.Candidates = append([]execution.Candidate(nil), value.Candidates...)
		for i := range value.Candidates {
			c := &value.Candidates[i]
			next := permutation[index(c.Source)]
			c.Source, c.Field = sources[next], members[next]
		}
		sort.Slice(value.Candidates, func(i, j int) bool { return index(value.Candidates[i].Source) < index(value.Candidates[j].Source) })
	}
	return value
}
