package codegen

import (
	"bytes"
	"encoding/json"
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

const openCodeModelsDevSource = "https://models.dev/api.json"

type openCodeOpenAICatalogFixture struct {
	Source   string `json:"source"`
	Captured string `json:"captured"`
	Provider struct {
		ID     OpenCodeProviderID `json:"id"`
		Name   string             `json:"name"`
		Models map[string]struct {
			ID   OpenCodeModelID `json:"id"`
			Name string          `json:"name"`
		} `json:"models"`
	} `json:"provider"`
}

func loadOpenCodeOpenAICatalogFixture(t *testing.T) openCodeOpenAICatalogFixture {
	t.Helper()
	path := filepath.Join("testdata", "opencode_openai_models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed OpenAI model fixture %q: %v; restore the fixture before validating the provider catalog", path, err)
	}

	fixture, err := decodeOpenCodeOpenAICatalogFixture(data)
	if err != nil {
		t.Fatalf("validate committed OpenAI model fixture %q: %v", path, err)
	}
	return fixture
}

func decodeOpenCodeOpenAICatalogFixture(data []byte) (openCodeOpenAICatalogFixture, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture openCodeOpenAICatalogFixture
	if err := decoder.Decode(&fixture); err != nil {
		return openCodeOpenAICatalogFixture{}, fmt.Errorf("strictly decode models.dev OpenAI provider subset: %w; remove unknown or malformed fields", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return openCodeOpenAICatalogFixture{}, fmt.Errorf("strictly decode models.dev OpenAI provider subset: trailing JSON value found; keep exactly one catalog document")
	}
	if fixture.Source != openCodeModelsDevSource {
		return openCodeOpenAICatalogFixture{}, fmt.Errorf("models.dev OpenAI provider subset source = %q, want exact externally resolvable URL %q", fixture.Source, openCodeModelsDevSource)
	}
	date, err := time.Parse(time.DateOnly, fixture.Captured)
	if err != nil || date.Format(time.DateOnly) != fixture.Captured {
		return openCodeOpenAICatalogFixture{}, fmt.Errorf("models.dev OpenAI provider subset capture date %q is invalid; use strict YYYY-MM-DD syntax", fixture.Captured)
	}
	if fixture.Provider.ID == "" || fixture.Provider.Name == "" || len(fixture.Provider.Models) == 0 {
		return openCodeOpenAICatalogFixture{}, fmt.Errorf("models.dev OpenAI provider subset is incomplete; provider id, name, and models are required")
	}
	for key, model := range fixture.Provider.Models {
		if key == "" || string(model.ID) != key || model.Name == "" {
			return openCodeOpenAICatalogFixture{}, fmt.Errorf("models.dev OpenAI model entry %q is invalid; map key and model id must match and name must be present", key)
		}
	}
	return fixture, nil
}

func TestOpenCodeOpenAICatalogMatchesCommittedFixture(t *testing.T) {
	t.Parallel()

	fixture := loadOpenCodeOpenAICatalogFixture(t)
	if fixture.Provider.ID != "openai" || fixture.Provider.Name != "OpenAI" {
		t.Fatalf("models.dev provider identity = %q (%q), want openai (OpenAI)", fixture.Provider.ID, fixture.Provider.Name)
	}
	wantModelNames := map[string]string{
		"gpt-5.6-sol":   "GPT-5.6 Sol",
		"gpt-5.6-terra": "GPT-5.6 Terra",
		"gpt-5.6-luna":  "GPT-5.6 Luna",
	}
	if len(fixture.Provider.Models) != len(wantModelNames) {
		t.Fatalf("models.dev OpenAI provider subset contains %d models, want exact fixed subset of %d", len(fixture.Provider.Models), len(wantModelNames))
	}
	for modelID, wantName := range wantModelNames {
		model, ok := fixture.Provider.Models[modelID]
		if !ok || string(model.ID) != modelID || model.Name != wantName {
			t.Errorf("models.dev OpenAI model %q = %#v, want id %q and name %q", modelID, model, modelID, wantName)
		}
	}

	validated, err := validateOpenCodeProviderVariants(openCodeOpenAIVariants)
	if err != nil {
		t.Fatalf("validate production OpenAI variant catalog: %v", err)
	}
	if len(validated) != 3 {
		t.Fatalf("validated OpenAI variant count = %d, want exactly 3", len(validated))
	}

	qualifiedModels := make([]string, 0, len(validated))
	seenModels := make(map[string]struct{}, len(validated))
	for _, variant := range validated {
		if variant.Provider != fixture.Provider.ID {
			t.Errorf("production variant %q provider = %q, want fixture provider %q", variant.Model, variant.Provider, fixture.Provider.ID)
		}
		if _, ok := fixture.Provider.Models[string(variant.Model)]; !ok {
			t.Errorf("production model %q is absent from the committed models.dev OpenAI provider subset", variant.Model)
		}
		seenModels[string(variant.Model)] = struct{}{}
		qualifiedModels = append(qualifiedModels, variant.qualifiedModel())
	}
	if len(seenModels) != len(fixture.Provider.Models) {
		t.Errorf("production catalog covers %d distinct models, committed models.dev subset contains %d", len(seenModels), len(fixture.Provider.Models))
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

func TestDecodeOpenCodeOpenAICatalogFixtureRejectsInvalidProvenanceAndShape(t *testing.T) {
	t.Parallel()

	valid, err := os.ReadFile(filepath.Join("testdata", "opencode_openai_models.json"))
	if err != nil {
		t.Fatalf("read committed OpenAI model fixture for negative cases: %v", err)
	}
	tests := []struct {
		name      string
		data      []byte
		wantError string
	}{
		{
			name:      "non-resolvable-source-description",
			data:      bytes.Replace(valid, []byte(openCodeModelsDevSource), []byte("OpenAI model variants"), 1),
			wantError: "want exact externally resolvable URL",
		},
		{
			name:      "non-canonical-capture-date",
			data:      bytes.Replace(valid, []byte(`"2026-07-21"`), []byte(`"2026-7-21"`), 1),
			wantError: "strict YYYY-MM-DD syntax",
		},
		{
			name:      "unknown-provider-field",
			data:      bytes.Replace(valid, []byte(`"name": "OpenAI"`), []byte(`"name": "OpenAI", "owner": "unknown"`), 1),
			wantError: "unknown field",
		},
		{
			name:      "trailing-document",
			data:      append(append([]byte(nil), valid...), []byte(` {}`)...),
			wantError: "trailing JSON value",
		},
		{
			name:      "model-key-id-mismatch",
			data:      bytes.Replace(valid, []byte(`"id": "gpt-5.6-sol"`), []byte(`"id": "gpt-5.6-sol-renamed"`), 1),
			wantError: "map key and model id must match",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeOpenCodeOpenAICatalogFixture(test.data)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("decode error = %v, want error containing %q", err, test.wantError)
			}
		})
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
