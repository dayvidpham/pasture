package timeouts

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsRoot returns the repository root. Tests run with internal/timeouts as
// the working directory.
func docsRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

// tier pairs a Profile field name with the value that field returns, so a
// document is checked against the pair and not against a number that could
// belong to any tier. Add an entry here when a tier is added to Profile.
type tier struct {
	field string
	value string
}

func productionTiers() []tier {
	profile := ProductionProfile()
	return []tier{
		{field: "SQLiteBusy", value: profile.SQLiteBusy().String()},
		{field: "Ingress", value: profile.Ingress().String()},
		{field: "StartSlice", value: profile.StartSlice().String()},
		{field: "WorkflowResult", value: profile.WorkflowResult().String()},
	}
}

// compact removes every space so a document may write "500 ms" where a Go
// duration prints "500ms", and may pad a table column. The field name, the
// number and the unit still have to match, and they still have to sit next to
// each other.
func compact(text string) string { return strings.ReplaceAll(text, " ", "") }

// skippedDocDirs are excluded from the markdown scan. llm/ holds dated research
// snapshots that keep their old vocabulary on purpose, and legacy/ preserves
// the retired substrate.
var skippedDocDirs = map[string]bool{
	".git":     true,
	"legacy":   true,
	"llm":      true,
	"vendor":   true,
	"result":   true,
	"testdata": true,
}

// retiredBusyTimeoutLiterals are the forms of the five-second SQLite retry that
// the profile replaced. A document that still shows one is telling a reader a
// number the code no longer uses.
var retiredBusyTimeoutLiterals = []string{
	"busy_timeout=5000",
	"busy_timeout(5000)",
	"busy_timeout = 5000",
}

// skippedDocFile reports whether one document is excluded by name. The protocol
// research examples are verbatim records of a different project's requirements
// work, kept as written, for the same reason llm/ is skipped: correcting a
// number in them would falsify the record. Generated skill bodies are NOT
// excluded, except the generated copies of these same research records — the
// match is by file name, so it covers them wherever they are emitted. A wrong
// number in any other generated body is a real defect, repaired in the
// generator.
func skippedDocFile(name string) bool {
	return strings.HasPrefix(name, "RESEARCH_EXAMPLE-") && strings.EqualFold(filepath.Ext(name), ".md")
}

// markdownDocs walks the repository and returns every markdown file outside the
// skipped directories and the skipped file names. A glob rather than a list, so a document added later is
// covered without anyone remembering to register it.
func markdownDocs(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedDocDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if skippedDocFile(entry.Name()) {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			// CLAUDE.md and GEMINI.md are symlinks to AGENTS.md. Report the
			// underlying document once.
			target, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				target = path
			}
			if !seen[target] {
				seen[target] = true
				found = append(found, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) == 0 {
		t.Fatal("no markdown files found; the walk is broken, not the documents")
	}
	return found
}

// TestDocsDoNotRepeatTheRetiredSQLiteRetry fails when any markdown document in
// the repository states the retired five-second SQLite lock retry.
func TestDocsDoNotRepeatTheRetiredSQLiteRetry(t *testing.T) {
	t.Parallel()
	root := docsRoot(t)
	for _, path := range markdownDocs(t, root) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}
		for _, literal := range retiredBusyTimeoutLiterals {
			if strings.Contains(string(body), literal) {
				t.Errorf("%s states %q; the SQLite busy timeout comes from timeouts.ProductionProfile (%s). Name the profile, or state the live value.",
					relative, literal, ProductionProfile().SQLiteBusy())
			}
		}
	}
}

// TestAgentsDocStatesTheLiveProductionTiers pins the AGENTS.md tier table to the
// values ProductionProfile returns. Each tier is matched as a table row — the
// field name immediately followed by its own value — so changing one tier's
// number cannot be satisfied by the same number appearing elsewhere.
func TestAgentsDocStatesTheLiveProductionTiers(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join(docsRoot(t), "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	table := compact(string(body))
	for _, tr := range productionTiers() {
		want := compact(fmt.Sprintf("`%s` | **%s**", tr.field, tr.value))
		if !strings.Contains(table, want) {
			t.Errorf("the AGENTS.md timeout table has no row giving %s as %s; update it to match internal/timeouts.", tr.field, tr.value)
		}
	}
}

// TestPackageDocStatesTheLiveProductionTiers pins the same rows in this
// package's own doc comment, which is what a reader of the code sees first.
func TestPackageDocStatesTheLiveProductionTiers(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("profile.go")
	if err != nil {
		t.Fatalf("read profile.go: %v", err)
	}
	doc, _, found := strings.Cut(string(body), "\npackage timeouts")
	if !found {
		t.Fatal("profile.go has no package clause")
	}
	table := compact(doc)
	for _, tr := range productionTiers() {
		want := compact(fmt.Sprintf("%s  %s", tr.field, tr.value))
		if !strings.Contains(table, want) {
			t.Errorf("the package doc tier table has no row giving %s as %s; update profile.go to match ProductionProfile.", tr.field, tr.value)
		}
	}
}
