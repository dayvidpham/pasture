package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	hostcodex "github.com/dayvidpham/pasture/internal/install/host/codex"
	"github.com/dayvidpham/pasture/internal/install/registry"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

type layoutFixture struct {
	Contract        string `json:"contract"`
	RuntimeContract string `json:"runtime_contract"`
	Components      []struct {
		Extension      string   `json:"extension"`
		Package        string   `json:"package"`
		Prefix         string   `json:"prefix"`
		DefaultEnabled bool     `json:"default_enabled"`
		PublicFiles    []string `json:"public_files,omitempty"`
	} `json:"components"`
}

func TestContractMatchesReviewedGlobalLayout(t *testing.T) {
	t.Parallel()
	fixtureBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "install", "global", "codex", "layout.json"))
	if err != nil {
		t.Fatalf("read Codex layout fixture: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(fixtureBytes)))
	decoder.DisallowUnknownFields()
	var fixture layoutFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode Codex layout fixture: %v", err)
	}
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	contract, err := hostcodex.NewActivationContract(descriptor, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if contract.ID().String() != fixture.Contract {
		t.Fatalf("contract fixture mismatch: got %s / %s", contract.ID(), descriptor.RuntimeContractID())
	}
	if descriptor.RuntimeContractID().String() != fixture.RuntimeContract || len(fixture.Components) != 3 {
		t.Fatalf("runtime/layout fixture mismatch: %s", descriptor.RuntimeContractID())
	}
	for index, component := range descriptor.Components() {
		row := fixture.Components[index]
		if component.Extension().String() != row.Extension || component.DefaultEnabled() != row.DefaultEnabled {
			t.Fatalf("component %d does not match fixture: %+v", index, row)
		}
	}
}

func TestEachCellInstallsAndRemovesWithSiblingsAbsent(t *testing.T) {
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range descriptor.Components() {
		component := component
		t.Run(component.Extension().String(), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			unrelated := filepath.Join(home, "keep.txt")
			if err := os.WriteFile(unrelated, []byte("external"), 0o600); err != nil {
				t.Fatal(err)
			}
			contract, err := hostcodex.NewActivationContract(descriptor, home)
			if err != nil {
				t.Fatal(err)
			}
			coordinate, _ := cell.New(artifact.HarnessCodex, component.Extension())
			lookup, _ := activation.NewComponentDescriptor(coordinate)
			bound, err := activation.LookupComponentActivation(contract, lookup)
			if err != nil {
				t.Fatal(err)
			}
			key, _ := registry.GlobalKey(coordinate)
			controller := hostcodex.NewController()
			out, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
			if err != nil {
				t.Fatalf("Ensure(%s) failed: %v", coordinate, err)
			}
			if out.Record == nil || out.Observation != registry.ObservationInstalled {
				t.Fatalf("Ensure(%s) returned no installed fact: %+v", coordinate, out)
			}
			if component.Extension() == artifact.ExtensionHooks {
				if out.Status != apply.InstalledPendingTrust() || out.Record.Trust() != registry.TrustPending || !strings.Contains(out.Diagnostic, "native hooks interface") {
					t.Fatalf("hooks did not report typed pending trust: %+v", out)
				}
			} else if out.Status != apply.Completed() || out.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("non-hook component reported wrong status/trust: %+v", out)
			}
			for _, sibling := range descriptor.Components() {
				if sibling.Extension() == component.Extension() {
					continue
				}
				for _, entry := range sibling.Bundle().Manifest().Entries() {
					if entry.IsRegular() {
						if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(entry.Path().String()))); !os.IsNotExist(err) {
							t.Fatalf("installing %s materialized sibling %s path %q", component.Extension(), sibling.Extension(), entry.Path())
						}
					}
				}
			}
			removed, err := controller.Remove(context.Background(), apply.InstallerSource(), key, bound, *out.Record)
			if err != nil {
				t.Fatalf("Remove(%s) failed: %v", coordinate, err)
			}
			if removed.Observation != registry.ObservationAbsent || removed.Record == nil || removed.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("Remove(%s) returned wrong fact: %+v", coordinate, removed)
			}
			if content, err := os.ReadFile(unrelated); err != nil || string(content) != "external" {
				t.Fatalf("unrelated sibling changed: content=%q err=%v", content, err)
			}
		})
	}
}

func TestEachCellInspectsAndUpdatesExactOwnedLeaf(t *testing.T) {
	for _, extension := range []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks} {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			name := map[artifact.Extension]string{
				artifact.ExtensionSkills: ".agents/skills/example/SKILL.md",
				artifact.ExtensionAgents: ".codex/agents/pasture-example.toml",
				artifact.ExtensionHooks:  ".codex/hooks/events/SessionStart.sh",
			}[extension]
			coordinate, _ := cell.New(artifact.HarnessCodex, extension)
			key, _ := registry.GlobalKey(coordinate)
			controller := hostcodex.NewController()
			first := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{name: {content: "first", mode: 0o644}}))
			installed, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, first, nil)
			if err != nil {
				t.Fatal(err)
			}
			inspected, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, first, installed.Record)
			if err != nil || inspected.Observation != registry.ObservationInstalled || inspected.Record == nil || !inspected.Record.Managed() {
				t.Fatalf("Inspect returned wrong managed fact: out=%+v err=%v", inspected, err)
			}
			second := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{name: {content: "second", mode: 0o644}}))
			updated, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, second, inspected.Record)
			if err != nil || updated.Record == nil || updated.Record.ArtifactID() == inspected.Record.ArtifactID() {
				t.Fatalf("update did not produce a new exact artifact fact: out=%+v err=%v", updated, err)
			}
			if got, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name))); err != nil || string(got) != "second" {
				t.Fatalf("updated leaf = %q, %v; want second", got, err)
			}
		})
	}
}

func TestGlobalControllerRejectsProjectAndMismatchedKeys(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionAgents)
	bound := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{".codex/agents/pasture-worker.toml": {content: "name='worker'", mode: 0o644}}))
	root, err := registry.CanonicalProjectRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectKey, _ := registry.ProjectKey(root, coordinate)
	controller := hostcodex.NewController()
	if _, err := controller.Ensure(context.Background(), apply.InstallerSource(), projectKey, bound, nil); err == nil {
		t.Fatal("global controller accepted a project-scoped key")
	}
	skills, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionSkills)
	mismatch, _ := registry.GlobalKey(skills)
	if _, err := controller.Ensure(context.Background(), apply.InstallerSource(), mismatch, bound, nil); err == nil {
		t.Fatal("global controller accepted a mismatched scoped key")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("scope rejection mutated home: entries=%v err=%v", entries, err)
	}
}

func TestControllerStopsOnPartialConflictAndPreservesFacts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	bundle := bundleFor(t, map[string]fileSpec{
		".agents/skills/alpha/SKILL.md": {content: "alpha", mode: 0o644},
		".agents/skills/zeta/SKILL.md":  {content: "desired", mode: 0o644},
	})
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionSkills)
	strategy, _ := activation.NewDirectFile(bundle, home)
	bound, _ := activation.NewComponentActivation(coordinate, strategy)
	conflict := filepath.Join(home, ".agents", "skills", "zeta", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, _ := registry.GlobalKey(coordinate)
	out, err := hostcodex.NewController().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
	if err == nil {
		t.Fatal("Ensure accepted an external conflicting leaf")
	}
	if out.Observation != registry.ObservationUnknown || out.Record == nil || out.Record.LastOutcome() != registry.OutcomeFailed {
		t.Fatalf("partial failure did not retain an unknown failed fact: %+v", out)
	}
	if got, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md")); err != nil || string(got) != "alpha" {
		t.Fatalf("verified earlier leaf missing after partial failure: %q, %v", got, err)
	}
	if got, err := os.ReadFile(conflict); err != nil || string(got) != "external" {
		t.Fatalf("conflicting external leaf changed: %q, %v", got, err)
	}
}

func TestControllerRejectsSymlinkRootWithoutMutation(t *testing.T) {
	t.Parallel()
	realHome := t.TempDir()
	parent := t.TempDir()
	linkedHome := filepath.Join(parent, "home")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatal(err)
	}
	bundle := bundleFor(t, map[string]fileSpec{".codex/agents/pasture-worker.toml": {content: "name='worker'", mode: 0o644}})
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionAgents)
	strategy, _ := activation.NewDirectFile(bundle, linkedHome)
	bound, _ := activation.NewComponentActivation(coordinate, strategy)
	key, _ := registry.GlobalKey(coordinate)
	if _, err := hostcodex.NewController().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
		t.Fatal("Ensure followed a symlinked home root")
	}
	if entries, err := os.ReadDir(realHome); err != nil || len(entries) != 0 {
		t.Fatalf("symlink rejection mutated real target: entries=%v err=%v", entries, err)
	}
}

type fileSpec struct {
	content string
	mode    uint32
}

func bundleFor(t *testing.T, files map[string]fileSpec) artifact.Bundle {
	t.Helper()
	fsys := fstest.MapFS{}
	entries := make([]artifact.Entry, 0, len(files))
	for name, spec := range files {
		path, err := artifact.NewPath(name)
		if err != nil {
			t.Fatal(err)
		}
		mode, err := artifact.NewMode(spec.mode)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := artifact.NewFileEntry(path, mode, artifact.DigestBytes([]byte(spec.content)))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
		fsys[name] = &fstest.MapFile{Data: []byte(spec.content), Mode: os.FileMode(spec.mode)}
	}
	manifest, err := artifact.NewManifest(entries...)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := artifact.NewBundle(fsys, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func activationFor(t *testing.T, coordinate cell.Cell, home string, bundle artifact.Bundle) activation.ComponentActivation {
	t.Helper()
	strategy, err := activation.NewDirectFile(bundle, home)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := activation.NewComponentActivation(coordinate, strategy)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
