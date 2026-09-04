package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/host/claudecode"
	"github.com/dayvidpham/pasture/internal/install/host/codex"
	"github.com/dayvidpham/pasture/internal/install/host/opencode"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
	targetclaude "github.com/dayvidpham/pasture/internal/target/claudecode"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

// isolatedInstallService composes the real installer graph rooted at an
// isolated temporary home and state file, so verb tests exercise production
// wiring without touching the operator's real home or trust state.
func isolatedInstallService(t *testing.T, home string) *service.Service {
	t.Helper()
	statePath := filepath.Join(home, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	registryFile, err := service.NewFileRegistry(statePath)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := composeInstallService(installComposition{
		Home: filepath.Clean(home), StatePath: statePath, Registry: registryFile,
		ClaudeRunner: &recordingRunner{}, ClaudeManifests: emptyManifestReader{},
		NewClaudeController:   claudecode.NewController,
		NewOpenCodeController: opencode.New,
		ClaudeDescriptor:      targetclaude.Descriptor,
		CodexDescriptor:       targetcodex.Descriptor,
		NewCodexContract:      codex.NewActivationContract,
		NewCodexPolicies:      codex.NewDirectFilePolicies,
	})
	if err != nil {
		t.Fatalf("compose isolated installer service: %v", err)
	}
	return svc
}

func runVerb(t *testing.T, makeService installServiceFactory, newCmd func(installServiceFactory) *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd := newCmd(makeService)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestParseHarnessAlias(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want ir.HarnessID
		ok   bool
	}{
		{"claude", ir.HarnessClaudeCode, true},
		{"claude-code", ir.HarnessClaudeCode, true},
		{"opencode", ir.HarnessOpenCode, true},
		{"codex", ir.HarnessCodex, true},
		{"Claude", "", false},
		{"", "", false},
		{"nope", "", false},
	} {
		got, err := parseHarnessAlias(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("parseHarnessAlias(%q) = %q,%v; want %q,nil", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseHarnessAlias(%q) accepted an invalid harness", tc.in)
		}
	}
}

func TestResolveInstallCells(t *testing.T) {
	t.Parallel()

	skills, _ := cell.New(ir.HarnessOpenCode, cell.SkillsAxis())
	agents, _ := cell.New(ir.HarnessOpenCode, cell.AgentsAxis())

	t.Run("bare is ambiguous", func(t *testing.T) {
		cells, needHelp, err := resolveInstallCells(nil)
		if err != nil || !needHelp || cells != nil {
			t.Fatalf("resolveInstallCells(nil) = %v,%v,%v", cells, needHelp, err)
		}
	})
	t.Run("lone harness is ambiguous", func(t *testing.T) {
		cells, needHelp, err := resolveInstallCells([]string{"opencode"})
		if err != nil || !needHelp || cells != nil {
			t.Fatalf("lone harness = %v,%v,%v", cells, needHelp, err)
		}
	})
	t.Run("named cells resolve in canonical order", func(t *testing.T) {
		cells, needHelp, err := resolveInstallCells([]string{"opencode", "agents", "skills"})
		if err != nil || needHelp {
			t.Fatalf("named cells = %v,%v,%v", cells, needHelp, err)
		}
		if len(cells) != 2 || cells[0] != skills || cells[1] != agents {
			t.Fatalf("cells not canonicalized: %v", cells)
		}
	})
	t.Run("alias resolves", func(t *testing.T) {
		cells, _, err := resolveInstallCells([]string{"claude", "skills"})
		if err != nil || len(cells) != 1 || cells[0].Harness() != ir.HarnessClaudeCode {
			t.Fatalf("claude alias = %v,%v", cells, err)
		}
	})
	t.Run("duplicate extension rejected", func(t *testing.T) {
		if _, _, err := resolveInstallCells([]string{"opencode", "skills", "skills"}); err == nil {
			t.Fatal("duplicate extension was not rejected")
		}
	})
	t.Run("invalid harness rejected", func(t *testing.T) {
		if _, _, err := resolveInstallCells([]string{"nope", "skills"}); err == nil {
			t.Fatal("invalid harness was not rejected")
		}
	})
	t.Run("invalid extension rejected", func(t *testing.T) {
		if _, _, err := resolveInstallCells([]string{"opencode", "bogus"}); err == nil {
			t.Fatal("invalid extension was not rejected")
		}
	})
}

func TestInstallVerb_BareAndLoneHarnessPrintHelp(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	factory := func() (*service.Service, error) { return isolatedInstallService(t, home), nil }
	for _, args := range [][]string{{}, {"opencode"}} {
		out, err := runVerb(t, factory, newInstallVerbCommand, args...)
		if err != nil {
			t.Fatalf("install %v returned error: %v", args, err)
		}
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, "install [harness] [extension...]") {
			t.Fatalf("install %v did not print help:\n%s", args, out)
		}
	}
}

func TestInstallVerb_AdditiveMultiCellLeavesSiblingUntouched(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	factory := func() (*service.Service, error) { return isolatedInstallService(t, home), nil }

	out, err := runVerb(t, factory, newInstallVerbCommand, "opencode", "skills", "agents", "--json")
	if err != nil {
		t.Fatalf("install opencode skills agents: %v\n%s", err, out)
	}
	if !strings.Contains(out, "opencode.skills") || !strings.Contains(out, "opencode.agents") {
		t.Fatalf("install did not report both cells:\n%s", out)
	}
	if strings.Contains(out, "opencode.hooks") {
		t.Fatalf("install touched the unnamed hooks sibling:\n%s", out)
	}

	// Uninstall only skills; agents must remain installed, hooks never present.
	out, err = runVerb(t, factory, newUninstallVerbCommand, "opencode", "skills", "--json")
	if err != nil {
		t.Fatalf("uninstall opencode skills: %v\n%s", err, out)
	}
	if !strings.Contains(out, "opencode.skills") || strings.Contains(out, "opencode.agents") {
		t.Fatalf("uninstall was not scoped to skills only:\n%s", out)
	}

	// Confirm persisted state: skills absent, agents still installed.
	statePath := filepath.Join(home, "state", "installations.yaml")
	store, loadErr := registry.Load(statePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	skills, _ := cell.New(ir.HarnessOpenCode, cell.SkillsAxis())
	agents, _ := cell.New(ir.HarnessOpenCode, cell.AgentsAxis())
	if got := lookupGlobal(t, store, skills).Observation().String(); got != "absent" {
		t.Fatalf("skills should be absent after uninstall, got %s", got)
	}
	if got := lookupGlobal(t, store, agents).Observation().String(); got != "installed" {
		t.Fatalf("agents sibling should remain installed, got %s", got)
	}
}

func TestInstallVerb_AttemptAllReportsEveryCellOnFailure(t *testing.T) {
	t.Parallel()

	// A failing registry makes every ApplyCell fail; attempt-all must still
	// report one failed row per named cell rather than stopping at the first.
	svc, err := service.New(service.Config{Registry: failingRegistry{}})
	if err != nil {
		t.Fatal(err)
	}
	factory := func() (*service.Service, error) { return svc, nil }
	out, err := runVerb(t, factory, newInstallVerbCommand, "opencode", "skills", "agents")
	if err == nil {
		t.Fatalf("expected a nonzero failure when cells fail:\n%s", out)
	}
	if !strings.Contains(out, "opencode.skills") || !strings.Contains(out, "opencode.agents") {
		t.Fatalf("attempt-all did not report every cell after failure:\n%s", out)
	}
	if strings.Count(out, "failed") < 2 {
		t.Fatalf("both failing cells should be reported, got:\n%s", out)
	}
}

func lookupGlobal(t *testing.T, store registry.Store, c cell.Cell) registry.Record {
	t.Helper()
	key, err := registry.GlobalKey(c)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := store.Lookup(key)
	if !ok {
		t.Fatalf("no persisted record for %s", c)
	}
	return rec
}
