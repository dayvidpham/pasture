package codegen

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

type openCodeOpenAIModelFixture struct {
	ID   OpenCodeModelID     `json:"id"`
	Slug OpenCodeVariantSlug `json:"slug"`
}

type openCodeOpenAICatalogFixture struct {
	Source   string                       `json:"source"`
	Captured string                       `json:"captured"`
	Provider OpenCodeProviderID           `json:"provider"`
	Models   []openCodeOpenAIModelFixture `json:"models"`
}

func loadOpenCodeOpenAICatalogFixture(t *testing.T) openCodeOpenAICatalogFixture {
	t.Helper()
	path := filepath.Join("testdata", "opencode_openai_models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed OpenAI model fixture %q: %v; restore the fixture before validating the provider catalog", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture openCodeOpenAICatalogFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("strictly decode committed OpenAI model fixture %q: %v; fix unknown, missing, or malformed catalog fields", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("strictly decode committed OpenAI model fixture %q: trailing JSON value found; keep exactly one catalog document", path)
	}
	if fixture.Source == "" || fixture.Captured == "" || fixture.Provider == "" || len(fixture.Models) == 0 {
		t.Fatalf("committed OpenAI model fixture %q is incomplete: source, captured, provider, and models are required", path)
	}
	return fixture
}

func openCodeOpenAIFixtureVariants(fixture openCodeOpenAICatalogFixture) []OpenCodeProviderVariant {
	variants := make([]OpenCodeProviderVariant, 0, len(fixture.Models))
	for _, model := range fixture.Models {
		variants = append(variants, OpenCodeProviderVariant{
			Provider: fixture.Provider,
			Model:    model.ID,
			Slug:     model.Slug,
		})
	}
	return variants
}

func TestOpenCodeOpenAICatalogMatchesCommittedFixture(t *testing.T) {
	t.Parallel()

	fixture := loadOpenCodeOpenAICatalogFixture(t)
	want := openCodeOpenAIFixtureVariants(fixture)
	if !reflect.DeepEqual(openCodeOpenAIVariants, want) {
		t.Fatalf("OpenAI variant catalog = %#v, want exact committed fixture %#v", openCodeOpenAIVariants, want)
	}

	validated, err := validateOpenCodeProviderVariants(openCodeOpenAIVariants)
	if err != nil {
		t.Fatalf("validate production OpenAI variant catalog: %v", err)
	}
	if len(validated) != 3 {
		t.Fatalf("validated OpenAI variant count = %d, want exactly 3", len(validated))
	}

	qualifiedModels := make([]string, 0, len(validated))
	for _, variant := range validated {
		qualifiedModels = append(qualifiedModels, variant.qualifiedModel())
	}
	sort.Strings(qualifiedModels)
	wantModels := []string{
		"openai/gpt-5.6-luna",
		"openai/gpt-5.6-sol",
		"openai/gpt-5.6-terra",
	}
	if !reflect.DeepEqual(qualifiedModels, wantModels) {
		t.Fatalf("qualified OpenAI models = %v, want %v", qualifiedModels, wantModels)
	}
}

func TestOpenCodeOpenAICatalogEmitsEveryRoleDeterministically(t *testing.T) {
	t.Parallel()

	forward := emitOpenCodeAgentFiles(t, openCodeOpenAIVariants)
	reversedCatalog := append([]OpenCodeProviderVariant(nil), openCodeOpenAIVariants...)
	for left, right := 0, len(reversedCatalog)-1; left < right; left, right = left+1, right-1 {
		reversedCatalog[left], reversedCatalog[right] = reversedCatalog[right], reversedCatalog[left]
	}
	reversed := emitOpenCodeAgentFiles(t, reversedCatalog)
	if len(forward) != len(reversed) {
		t.Fatalf("production emitter generated %d files from the forward catalog and %d from the reversed catalog", len(forward), len(reversed))
	}
	for index := range forward {
		if filepath.Base(forward[index].Path) != filepath.Base(reversed[index].Path) || forward[index].Content != reversed[index].Content {
			t.Fatalf("production emitter output at index %d changed when the OpenAI catalog input order was reversed", index)
		}
	}

	byName := make(map[string]GeneratedFile, len(forward))
	for _, file := range forward {
		byName[filepath.Base(file.Path)] = file
	}

	toolRoleCount := 0
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		toolRoleCount++
		defaultFile, ok := byName[string(roleID)+"--default.md"]
		if !ok {
			t.Errorf("role %q is missing its default OpenCode agent definition", roleID)
			continue
		}
		defaultFrontmatter, defaultBody := decodeOpenCodeAgent(t, defaultFile)

		for _, variant := range openCodeOpenAIVariants {
			name := variant.filename(roleID)
			file, ok := byName[name]
			if !ok {
				t.Errorf("role %q is missing OpenAI variant file %q", roleID, name)
				continue
			}
			frontmatter, body := decodeOpenCodeAgent(t, file)
			if frontmatter.Model != variant.qualifiedModel() {
				t.Errorf("%s model = %q, want %q", name, frontmatter.Model, variant.qualifiedModel())
			}
			if frontmatter.Description != defaultFrontmatter.Description || frontmatter.Mode != defaultFrontmatter.Mode {
				t.Errorf("%s changed shared description or mode", name)
			}
			if !reflect.DeepEqual(frontmatter.Permission, defaultFrontmatter.Permission) {
				t.Errorf("%s permissions = %v, want unchanged %v", name, frontmatter.Permission, defaultFrontmatter.Permission)
			}
			if body != defaultBody {
				t.Errorf("%s changed the shared semantic agent body", name)
			}
		}
	}

	wantFileCount := toolRoleCount * (2 + len(openCodeOpenAIVariants))
	if len(forward) != wantFileCount {
		t.Errorf("production emitter generated %d files, want %d legacy, default, and OpenAI role variants", len(forward), wantFileCount)
	}
}

func TestOpenCodeOpenAICatalogFilenamesAreExactForEveryRole(t *testing.T) {
	t.Parallel()

	wantSuffixes := []string{
		"--openai--gpt-5-6-sol.md",
		"--openai--gpt-5-6-terra.md",
		"--openai--gpt-5-6-luna.md",
	}
	for roleID, roleSpec := range RoleSpecs {
		if len(roleSpec.Tools) == 0 {
			continue
		}
		for index, variant := range openCodeOpenAIVariants {
			want := string(roleID) + wantSuffixes[index]
			if got := variant.filename(protocol.RoleId(roleID)); got != want {
				t.Errorf("OpenAI variant %q filename for role %q = %q, want %q", variant.Model, roleID, got, want)
			}
		}
	}
}
