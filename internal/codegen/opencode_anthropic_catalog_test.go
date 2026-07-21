package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

type openCodeAnthropicFixture struct {
	Source    string `json:"_source"`
	Captured  string `json:"_captured"`
	Anthropic struct {
		ID     OpenCodeProviderID `json:"id"`
		Name   string             `json:"name"`
		Models map[OpenCodeModelID]struct {
			ID   OpenCodeModelID `json:"id"`
			Name string          `json:"name"`
		} `json:"models"`
	} `json:"anthropic"`
}

func loadOpenCodeAnthropicFixture(t *testing.T) openCodeAnthropicFixture {
	t.Helper()

	path := filepath.Join("testdata", "opencode_anthropic_models.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open Anthropic catalog fixture %q: %v; restore the committed fixture before testing the provider catalog", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close Anthropic catalog fixture %q after decoding: %v", path, err)
		}
	}()

	fixture, err := decodeOpenCodeAnthropicFixture(file)
	if err != nil {
		t.Fatalf("strictly decode Anthropic catalog fixture %q: %v; refresh it from %s with validated provenance and no unvalidated fields", path, err, "https://models.dev/api.json")
	}
	return fixture
}

func decodeOpenCodeAnthropicFixture(reader io.Reader) (openCodeAnthropicFixture, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var fixture openCodeAnthropicFixture
	if err := decoder.Decode(&fixture); err != nil {
		return openCodeAnthropicFixture{}, fmt.Errorf("decode fixture JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return openCodeAnthropicFixture{}, fmt.Errorf("fixture contains data after its first JSON value: %v; keep exactly one catalog document", err)
	}
	if fixture.Captured == "" {
		return openCodeAnthropicFixture{}, errors.New("fixture _captured is required; record the models.dev snapshot date as YYYY-MM-DD")
	}
	captured, err := time.Parse(time.DateOnly, fixture.Captured)
	if err != nil || captured.Format(time.DateOnly) != fixture.Captured {
		return openCodeAnthropicFixture{}, fmt.Errorf("fixture _captured value %q is not a valid canonical ISO-8601 calendar date; use YYYY-MM-DD for the actual snapshot date", fixture.Captured)
	}
	return fixture, nil
}

func TestOpenCodeAnthropicCatalogMatchesStrictFixture(t *testing.T) {
	t.Parallel()

	fixture := loadOpenCodeAnthropicFixture(t)
	if fixture.Source != "https://models.dev/api.json" {
		t.Fatalf("Anthropic fixture provenance = source %q, captured %q; record the models.dev source and capture date", fixture.Source, fixture.Captured)
	}
	if fixture.Anthropic.ID != openCodeAnthropicProvider || fixture.Anthropic.Name != "Anthropic" {
		t.Fatalf("Anthropic fixture provider = (%q, %q), want (%q, %q)", fixture.Anthropic.ID, fixture.Anthropic.Name, openCodeAnthropicProvider, "Anthropic")
	}

	want := map[OpenCodeModelID]struct {
		name string
		slug OpenCodeVariantSlug
	}{
		"claude-fable-5":   {name: "Claude Fable 5", slug: "fable-5"},
		"claude-sonnet-5":  {name: "Claude Sonnet 5", slug: "sonnet-5"},
		"claude-opus-4-8":  {name: "Claude Opus 4.8", slug: "opus-4-8"},
		"claude-haiku-4-5": {name: "Claude Haiku 4.5 (latest)", slug: "haiku-4-5"},
	}
	if len(fixture.Anthropic.Models) != len(want) {
		t.Fatalf("Anthropic fixture contains %d models, want exactly %d", len(fixture.Anthropic.Models), len(want))
	}
	if len(openCodeAnthropicCatalog) != len(want) {
		t.Fatalf("Anthropic production catalog contains %d variants, want exactly %d", len(openCodeAnthropicCatalog), len(want))
	}

	for modelID, expected := range want {
		model, ok := fixture.Anthropic.Models[modelID]
		if !ok {
			t.Errorf("Anthropic fixture is missing exact models.dev model ID %q", modelID)
			continue
		}
		if model.ID != modelID || model.Name != expected.name {
			t.Errorf("Anthropic fixture model %q = (%q, %q), want (%q, %q)", modelID, model.ID, model.Name, modelID, expected.name)
		}
	}

	seen := make(map[OpenCodeModelID]OpenCodeVariantSlug, len(openCodeAnthropicCatalog))
	for _, variant := range openCodeAnthropicCatalog {
		if variant.Provider != openCodeAnthropicProvider {
			t.Errorf("Anthropic catalog model %q uses provider %q, want %q", variant.Model, variant.Provider, openCodeAnthropicProvider)
		}
		expected, ok := want[variant.Model]
		if !ok {
			t.Errorf("Anthropic production catalog contains unexpected model ID %q", variant.Model)
			continue
		}
		if variant.Slug != expected.slug {
			t.Errorf("Anthropic model %q slug = %q, want %q", variant.Model, variant.Slug, expected.slug)
		}
		seen[variant.Model] = variant.Slug
	}
	if len(seen) != len(want) {
		t.Errorf("Anthropic production catalog covers %d distinct exact model IDs, want %d", len(seen), len(want))
	}
	if _, err := validateOpenCodeProviderVariants(openCodeAnthropicCatalog); err != nil {
		t.Fatalf("validate committed Anthropic production catalog: %v", err)
	}
}

func TestOpenCodeAnthropicFixtureRejectsInvalidCaptureDate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		captured string
	}{
		{name: "malformed", captured: "unknown"},
		{name: "impossible-calendar-date", captured: "2026-02-30"},
		{name: "non-canonical", captured: "2026-7-1"},
		{name: "missing", captured: ""},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			document := fmt.Sprintf(`{
  "_source": "https://models.dev/api.json",
  "_captured": %q,
  "anthropic": {"id": "anthropic", "name": "Anthropic", "models": {}}
}`, testCase.captured)
			if _, err := decodeOpenCodeAnthropicFixture(strings.NewReader(document)); err == nil || !strings.Contains(err.Error(), "_captured") {
				t.Fatalf("decode capture date %q error = %v, want actionable _captured validation failure", testCase.captured, err)
			}
		})
	}
}

func TestOpenCodeAnthropicCatalogEmitsEveryRoleVariant(t *testing.T) {
	t.Parallel()

	files := emitOpenCodeAgentFiles(t, openCodeAnthropicCatalog)
	byName := make(map[string]GeneratedFile, len(files))
	gotNames := make([]string, 0, len(files))
	for _, file := range files {
		name := filepath.Base(file.Path)
		if _, exists := byName[name]; exists {
			t.Fatalf("production emitter returned duplicate filename %q", name)
		}
		byName[name] = file
		gotNames = append(gotNames, name)
	}

	var wantNames []string
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		role := string(roleID)
		wantNames = append(wantNames, role+".md", role+"--default.md")
		for _, variant := range openCodeAnthropicCatalog {
			wantNames = append(wantNames, variant.filename(roleID))
		}
	}
	sort.Strings(wantNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("production emitter filenames = %v, want complete deterministic inventory %v", gotNames, wantNames)
	}

	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		legacyName := string(roleID) + ".md"
		legacy, ok := byName[legacyName]
		if !ok {
			t.Fatalf("production emitter omitted legacy role definition %q", legacyName)
		}
		legacyFrontmatter, legacyBody := decodeOpenCodeAgent(t, legacy)

		for _, variant := range openCodeAnthropicCatalog {
			name := variant.filename(roleID)
			file, ok := byName[name]
			if !ok {
				t.Errorf("production emitter omitted Anthropic role variant %q", name)
				continue
			}
			frontmatter, body := decodeOpenCodeAgent(t, file)
			if frontmatter.Model != variant.qualifiedModel() {
				t.Errorf("%s model = %q, want exact ID %q", name, frontmatter.Model, variant.qualifiedModel())
			}
			if frontmatter.Description != legacyFrontmatter.Description || frontmatter.Mode != legacyFrontmatter.Mode || !reflect.DeepEqual(frontmatter.Permission, legacyFrontmatter.Permission) {
				t.Errorf("%s changed shared role frontmatter beyond the model field", name)
			}
			if body != legacyBody {
				t.Errorf("%s changed the shared semantic body for role %q", name, roleID)
			}
		}
	}

	if _, ok := byName[(OpenCodeProviderVariant{Provider: openCodeAnthropicProvider, Slug: "fable-5"}).filename(protocol.RoleWorker)]; !ok {
		t.Error("production emitter did not expose the requested worker--anthropic--fable-5.md definition")
	}
}
