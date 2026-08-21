package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/host/claudecode"
	"github.com/dayvidpham/pasture/internal/install/host/codex"
	"github.com/dayvidpham/pasture/internal/install/host/opencode"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	targetclaude "github.com/dayvidpham/pasture/internal/target/claudecode"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
)

type recordingRunner struct{ calls int }

func (r *recordingRunner) Run(context.Context, activation.CommandSchema) (claudecode.CommandResult, error) {
	r.calls++
	return claudecode.CommandResult{}, errors.New("isolated Claude executable is unavailable")
}

type emptyManifestReader struct{}

func (emptyManifestReader) ReadPluginManifest(string) ([]byte, error) {
	return nil, errors.New("manifest was not requested")
}

func TestComposeInstallService_UsesInjectedGraphThroughCobra(t *testing.T) {
	home := filepath.Clean(t.TempDir())
	statePath := filepath.Join(home, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	registryFile, err := service.NewFileRegistry(statePath)
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	var claudeControllers, opencodeControllers, codexContracts, codexPolicies int
	config := installComposition{
		Home: home, StatePath: statePath, Registry: registryFile,
		ClaudeRunner: runner, ClaudeManifests: emptyManifestReader{},
		NewClaudeController: func(r claudecode.Runner, m claudecode.ManifestReader) (*claudecode.Controller, error) {
			claudeControllers++
			return claudecode.NewController(r, m)
		},
		NewOpenCodeController: func(root string) (opencode.Controller, error) {
			opencodeControllers++
			return opencode.New(root)
		},
		ClaudeDescriptor: targetclaude.Descriptor,
		CodexDescriptor:  targetcodex.Descriptor,
		NewCodexContract: func(target targetcodex.TargetDescriptor, root string) (activation.ActivationContract, error) {
			codexContracts++
			return codex.NewActivationContract(target, root)
		},
		NewCodexPolicies: func(target targetcodex.TargetDescriptor, root string) ([3]apply.DirectFilePolicy, error) {
			codexPolicies++
			return codex.NewDirectFilePolicies(target, root)
		},
	}

	serviceUnderTest, err := composeInstallService(config)
	if err != nil {
		t.Fatalf("compose isolated installer graph: %v", err)
	}
	desired := selectionDocument(t)
	cmd := newInstallApplySelectionCommand(func() (*service.Service, error) { return serviceUnderTest, nil })
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--desired", desired, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute composed Cobra handler: %v\n%s", err, output.String())
	}
	if claudeControllers != 1 || opencodeControllers != 1 || codexContracts != 1 || codexPolicies != 1 {
		t.Fatalf("injected composition calls = Claude %d, OpenCode %d, Codex contract %d, policies %d", claudeControllers, opencodeControllers, codexContracts, codexPolicies)
	}
	if runner.calls == 0 {
		t.Fatal("the composed Claude controller was not exercised by the production handler")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills")); err != nil {
		t.Fatalf("real OpenCode direct-file graph did not use isolated home: %v", err)
	}
	if strings.Contains(output.String(), "/home/") {
		t.Fatalf("handler leaked real HOME into output: %s", output.String())
	}
}

func selectionDocument(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "desired.yaml")
	states := make(map[cell.Cell]bool, len(cell.CanonicalCells()))
	for _, coordinate := range cell.CanonicalCells() {
		states[coordinate] = coordinate.Harness() == ir.HarnessOpenCode && coordinate.Extension() == cell.SkillsAxis()
	}
	sel, err := selection.New(states)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := sel.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

type failingRegistry struct{}

func (failingRegistry) Load(context.Context) (registry.Store, error) {
	return registry.Store{}, errors.New("isolated registry read denied")
}
func (failingRegistry) Save(context.Context, registry.Store) error { return nil }

func TestApplyCell_InjectedFailureReturnsActionableNonzeroStatus(t *testing.T) {
	serviceUnderTest, err := service.New(service.Config{Registry: failingRegistry{}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := newInstallApplyCellCommand(func() (*service.Service, error) { return serviceUnderTest, nil })
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--harness", "opencode", "--extension", "skills", "--enabled=true"})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("injected apply failure unexpectedly returned success")
	}
	if !strings.Contains(err.Error(), "registry-load") || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("failure lost actionable diagnostic: %v", err)
	}
	if !strings.Contains(output.String(), "registry-load") || !strings.Contains(output.String(), "retry") {
		t.Fatalf("Cobra failure output lost actionable diagnostic: %q", output.String())
	}
}
