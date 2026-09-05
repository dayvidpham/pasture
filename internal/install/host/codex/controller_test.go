package codex_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	hostcodex "github.com/dayvidpham/pasture/internal/install/host/codex"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
	"github.com/dayvidpham/pasture/internal/testutil"
)

type layoutFixture struct {
	Components []struct {
		Extension      string   `json:"extension"`
		Package        string   `json:"package"`
		Prefix         string   `json:"prefix"`
		DefaultEnabled bool     `json:"default_enabled"`
		PublicFiles    []string `json:"public_files,omitempty"`
	} `json:"components"`
}

type codexExecutor struct {
	direct *apply.DirectFileActivator
}

func newCodexExecutor(t *testing.T, descriptor targetcodex.TargetDescriptor, home string) codexExecutor {
	t.Helper()
	policies, err := hostcodex.NewDirectFilePolicies(descriptor, home)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := apply.NewDirectFileActivator(policies[:]...)
	if err != nil {
		t.Fatal(err)
	}
	return codexExecutor{direct: direct}
}

func (e codexExecutor) Inspect(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return e.direct.Inspect(ctx, source, key, act, prior)
}

func (e codexExecutor) Ensure(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return e.direct.Ensure(ctx, source, key, act, prior)
}

func (e codexExecutor) Remove(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	return e.direct.Remove(ctx, source, key, act, prior)
}

func TestCodexExportsPoliciesRatherThanAnActivator(t *testing.T) {
	t.Parallel()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	policies, err := hostcodex.NewDirectFilePolicies(descriptor, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for index, policy := range policies {
		if !policy.Cell().IsValid() || policy.Cell().Harness() != artifact.HarnessCodex {
			t.Fatalf("policy %d is invalid or foreign: %s", index, policy.Cell())
		}
	}
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
	// The ids and the admission are read from the Codex runtime contract, the
	// one root, never restated in the fixture.
	root := runtime.Codex0_153_0()
	if contract.ID().String() != hostcodex.ActivationContractID() || contract.ID().String() != "codex/global@"+root.Versions().Min().String() {
		t.Fatalf("contract id %s is not derived from the Codex runtime contract version %s", contract.ID(), root.Versions().Min())
	}
	if descriptor.RuntimeContractID() != root.ID() || len(fixture.Components) != 3 {
		t.Fatalf("runtime/layout fixture mismatch: %s", descriptor.RuntimeContractID())
	}
	if contract.HostVersions().HasUpperBound() || contract.HostVersions().Min().String() != root.Versions().Min().String() {
		t.Fatalf("installer admission %q is not the runtime contract's floor %q", contract.HostVersions().Describe(), root.Versions().Describe())
	}
	for index, component := range descriptor.Components() {
		row := fixture.Components[index]
		layout := component.Layout()
		if component.Extension().String() != row.Extension || component.PackageID().String() != row.Package || component.DefaultEnabled() != row.DefaultEnabled || layout.Extension() != component.Extension() || layout.Prefix() != row.Prefix {
			t.Fatalf("component %d does not match fixture: %+v", index, row)
		}
		if strings.Join(layout.PublicFiles(), "\x00") != strings.Join(row.PublicFiles, "\x00") {
			t.Fatalf("component %s public files = %v, fixture = %v", component.Extension(), layout.PublicFiles(), row.PublicFiles)
		}
		for _, entry := range component.Bundle().Manifest().Entries() {
			if !entry.IsRegular() {
				continue
			}
			allowed := strings.HasPrefix(entry.Path().String(), row.Prefix)
			for _, exact := range row.PublicFiles {
				allowed = allowed || entry.Path().String() == exact
			}
			if !allowed {
				t.Fatalf("fixture does not authorize %s path %q", component.Extension(), entry.Path())
			}
		}
	}
}

func TestInstallerCompatibilityRangeBoundaries(t *testing.T) {
	t.Parallel()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	contract, err := hostcodex.NewActivationContract(descriptor, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The admission is a floor at the recorded version: derive every probe
	// from it so the boundary test follows the contract when it moves.
	min := contract.HostVersions().Min()
	cases := map[string]bool{
		testutil.BelowFloor(t, min).String():    false,
		min.String():                            true,
		testutil.Bump(t, min, 0, 0, 1).String(): true,
		testutil.Bump(t, min, 0, 1, 0).String(): true,
		testutil.Bump(t, min, 1, 0, 0).String(): true,
		min.String() + "-rc.1":                  false,
		min.String() + "+build.7":               true,
	}
	for text, want := range cases {
		version, err := runtime.ParseHostVersion(text)
		if err != nil {
			t.Fatalf("ParseHostVersion(%q): %v", text, err)
		}
		if got := contract.HostVersions().Allows(version); got != want {
			t.Fatalf("HostVersions().Allows(%q) = %v, want %v", text, got, want)
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
			sentinels := map[string][]byte{
				"keep.txt":                  []byte("external"),
				".agents/unrelated.txt":     []byte("agents-external"),
				".codex/unrelated.toml":     []byte("codex-external"),
				".codex/private-trust.json": []byte("private-trust-sentinel"),
				".git/config":               []byte("git-state-sentinel"),
			}
			for name, content := range sentinels {
				path := filepath.Join(home, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
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
			controller := newCodexExecutor(t, descriptor, home)
			out, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
			if err != nil {
				t.Fatalf("Ensure(%s) failed: %v", coordinate, err)
			}
			if out.Record == nil || out.Observation != registry.ObservationInstalled {
				t.Fatalf("Ensure(%s) returned no installed fact: %+v", coordinate, out)
			}
			if out.Record.LastOperation() != registry.OperationEnsure || out.Record.LastOutcome() != registry.OutcomeCompleted {
				t.Fatalf("Ensure(%s) record operation/outcome = %s/%s", coordinate, out.Record.LastOperation(), out.Record.LastOutcome())
			}
			if component.Extension() == artifact.ExtensionHooks {
				if out.Status != apply.InstalledPendingTrust() || out.Record.Trust() != registry.TrustPending || !strings.Contains(out.Diagnostic, "native hooks interface") {
					t.Fatalf("hooks did not report typed pending trust: %+v", out)
				}
			} else if out.Status != apply.Completed() || out.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("non-hook component reported wrong status/trust: %+v", out)
			}
			installedSnapshot := snapshotTree(t, home)
			repeated, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, bound, out.Record)
			if err != nil || repeated.Record == nil || repeated.Record.ArtifactID() != out.Record.ArtifactID() || !repeated.Record.Managed() {
				t.Fatalf("idempotent Ensure(%s) changed ownership or failed: out=%+v err=%v", coordinate, repeated, err)
			}
			assertCodexTrust(t, component.Extension(), repeated, registry.ObservationInstalled)
			if repeated.Status != out.Status || repeated.Record.LastOperation() != registry.OperationEnsure || repeated.Record.LastOutcome() != registry.OutcomeCompleted {
				t.Fatalf("repeated Ensure(%s) status/record drifted: %+v", coordinate, repeated)
			}
			if got := snapshotTree(t, home); got != installedSnapshot {
				t.Fatalf("repeated Ensure(%s) changed the filesystem", coordinate)
			}
			status, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, bound, repeated.Record)
			if err != nil || status.Record == nil || status.Observation != registry.ObservationInstalled || !status.Record.Managed() {
				t.Fatalf("post-ensure Inspect(%s) returned wrong fact: out=%+v err=%v", coordinate, status, err)
			}
			assertCodexTrust(t, component.Extension(), status, registry.ObservationInstalled)
			if status.Status != out.Status || status.Record.LastOperation() != registry.OperationInspect || status.Record.LastOutcome() != registry.OutcomeCompleted {
				t.Fatalf("Inspect(%s) status/record = %s/%s/%s", coordinate, status.Status, status.Record.LastOperation(), status.Record.LastOutcome())
			}
			if got := snapshotTree(t, home); got != installedSnapshot {
				t.Fatalf("Inspect(%s) changed the filesystem", coordinate)
			}
			assertInstalledRegularFiles(t, home, component.Bundle())
			assertRecordMatchesBundle(t, *status.Record, component.Bundle())
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
			removed, err := controller.Remove(context.Background(), apply.InstallerSource(), key, bound, *status.Record)
			if err != nil {
				t.Fatalf("Remove(%s) failed: %v", coordinate, err)
			}
			if removed.Observation != registry.ObservationAbsent || removed.Record == nil || removed.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("Remove(%s) returned wrong fact: %+v", coordinate, removed)
			}
			if removed.Status != apply.Completed() || removed.Record.LastOperation() != registry.OperationRemove || removed.Record.LastOutcome() != registry.OutcomeCompleted {
				t.Fatalf("Remove(%s) status/record = %s/%s/%s", coordinate, removed.Status, removed.Record.LastOperation(), removed.Record.LastOutcome())
			}
			removedSnapshot := snapshotTree(t, home)
			postRemove, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, bound, removed.Record)
			if err != nil || postRemove.Record == nil || postRemove.Observation != registry.ObservationAbsent {
				t.Fatalf("post-remove Inspect(%s) returned wrong fact: out=%+v err=%v", coordinate, postRemove, err)
			}
			if postRemove.Status != apply.Completed() || postRemove.Record.LastOperation() != registry.OperationInspect || postRemove.Record.LastOutcome() != registry.OutcomeCompleted || postRemove.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("absent Inspect(%s) status/record drifted: %+v", coordinate, postRemove)
			}
			if got := snapshotTree(t, home); got != removedSnapshot {
				t.Fatalf("absent Inspect(%s) changed the filesystem", coordinate)
			}
			repeatedRemove, err := controller.Remove(context.Background(), apply.InstallerSource(), key, bound, *removed.Record)
			if err != nil || repeatedRemove.Observation != registry.ObservationAbsent || repeatedRemove.Status != apply.Completed() || repeatedRemove.Record == nil || repeatedRemove.Record.LastOperation() != registry.OperationRemove || repeatedRemove.Record.LastOutcome() != registry.OutcomeCompleted || repeatedRemove.Record.Trust() != registry.TrustNotApplicable {
				t.Fatalf("repeated Remove(%s) did not converge: out=%+v err=%v", coordinate, repeatedRemove, err)
			}
			if got := snapshotTree(t, home); got != removedSnapshot {
				t.Fatalf("repeated Remove(%s) changed the filesystem", coordinate)
			}
			for _, entry := range component.Bundle().Manifest().Entries() {
				if entry.IsRegular() {
					if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(entry.Path().String()))); !os.IsNotExist(err) {
						t.Fatalf("removed %s left artifact %q", component.Extension(), entry.Path())
					}
				}
			}
			for name, want := range sentinels {
				content, readErr := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
				if readErr != nil || !bytes.Equal(content, want) {
					t.Fatalf("sentinel %q changed: content=%q err=%v", name, content, readErr)
				}
			}
		})
	}
}

func TestExactExternalCellRemainsUnmanaged(t *testing.T) {
	paths := map[artifact.Extension]string{
		artifact.ExtensionSkills: ".agents/skills/external/SKILL.md",
		artifact.ExtensionAgents: ".codex/agents/pasture-external.toml",
		artifact.ExtensionHooks:  ".codex/hooks/events/SessionStart.sh",
	}
	for extension, name := range paths {
		extension, name := extension, name
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			bundle := bundleFor(t, map[string]fileSpec{name: {content: "external", mode: 0o644}})
			destination := filepath.Join(home, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(destination, []byte("external"), 0o644); err != nil {
				t.Fatal(err)
			}
			coordinate, _ := cell.New(artifact.HarnessCodex, extension)
			key, _ := registry.GlobalKey(coordinate)
			bound := activationFor(t, coordinate, home, bundle)
			testTarget := descriptorWithBundle(t, extension, bundle)
			out, err := newCodexExecutor(t, testTarget, home).Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
			if err != nil || out.Record == nil || out.Record.Managed() || out.Record.Observation() != registry.ObservationInstalled {
				t.Fatalf("exact external cell was adopted or rejected: out=%+v err=%v", out, err)
			}
			assertCodexTrust(t, extension, out, registry.ObservationInstalled)
			if got, err := os.ReadFile(destination); err != nil || string(got) != "external" {
				t.Fatalf("exact external cell changed: %q err=%v", got, err)
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
			first := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{name: {content: "first", mode: 0o644}}))
			controller := newCodexExecutor(t, descriptorWithBundle(t, extension, first.Strategy().(activation.DirectFile).Bundle()), home)
			installed, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, first, nil)
			if err != nil {
				t.Fatal(err)
			}
			inspected, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, first, installed.Record)
			if err != nil || inspected.Observation != registry.ObservationInstalled || inspected.Record == nil || !inspected.Record.Managed() {
				t.Fatalf("Inspect returned wrong managed fact: out=%+v err=%v", inspected, err)
			}
			assertCodexTrust(t, extension, inspected, registry.ObservationInstalled)
			second := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{name: {content: "second", mode: 0o644}}))
			updated, err := newCodexExecutor(t, descriptorWithBundle(t, extension, second.Strategy().(activation.DirectFile).Bundle()), home).Ensure(context.Background(), apply.InstallerSource(), key, second, inspected.Record)
			if err != nil || updated.Record == nil || updated.Record.ArtifactID() == inspected.Record.ArtifactID() {
				t.Fatalf("update did not produce a new exact artifact fact: out=%+v err=%v", updated, err)
			}
			assertCodexTrust(t, extension, updated, registry.ObservationInstalled)
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
	controller := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionAgents, bound.Strategy().(activation.DirectFile).Bundle()), home)
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

func TestPoliciesRejectForeignBundleAndDestinationBeforeFilesystemAccess(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	executor := newCodexExecutor(t, descriptor, home)
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionAgents)
	key, _ := registry.GlobalKey(coordinate)
	component := descriptor.Agents()
	foreignBundle := bundleFor(t, map[string]fileSpec{".codex/agents/pasture-foreign.toml": {content: "foreign", mode: 0o644}})
	for name, binding := range map[string]activation.ComponentActivation{
		"bundle":      activationFor(t, coordinate, home, foreignBundle),
		"destination": activationFor(t, coordinate, t.TempDir(), component.Bundle()),
	} {
		before := snapshotTree(t, home)
		if _, err := executor.Ensure(context.Background(), apply.InstallerSource(), key, binding, nil); err == nil {
			t.Fatalf("policy accepted foreign %s", name)
		}
		if got := snapshotTree(t, home); got != before {
			t.Fatalf("foreign %s rejection changed the reviewed home", name)
		}
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
	executor := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionSkills, bundle), home)
	out, err := executor.Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
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
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	repaired, err := executor.Ensure(context.Background(), apply.InstallerSource(), key, bound, out.Record)
	if err != nil || repaired.Record == nil || repaired.Observation != registry.ObservationInstalled || !repaired.Record.Managed() {
		t.Fatalf("ordinary retry did not repair partial conflict: out=%+v err=%v", repaired, err)
	}
	assertInstalledRegularFiles(t, home, bundle)
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
	if _, err := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionAgents, bundle), linkedHome).Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
		t.Fatal("Ensure followed a symlinked home root")
	}
	if entries, err := os.ReadDir(realHome); err != nil || len(entries) != 0 {
		t.Fatalf("symlink rejection mutated real target: entries=%v err=%v", entries, err)
	}
}

func TestControllerRejectsIntermediateAndLeafSymlinks(t *testing.T) {
	t.Parallel()
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionAgents)
	key, _ := registry.GlobalKey(coordinate)
	bundle := bundleFor(t, map[string]fileSpec{".codex/agents/pasture-worker.toml": {content: "desired", mode: 0o644}})

	t.Run("intermediate", func(t *testing.T) {
		home := t.TempDir()
		external := t.TempDir()
		if err := os.Symlink(external, filepath.Join(home, ".codex")); err != nil {
			t.Fatal(err)
		}
		bound := activationFor(t, coordinate, home, bundle)
		if _, err := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionAgents, bundle), home).Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
			t.Fatal("Ensure followed an intermediate symlink")
		}
		if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
			t.Fatalf("intermediate symlink target changed: %v, %v", entries, err)
		}
	})

	t.Run("leaf", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "external")
		if err := os.WriteFile(target, []byte("external"), 0o600); err != nil {
			t.Fatal(err)
		}
		leaf := filepath.Join(home, ".codex", "agents", "pasture-worker.toml")
		if err := os.MkdirAll(filepath.Dir(leaf), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, leaf); err != nil {
			t.Fatal(err)
		}
		bound := activationFor(t, coordinate, home, bundle)
		if _, err := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionAgents, bundle), home).Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
			t.Fatal("Ensure followed a leaf symlink")
		}
		if content, err := os.ReadFile(target); err != nil || string(content) != "external" {
			t.Fatalf("leaf symlink target changed: %q, %v", content, err)
		}
	})
}

func TestManagedDriftIsPreservedAndRejected(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	name := ".agents/skills/example/SKILL.md"
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionSkills)
	key, _ := registry.GlobalKey(coordinate)
	bundle := bundleFor(t, map[string]fileSpec{name: {content: "managed", mode: 0o644}})
	bound := activationFor(t, coordinate, home, bundle)
	executor := newCodexExecutor(t, descriptorWithBundle(t, artifact.ExtensionSkills, bundle), home)
	installed, err := executor.Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, filepath.FromSlash(name))
	if err := os.WriteFile(path, []byte("user-drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Ensure(context.Background(), apply.InstallerSource(), key, bound, installed.Record); err == nil {
		t.Fatal("Ensure overwrote a drifted managed leaf")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "user-drift" {
		t.Fatalf("managed drift changed: %q, %v", content, err)
	}
	if _, err := executor.Remove(context.Background(), apply.InstallerSource(), key, bound, *installed.Record); err == nil {
		t.Fatal("Remove deleted a drifted managed leaf")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "user-drift" {
		t.Fatalf("managed drift changed after remove: %q, %v", content, err)
	}
}

func TestGlobalHookCommandReachesInstalledRunnerFromUnrelatedDirectory(t *testing.T) {
	t.Parallel()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	contract, err := hostcodex.NewActivationContract(descriptor, home)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionHooks)
	lookup, _ := activation.NewComponentDescriptor(coordinate)
	bound, err := activation.LookupComponentActivation(contract, lookup)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := registry.GlobalKey(coordinate)
	if _, err := newCodexExecutor(t, descriptor, home).Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	command := config.Hooks["SessionStart"][0].Hooks[0].Command
	if !strings.HasPrefix(command, "sh ~/.codex/hooks/events/SessionStart.sh") {
		t.Fatalf("SessionStart global command = %q", command)
	}
	marker := filepath.Join(t.TempDir(), "invoked")
	fakePasture := filepath.Join(t.TempDir(), "pasture")
	if err := os.WriteFile(fakePasture, []byte("#!/bin/sh\nprintf '%s' \"$*\" > \"$MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PASTURE_BIN="+fakePasture, "MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("global hook command failed from unrelated cwd: %v: %s", err, output)
	}
	args, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("installed runner did not reach the injected Pasture binary: %v", err)
	}
	if !strings.Contains(string(args), "hook lifecycle --harness codex --event SessionStart --host-version "+runtime.Codex0_153_0().Versions().Min().String()) {
		t.Fatalf("installed global runner forwarded wrong argv: %q", args)
	}
}

func TestProductionServicePersistsCodexPendingTrustThroughCellAndSelection(t *testing.T) {
	for _, entryPoint := range []string{"cell", "selection"} {
		entryPoint := entryPoint
		t.Run(entryPoint, func(t *testing.T) {
			home := t.TempDir()
			svc, repo := newCodexService(t, home)
			applyEnabled := func(enabled bool) apply.Result {
				t.Helper()
				if entryPoint == "selection" {
					result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: codexSelection(t, enabled), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
					if err != nil {
						t.Fatalf("ApplySelection(enabled=%v): %v", enabled, err)
					}
					return result
				}
				var rows []apply.ActionRow
				for _, extension := range []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks} {
					coordinate, _ := cell.New(artifact.HarnessCodex, extension)
					result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: enabled, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
					if err != nil {
						t.Fatalf("ApplyCell(%s, enabled=%v): %v", extension, enabled, err)
					}
					rows = append(rows, result.Rows()...)
				}
				return apply.NewResult(apply.InstallerSource(), registry.ScopeGlobal, true, rows)
			}

			installed := applyEnabled(true)
			assertCodexServiceRows(t, installed.Rows(), apply.Ensure(), registry.ObservationInstalled)
			assertCodexStore(t, repo.snapshot(), registry.ObservationInstalled, registry.OperationEnsure)
			fsInstalled := snapshotTree(t, home)
			storeInstalled := repo.snapshot()

			repeated := applyEnabled(true)
			assertCodexServiceRows(t, repeated.Rows(), apply.Ensure(), registry.ObservationInstalled)
			if got := snapshotTree(t, home); got != fsInstalled {
				t.Fatalf("repeated ensure changed filesystem\nfirst: %s\nagain: %s", fsInstalled, got)
			}
			if got := repo.snapshot(); !reflect.DeepEqual(got.Ordered(), storeInstalled.Ordered()) {
				t.Fatalf("repeated ensure changed exact registry records\nfirst=%+v\nagain=%+v", storeInstalled.Ordered(), got.Ordered())
			}

			removed := applyEnabled(false)
			assertCodexServiceRows(t, removed.Rows(), apply.RemoveOp(), registry.ObservationAbsent)
			assertCodexStore(t, repo.snapshot(), registry.ObservationAbsent, registry.OperationRemove)
		})
	}
}

func TestProductionServiceRejectsProjectCodexBeforeMutation(t *testing.T) {
	for _, entryPoint := range []string{"cell", "selection"} {
		entryPoint := entryPoint
		t.Run(entryPoint, func(t *testing.T) {
			home := t.TempDir()
			svc, repo := newCodexService(t, home)
			root, err := registry.CanonicalProjectRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			scope, err := apply.ProjectScope(root)
			if err != nil {
				t.Fatal(err)
			}
			beforeFS := snapshotTree(t, home)
			beforeStore := repo.snapshot()
			var result apply.Result
			if entryPoint == "selection" {
				result, err = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: codexSelection(t, true), Scope: scope, Source: apply.InstallerSource()})
			} else {
				coordinate, _ := cell.New(artifact.HarnessCodex, artifact.ExtensionHooks)
				result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: scope, Source: apply.InstallerSource()})
			}
			if err != nil || result.OK() || len(result.Rows()) == 0 || result.Rows()[0].Status() != apply.Failed() {
				t.Fatalf("%s accepted project-scoped global Codex activation", entryPoint)
			}
			if got := snapshotTree(t, home); got != beforeFS {
				t.Fatalf("%s project rejection changed filesystem: before=%s after=%s", entryPoint, beforeFS, got)
			}
			if got := repo.snapshot(); !reflect.DeepEqual(got.Ordered(), beforeStore.Ordered()) {
				t.Fatalf("%s project rejection changed registry: before=%+v after=%+v", entryPoint, beforeStore.Ordered(), got.Ordered())
			}
		})
	}
}

type memoryRegistry struct {
	mu    sync.Mutex
	store registry.Store
}

func (r *memoryRegistry) Load(context.Context) (registry.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneStore(r.store), nil
}

func (r *memoryRegistry) Save(_ context.Context, store registry.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = cloneStore(store)
	return nil
}

func (r *memoryRegistry) snapshot() registry.Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneStore(r.store)
}

func cloneStore(source registry.Store) registry.Store {
	copy := registry.New()
	for _, record := range source.Ordered() {
		_ = copy.Upsert(record)
	}
	return copy
}

func newCodexService(t *testing.T, home string) (*service.Service, *memoryRegistry) {
	t.Helper()
	descriptor, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	codexContract, err := hostcodex.NewActivationContract(descriptor, home)
	if err != nil {
		t.Fatal(err)
	}
	codexPolicies, err := hostcodex.NewDirectFilePolicies(descriptor, home)
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessCodex: codexContract}
	policies := append([]apply.DirectFilePolicy(nil), codexPolicies[:]...)
	for _, harness := range []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode} {
		contract, additional := stubDirectFileContract(t, harness, t.TempDir())
		contracts[harness] = contract
		policies = append(policies, additional[:]...)
	}
	direct, err := apply.NewDirectFileActivator(policies...)
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryRegistry{store: registry.New()}
	svc, err := service.New(service.Config{Registry: repo, Contracts: contracts, Activators: []apply.Activator{direct}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo
}

func stubDirectFileContract(t *testing.T, harness ir.HarnessID, root string) (activation.ActivationContract, [3]apply.DirectFilePolicy) {
	t.Helper()
	var bindings [3]activation.ComponentActivation
	var policies [3]apply.DirectFilePolicy
	for index, extension := range []artifact.Extension{artifact.ExtensionSkills, artifact.ExtensionAgents, artifact.ExtensionHooks} {
		coordinate, err := cell.New(harness, extension)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.ToSlash(filepath.Join("stub", string(harness), extension.String()+".txt"))
		bundle := bundleFor(t, map[string]fileSpec{name: {content: "stub", mode: 0o644}})
		strategy, _ := activation.NewDirectFile(bundle, root)
		bindings[index], err = activation.NewComponentActivation(coordinate, strategy)
		if err != nil {
			t.Fatal(err)
		}
		policies[index], err = apply.PassThroughDirectFile(coordinate)
		if err != nil {
			t.Fatal(err)
		}
	}
	exhaustive, err := activation.NewExhaustiveComponentActivations(bindings[0], bindings[1], bindings[2])
	if err != nil {
		t.Fatal(err)
	}
	id, _ := activation.NewActivationContractID(string(harness) + "/test@0.153.0")
	probe, _ := activation.NewCommandSchema("true", "--version")
	version, _ := runtime.ParseHostVersion("0.153.0")
	versions, _ := runtime.NewVersionConstraint(version, version, false)
	contract, err := activation.NewActivationContract(id, harness, versions, probe, exhaustive)
	if err != nil {
		t.Fatal(err)
	}
	return contract, policies
}

func codexSelection(t *testing.T, enabled bool) selection.Selection {
	t.Helper()
	states := make(map[cell.Cell]bool, len(cell.CanonicalCells()))
	for _, coordinate := range cell.CanonicalCells() {
		states[coordinate] = coordinate.Harness() == artifact.HarnessCodex && enabled
	}
	selected, err := selection.New(states)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

func assertCodexServiceRows(t *testing.T, rows []apply.ActionRow, operation apply.Operation, observation registry.Observation) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("Codex service rows = %d, want exactly 3: %+v", len(rows), rows)
	}
	for _, row := range rows {
		wantStatus := apply.Completed()
		if row.Cell().Extension() == artifact.ExtensionHooks && observation == registry.ObservationInstalled {
			wantStatus = apply.InstalledPendingTrust()
		}
		if row.Cell().Harness() != artifact.HarnessCodex || row.Operation() != operation || row.Status() != wantStatus || row.Management() != apply.ManagementPasture || row.Observation() != observation {
			t.Fatalf("unexpected Codex service row: cell=%s operation=%s status=%s management=%s observation=%s", row.Cell(), row.Operation(), row.Status(), row.Management(), row.Observation())
		}
	}
}

func assertCodexStore(t *testing.T, store registry.Store, observation registry.Observation, operation registry.Operation) {
	t.Helper()
	records := store.Ordered()
	if len(records) != 3 {
		t.Fatalf("Codex registry records = %d, want 3", len(records))
	}
	for _, record := range records {
		wantTrust := registry.TrustNotApplicable
		if record.Key().Cell().Extension() == artifact.ExtensionHooks && observation == registry.ObservationInstalled {
			wantTrust = registry.TrustPending
		}
		if record.Key().Scope() != registry.ScopeGlobal || record.Key().Cell().Harness() != artifact.HarnessCodex || !record.Managed() || record.Observation() != observation || record.Trust() != wantTrust || record.LastOperation() != operation || record.LastOutcome() != registry.OutcomeCompleted {
			t.Fatalf("unexpected persisted Codex record: key=%s observation=%s trust=%s operation=%s outcome=%s", record.Key().Cell(), record.Observation(), record.Trust(), record.LastOperation(), record.LastOutcome())
		}
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s|%s|", filepath.ToSlash(relative), info.Mode())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "%s", artifact.DigestBytes(content))
		}
		snapshot.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}

type fileSpec struct {
	content string
	mode    uint32
}

func assertCodexTrust(t *testing.T, extension artifact.Extension, out apply.Outcome, observation registry.Observation) {
	t.Helper()
	if out.Record == nil || out.Record.Observation() != observation {
		t.Fatalf("%s outcome lacks expected %s record: %+v", extension, observation, out)
	}
	if extension == artifact.ExtensionHooks && observation == registry.ObservationInstalled {
		if out.Status != apply.InstalledPendingTrust() || out.Record.Trust() != registry.TrustPending || !strings.Contains(out.Diagnostic, "native hooks interface") {
			t.Fatalf("installed hooks lack pending-trust fact: %+v", out)
		}
		return
	}
	if out.Record.Trust() != registry.TrustNotApplicable {
		t.Fatalf("%s %s trust = %s, want not-applicable", extension, observation, out.Record.Trust())
	}
}

func assertInstalledRegularFiles(t *testing.T, home string, bundle artifact.Bundle) {
	t.Helper()
	for _, entry := range bundle.Manifest().Entries() {
		if !entry.IsRegular() {
			continue
		}
		path := filepath.Join(home, filepath.FromSlash(entry.Path().String()))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read installed regular path %q: %v", entry.Path(), err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != entry.Mode().Bits() || artifact.DigestBytes(content) != entry.Digest() {
			t.Fatalf("installed path %q differs in type, mode, bytes, or digest", entry.Path())
		}
	}
}

func assertRecordMatchesBundle(t *testing.T, record registry.Record, bundle artifact.Bundle) {
	t.Helper()
	want := map[string]artifact.Entry{}
	for _, entry := range bundle.Manifest().Entries() {
		if entry.IsRegular() {
			want[entry.Path().String()] = entry
		}
	}
	leaves := record.Leaves()
	if len(leaves) != len(want) {
		t.Fatalf("record owns %d leaves, immutable bundle has %d regular entries", len(leaves), len(want))
	}
	for _, leaf := range leaves {
		entry, present := want[leaf.Path().String()]
		if !present || leaf.Type() != artifact.RegularFileType() || leaf.Mode().Bits() != entry.Mode().Bits() || leaf.Digest() != entry.Digest() {
			t.Fatalf("record has unexpected or drifted owned leaf %q", leaf.Path())
		}
	}
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

func descriptorWithBundle(t *testing.T, extension artifact.Extension, replacement artifact.Bundle) targetcodex.TargetDescriptor {
	t.Helper()
	production, err := targetcodex.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	components := make([]targetcodex.Component, 0, 3)
	for _, candidate := range production.Components() {
		bundle := candidate.Bundle()
		if candidate.Extension() == extension {
			bundle = replacement
		}
		component, componentErr := targetcodex.NewComponent(candidate.Extension(), candidate.PackageID(), bundle, candidate.DefaultEnabled())
		if componentErr != nil {
			t.Fatal(componentErr)
		}
		components = append(components, component)
	}
	descriptor, err := targetcodex.NewTargetDescriptor(components[0], components[1], components[2])
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}
