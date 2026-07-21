package promotion_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/promotion"
	"github.com/dayvidpham/pasture/internal/testutil"
)

type marketplaceValidationFixture struct {
	Name      string `yaml:"name"`
	Mutation  string `yaml:"mutation"`
	WantError bool   `yaml:"want_error"`
}

func TestValidateMarketplaceFixtureMatrix(t *testing.T) {
	var fixtures []marketplaceValidationFixture
	testutil.LoadFixtures(t, testutil.MarketplaceValidation, &fixtures)
	projection := projectCandidateTree(t)

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			catalog := projectedCatalog(projection)
			plugins := catalog["plugins"].([]map[string]any)
			switch fixture.Mutation {
			case "valid":
			case "duplicate":
				catalog["plugins"] = append(plugins, cloneMap(plugins[0]))
			case "wrong_catalog":
				catalog["name"] = "other"
			case "padded_name":
				plugins[0]["name"] = " " + plugins[0]["name"].(string)
			case "wrong_path":
				plugins[0]["source"].(map[string]any)["path"] = "internal/target/claudecode/assets/other"
			case "wrong_url":
				plugins[0]["source"].(map[string]any)["url"] = "https://github.com/example/pasture.git"
			case "duplicate_source_path":
				plugins[1]["source"].(map[string]any)["path"] = plugins[0]["source"].(map[string]any)["path"]
			case "moving_ref":
				source := plugins[0]["source"].(map[string]any)
				delete(source, "sha")
				source["ref"] = "main"
			case "mixed_union":
				plugins[0]["source"].(map[string]any)["repo"] = promotion.PastureRepository
			case "unsupported_source":
				plugins[0]["source"].(map[string]any)["source"] = "directory"
			case "unknown_source_field":
				plugins[0]["source"].(map[string]any)["branch"] = "main"
			default:
				t.Fatalf("unknown fixture mutation %q", fixture.Mutation)
			}
			data, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			err = promotion.ValidateMarketplace(data, "fixture", projection)
			if (err != nil) != fixture.WantError {
				t.Fatalf("ValidateMarketplace error = %v, want_error=%v", err, fixture.WantError)
			}
		})
	}
}

func TestValidateMarketplaceRejectsDuplicateProjectionAuthorityPath(t *testing.T) {
	projection := projectCandidateTree(t)
	projection.Entries[1].Source.Path = projection.Entries[0].Source.Path
	data, err := json.Marshal(projectedCatalog(projection))
	if err != nil {
		t.Fatal(err)
	}
	if err := promotion.ValidateMarketplace(data, "fixture", projection); err == nil {
		t.Fatal("expected duplicate candidate-owned source path to fail before catalog comparison")
	}
}

func projectedCatalog(projection promotion.Projection) map[string]any {
	plugins := make([]map[string]any, 0, len(projection.Entries))
	for _, entry := range projection.Entries {
		plugins = append(plugins, map[string]any{
			"name": entry.Name, "description": entry.Description, "version": entry.Version,
			"source": map[string]any{"source": entry.Source.Source, "url": entry.Source.URL, "path": entry.Source.Path, "sha": entry.Source.SHA},
		})
	}
	return map[string]any{"name": projection.MarketplaceName, "plugins": plugins}
}

func cloneMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
