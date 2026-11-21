package antigravity_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	hostantigravity "github.com/dayvidpham/pasture/internal/install/host/antigravity"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
	targetantigravity "github.com/dayvidpham/pasture/internal/target/antigravity"
)

func TestActivationContractAndPolicyInstallAndRemoveSkills(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	desc, err := targetantigravity.Descriptor()
	if err != nil {
		t.Fatalf("Descriptor(): %v", err)
	}
	contract, err := hostantigravity.NewActivationContract(desc, home)
	if err != nil {
		t.Fatalf("NewActivationContract(): %v", err)
	}
	policy, err := hostantigravity.NewDirectFilePolicy(desc, home)
	if err != nil {
		t.Fatalf("NewDirectFilePolicy(): %v", err)
	}

	// Verify strategies per cell.
	for _, tc := range []struct {
		ext  cell.Extension
		want activation.StrategyKind
	}{
		{cell.SkillsAxis(), activation.DirectFileKindValue()},
		{cell.AgentsAxis(), activation.UnsupportedKindValue()},
		{cell.HooksAxis(), activation.UnsupportedKindValue()},
	} {
		c, _ := cell.New(artifact.HarnessAntigravityCLI, tc.ext)
		d, _ := activation.NewComponentDescriptor(c)
		bound, err := activation.LookupComponentActivation(contract, d)
		if err != nil {
			t.Fatalf("LookupComponentActivation(%s): %v", c, err)
		}
		if bound.Strategy().Kind() != tc.want {
			t.Fatalf("%s strategy = %s, want %s", c, bound.Strategy().Kind(), tc.want)
		}
	}

	// Wire service with stub sibling contracts and test skills ensure/remove + unsupported refusal.
	contracts := map[ir.HarnessID]activation.ActivationContract{
		ir.HarnessAntigravityCLI: contract,
	}
	policies := []apply.DirectFilePolicy{policy}
	for _, h := range []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode, ir.HarnessCodex} {
		var acts [3]activation.ComponentActivation
		for i, ext := range cell.CanonicalExtensions() {
			c, _ := cell.New(h, ext)
			s, err := activation.NewUnsupported("unused in test")
			if err != nil {
				t.Fatal(err)
			}
			acts[i], err = activation.NewComponentActivation(c, s)
			if err != nil {
				t.Fatal(err)
			}
		}
		ex, err := activation.NewExhaustiveComponentActivations(acts[0], acts[1], acts[2])
		if err != nil {
			t.Fatal(err)
		}
		id, _ := activation.NewActivationContractID(string(h) + "/test@1")
		probe, _ := activation.NewCommandSchema("unused", "--version")
		c, err := activation.NewActivationContract(id, h, runtime.AntigravityCLI1_5_9().Versions(), probe, ex)
		if err != nil {
			t.Fatal(err)
		}
		contracts[h] = c
	}

	direct, err := apply.NewDirectFileActivator(policies...)
	if err != nil {
		t.Fatalf("NewDirectFileActivator(): %v", err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	reg, err := service.NewFileRegistry(filepath.Join(stateDir, "installations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		Registry:   reg,
		Contracts:  contracts,
		Activators: []apply.Activator{direct, apply.NewUnsupportedActivator()},
	})
	if err != nil {
		t.Fatalf("service.New(): %v", err)
	}

	skillsCell, _ := cell.New(artifact.HarnessAntigravityCLI, cell.SkillsAxis())
	res, err := svc.ApplyCell(context.Background(), service.CellRequest{
		Cell:    skillsCell,
		Enabled: true,
		Scope:   apply.GlobalScope(),
		Source:  apply.InstallerSource(),
	})
	if err != nil {
		t.Fatalf("ApplyCell(skills, true): %v", err)
	}
	if !res.OK() || len(res.Rows()) != 1 || res.Rows()[0].Status() != apply.Completed() {
		t.Fatalf("unexpected skills install result: ok=%v rows=%+v", res.OK(), res.Rows())
	}

	workerSkill := filepath.Join(home, ".gemini", "config", "skills", "worker", "SKILL.md")
	data, err := os.ReadFile(workerSkill)
	if err != nil {
		t.Fatalf("expected installed skill at %q: %v", workerSkill, err)
	}
	if !strings.Contains(string(data), "name: worker") {
		t.Fatalf("installed worker SKILL.md missing frontmatter name: %q", string(data)[:min(len(data), 120)])
	}

	// Attempting to install agents or hooks must fail actionably.
	for _, ext := range []cell.Extension{cell.AgentsAxis(), cell.HooksAxis()} {
		c, _ := cell.New(artifact.HarnessAntigravityCLI, ext)
		badRes, badErr := svc.ApplyCell(context.Background(), service.CellRequest{
			Cell:    c,
			Enabled: true,
			Scope:   apply.GlobalScope(),
			Source:  apply.InstallerSource(),
		})
		if badErr != nil {
			t.Fatalf("ApplyCell(%s, true) returned preplan err %v, want failed row", c, badErr)
		}
		if badRes.OK() || len(badRes.Rows()) != 1 || badRes.Rows()[0].Status() != apply.Failed() {
			t.Fatalf("ApplyCell(%s, true) = ok:%v rows:%+v, want failed", c, badRes.OK(), badRes.Rows())
		}
	}

	// Uninstall skills cleanly removes the installed files.
	remRes, err := svc.ApplyCell(context.Background(), service.CellRequest{
		Cell:    skillsCell,
		Enabled: false,
		Scope:   apply.GlobalScope(),
		Source:  apply.InstallerSource(),
	})
	if err != nil {
		t.Fatalf("ApplyCell(skills, false): %v", err)
	}
	if !remRes.OK() || len(remRes.Rows()) != 1 || remRes.Rows()[0].Observation() != registry.ObservationAbsent {
		t.Fatalf("unexpected skills remove result: ok=%v rows=%+v", remRes.OK(), remRes.Rows())
	}
	if _, statErr := os.Stat(workerSkill); !os.IsNotExist(statErr) {
		t.Fatalf("worker skill still exists after uninstall: %v", statErr)
	}
}
