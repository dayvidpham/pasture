package codegen

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

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

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var fixture openCodeAnthropicFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("strictly decode Anthropic catalog fixture %q: %v; refresh it from %s without adding unvalidated fields", path, err, "https://models.dev/api.json")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("Anthropic catalog fixture %q contains data after its first JSON value: %v; keep exactly one catalog document", path, err)
	}
	return fixture
}

func TestOpenCodeAnthropicCatalogMatchesStrictFixture(t *testing.T) {
	t.Parallel()

	fixture := loadOpenCodeAnthropicFixture(t)
	if fixture.Source != "https://models.dev/api.json" || fixture.Captured == "" {
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
