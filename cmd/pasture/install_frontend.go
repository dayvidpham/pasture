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

// installComposition is the deliberately small boundary between CLI wiring
// and the real installer graph. It keeps paths and external seams injectable
// without introducing a general-purpose dependency container.
type installComposition struct {
	Home, StatePath       string
	Registry              service.Registry
	ClaudeRunner          claudecode.Runner
	ClaudeManifests       claudecode.ManifestReader
	NewClaudeController   func(claudecode.Runner, claudecode.ManifestReader) (*claudecode.Controller, error)
	NewOpenCodeController func(string) (opencode.Controller, error)
	ClaudeDescriptor      func() (targetclaude.TargetDescriptor, error)
	CodexDescriptor       func() (targetcodex.TargetDescriptor, error)
	NewCodexContract      func(targetcodex.TargetDescriptor, string) (activation.ActivationContract, error)
	NewCodexPolicies      func(targetcodex.TargetDescriptor, string) ([3]apply.DirectFilePolicy, error)
}

func productionInstallService() (*service.Service, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("installer composition/home: user home is unavailable; global destinations cannot be resolved: %w", err)
	}
	statePath := defaultInstallStatePath()
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, fmt.Errorf("installer composition/state-directory: create private state directory: %w", err)
	}
	state, err := service.NewFileRegistry(statePath)
	if err != nil {
		return nil, fmt.Errorf("installer composition/registry: create state registry at %q: %w", statePath, err)
	}
	return composeInstallService(installComposition{
		Home: home, StatePath: statePath, Registry: state,
		ClaudeRunner: claudecode.OSRunner{}, ClaudeManifests: claudecode.OSManifestReader{},
		NewClaudeController: claudecode.NewController, NewOpenCodeController: opencode.New,
		ClaudeDescriptor: targetclaude.Descriptor, CodexDescriptor: targetcodex.Descriptor,
		NewCodexContract: codex.NewActivationContract, NewCodexPolicies: codex.NewDirectFilePolicies,
	})
}

func composeInstallService(config installComposition) (*service.Service, error) {
	if config.Home == "" || !filepath.IsAbs(config.Home) || filepath.Clean(config.Home) != config.Home {
		return nil, fmt.Errorf("installer composition/paths: home %q is not a canonical absolute path", config.Home)
	}
	if config.StatePath == "" || !filepath.IsAbs(config.StatePath) || filepath.Clean(config.StatePath) != config.StatePath {
		return nil, fmt.Errorf("installer composition/paths: state path %q is not a canonical absolute path", config.StatePath)
	}
	state := config.Registry
	if state == nil {
		return nil, fmt.Errorf("installer composition/registry: registry dependency is nil")
	}
	for name, dependency := range map[string]any{"Claude runner": config.ClaudeRunner, "Claude manifest reader": config.ClaudeManifests, "Claude controller factory": config.NewClaudeController, "OpenCode controller factory": config.NewOpenCodeController, "Claude descriptor factory": config.ClaudeDescriptor, "Codex descriptor factory": config.CodexDescriptor, "Codex contract factory": config.NewCodexContract, "Codex policy factory": config.NewCodexPolicies} {
		if dependency == nil {
			return nil, fmt.Errorf("installer composition/dependencies: %s is nil", name)
		}
	}

	claudeTarget, err := config.ClaudeDescriptor()
	if err != nil {
		return nil, fmt.Errorf("installer composition/Claude target: describe embedded target: %w", err)
	}
	claudeContract, err := claudecode.Contract(claudeTarget)
	if err != nil {
		return nil, fmt.Errorf("installer composition/Claude contract: bind activation contract: %w", err)
	}
	claudeController, err := config.NewClaudeController(config.ClaudeRunner, config.ClaudeManifests)
	if err != nil {
		return nil, fmt.Errorf("installer composition/Claude controller: construct controller: %w", err)
	}

	opencodeController, err := config.NewOpenCodeController(filepath.Join(config.Home, ".config", "opencode"))
	if err != nil {
		return nil, fmt.Errorf("installer composition/OpenCode controller: construct controller: %w", err)
	}
	opencodeTarget := opencodeController.Descriptor()
	opencodeContract := opencodeController.Contract()
	opencodePolicies := make([]apply.DirectFilePolicy, 0, 3)
	for _, component := range opencodeTarget.Components() {
		c, cellErr := cell.New(ir.HarnessOpenCode, component.Extension())
		if cellErr != nil {
			return nil, fmt.Errorf("installer composition/OpenCode policy: create %s cell: %w", component.Extension(), cellErr)
		}
		policy, policyErr := apply.PassThroughDirectFile(c)
		if policyErr != nil {
			return nil, fmt.Errorf("installer composition/OpenCode policy: bind %s policy: %w", component.Extension(), policyErr)
		}
		opencodePolicies = append(opencodePolicies, policy)
	}

	codexTarget, err := config.CodexDescriptor()
	if err != nil {
		return nil, fmt.Errorf("installer composition/Codex target: describe embedded target: %w", err)
	}
	codexContract, err := config.NewCodexContract(codexTarget, config.Home)
	if err != nil {
		return nil, fmt.Errorf("installer composition/Codex contract: bind activation contract: %w", err)
	}
	codexPolicies, err := config.NewCodexPolicies(codexTarget, config.Home)
	if err != nil {
		return nil, fmt.Errorf("installer composition/Codex policy: bind direct-file policies: %w", err)
	}

	policies := append(opencodePolicies, codexPolicies[:]...)
	direct, err := apply.NewDirectFileActivator(policies...)
	if err != nil {
		return nil, fmt.Errorf("installer composition/activator: construct direct-file activator: %w", err)
	}
	svc, err := service.New(service.Config{
		Registry: state,
		Contracts: map[ir.HarnessID]activation.ActivationContract{
			ir.HarnessClaudeCode: claudeContract,
			ir.HarnessOpenCode:   opencodeContract,
			ir.HarnessCodex:      codexContract,
		},
		Activators: []apply.Activator{claudeController.Activator(), direct},
		Group:      claudeController,
	})
	if err != nil {
		return nil, fmt.Errorf("installer composition/service: construct application service: %w", err)
	}
	return svc, nil
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

type installServiceFactory func() (*service.Service, error)

func runApplySelection(cmd *cobra.Command, args []string) error {
	return runApplySelectionWith(cmd, args, productionInstallService)
}
func runApplySelectionWith(cmd *cobra.Command, _ []string, makeService installServiceFactory) error {
	desiredPath, _ := cmd.Flags().GetString("desired")
	installSource, _ := cmd.Flags().GetString("source")
	installJSON, _ := cmd.Flags().GetBool("json")
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
	svc, err := makeService()
	if err != nil {
		return fmt.Errorf("install apply-selection: compose installer service: %w", err)
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

func runApplyCell(cmd *cobra.Command, args []string) error {
	return runApplyCellWith(cmd, args, productionInstallService)
}
func runApplyCellWith(cmd *cobra.Command, _ []string, makeService installServiceFactory) error {
	installHarness, _ := cmd.Flags().GetString("harness")
	installExtension, _ := cmd.Flags().GetString("extension")
	installSource, _ := cmd.Flags().GetString("source")
	installEnabled, _ := cmd.Flags().GetBool("enabled")
	installJSON, _ := cmd.Flags().GetBool("json")
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
	svc, err := makeService()
	if err != nil {
		return fmt.Errorf("install apply-cell: compose installer service: %w", err)
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
	if _, err := fmt.Fprintf(w, "apply %s (%s): %s\n", result.Source(), result.Scope(), map[bool]string{true: "ok", false: "failed"}[result.OK()]); err != nil {
		return fmt.Errorf("installer output: write apply header: %w", err)
	}
	for _, row := range result.Rows() {
		if _, err := fmt.Fprintf(w, "  %-20s %-8s %-22s %s\n", row.Cell(), row.Operation(), row.Status(), row.Diagnostic()); err != nil {
			return fmt.Errorf("installer output: write apply row %s: %w", row.Cell(), err)
		}
	}
	return nil
}

func newInstallApplySelectionCommand(makeService installServiceFactory) *cobra.Command {
	cmd := &cobra.Command{Use: "apply-selection", Short: "Apply an exhaustive desired installer selection (scripting/Home Manager surface)", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return runApplySelectionWith(cmd, args, makeService) }
	cmd.Flags().String("desired", "", "Path to an exhaustive effective-selection document")
	cmd.Flags().String("source", "installer", "Controller source: installer or home-manager")
	cmd.Flags().Bool("json", false, "Write the deterministic apply-result document")
	return cmd
}
func newInstallApplyCellCommand(makeService installServiceFactory) *cobra.Command {
	cmd := &cobra.Command{Use: "apply-cell", Short: "Apply one explicitly selected installer cell (scripting/Home Manager surface)", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return runApplyCellWith(cmd, args, makeService) }
	cmd.Flags().String("harness", "", "Harness: claude-code, opencode, or codex")
	cmd.Flags().String("extension", "", "Extension: skills, agents, or hooks")
	cmd.Flags().Bool("enabled", false, "Desired state for the cell")
	cmd.Flags().String("source", "installer", "Controller source: installer or home-manager")
	cmd.Flags().Bool("json", false, "Write the deterministic apply-result document")
	return cmd
}

var installApplySelectionCmd = newInstallApplySelectionCommand(productionInstallService)
var installApplyCellCmd = newInstallApplyCellCommand(productionInstallService)

func init() {
	installCmd.AddCommand(installApplySelectionCmd, installApplyCellCmd)
}
