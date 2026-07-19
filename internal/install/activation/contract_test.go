package activation_test

import (
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func mustBundle(t *testing.T, rel, content string) artifact.Bundle {
	t.Helper()
	p, err := artifact.NewPath(rel)
	if err != nil {
		t.Fatalf("NewPath: %v", err)
	}
	mode, err := artifact.NewMode(0o644)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	entry, err := artifact.NewFileEntry(p, mode, artifact.DigestBytes([]byte(content)))
	if err != nil {
		t.Fatalf("NewFileEntry: %v", err)
	}
	manifest, err := artifact.NewManifest(entry)
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	src := fstest.MapFS{rel: &fstest.MapFile{Data: []byte(content), Mode: 0o644}}
	bundle, err := artifact.NewBundle(src, manifest)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	return bundle
}

func claudeContract(t *testing.T) activation.ActivationContract {
	t.Helper()
	host, _ := runtime.ParseHostVersion("2.1.210")
	constraint, _ := runtime.NewExactVersion(host)
	probe, err := activation.NewCommandSchema("claude", "--version")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	addSkills, _ := activation.NewCommandSchema("claude", "plugin", "install", "pasture-skills", "--scope", "user")
	native, err := activation.NewNativePlugin("pasture-skills", addSkills)
	if err != nil {
		t.Fatalf("native: %v", err)
	}
	agentsNative, _ := activation.NewNativePlugin("pasture-agents", addSkills)
	hooksNative, _ := activation.NewNativePlugin("pasture-hooks", addSkills)

	skillsCell, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	agentsCell, _ := cell.New(ir.HarnessClaudeCode, cell.AgentsAxis())
	hooksCell, _ := cell.New(ir.HarnessClaudeCode, cell.HooksAxis())

	skills, _ := activation.NewComponentActivation(skillsCell, native)
	agents, _ := activation.NewComponentActivation(agentsCell, agentsNative)
	hooks, _ := activation.NewComponentActivation(hooksCell, hooksNative)

	exhaustive, err := activation.NewExhaustiveComponentActivations(skills, agents, hooks)
	if err != nil {
		t.Fatalf("exhaustive: %v", err)
	}
	id, _ := activation.NewActivationContractID("claude-code/activation@2.1.210")
	contract, err := activation.NewActivationContract(id, ir.HarnessClaudeCode, constraint, probe, exhaustive)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	return contract
}

func TestLookupResolvesBoundComponent(t *testing.T) {
	contract := claudeContract(t)
	skillsCell, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	desc, _ := activation.NewComponentDescriptor(skillsCell)
	act, err := activation.LookupComponentActivation(contract, desc)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if act.Cell().String() != "claude-code.skills" {
		t.Errorf("resolved wrong cell: %s", act.Cell())
	}
	if act.Strategy().Kind() != activation.NativePluginKindValue() {
		t.Errorf("strategy kind = %s", act.Strategy().Kind())
	}
}

func TestLookupRejectsWrongHarness(t *testing.T) {
	contract := claudeContract(t)
	codexSkills, _ := cell.New(ir.HarnessCodex, cell.SkillsAxis())
	desc, _ := activation.NewComponentDescriptor(codexSkills)
	if _, err := activation.LookupComponentActivation(contract, desc); err == nil {
		t.Fatal("wrong-harness lookup = nil error, want rejection")
	}
}

func TestLookupRejectsZeroDescriptorAndZeroContract(t *testing.T) {
	contract := claudeContract(t)
	if _, err := activation.LookupComponentActivation(contract, activation.ComponentDescriptor{}); err == nil {
		t.Error("zero descriptor = nil error, want rejection")
	}
	skillsCell, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	desc, _ := activation.NewComponentDescriptor(skillsCell)
	if _, err := activation.LookupComponentActivation(activation.ActivationContract{}, desc); err == nil {
		t.Error("zero contract = nil error, want rejection")
	}
}

func TestExhaustiveRejectsMisSlottedAndMixedHarness(t *testing.T) {
	skillsCell, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	agentsCell, _ := cell.New(ir.HarnessClaudeCode, cell.AgentsAxis())
	hooksCell, _ := cell.New(ir.HarnessClaudeCode, cell.HooksAxis())
	native, _ := activation.NewNativePlugin("p", mustSchema(t))
	sAct, _ := activation.NewComponentActivation(skillsCell, native)
	aAct, _ := activation.NewComponentActivation(agentsCell, native)
	hAct, _ := activation.NewComponentActivation(hooksCell, native)

	// agents slot fed a skills activation.
	if _, err := activation.NewExhaustiveComponentActivations(sAct, sAct, hAct); err == nil {
		t.Error("mis-slotted axis = nil error, want rejection")
	}
	// mixed harness.
	codexHooks, _ := cell.New(ir.HarnessCodex, cell.HooksAxis())
	codexHooksAct, _ := activation.NewComponentActivation(codexHooks, native)
	if _, err := activation.NewExhaustiveComponentActivations(sAct, aAct, codexHooksAct); err == nil {
		t.Error("mixed harness = nil error, want rejection")
	}
}

func mustSchema(t *testing.T) activation.CommandSchema {
	t.Helper()
	s, err := activation.NewCommandSchema("claude", "--version")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestComponentActivationRejectsNilStrategy(t *testing.T) {
	skillsCell, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	if _, err := activation.NewComponentActivation(skillsCell, nil); err == nil {
		t.Fatal("nil strategy = nil error, want rejection")
	}
}

func TestDirectFileAndPendingTrustStrategies(t *testing.T) {
	bundle := mustBundle(t, "plugin/pasture-hooks.ts", "export default {}\n")
	df, err := activation.NewDirectFile(bundle, "plugin")
	if err != nil {
		t.Fatalf("NewDirectFile: %v", err)
	}
	if df.Kind() != activation.DirectFileKindValue() {
		t.Errorf("direct-file kind = %s", df.Kind())
	}
	if df.Bundle().ID() != bundle.ID() {
		t.Error("direct-file bundle identity mismatch")
	}
	native, _ := activation.NewNativePlugin("pasture-hooks", mustSchema(t))
	pending, err := activation.NewNativePluginPendingTrust(native)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending.Kind() != activation.NativePluginPendingTrustKindValue() {
		t.Errorf("pending kind = %s", pending.Kind())
	}
	if _, err := activation.NewDirectFile(bundle, ""); err == nil {
		t.Error("empty destination = nil error, want rejection")
	}
}
