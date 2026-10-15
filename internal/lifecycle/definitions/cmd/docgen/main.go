// Command docgen regenerates the remaining lifecycle definition owner bodies:
// schema, retention, epoch implementation, and interpreter, plus the engine
// execution and interpretation source inventories the epoch and interpreter
// documents embed. It is invoked by `go generate
// ./internal/lifecycle/definitions` (wired into `make generate`). Output is
// deterministic, so a second run is zero-diff.
//
// Source inventory contract, enforced by the owner agreement checks:
//   - Paths are repository-relative with forward slashes, sorted, and hashed
//     over the exact file bytes the generator read. No absolute paths, no test
//     files, no testdata, no checkout dirt.
//   - The epoch inventory is the complete production epoch execution surface:
//     every non-test Go source under internal/engine. A file the engine adds
//     later fails the coverage check until it is listed here or explicitly
//     classified beside it.
//   - The interpreter inventory is the explicit interpretation pipeline plus
//     its actual semantic dependencies: the verified-L2 event owner with its
//     decision/consultation vocabulary, the legalize transform, the middle-end
//     derivation, the receipt record codec, the model record type, the runtime
//     semantic vocabulary the transform discriminates, and the backend
//     consultation builder the gate arm derives through. The interpretation
//     vocabulary itself is excluded on purpose: it is an input coordinate the
//     pipeline stamps, tracked by its own metamodel owner and bridge, never by
//     the interpreter identity.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
)

// epochSources is the explicit engine execution source inventory. Every entry
// is a production epoch execution, control, queue, recovery, or projection
// source; there is no exclusion list because the whole engine package
// executes or recovers epochs.
var epochSources = []string{
	"internal/engine/activities.go",
	"internal/engine/control.go",
	"internal/engine/dbosinit.go",
	"internal/engine/engine.go",
	"internal/engine/projection.go",
	"internal/engine/queue.go",
	"internal/engine/review.go",
	"internal/engine/schema_gate.go",
	"internal/engine/slice.go",
	"internal/engine/version.go",
	"internal/engine/workflow.go",
}

// interpreterSources is the explicit interpretation source inventory with the
// classification each entry carries in the package doc.
var interpreterSources = []string{
	"internal/lifecycle/backend/backend.go",
	"internal/lifecycle/legalize/legalize.go",
	"internal/lifecycle/middleend/evaluate.go",
	"internal/lifecycle/model/interpreted.go",
	"internal/lifecycle/receipt/interpreted.go",
	"internal/lifecycle/waist/consultation.go",
	"internal/lifecycle/waist/decision.go",
	"internal/lifecycle/waist/event.go",
	"internal/runtime/lifecycle.go",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "docgen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	definitionsDir := filepath.Join(root, "internal", "lifecycle", "definitions")
	generatedDir := filepath.Join(definitionsDir, "generated")
	if err := os.MkdirAll(generatedDir, 0755); err != nil {
		return fmt.Errorf("create generated directory: %w", err)
	}
	epochInventory, err := hashSources(root, epochSources)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(generatedDir, "epoch-sources.json"), epochInventory); err != nil {
		return err
	}
	interpreterInventory, err := hashSources(root, interpreterSources)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(generatedDir, "interpreter-sources.json"), interpreterInventory); err != nil {
		return err
	}
	// The bodies embed their source inventories, so they are assembled from
	// the freshly hashed inventories rather than the previously compiled
	// embedded ones; the next build embeds exactly what was hashed here,
	// which is what keeps repeated generation zero-diff.
	epochSpec, err := definitions.AssembleEpochSpecification(toSourceEntries(epochInventory))
	if err != nil {
		return fmt.Errorf("describe epoch specification: %w", err)
	}
	epochBody, err := definitions.AssembleEpochBody(epochSpec)
	if err != nil {
		return fmt.Errorf("generate epoch.json: %w", err)
	}
	interpreterSpec, err := definitions.AssembleInterpreterSpecification(toSourceEntries(interpreterInventory))
	if err != nil {
		return fmt.Errorf("describe interpreter specification: %w", err)
	}
	interpreterBody, err := definitions.AssembleInterpreterBody(interpreterSpec)
	if err != nil {
		return fmt.Errorf("generate interpreter.json: %w", err)
	}
	bodies := []struct {
		name string
		body func() ([]byte, error)
	}{
		{"schema.json", definitions.GenerateSchemaBody},
		{"retention.json", definitions.GenerateRetentionBody},
	}
	for _, target := range bodies {
		body, err := target.body()
		if err != nil {
			return fmt.Errorf("generate %s: %w", target.name, err)
		}
		if err := os.WriteFile(filepath.Join(generatedDir, target.name), body, 0644); err != nil {
			return fmt.Errorf("write %s: %w", target.name, err)
		}
	}
	assembled := []struct {
		name string
		body []byte
	}{
		{"epoch.json", epochBody},
		{"interpreter.json", interpreterBody},
	}
	for _, target := range assembled {
		if err := os.WriteFile(filepath.Join(generatedDir, target.name), target.body, 0644); err != nil {
			return fmt.Errorf("write %s: %w", target.name, err)
		}
	}
	return nil
}

func toSourceEntries(entries []sourceEntry) []definitions.SourceEntry {
	converted := make([]definitions.SourceEntry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, definitions.SourceEntry{Path: entry.Path, SHA256: entry.SHA256})
	}
	return converted
}

// repositoryRoot resolves the repository root from this source file's own
// path, so generation never depends on the invoker's working directory. The
// go.mod marker proves the resolution landed on the module root.
func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate the generator source file")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..")
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve the repository root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if _, err := os.Stat(filepath.Join(absolute, "go.mod")); err != nil {
		return "", fmt.Errorf("repository root %q holds no go.mod marker: %w", absolute, err)
	}
	return absolute, nil
}

type sourceEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// hashSources reads every listed source relative to the repository root and
// returns the sorted path/sha256 inventory. An unlisted, absolute, or missing
// path fails; the inventory never contains test files or checkout dirt.
func hashSources(root string, paths []string) ([]sourceEntry, error) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	entries := make([]sourceEntry, 0, len(sorted))
	for _, path := range sorted {
		if filepath.IsAbs(path) || path != filepath.ToSlash(path) {
			return nil, fmt.Errorf("source path %q is not repository-relative with forward slashes", path)
		}
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, fmt.Errorf("read source %q: %w", path, err)
		}
		sum := sha256.Sum256(content)
		entries = append(entries, sourceEntry{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	return entries, nil
}

func writeJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
