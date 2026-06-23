package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelsDevSnapshot is the subset of the models.dev catalog shape we care about:
// a provider node keyed by provider id, each carrying a models map keyed by
// model id. Only the fields needed for catalog-existence checks are decoded.
type modelsDevSnapshot map[string]struct {
	ID     string `json:"id"`
	Models map[string]struct {
		ID string `json:"id"`
	} `json:"models"`
}

// loadModelsSnapshot reads the committed models.dev snapshot fixture from disk.
// It deliberately does NOT fetch from the network: the snapshot is the source
// of truth at test time (the build/test sandbox may have no network).
func loadModelsSnapshot(t *testing.T) modelsDevSnapshot {
	t.Helper()
	path := filepath.Join("testdata", "opencode_models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read models.dev snapshot fixture %q: %v — the committed snapshot must exist; "+
			"refresh it from https://models.dev/api.json (anthropic provider subset)", path, err)
	}
	// The fixture carries leading `_comment` / `_source` / `_captured` metadata
	// keys alongside provider nodes; decode into a generic map first, then keep
	// only the provider nodes (those with a non-empty id + models).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode models.dev snapshot fixture %q: %v", path, err)
	}
	snap := make(modelsDevSnapshot)
	for key, rawNode := range raw {
		if strings.HasPrefix(key, "_") {
			continue // metadata key, not a provider node
		}
		var node struct {
			ID     string `json:"id"`
			Models map[string]struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if err := json.Unmarshal(rawNode, &node); err != nil {
			t.Fatalf("decode provider node %q in snapshot %q: %v", key, path, err)
		}
		snap[key] = node
	}
	return snap
}

// catalogHas reports whether the snapshot contains the given fully-qualified
// OpenCode model id ("<provider>/<model-id>") as an actual catalog entry.
func catalogHas(snap modelsDevSnapshot, qualifiedID string) bool {
	provider, modelID, ok := strings.Cut(qualifiedID, "/")
	if !ok {
		return false
	}
	node, ok := snap[provider]
	if !ok {
		return false
	}
	_, ok = node.Models[modelID]
	return ok
}

// TestOpenCodeModelsExistInCatalogSnapshot asserts every model id the generator
// maps to (openCodeModel values) EXISTS in the committed models.dev catalog
// snapshot — catalog-existence, not merely that the lookup map has the key. This
// guards against drift between the generator's hardcoded model ids and the real
// models.dev catalog (e.g. a typo or a model that was renamed/removed).
func TestOpenCodeModelsExistInCatalogSnapshot(t *testing.T) {
	t.Parallel()

	snap := loadModelsSnapshot(t)
	if len(snap) == 0 {
		t.Fatalf("models.dev snapshot fixture decoded to zero provider nodes")
	}

	if len(openCodeModel) == 0 {
		t.Fatalf("openCodeModel lookup table is empty — nothing to validate")
	}

	for nickname, qualifiedID := range openCodeModel {
		if !catalogHas(snap, qualifiedID) {
			t.Errorf("model %q (nickname %q) is NOT present in the models.dev snapshot catalog "+
				"(testdata/opencode_models.json) — either the id is wrong/stale or the snapshot "+
				"is out of date; refresh from https://models.dev/api.json", qualifiedID, nickname)
		}
	}
}
