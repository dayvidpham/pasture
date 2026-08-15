package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
	target "github.com/dayvidpham/pasture/internal/target/claudecode"
)

type nativeSnapshot struct {
	marketplace bool
	plugins     map[cell.Cell]pluginRow
	legacy      *pluginRow
}

type groupSession struct {
	request             service.GroupSelection
	steps               []service.GroupStep
	initial             nativeSnapshot
	actions             map[cell.Cell]service.GroupAction
	managed             map[cell.Cell]bool
	optionalUnavailable error
	ran                 bool
	terminal            bool
}

type probeUnavailableError struct{ cause error }

func (e *probeUnavailableError) Error() string { return e.cause.Error() }
func (e *probeUnavailableError) Unwrap() error { return e.cause }

// Controller is both the ordinary native-plugin activator and Claude's one
// selection-wide reconciler. The latter is required only for the exact legacy
// monolith transition; both paths use the same probes, codecs, and actions.
type Controller struct {
	runner    Runner
	manifests ManifestReader
	artifacts map[cell.Cell]artifact.BundleID

	mu      sync.Mutex
	pending *groupSession
}

func NewController(runner Runner, manifests ManifestReader) (*Controller, error) {
	if runner == nil {
		return nil, fault("Claude controller construction", "non-nil native runner", "the runner is nil", "NewController", "wiring Claude activation", "no reviewed command can execute", "provide OSRunner in production", nil)
	}
	if manifests == nil {
		return nil, fault("Claude controller construction", "non-nil manifest reader", "the manifest reader is nil", "NewController", "wiring versionless-row verification", "selected release identity could be guessed", "provide OSManifestReader in production", nil)
	}
	descriptor, err := target.Descriptor()
	if err != nil {
		return nil, fault("Claude controller construction", "immutable embedded target descriptor", err.Error(), "NewController", "wiring artifact identity records", "native actions cannot be tied to exact shipped bytes", "regenerate and rebuild the embedded Claude target", err)
	}
	artifacts := make(map[cell.Cell]artifact.BundleID, 3)
	for _, component := range descriptor.Components() {
		cc, cellErr := cell.New(ir.HarnessClaudeCode, component.Extension())
		if cellErr != nil {
			return nil, cellErr
		}
		artifacts[cc] = component.Bundle().ID()
	}
	return &Controller{runner: runner, manifests: manifests, artifacts: artifacts}, nil
}

func (c *Controller) Harness() ir.HarnessID { return ir.HarnessClaudeCode }
func (c *Controller) StrategyKind() activation.StrategyKind {
	return activation.NativePluginKindValue()
}

func (c *Controller) PlanSelection(ctx context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	if request.Scope.Kind() != registry.ScopeGlobal {
		return service.GroupPlan{Handled: false}, nil
	}
	if request.Source != apply.InstallerSource() && request.Source != apply.HomeManagerSource() {
		return service.GroupPlan{}, fault("Claude group planning", "valid controller source", "the source is invalid", "Controller.PlanSelection", "planning exhaustive sibling intent", "ownership cannot be selected", "use installer or home-manager source", nil)
	}
	if err := validateActivations(request.Activation); err != nil {
		return service.GroupPlan{}, err
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		var unavailable *probeUnavailableError
		if !optionalSelection(request) || !errors.As(err, &unavailable) {
			return service.GroupPlan{}, err
		}
		steps, stepErr := groupSteps(request, nativeSnapshot{})
		if stepErr != nil {
			return service.GroupPlan{}, stepErr
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.pending != nil && !c.pending.terminal {
			return service.GroupPlan{}, fault("Claude group planning", "one active reconciliation", "another group plan has not finished inspection", "Controller.PlanSelection", "creating an optional preservation plan", "two plans could exchange live facts", "finish the current apply request before starting another", nil)
		}
		c.pending = &groupSession{request: request, steps: steps, actions: map[cell.Cell]service.GroupAction{}, managed: map[cell.Cell]bool{}, optionalUnavailable: unavailable}
		return service.GroupPlan{Handled: true, Steps: append([]service.GroupStep(nil), steps...)}, nil
	}
	if err := validatePrior(request.Prior, snapshot); err != nil {
		return service.GroupPlan{}, err
	}
	steps, err := groupSteps(request, snapshot)
	if err != nil {
		return service.GroupPlan{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != nil && !c.pending.terminal {
		return service.GroupPlan{}, fault("Claude group planning", "one active reconciliation", "another group plan has not finished inspection", "Controller.PlanSelection", "creating a selection-wide plan", "two plans could exchange live facts", "finish the current apply request before starting another; concurrent installers are unsupported", nil)
	}
	c.pending = &groupSession{request: request, steps: steps, initial: snapshot, actions: map[cell.Cell]service.GroupAction{}, managed: map[cell.Cell]bool{}}
	return service.GroupPlan{Handled: true, Steps: append([]service.GroupStep(nil), steps...)}, nil
}

func groupSteps(request service.GroupSelection, snapshot nativeSnapshot) ([]service.GroupStep, error) {
	steps := make([]service.GroupStep, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		key, keyErr := request.Scope.Key(cc)
		if keyErr != nil {
			return nil, keyErr
		}
		op := apply.Inspect()
		if request.Selection.Enabled(cc) {
			op = apply.Ensure()
		} else if prior, ok := request.Prior[cc]; ok && prior.Managed() || snapshot.legacy != nil {
			op = apply.RemoveOp()
		}
		steps = append(steps, service.NewGroupStep(cc, key, op))
	}
	return steps, nil
}

func (c *Controller) Execute(ctx context.Context, step service.GroupStep) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.pending
	if s == nil || !containsStep(s.steps, step) {
		return fault("Claude group execution", "step from the active canonical plan", fmt.Sprintf("step %s is stale or foreign", step.Cell()), "Controller.Execute", "executing selection-wide reconciliation", "no action was started", "rerun apply-selection to create a fresh plan", nil)
	}
	if s.ran {
		return nil
	}
	s.ran = true
	c.reconcile(ctx, s)
	return nil
}

func (c *Controller) Inspect(_ context.Context, step service.GroupStep) (service.GroupAction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.pending
	if s == nil || !s.ran {
		return service.GroupAction{}, fault("Claude group inspection", "executed active plan", "no executed group plan is active", "Controller.Inspect", "returning a confirmed group fact", "the service must not persist a guessed record", "rerun the full selection", nil)
	}
	action, ok := s.actions[step.Cell()]
	if !ok {
		return service.GroupAction{}, fault("Claude group inspection", "one exact action per canonical cell", fmt.Sprintf("no action was recorded for %s", step.Cell()), "Controller.Inspect", "returning a confirmed group fact", "the service must stop without persisting a guessed record", "repair the controller and rerun the full selection", nil)
	}
	if action.Row.Status() == apply.Failed() {
		s.terminal = true
	}
	if step.Cell() == s.steps[len(s.steps)-1].Cell() {
		c.pending = nil
	}
	return action, nil
}

func (c *Controller) PreflightCell(ctx context.Context, request service.GroupCell) error {
	if request.Scope.Kind() != registry.ScopeGlobal {
		return nil
	}
	if err := validateActivations(request.Activation); err != nil {
		return err
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		return err
	}
	if snapshot.legacy != nil {
		return fault("Claude cell preflight", "split-only native state", "the exact v0.0.4 monolith or an incomplete transition requires all three sibling choices", "Controller.PreflightCell", "validating a context-free cell request", "no mutation was attempted because sibling intent is unavailable", "rerun `pasture install` or apply-selection with an exhaustive desired document", nil)
	}
	return validatePrior(request.Prior, snapshot)
}

func (c *Controller) inspectCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := validateOrdinary(key, act, prior); err != nil {
		return apply.Outcome{}, err
	}
	snapshot, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if snapshot.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude cell inspection", "split-only state for apply-cell", "the exact legacy monolith requires exhaustive sibling intent", "Controller.Inspect", "inspecting an ordinary cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	row, present := snapshot.plugins[key.Cell()]
	if !present {
		return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationAbsent, Diagnostic: "the exact split plugin is absent"}, nil
	}
	managed := prior != nil && prior.Managed()
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Diagnostic: fmt.Sprintf("exact user-scoped plugin %s is installed", row.ID), Record: c.observedRecord(key, act, source, managed, registry.OperationInspect, registry.OutcomeCompleted, registry.ObservationInstalled, "exact native postcondition observed")}, nil
}

func (c *Controller) ensureCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	if err := validateOrdinary(key, act, prior); err != nil {
		return apply.Outcome{}, err
	}
	before, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if before.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude cell ensure", "split-only state", "legacy migration requires exhaustive sibling intent", "Controller.Ensure", "ensuring one cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	if _, external := before.plugins[key.Cell()]; external && (prior == nil || !prior.Managed()) {
		return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Diagnostic: "exact matching plugin remains externally owned"}, nil
	}
	if err := c.ensureMarketplace(ctx, before.marketplace); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	pkg := nativePackage(act)
	action := command("claude", "plugin", "install", selector(pkg), "--scope", "user")
	if _, installed := before.plugins[key.Cell()]; installed {
		action = command("claude", "plugin", "update", selector(pkg), "--scope", "user")
	}
	if _, err := c.run(ctx, action); err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	after, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if _, ok := after.plugins[key.Cell()]; !ok {
		return apply.Outcome{Observation: registry.ObservationAbsent}, fmt.Errorf("Claude ensure postcondition for %s is absent", key.Cell())
	}
	record := c.observedRecord(key, act, source, true, registry.OperationEnsure, registry.OutcomeCompleted, registry.ObservationInstalled, "exact native ensure postcondition confirmed")
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationInstalled, Record: record, Diagnostic: "exact native ensure postcondition confirmed"}, nil
}

func (c *Controller) removeCell(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	if err := validateOrdinary(key, act, &prior); err != nil {
		return apply.Outcome{}, err
	}
	if !prior.Managed() {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude plugin removal", "Pasture-managed prior fact", "the prior record is externally owned", "Controller.Remove", "authorizing native uninstall", "the external plugin was preserved", "remove it manually or rerun with a Pasture-managed record", nil)
	}
	before, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if before.legacy != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, fault("Claude plugin removal", "split-only state", "legacy migration requires exhaustive sibling intent", "Controller.Remove", "removing one cell", "no mutation was attempted", "rerun the full installer or apply-selection", nil)
	}
	if _, installed := before.plugins[key.Cell()]; installed {
		if _, err := c.run(ctx, command("claude", "plugin", "uninstall", selector(nativePackage(act)), "--scope", "user")); err != nil {
			return apply.Outcome{Observation: registry.ObservationUnknown}, err
		}
	}
	after, err := c.probe(ctx)
	if err != nil {
		return apply.Outcome{Observation: registry.ObservationUnknown}, err
	}
	if _, remains := after.plugins[key.Cell()]; remains {
		return apply.Outcome{Observation: registry.ObservationInstalled}, fmt.Errorf("Claude remove postcondition for %s remains installed", key.Cell())
	}
	record := c.observedRecord(key, act, source, true, registry.OperationRemove, registry.OutcomeCompleted, registry.ObservationAbsent, "exact native remove postcondition confirmed")
	return apply.Outcome{Status: apply.Completed(), Observation: registry.ObservationAbsent, Record: record, Diagnostic: "exact native remove postcondition confirmed"}, nil
}

func (c *Controller) reconcile(ctx context.Context, s *groupSession) {
	if s.optionalUnavailable != nil {
		for _, step := range s.steps {
			diagnostic := fmt.Sprintf("optional all-false Claude probe was unavailable; no state was claimed or mutated: %v", s.optionalUnavailable)
			s.actions[step.Cell()] = service.GroupAction{Row: apply.NewActionRow(step.Cell(), step.Operation(), apply.Completed(), apply.ManagementUnknown, registry.ObservationUnknown, diagnostic)}
		}
		return
	}
	failedCell := cell.Cell{}
	failedReason := ""
	current := s.initial
	marketReady := false
	// Desired split packages are always established before the monolith changes.
	for _, step := range s.steps {
		cc := step.Cell()
		if !s.request.Selection.Enabled(cc) {
			continue
		}
		prior, hadPrior := s.request.Prior[cc]
		_, installed := current.plugins[cc]
		if installed && (!hadPrior || !prior.Managed()) {
			continue // exact external match is satisfying but never adopted
		}
		if !marketReady {
			if err := c.ensureMarketplace(ctx, current.marketplace); err != nil {
				failedCell, failedReason = cc, err.Error()
				break
			}
			marketReady = true
		}
		current.marketplace = true
		pkg := packageFor(cc.Extension())
		action := command("claude", "plugin", "install", selector(pkg), "--scope", "user")
		if installed {
			action = command("claude", "plugin", "update", selector(pkg), "--scope", "user")
		}
		if _, err := c.run(ctx, action); err != nil {
			failedCell, failedReason = cc, err.Error()
			break
		}
		next, err := c.probe(ctx)
		if err != nil {
			failedCell, failedReason = cc, err.Error()
			break
		}
		current = next
		if _, ok := current.plugins[cc]; !ok {
			failedCell, failedReason = cc, "native ensure returned success but the exact split plugin is absent"
			break
		}
		s.managed[cc] = true
	}
	if failedReason == "" && current.legacy != nil {
		if _, err := c.run(ctx, command("claude", "plugin", "uninstall", selector(LegacyPackage), "--scope", "user")); err != nil {
			failedCell, _ = cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
			failedReason = err.Error()
		} else if next, err := c.probe(ctx); err != nil {
			failedCell, _ = cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
			failedReason = err.Error()
		} else {
			current = next
			if current.legacy != nil {
				failedCell, _ = cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
				failedReason = "legacy uninstall returned success but the exact monolith remains"
			}
		}
	}
	// Unselected split packages are removed only when prior inventory or the
	// exact monolith transition establishes Pasture authority.
	if failedReason == "" {
		for _, step := range s.steps {
			cc := step.Cell()
			if s.request.Selection.Enabled(cc) {
				continue
			}
			_, installed := current.plugins[cc]
			prior, hadPrior := s.request.Prior[cc]
			authorized := hadPrior && prior.Managed() || s.initial.legacy != nil
			if !installed || !authorized {
				continue
			}
			if _, err := c.run(ctx, command("claude", "plugin", "uninstall", selector(packageFor(cc.Extension())), "--scope", "user")); err != nil {
				failedCell, failedReason = cc, err.Error()
				break
			}
			next, err := c.probe(ctx)
			if err != nil {
				failedCell, failedReason = cc, err.Error()
				break
			}
			current = next
			if _, remains := current.plugins[cc]; remains {
				failedCell, failedReason = cc, "native uninstall returned success but the split plugin remains"
				break
			}
		}
	}
	c.buildGroupActions(s, current, failedCell, failedReason)
}

func (c *Controller) buildGroupActions(s *groupSession, live nativeSnapshot, failedCell cell.Cell, failedReason string) {
	failureSeen := false
	for _, step := range s.steps {
		cc := step.Cell()
		desired := s.request.Selection.Enabled(cc)
		rowStatus := apply.Completed()
		observation := registry.ObservationAbsent
		management := apply.ManagementUnknown
		diagnostic := "desired false; exact split plugin is absent"
		var record *registry.Record
		plugin, installed := live.plugins[cc]
		prior, hadPrior := s.request.Prior[cc]
		managed := hadPrior && prior.Managed() || s.initial.legacy != nil || s.managed[cc]
		if installed {
			observation = registry.ObservationInstalled
			management = apply.ManagementExternal
			diagnostic = "exact matching split plugin remains externally owned"
			if managed {
				management = apply.ManagementPasture
				record = c.observedRecord(step.Key(), s.request.Activation[cc], s.request.Source, true, operationRecord(step.Operation()), registry.OutcomeCompleted, observation, "exact group postcondition confirmed")
				diagnostic = fmt.Sprintf("exact managed plugin %s is installed", plugin.ID)
			}
		} else if managed {
			management = apply.ManagementPasture
			record = c.observedRecord(step.Key(), s.request.Activation[cc], s.request.Source, true, operationRecord(step.Operation()), registry.OutcomeCompleted, observation, "exact group absence confirmed")
		}
		if failedReason != "" {
			satisfied := desired == installed
			switch {
			case cc == failedCell:
				rowStatus = apply.Failed()
				diagnostic = failedReason
				failureSeen = true
				if record != nil {
					record = c.observedRecord(step.Key(), s.request.Activation[cc], s.request.Source, managed, operationRecord(step.Operation()), registry.OutcomeFailed, observation, failedReason)
				}
			case failureSeen || !satisfied:
				rowStatus = apply.Unattempted()
				diagnostic = "an earlier Claude group action failed; this unsatisfied cell was not attempted"
				record = nil
			}
		}
		s.actions[cc] = service.GroupAction{Row: apply.NewActionRow(cc, step.Operation(), rowStatus, management, observation, diagnostic), Record: record}
	}
}

func (c *Controller) probe(ctx context.Context) (nativeSnapshot, error) {
	versionResult, err := c.run(ctx, command("claude", "--version"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	versionText := strings.TrimSpace(string(versionResult.Stdout))
	versionText = strings.TrimSuffix(versionText, " (Claude Code)")
	host, err := runtime.ParseHostVersion(versionText)
	if err != nil || !runtime.ClaudeCode2_1_210().Supports(host) {
		return nativeSnapshot{}, &probeUnavailableError{cause: fault("Claude host probe", ">=2.1.210 and <2.2.0", fmt.Sprintf("reported host version %q is outside the reviewed range", strings.TrimSpace(string(versionResult.Stdout))), "Controller.probe", "checking compatibility before native mutation", "no marketplace or plugin action was attempted", "install a reviewed Claude Code 2.1.x version or update Pasture's reviewed activation contract", err)}
	}
	marketResult, err := c.run(ctx, command("claude", "plugin", "marketplace", "list", "--json"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	markets, err := decodeMarketplaces(marketResult.Stdout)
	if err != nil {
		var unavailable *codecUnavailableError
		if errors.As(err, &unavailable) {
			return nativeSnapshot{}, &probeUnavailableError{cause: err}
		}
		return nativeSnapshot{}, err
	}
	hasMarket := false
	for _, market := range markets {
		if market.Name != MarketplaceName {
			continue
		}
		if market.Source != marketplaceSourceGitHub || market.Repo != MarketplaceRepo {
			return nativeSnapshot{}, fault("Claude marketplace validation", "exact aura-plugins GitHub source", fmt.Sprintf("marketplace %s points to a different source", MarketplaceName), "Controller.probe", "classifying shared native state", "no plugin mutation was attempted", "remove or repair the conflicting marketplace manually, then rerun", nil)
		}
		hasMarket = true
	}
	pluginResult, err := c.run(ctx, command("claude", "plugin", "list", "--available", "--json"))
	if err != nil {
		return nativeSnapshot{}, &probeUnavailableError{cause: err}
	}
	rows, err := decodePlugins(pluginResult.Stdout)
	if err != nil {
		var unavailable *codecUnavailableError
		if errors.As(err, &unavailable) {
			return nativeSnapshot{}, &probeUnavailableError{cause: err}
		}
		return nativeSnapshot{}, err
	}
	snapshot := nativeSnapshot{marketplace: hasMarket, plugins: map[cell.Cell]pluginRow{}}
	for _, row := range rows {
		cc, exact, legacy := classifyRow(row)
		if !exact && !legacy {
			if isPastureIdentity(row) {
				return nativeSnapshot{}, fault("Claude native-state classification", "exact split packages or exact user-scoped v0.0.4 monolith", fmt.Sprintf("near-match or unknown Pasture plugin row %q was found", row.ID), "Controller.probe", "classifying legacy and split state before mutation", "no plugin or marketplace mutation was attempted", "remove or repair the ambiguous row manually, then rerun the full selection", nil)
			}
			continue
		}
		if err := c.verifyRow(row, legacy); err != nil {
			return nativeSnapshot{}, err
		}
		if legacy {
			if snapshot.legacy != nil {
				return nativeSnapshot{}, fmt.Errorf("exact legacy monolith appears more than once")
			}
			copy := row
			snapshot.legacy = &copy
			continue
		}
		if _, duplicate := snapshot.plugins[cc]; duplicate {
			return nativeSnapshot{}, fmt.Errorf("exact split plugin %s appears more than once", cc)
		}
		snapshot.plugins[cc] = row
	}
	return snapshot, nil
}

func optionalSelection(request service.GroupSelection) bool {
	if len(request.Prior) != 0 {
		return false
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		if request.Selection.Enabled(cc) {
			return false
		}
	}
	return true
}

func (c *Controller) verifyRow(row pluginRow, legacy bool) error {
	wantPackage, wantVersion := row.Name, LegacyVersion
	if legacy {
		wantPackage = LegacyPackage
	}
	if row.Scope != "user" || !row.Enabled || row.Marketplace != MarketplaceName || row.ID != selector(wantPackage) || row.Name != wantPackage {
		return fault("Claude plugin-row validation", "exact enabled user-scoped selector", fmt.Sprintf("row %q has wrong name, marketplace, scope, enabled state, or selector", row.ID), "Controller.verifyRow", "classifying native ownership", "no mutation was attempted", "repair the conflicting plugin row manually and rerun", nil)
	}
	if row.Version != nil {
		if *row.Version != wantVersion {
			return fault("Claude plugin release validation", wantVersion, fmt.Sprintf("row %q reports version %q", row.ID, *row.Version), "Controller.verifyRow", "proving selected release identity", "no mutation was attempted", "install the exact reviewed plugin release or update Pasture's immutable target", nil)
		}
		return nil
	}
	data, err := c.manifests.ReadPluginManifest(row.InstallPath)
	if err != nil {
		return fault("Claude versionless-row validation", "matching native plugin manifest", err.Error(), row.InstallPath, "proving selected release identity", "the versionless row cannot be accepted", "repair the native installation so its manifest is readable and exact", err)
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Name != wantPackage || manifest.Version != wantVersion {
		return fault("Claude versionless-row validation", "matching name and version in plugin manifest", fmt.Sprintf("manifest reports name %q version %q: %v", manifest.Name, manifest.Version, err), row.InstallPath, "proving selected release identity", "the versionless row cannot be accepted", "repair the native installation so its manifest matches the selected immutable component", err)
	}
	return nil
}

func classifyRow(row pluginRow) (cell.Cell, bool, bool) {
	if row.ID == selector(LegacyPackage) || row.Name == LegacyPackage {
		return cell.Cell{}, row.ID == selector(LegacyPackage) && row.Name == LegacyPackage, true
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		pkg := packageFor(extension)
		if row.ID == selector(pkg) || row.Name == pkg {
			return cc, row.ID == selector(pkg) && row.Name == pkg, false
		}
	}
	return cell.Cell{}, false, false
}

func (c *Controller) ensureMarketplace(ctx context.Context, present bool) error {
	if present {
		_, err := c.run(ctx, command("claude", "plugin", "marketplace", "update", MarketplaceName))
		return err
	}
	_, err := c.run(ctx, command("claude", "plugin", "marketplace", "add", MarketplaceRepo, "--scope", "user"))
	return err
}

func (c *Controller) run(ctx context.Context, schema activation.CommandSchema) (CommandResult, error) {
	const managerTimeout = 2 * time.Minute
	runCtx, cancel := context.WithTimeout(ctx, managerTimeout)
	defer cancel()
	return c.runner.Run(runCtx, schema)
}

func validateActivations(acts map[cell.Cell]activation.ComponentActivation) error {
	if len(acts) != 3 {
		return fmt.Errorf("Claude group activation map has %d entries instead of three", len(acts))
	}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		act, ok := acts[cc]
		if !ok || !act.IsValid() || act.Cell() != cc || act.Strategy().Kind() != activation.NativePluginKindValue() || nativePackage(act) != packageFor(extension) {
			return fmt.Errorf("Claude activation for %s is missing or contradictory", cc)
		}
	}
	return nil
}

func validatePrior(prior map[cell.Cell]registry.Record, live nativeSnapshot) error {
	for cc, record := range prior {
		if cc.Harness() != ir.HarnessClaudeCode || !record.IsValid() || record.Cell() != cc || record.Strategy() != activation.NativePluginKindValue() {
			return fmt.Errorf("Claude prior record for %s has a wrong key, cell, or strategy", cc)
		}
		if record.Managed() {
			_, installed := live.plugins[cc]
			if record.Observation() == registry.ObservationInstalled && !installed {
				return fault("Claude managed-state validation", "live identity agrees with managed inventory", fmt.Sprintf("managed record for %s says installed but exact live plugin is absent", cc), "validatePrior", "preflighting ownership before mutation", "no native mutation was attempted", "inspect and manually repair the missing managed component or registry ambiguity", nil)
			}
		}
	}
	return nil
}

func validateOrdinary(key registry.Key, act activation.ComponentActivation, prior *registry.Record) error {
	if !key.IsValid() || key.Scope() != registry.ScopeGlobal || key.Cell().Harness() != ir.HarnessClaudeCode {
		return fmt.Errorf("Claude activator requires a valid global Claude key")
	}
	if !act.IsValid() || act.Cell() != key.Cell() || act.Strategy().Kind() != activation.NativePluginKindValue() || nativePackage(act) != packageFor(key.Cell().Extension()) {
		return fmt.Errorf("Claude activation contradicts scoped cell %s", key.Cell())
	}
	if prior != nil && (!prior.IsValid() || prior.Key() != key || prior.Strategy() != activation.NativePluginKindValue()) {
		return fmt.Errorf("Claude prior record contradicts scoped cell %s", key.Cell())
	}
	return nil
}

func (c *Controller) observedRecord(key registry.Key, act activation.ComponentActivation, source apply.Source, managed bool, operation registry.Operation, outcome registry.Outcome, observation registry.Observation, diagnostic string) *registry.Record {
	version, _ := registry.NewVersion(LegacyVersion)
	selectorValue, _ := registry.NewSelector(selector(nativePackage(act)))
	registrySource := registry.SourceInstaller
	if source == apply.HomeManagerSource() {
		registrySource = registry.SourceHomeManager
	}
	record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registrySource, Strategy: activation.NativePluginKindValue(), Managed: managed, ArtifactID: c.artifacts[key.Cell()], Version: version, Selector: selectorValue, Observation: observation, Trust: registry.TrustNotApplicable, LastOperation: operation, LastOutcome: outcome, Diagnostic: diagnostic})
	if err != nil {
		return nil
	}
	return &record
}

func nativePackage(act activation.ComponentActivation) string {
	plugin, ok := act.Strategy().(activation.NativePlugin)
	if !ok {
		return ""
	}
	return plugin.Package()
}

func containsStep(steps []service.GroupStep, step service.GroupStep) bool {
	for _, candidate := range steps {
		if candidate.Cell() == step.Cell() && candidate.Key() == step.Key() && candidate.Operation() == step.Operation() {
			return true
		}
	}
	return false
}

func operationRecord(operation apply.Operation) registry.Operation {
	if operation == apply.Ensure() {
		return registry.OperationEnsure
	}
	if operation == apply.RemoveOp() {
		return registry.OperationRemove
	}
	return registry.OperationInspect
}

var _ service.GroupReconciler = (*Controller)(nil)

// Activator exposes the ordinary apply-cell path without creating a second
// implementation authority. It delegates to the same controller used by the
// selection-wide reconciler.
type Activator struct{ controller *Controller }

func (c *Controller) Activator() *Activator { return &Activator{controller: c} }
func (a *Activator) StrategyKind() activation.StrategyKind {
	return activation.NativePluginKindValue()
}
func (a *Activator) Inspect(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return a.controller.inspectCell(ctx, source, key, act, prior)
}
func (a *Activator) Ensure(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior *registry.Record) (apply.Outcome, error) {
	return a.controller.ensureCell(ctx, source, key, act, prior)
}
func (a *Activator) Remove(ctx context.Context, source apply.Source, key registry.Key, act activation.ComponentActivation, prior registry.Record) (apply.Outcome, error) {
	return a.controller.removeCell(ctx, source, key, act, prior)
}

var _ apply.Activator = (*Activator)(nil)
