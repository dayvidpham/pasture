package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/host/claudecode"
	"github.com/dayvidpham/pasture/internal/install/host/codex"
	"github.com/dayvidpham/pasture/internal/install/host/opencode"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	targetclaude "github.com/dayvidpham/pasture/internal/target/claudecode"
	targetcodex "github.com/dayvidpham/pasture/internal/target/codex"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/spf13/cobra"
)

var (
	desiredPath      string
	installSource    string
	installJSON      bool
	installHarness   string
	installExtension string
	installEnabled   bool
)

func productionInstallService() (*service.Service, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("installer composition: user home is unavailable; the global destinations cannot be resolved: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(defaultInstallStatePath()), 0o700); err != nil {
		return nil, fmt.Errorf("installer composition: create private state directory: %w", err)
	}
	state, err := service.NewFileRegistry(defaultInstallStatePath())
	if err != nil {
		return nil, err
	}

	claudeTarget, err := targetclaude.Descriptor()
	if err != nil {
		return nil, err
	}
	claudeContract, err := claudecode.Contract(claudeTarget)
	if err != nil {
		return nil, err
	}
	claudeController, err := claudecode.NewController(claudecode.OSRunner{}, claudecode.OSManifestReader{})
	if err != nil {
		return nil, err
	}

	opencodeController, err := opencode.New(filepath.Join(home, ".config", "opencode"))
	if err != nil {
		return nil, err
	}
	opencodeTarget := opencodeController.Descriptor()
	opencodeContract := opencodeController.Contract()
	opencodePolicies := make([]apply.DirectFilePolicy, 0, 3)
	for _, component := range opencodeTarget.Components() {
		c, cellErr := cell.New(ir.HarnessOpenCode, component.Extension())
		if cellErr != nil {
			return nil, cellErr
		}
		policy, policyErr := apply.PassThroughDirectFile(c)
		if policyErr != nil {
			return nil, policyErr
		}
		opencodePolicies = append(opencodePolicies, policy)
	}

	codexTarget, err := targetcodex.Descriptor()
	if err != nil {
		return nil, err
	}
	codexContract, err := codex.NewActivationContract(codexTarget, home)
	if err != nil {
		return nil, err
	}
	codexPolicies, err := codex.NewDirectFilePolicies(codexTarget, home)
	if err != nil {
		return nil, err
	}

	policies := append(opencodePolicies, codexPolicies[:]...)
	direct, err := apply.NewDirectFileActivator(policies...)
	if err != nil {
		return nil, err
	}
	return service.New(service.Config{
		Registry: state,
		Contracts: map[ir.HarnessID]activation.ActivationContract{
			ir.HarnessClaudeCode: claudeContract,
			ir.HarnessOpenCode:   opencodeContract,
			ir.HarnessCodex:      codexContract,
		},
		Activators: []apply.Activator{claudeController.Activator(), direct},
		Group:      claudeController,
	})
}

func sourceValue(value string) (apply.Source, error) {
	switch value {
	case "installer":
		return apply.InstallerSource(), nil
	case "home-manager":
		return apply.HomeManagerSource(), nil
	}
	return apply.Source(0), fmt.Errorf("install source %q is unsupported; use installer or home-manager", value)
}

func runApplySelection(cmd *cobra.Command, _ []string) error {
	if desiredPath == "" {
		return fmt.Errorf("install apply-selection: --desired is required; provide an exhaustive effective-selection file")
	}
	data, err := os.ReadFile(desiredPath)
	if err != nil {
		return fmt.Errorf("install apply-selection: read desired file %q: %w; provide a readable selection document", desiredPath, err)
	}
	sel, err := selection.Parse(data)
	if err != nil {
		return err
	}
	source, err := sourceValue(installSource)
	if err != nil {
		return err
	}
	svc, err := productionInstallService()
	if err != nil {
		return err
	}
	result, err := svc.ApplySelection(cmd.Context(), service.SelectionRequest{Selection: sel, Scope: apply.GlobalScope(), Source: source})
	if err != nil {
		if installJSON {
			writeJSON(cmd.OutOrStdout(), err)
		}
		return err
	}
	if installJSON || resolveFormat() == types.OutputJSON {
		return writeJSON(cmd.OutOrStdout(), result)
	}
	return writeApplyText(cmd.OutOrStdout(), result)
}

func runApplyCell(cmd *cobra.Command, _ []string) error {
	harness := ir.HarnessID(installHarness)
	if !harness.IsValid() {
		return fmt.Errorf("install apply-cell: invalid --harness %q; use claude-code, opencode, or codex", installHarness)
	}
	axis, err := cell.ParseExtension(installExtension)
	if err != nil {
		return fmt.Errorf("install apply-cell: invalid --extension %q: %w", installExtension, err)
	}
	c, err := cell.New(harness, axis)
	if err != nil {
		return err
	}
	source, err := sourceValue(installSource)
	if err != nil {
		return err
	}
	svc, err := productionInstallService()
	if err != nil {
		return err
	}
	result, err := svc.ApplyCell(cmd.Context(), service.CellRequest{Cell: c, Enabled: installEnabled, Scope: apply.GlobalScope(), Source: source})
	if err != nil {
		if installJSON {
			writeJSON(cmd.OutOrStdout(), err)
		}
		return err
	}
	if installJSON || resolveFormat() == types.OutputJSON {
		return writeJSON(cmd.OutOrStdout(), result)
	}
	return writeApplyText(cmd.OutOrStdout(), result)
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
func writeApplyText(w io.Writer, result apply.Result) error {
	fmt.Fprintf(w, "apply %s (%s): %s\n", result.Source(), result.Scope(), map[bool]string{true: "ok", false: "failed"}[result.OK()])
	for _, row := range result.Rows() {
		fmt.Fprintf(w, "  %-20s %-8s %-22s %s\n", row.Cell(), row.Operation(), row.Status(), row.Diagnostic())
	}
	return nil
}

var installApplySelectionCmd = &cobra.Command{Use: "apply-selection", Short: "Apply an exhaustive desired installer selection", Args: cobra.NoArgs, RunE: runApplySelection}
var installApplyCellCmd = &cobra.Command{Use: "apply-cell", Short: "Apply one explicitly selected installer cell", Args: cobra.NoArgs, RunE: runApplyCell}

func init() {
	installApplySelectionCmd.Flags().StringVar(&desiredPath, "desired", "", "Path to an exhaustive effective-selection document")
	installApplySelectionCmd.Flags().StringVar(&installSource, "source", "installer", "Controller source: installer or home-manager")
	installApplySelectionCmd.Flags().BoolVar(&installJSON, "json", false, "Write the deterministic apply-result document")
	installApplyCellCmd.Flags().StringVar(&installHarness, "harness", "", "Harness: claude-code, opencode, or codex")
	installApplyCellCmd.Flags().StringVar(&installExtension, "extension", "", "Extension: skills, agents, or hooks")
	installApplyCellCmd.Flags().BoolVar(&installEnabled, "enabled", false, "Desired state for the cell")
	installApplyCellCmd.Flags().StringVar(&installSource, "source", "installer", "Controller source: installer or home-manager")
	installApplyCellCmd.Flags().BoolVar(&installJSON, "json", false, "Write the deterministic apply-result document")
	installCmd.AddCommand(installApplySelectionCmd, installApplyCellCmd)
}
