package codex_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
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
	"github.com/dayvidpham/pasture/internal/runtime"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

type layoutFixture struct {
	Contract        string `json:"contract"`
	RuntimeContract string `json:"runtime_contract"`
	HostMin         string `json:"host_min"`
	HostMax         string `json:"host_max"`
	Components      []struct {
		Extension      string   `json:"extension"`
		Package        string   `json:"package"`
		Prefix         string   `json:"prefix"`
		DefaultEnabled bool     `json:"default_enabled"`
		PublicFiles    []string `json:"public_files,omitempty"`
	} `json:"components"`
}

// codexExecutor models the later frontend composition: one strategy-wide
// generic activator plus the Codex contract policy. The Codex controller itself
// must never become the DirectFile router.
type codexExecutor struct {
	policy hostcodex.Controller
	direct apply.DirectFileActivator
}

func newCodexExecutor() codexExecutor {
	return codexExecutor{policy: hostcodex.NewController(), direct: apply.NewDirectFileActivator()}
}

func (e codexExecutor) Inspect(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := e.policy.Validate(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := e.direct.Inspect(ctx, source, key, act, prior)
	return e.policy.Decorate(key, out, err)
}

func (e codexExecutor) Ensure(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := e.policy.Validate(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := e.direct.Ensure(ctx, source, key, act, prior)
	return e.policy.Decorate(key, out, err)
}

func (e codexExecutor) Remove(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	if err := e.policy.Validate(key, act); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	out, err := e.direct.Remove(ctx, source, key, act, prior)
	return e.policy.Decorate(key, out, err)
}

func TestControllerDoesNotClaimDirectFileActivatorSlot(t *testing.T) {
	t.Parallel()
	if _, ok := any(hostcodex.NewController()).(apply.Activator); ok {
		t.Fatal("Codex policy unexpectedly implements apply.Activator and claims the generic DirectFile strategy slot")
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
	if contract.ID().String() != fixture.Contract {
		t.Fatalf("contract fixture mismatch: got %s / %s", contract.ID(), descriptor.RuntimeContractID())
	}
	if descriptor.RuntimeContractID().String() != fixture.RuntimeContract || len(fixture.Components) != 3 {
		t.Fatalf("runtime/layout fixture mismatch: %s", descriptor.RuntimeContractID())
	}
	if contract.HostVersions().Min().String() != fixture.HostMin || contract.HostVersions().Max().String() != fixture.HostMax {
		t.Fatalf("installer host range = [%s,%s], fixture = [%s,%s]", contract.HostVersions().Min(), contract.HostVersions().Max(), fixture.HostMin, fixture.HostMax)
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
	cases := map[string]bool{
		"0.144.0":         false,
		"0.144.1":         true,
		"0.146.0":         true,
		"0.146.1":         false,
		"0.146.0-rc.1":    false,
		"0.146.0+build.7": true,
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
			controller := newCodexExecutor()
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
			repeated, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, bound, out.Record)
			if err != nil || repeated.Record == nil || repeated.Record.ArtifactID() != out.Record.ArtifactID() || !repeated.Record.Managed() {
				t.Fatalf("idempotent Ensure(%s) changed ownership or failed: out=%+v err=%v", coordinate, repeated, err)
			}
			assertCodexTrust(t, component.Extension(), repeated, registry.ObservationInstalled)
			status, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, bound, repeated.Record)
			if err != nil || status.Record == nil || status.Observation != registry.ObservationInstalled || !status.Record.Managed() {
				t.Fatalf("post-ensure Inspect(%s) returned wrong fact: out=%+v err=%v", coordinate, status, err)
			}
			assertCodexTrust(t, component.Extension(), status, registry.ObservationInstalled)
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
			postRemove, err := controller.Inspect(context.Background(), apply.InstallerSource(), key, bound, removed.Record)
			if err != nil || postRemove.Record == nil || postRemove.Observation != registry.ObservationAbsent {
				t.Fatalf("post-remove Inspect(%s) returned wrong fact: out=%+v err=%v", coordinate, postRemove, err)
			}
			repeatedRemove, err := controller.Remove(context.Background(), apply.InstallerSource(), key, bound, *removed.Record)
			if err != nil || repeatedRemove.Observation != registry.ObservationAbsent {
				t.Fatalf("repeated Remove(%s) did not converge: out=%+v err=%v", coordinate, repeatedRemove, err)
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
			out, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
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
			controller := newCodexExecutor()
			first := activationFor(t, coordinate, home, bundleFor(t, map[string]fileSpec{name: {content: "first", mode: 0o644}}))
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
			updated, err := controller.Ensure(context.Background(), apply.InstallerSource(), key, second, inspected.Record)
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
	controller := newCodexExecutor()
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
	out, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil)
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
	repaired, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, out.Record)
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
	if _, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
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
		if _, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
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
		if _, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err == nil {
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
	executor := newCodexExecutor()
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
	if _, err := newCodexExecutor().Ensure(context.Background(), apply.InstallerSource(), key, bound, nil); err != nil {
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
	if !strings.Contains(string(args), "hook lifecycle --harness codex --event SessionStart --host-version 0.146.0") {
		t.Fatalf("installed global runner forwarded wrong argv: %q", args)
	}
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
