package timeouts

import (
	"fmt"
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

// guardedDocs are the hand-written documents that state timeout facts. The
// research notes under llm/ are dated snapshots of an older library and are
// excluded on purpose: they carry their own snapshot note.
var guardedDocs = []string{
	"AGENTS.md",
	"CHANGELOG.md",
	"CONTRIBUTING.md",
	"README.md",
	"ROADMAP.md",
	"TESTING.md",
	"docs/dbos-architecture.md",
	"docs/codegen.md",
	"docs/VERSIONING.md",
}

// retiredBusyTimeoutLiterals are the forms of the five-second SQLite retry that
// the profile replaced. A document that still shows one is telling a reader a
// number the code no longer uses.
var retiredBusyTimeoutLiterals = []string{
	"busy_timeout=5000",
	"busy_timeout(5000)",
	"busy_timeout = 5000",
}

// TestDocsDoNotRepeatTheRetiredSQLiteRetry fails when a shipped document states
// the retired five-second SQLite lock retry. The live value belongs to
// ProductionProfile and is asserted separately below.
func TestDocsDoNotRepeatTheRetiredSQLiteRetry(t *testing.T) {
	t.Parallel()
	root := docsRoot(t)
	for _, relative := range guardedDocs {
		body, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		for _, literal := range retiredBusyTimeoutLiterals {
			if strings.Contains(string(body), literal) {
				t.Errorf("%s states %q; the SQLite busy timeout comes from timeouts.ProductionProfile (%s). Name the profile, or state the live value.",
					relative, literal, ProductionProfile().SQLiteBusy())
			}
		}
	}
}

// TestAgentsDocStatesTheLiveProductionTiers pins the tier table in AGENTS.md to
// the values ProductionProfile actually returns, so a profile change that is
// not written up fails here instead of misleading a reader.
func TestAgentsDocStatesTheLiveProductionTiers(t *testing.T) {
	t.Parallel()
	profile := ProductionProfile()
	body, err := os.ReadFile(filepath.Join(docsRoot(t), "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	// Spaces are removed so the prose may write "500 ms" where the Go
	// duration prints "500ms". The number and the unit still have to match.
	compact := strings.ReplaceAll(string(body), " ", "")
	for _, want := range []string{
		fmt.Sprintf("**%s**", profile.SQLiteBusy()),
		fmt.Sprintf("**%s**", profile.Ingress()),
		fmt.Sprintf("**%s**", profile.StartSlice()),
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("AGENTS.md does not state %s; update the timeout tier table to match internal/timeouts.", want)
		}
	}
}

// TestPackageDocStatesTheLiveProductionTiers pins the same three values in this
// package's own doc comment, which is what a reader of the code sees first.
func TestPackageDocStatesTheLiveProductionTiers(t *testing.T) {
	t.Parallel()
	profile := ProductionProfile()
	body, err := os.ReadFile("profile.go")
	if err != nil {
		t.Fatalf("read profile.go: %v", err)
	}
	doc, _, found := strings.Cut(string(body), "\npackage timeouts")
	if !found {
		t.Fatal("profile.go has no package clause")
	}
	for _, want := range []string{profile.SQLiteBusy().String(), profile.Ingress().String(), profile.StartSlice().String()} {
		if !strings.Contains(doc, want) {
			t.Errorf("the package doc does not state %s; update the tier table in profile.go to match ProductionProfile.", want)
		}
	}
}
