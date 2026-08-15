package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
	target "github.com/dayvidpham/pasture/internal/target/claudecode"
)

func TestContractBindsImmutableComponentsAndReviewedRange(t *testing.T) {
	t.Parallel()
	descriptor, err := target.Descriptor()
	require.NoError(t, err)
	contract, err := Contract(descriptor)
	require.NoError(t, err)

	assert.Equal(t, ir.HarnessClaudeCode, contract.Harness())
	assert.Equal(t, "2.1.210", contract.HostVersions().Min().String())
	assert.Equal(t, "2.2.0-0", contract.HostVersions().Max().String())
	assert.True(t, contract.HostVersions().Allows(mustHost(t, "2.1.210")))
	assert.True(t, contract.HostVersions().Allows(mustHost(t, "2.1.233")))
	assert.False(t, contract.HostVersions().Allows(mustHost(t, "2.1.209")))
	assert.False(t, contract.HostVersions().Allows(mustHost(t, "2.2.0")))
	assert.Equal(t, "claude --version", contract.VersionProbe().String())

	wantPackages := []string{"pasture-skills", "pasture-agents", "pasture-hooks"}
	for i, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		desc, _ := activation.NewComponentDescriptor(cc)
		act, lookupErr := activation.LookupComponentActivation(contract, desc)
		require.NoError(t, lookupErr)
		plugin, ok := act.Strategy().(activation.NativePlugin)
		require.True(t, ok)
		assert.Equal(t, wantPackages[i], plugin.Package())
		assert.Contains(t, commandStrings(plugin.Managers()), "claude plugin install "+selector(wantPackages[i])+" --scope user")
		assert.Contains(t, commandStrings(plugin.Managers()), "claude plugin uninstall "+selector(wantPackages[i])+" --scope user")
	}
}

func TestClaudeGroupIsolatesEveryCellAndSiblingCombination(t *testing.T) {
	t.Parallel()
	for mask := 0; mask < 8; mask++ {
		mask := mask
		t.Run(fmt.Sprintf("desired-%03b", mask), func(t *testing.T) {
			t.Parallel()
			host := newHost(true, exactLegacy())
			controller := mustController(t, host)
			request := groupRequest(t, mask, nil)
			plan, err := controller.PlanSelection(context.Background(), request)
			require.NoError(t, err)
			require.True(t, plan.Handled())
			actions := executeGroup(t, controller, request, plan)

			assert.NotContains(t, host.plugins, selector(LegacyPackage))
			for i, extension := range cell.CanonicalExtensions() {
				cc, _ := cell.New(ir.HarnessClaudeCode, extension)
				_, installed := host.plugins[selector(packageFor(extension))]
				assert.Equal(t, mask&(1<<i) != 0, installed, cc.String())
				assert.Equal(t, apply.Completed(), actions[i].Row().Status())
			}
			wantMutations := make([]string, 0, 5)
			if mask != 0 {
				for i, extension := range cell.CanonicalExtensions() {
					if mask&(1<<i) != 0 {
						wantMutations = append(wantMutations, "marketplace update")
						wantMutations = append(wantMutations, "install "+selector(packageFor(extension)))
					}
				}
			}
			wantMutations = append(wantMutations, "uninstall "+selector(LegacyPackage))
			assert.Equal(t, wantMutations, host.mutations)
		})
	}
}

func TestClaudeGroupEachCellWorksWithSiblingsAbsent(t *testing.T) {
	t.Parallel()
	for i, extension := range cell.CanonicalExtensions() {
		i, extension := i, extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			host := newHost(false)
			controller := mustController(t, host)
			request := groupRequest(t, 1<<i, nil)
			plan, err := controller.PlanSelection(context.Background(), request)
			require.NoError(t, err)
			actions := executeGroup(t, controller, request, plan)
			require.Len(t, host.plugins, 1)
			assert.Contains(t, host.plugins, selector(packageFor(extension)))
			for _, action := range actions {
				assert.NotEqual(t, apply.Failed(), action.Row().Status())
			}
		})
	}
}

func TestExactExternalMatchStaysExternalAndIsNeverRemoved(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactSplit("pasture-skills"))
	controller := mustController(t, host)
	request := groupRequest(t, 0, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	actions := executeGroup(t, controller, request, plan)

	assert.Contains(t, host.plugins, selector("pasture-skills"))
	assert.Empty(t, host.mutations)
	assert.Equal(t, apply.ManagementExternal, actions[0].Row().Management())
	record, ok := actions[0].Record()
	require.True(t, ok)
	assert.False(t, record.Managed())
}

func TestLegacyMigrationPreservesExternalSplitPlugins(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactLegacy(), exactSplit("pasture-hooks"))
	controller := mustController(t, host)
	request := groupRequest(t, 0, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	facts := executeGroup(t, controller, request, plan)

	assert.NotContains(t, host.plugins, selector(LegacyPackage))
	assert.Contains(t, host.plugins, selector("pasture-hooks"), "an exact split install without managed authority remains external")
	assert.Equal(t, []string{"uninstall " + selector(LegacyPackage)}, host.mutations)
	hooks, ok := facts[indexOfExtension(artifact.ExtensionHooks)].Record()
	require.True(t, ok)
	assert.False(t, hooks.Managed())
	assert.Equal(t, apply.ManagementExternal, facts[indexOfExtension(artifact.ExtensionHooks)].Row().Management())
}

func TestNearMatchWrongScopeAndDuplicateFailBeforeMutation(t *testing.T) {
	t.Parallel()
	cases := map[string][]pluginRow{
		"wrong scope":  {func() pluginRow { row := exactLegacy(); row.Scope = "project"; return row }()},
		"near version": {func() pluginRow { row := exactLegacy(); version := "0.0.5"; row.Version = &version; return row }()},
		"duplicate":    {exactLegacy(), exactLegacy()},
		"unknown": {func() pluginRow {
			row := exactLegacy()
			row.ID, row.Name = "pasture-other@aura-plugins", "pasture-other"
			return row
		}()},
	}
	for name, rows := range cases {
		name, rows := name, rows
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			host := newHost(true, rows...)
			controller := mustController(t, host)
			_, err := controller.PlanSelection(context.Background(), groupRequest(t, 7, nil))
			require.Error(t, err)
			assert.Empty(t, host.mutations)
		})
	}
}

func TestFailureThenOrdinaryResumeConvergesWithoutRollback(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactLegacy())
	host.failOnce = "install " + selector("pasture-agents")
	controller := mustController(t, host)
	request := groupRequest(t, 7, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	actions := executeGroup(t, controller, request, plan)

	assert.Equal(t, apply.Completed(), actions[0].Row().Status())
	assert.Equal(t, apply.Failed(), actions[1].Row().Status())
	assert.Equal(t, apply.Completed(), actions[2].Row().Status(), "complete live facts remain settled; the service owns result-level stop attribution")
	assert.Contains(t, host.plugins, selector("pasture-skills"), "earlier verified effect is retained")
	assert.Contains(t, host.plugins, selector(LegacyPackage), "failure does not roll back or prematurely remove the monolith")

	plan, err = controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	actions = executeGroup(t, controller, request, plan)
	for _, action := range actions {
		assert.Equal(t, apply.Completed(), action.Row().Status())
	}
	assert.NotContains(t, host.plugins, selector(LegacyPackage))
	assert.Contains(t, host.plugins, selector("pasture-skills"))
	assert.Contains(t, host.plugins, selector("pasture-agents"))
	assert.Contains(t, host.plugins, selector("pasture-hooks"))
	assert.NotContains(t, host.mutations, "uninstall "+selector("pasture-skills"), "resume never rolls back an earlier success")
}

func TestApplyCellRejectsLegacyBeforeMutation(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactLegacy())
	controller := mustController(t, host)
	cc, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	err := controller.PreflightCell(context.Background(), service.GroupCell{Cell: cc, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource(), Activation: activations(t), Prior: map[cell.Cell]registry.Record{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exhaustive")
	assert.Empty(t, host.mutations)
}

func TestOptionalAllFalseUnavailableProbePreservesWithoutMutation(t *testing.T) {
	t.Parallel()
	runner := runnerFunc(func(context.Context, activation.CommandSchema) (CommandResult, error) {
		return CommandResult{}, errors.New("claude executable is unavailable")
	})
	controller, err := NewController(runner, manifestFixture{})
	require.NoError(t, err)
	request := groupRequest(t, 0, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	actions := executeGroup(t, controller, request, plan)
	require.Len(t, actions, 3)
	for _, action := range actions {
		assert.Equal(t, apply.Completed(), action.Row().Status())
		assert.Equal(t, registry.ObservationUnknown, action.Row().Observation())
		_, ok := action.Record()
		assert.False(t, ok)
		assert.Contains(t, action.Row().Diagnostic(), "no state was claimed or mutated")
	}
}

func TestRequiredOutOfRangeHostFailsBeforeMutation(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	host.version = "2.2.0"
	controller := mustController(t, host)
	_, err := controller.PlanSelection(context.Background(), groupRequest(t, 1, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviewed range")
	assert.Empty(t, host.mutations)
}

func TestCodecFixturesAndUnknownFields(t *testing.T) {
	t.Parallel()
	marketBytes := fixture(t, "marketplaces.json")
	markets, err := decodeMarketplaces(marketBytes)
	require.NoError(t, err)
	require.Len(t, markets, 1)
	assert.Equal(t, marketplaceSourceGitHub, markets[0].Source)
	assert.Equal(t, MarketplaceRepo, markets[0].Repo)
	variants, err := decodeMarketplaces(fixture(t, "marketplace-source-variants.json"))
	require.NoError(t, err)
	require.Len(t, variants, 4)
	assert.Equal(t, []marketplaceSource{marketplaceSourceGitHub, marketplaceSourceGit, marketplaceSourceURL, marketplaceSourceDirectory}, []marketplaceSource{variants[0].Source, variants[1].Source, variants[2].Source, variants[3].Source})

	for _, name := range []string{"empty.json", "available.json", "exact-splits.json", "exact-monolith.json"} {
		rows, decodeErr := decodePlugins(fixture(t, name))
		require.NoError(t, decodeErr, name)
		if name == "empty.json" || name == "available.json" {
			assert.Empty(t, rows)
		} else {
			assert.NotEmpty(t, rows)
		}
	}
	_, err = decodePlugins([]byte(`{"installed":[],"available":[],"future":true}`))
	require.Error(t, err)
	invalid := []string{
		`{"installed":[{"pluginId":"pasture-skills@aura-plugins","scope":"user","enabled":true,"installPath":"/tmp/x","installedAt":"x","lastUpdated":"x"}],"available":[]}`,
		`{"installed":[{"id":"pasture-skills@aura-plugins","scope":"user","enabled":true,"installPath":"/tmp/x","installedAt":"x","lastUpdated":"x","source":"forbidden"}],"available":[]}`,
		`{"installed":[],"available":[{"pluginId":"pasture-skills@other","name":"pasture-skills","marketplaceName":"aura-plugins","source":"./x"}]}`,
		`{"installed":[],"available":[{"pluginId":"pasture-skills@aura-plugins","name":"pasture-skills","marketplaceName":"aura-plugins","source":"./x"},{"pluginId":"pasture-skills@aura-plugins","name":"pasture-skills","marketplaceName":"aura-plugins","source":"./x"}]}`,
	}
	for _, input := range invalid {
		_, decodeErr := decodePlugins([]byte(input))
		require.Error(t, decodeErr, input)
	}
}

func commandStrings(commands []activation.CommandSchema) []string {
	out := make([]string, len(commands))
	for i, cmd := range commands {
		out[i] = cmd.String()
	}
	return out
}

func mustHost(t *testing.T, value string) runtime.HostVersion {
	t.Helper()
	host, err := runtime.ParseHostVersion(value)
	require.NoError(t, err)
	return host
}

func mustController(t *testing.T, host *fixtureHost) *Controller {
	t.Helper()
	controller, err := NewController(host, manifestFixture{})
	require.NoError(t, err)
	return controller
}

func activations(t *testing.T) map[cell.Cell]activation.ComponentActivation {
	t.Helper()
	descriptor, err := target.Descriptor()
	require.NoError(t, err)
	contract, err := Contract(descriptor)
	require.NoError(t, err)
	result := map[cell.Cell]activation.ComponentActivation{}
	for _, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		desc, _ := activation.NewComponentDescriptor(cc)
		act, lookupErr := activation.LookupComponentActivation(contract, desc)
		require.NoError(t, lookupErr)
		result[cc] = act
	}
	return result
}

func groupRequest(t *testing.T, mask int, prior map[cell.Cell]registry.Record) service.GroupSelection {
	t.Helper()
	states := map[cell.Cell]bool{}
	for _, cc := range cell.CanonicalCells() {
		states[cc] = false
	}
	for i, extension := range cell.CanonicalExtensions() {
		cc, _ := cell.New(ir.HarnessClaudeCode, extension)
		states[cc] = mask&(1<<i) != 0
	}
	sel, err := selection.New(states)
	require.NoError(t, err)
	if prior == nil {
		prior = map[cell.Cell]registry.Record{}
	}
	return service.GroupSelection{Selection: sel, Scope: apply.GlobalScope(), Source: apply.InstallerSource(), Prior: prior, Activation: activations(t)}
}

func executeGroup(t *testing.T, controller *Controller, request service.GroupSelection, plan service.GroupPlan) []service.GroupAction {
	t.Helper()
	var actions []service.GroupAction
	for _, step := range plan.Actions() {
		executeErr := controller.ExecuteAction(context.Background(), request, plan, step)
		facts, err := controller.InspectAction(context.Background(), request, plan, step, executeErr)
		require.NoError(t, err)
		actions = facts.Actions()
		if executeErr != nil {
			break
		}
	}
	return actions
}

type manifestFixture struct{}

func (manifestFixture) ReadPluginManifest(path string) ([]byte, error) {
	name := filepath.Base(filepath.Dir(path))
	if name == "" {
		return nil, errors.New("manifest path has no package")
	}
	return json.Marshal(map[string]string{"name": name, "version": LegacyVersion})
}

type runnerFunc func(context.Context, activation.CommandSchema) (CommandResult, error)

func (f runnerFunc) Run(ctx context.Context, schema activation.CommandSchema) (CommandResult, error) {
	return f(ctx, schema)
}

type fixtureHost struct {
	mu                         sync.Mutex
	version                    string
	marketplace                bool
	plugins                    map[string]pluginRow
	mutations                  []string
	failOnce                   string
	mutateThenError            string
	failNextProbeAfterMutation bool
}

func newHost(marketplace bool, rows ...pluginRow) *fixtureHost {
	host := &fixtureHost{version: "2.1.233", marketplace: marketplace, plugins: map[string]pluginRow{}}
	for _, row := range rows {
		key := row.ID
		if _, duplicate := host.plugins[key]; duplicate {
			key += "#duplicate"
		}
		host.plugins[key] = row
	}
	return host
}

func (h *fixtureHost) Run(_ context.Context, schema activation.CommandSchema) (CommandResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := schema.Args()
	joined := strings.Join(args, " ")
	switch joined {
	case "--version":
		return CommandResult{Stdout: []byte(h.version + " (Claude Code)\n")}, nil
	case "plugin marketplace list --json":
		if !h.marketplace {
			return CommandResult{Stdout: []byte("[]")}, nil
		}
		return CommandResult{Stdout: fixtureNoTest("marketplaces.json")}, nil
	case "plugin list --available --json":
		if h.failNextProbeAfterMutation && len(h.mutations) > 0 {
			h.failNextProbeAfterMutation = false
			return CommandResult{}, errors.New("injected post-mutation probe failure")
		}
		installed := make([]installedPluginWire, 0, len(h.plugins))
		for _, row := range h.plugins {
			enabled := row.Enabled
			installed = append(installed, installedPluginWire{ID: row.ID, Version: row.Version, Scope: row.Scope, Enabled: &enabled, InstallPath: row.InstallPath, InstalledAt: "2026-08-14T00:00:00Z", LastUpdated: "2026-08-14T00:00:00Z"})
		}
		data, _ := json.Marshal(pluginListWire{Installed: installed, Available: []availablePluginWire{}})
		return CommandResult{Stdout: data}, nil
	}
	mutation := mutationName(args)
	if mutation == "" {
		return CommandResult{}, fmt.Errorf("unexpected command %s", schema)
	}
	h.mutations = append(h.mutations, mutation)
	if h.failOnce == mutation {
		h.failOnce = ""
		return CommandResult{}, errors.New("injected native action failure")
	}
	parts := strings.SplitN(mutation, " ", 2)
	switch parts[0] {
	case "marketplace":
		h.marketplace = true
	case "install", "update":
		pkg := strings.TrimSuffix(parts[1], "@"+MarketplaceName)
		h.plugins[parts[1]] = exactSplit(pkg)
	case "uninstall":
		delete(h.plugins, parts[1])
	}
	if h.mutateThenError == mutation {
		h.mutateThenError = ""
		return CommandResult{}, errors.New("injected mutation-then-error")
	}
	return CommandResult{}, nil
}

func mutationName(args []string) string {
	joined := strings.Join(args, " ")
	if joined == "plugin marketplace update "+MarketplaceName {
		return "marketplace update"
	}
	if joined == "plugin marketplace add "+MarketplaceRepo+" --scope user" {
		return "marketplace add"
	}
	for _, verb := range []string{"install", "update", "uninstall"} {
		prefix := "plugin " + verb + " "
		if strings.HasPrefix(joined, prefix) && strings.HasSuffix(joined, " --scope user") {
			return verb + " " + strings.TrimSuffix(strings.TrimPrefix(joined, prefix), " --scope user")
		}
	}
	return ""
}

func exactLegacy() pluginRow { return exactSplit(LegacyPackage) }

func exactSplit(pkg string) pluginRow {
	version := LegacyVersion
	return pluginRow{ID: selector(pkg), Name: pkg, Marketplace: MarketplaceName, Version: &version, Scope: "user", Enabled: true, InstallPath: "/isolated-home/.claude/plugins/cache/aura-plugins/" + pkg + "/" + version}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "install", "global", "claude-code", name))
	require.NoError(t, err)
	return data
}

func fixtureNoTest(name string) []byte {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "install", "global", "claude-code", name))
	if err != nil {
		panic(err)
	}
	return data
}

func TestControllerImplementsProductionInterfaces(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	controller := mustController(t, host)
	assert.True(t, reflect.TypeOf(controller).Implements(reflect.TypeOf((*service.GroupReconciler)(nil)).Elem()))
	assert.True(t, reflect.TypeOf(controller.Activator()).Implements(reflect.TypeOf((*apply.Activator)(nil)).Elem()))
}

func TestServiceProductionPathPersistsExactScopedClaudeFact(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	controller := mustController(t, host)
	store := &memoryRegistry{store: registry.New()}
	contracts := map[ir.HarnessID]activation.ActivationContract{}
	descriptor, err := target.Descriptor()
	require.NoError(t, err)
	contracts[ir.HarnessClaudeCode], err = Contract(descriptor)
	require.NoError(t, err)
	contracts[ir.HarnessOpenCode] = fixtureDirectContract(t, ir.HarnessOpenCode, descriptor.Skills().Bundle())
	contracts[ir.HarnessCodex] = fixtureDirectContract(t, ir.HarnessCodex, descriptor.Skills().Bundle())
	sut, err := service.New(service.Config{Registry: store, Contracts: contracts, Activators: []apply.Activator{controller.Activator(), fixtureDirectActivator(t)}, Group: controller})
	require.NoError(t, err)

	result, err := sut.ApplySelection(context.Background(), service.SelectionRequest{Selection: groupRequest(t, 1, nil).Selection, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.True(t, result.OK(), "%#v", result.Rows())
	require.Len(t, result.Rows(), 3)
	assert.Equal(t, "claude-code.skills", result.Rows()[0].Cell().String())
	assert.Equal(t, apply.Completed(), result.Rows()[0].Status())
	key, _ := registry.GlobalKey(result.Rows()[0].Cell())
	record, ok := store.store.Lookup(key)
	require.True(t, ok)
	assert.True(t, record.Managed())
	assert.Equal(t, registry.ScopeGlobal, record.Key().Scope())
	assert.Equal(t, selector("pasture-skills"), record.Selector().String())
	assert.Equal(t, descriptor.Skills().Bundle().ID(), record.ArtifactID())
	assert.Equal(t, registry.ObservationInstalled, record.Observation())
}

func TestServiceFailureResumeKeepsFactsAndConverges(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactLegacy())
	host.failOnce = "install " + selector("pasture-agents")
	sut, store := serviceFixture(t, host)
	request := service.SelectionRequest{Selection: groupRequest(t, 7, nil).Selection, Scope: apply.GlobalScope(), Source: apply.InstallerSource()}

	first, err := sut.ApplySelection(context.Background(), request)
	require.NoError(t, err)
	require.False(t, first.OK())
	require.Len(t, first.Rows(), 3)
	assert.Equal(t, apply.Completed(), first.Rows()[0].Status())
	assert.Equal(t, apply.Failed(), first.Rows()[1].Status())
	assert.Equal(t, apply.Completed(), first.Rows()[2].Status())
	skillsKey, _ := registry.GlobalKey(first.Rows()[0].Cell())
	skills, ok := store.store.Lookup(skillsKey)
	require.True(t, ok)
	assert.Equal(t, registry.ObservationInstalled, skills.Observation())
	assert.Contains(t, host.plugins, selector(LegacyPackage))

	second, err := sut.ApplySelection(context.Background(), request)
	require.NoError(t, err)
	require.True(t, second.OK())
	for _, row := range second.Rows() {
		assert.Equal(t, apply.Completed(), row.Status())
		key, keyErr := registry.GlobalKey(row.Cell())
		require.NoError(t, keyErr)
		record, exists := store.store.Lookup(key)
		require.True(t, exists)
		assert.True(t, record.Managed())
		assert.Equal(t, registry.ObservationInstalled, record.Observation())
	}
	assert.NotContains(t, host.plugins, selector(LegacyPackage))
}

func serviceFixture(t *testing.T, host *fixtureHost) (*service.Service, *memoryRegistry) {
	t.Helper()
	controller := mustController(t, host)
	store := &memoryRegistry{store: registry.New()}
	contracts := map[ir.HarnessID]activation.ActivationContract{}
	descriptor, err := target.Descriptor()
	require.NoError(t, err)
	contracts[ir.HarnessClaudeCode], err = Contract(descriptor)
	require.NoError(t, err)
	contracts[ir.HarnessOpenCode] = fixtureDirectContract(t, ir.HarnessOpenCode, descriptor.Skills().Bundle())
	contracts[ir.HarnessCodex] = fixtureDirectContract(t, ir.HarnessCodex, descriptor.Skills().Bundle())
	sut, err := service.New(service.Config{Registry: store, Contracts: contracts, Activators: []apply.Activator{controller.Activator(), fixtureDirectActivator(t)}, Group: controller})
	require.NoError(t, err)
	return sut, store
}

type memoryRegistry struct {
	mu        sync.Mutex
	store     registry.Store
	failSaves int
}

func (r *memoryRegistry) Load(context.Context) (registry.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store, nil
}
func (r *memoryRegistry) Save(_ context.Context, store registry.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSaves > 0 {
		r.failSaves--
		return errors.New("injected atomic registry save failure")
	}
	r.store = store
	return nil
}

func fixtureDirectActivator(t *testing.T) *apply.DirectFileActivator {
	t.Helper()
	policies := make([]apply.DirectFilePolicy, 0, 6)
	for _, harness := range []ir.HarnessID{ir.HarnessOpenCode, ir.HarnessCodex} {
		for _, extension := range cell.CanonicalExtensions() {
			cc, err := cell.New(harness, extension)
			require.NoError(t, err)
			policy, err := apply.NewDirectFilePolicy(cc, func(apply.DirectFileRequest) error { return nil }, apply.PassThroughDecoration())
			require.NoError(t, err)
			policies = append(policies, policy)
		}
	}
	activator, err := apply.NewDirectFileActivator(policies...)
	require.NoError(t, err)
	return activator
}

func fixtureDirectContract(t *testing.T, harness ir.HarnessID, bundle artifact.Bundle) activation.ActivationContract {
	t.Helper()
	acts := make([]activation.ComponentActivation, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		cc, err := cell.New(harness, extension)
		require.NoError(t, err)
		strategy, err := activation.NewDirectFile(bundle, "/isolated/"+cc.String())
		require.NoError(t, err)
		act, err := activation.NewComponentActivation(cc, strategy)
		require.NoError(t, err)
		acts = append(acts, act)
	}
	exhaustive, err := activation.NewExhaustiveComponentActivations(acts[0], acts[1], acts[2])
	require.NoError(t, err)
	id, err := activation.NewActivationContractID(string(harness) + "/fixture@1")
	require.NoError(t, err)
	versions, err := runtime.NewExactVersion(mustHost(t, "1.0.0"))
	require.NoError(t, err)
	contract, err := activation.NewActivationContract(id, harness, versions, command("fixture", "--version"), exhaustive)
	require.NoError(t, err)
	return contract
}

var _ = artifact.HarnessClaudeCode

func TestPlanSelectionIsStatelessAndOrdersBoundedTypedActions(t *testing.T) {
	t.Parallel()
	host := newHost(true, exactLegacy(), exactSplit("pasture-hooks"))
	controller := mustController(t, host)
	request := groupRequest(t, 3, nil)
	first, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	second, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, first.Actions(), second.Actions())
	require.Len(t, first.ResultCells(), 3)
	kinds := make([]service.GroupActionKind, 0, len(first.Actions()))
	controls := make([]string, 0, len(first.Actions()))
	for _, action := range first.Actions() {
		kinds = append(kinds, action.Kind())
		controls = append(controls, action.ControlCell().String())
	}
	assert.Equal(t, []service.GroupActionKind{service.EnsureCellGroupAction(), service.EnsureCellGroupAction(), service.RemoveSharedGroupAction()}, kinds)
	assert.Equal(t, []string{"claude-code.skills", "claude-code.agents", "claude-code.skills"}, controls)
}

func TestOptionalAmbiguousAllFalsePreservesWithoutMutationOrInventory(t *testing.T) {
	t.Parallel()
	row := exactLegacy()
	row.Scope = "project"
	host := newHost(true, row)
	controller := mustController(t, host)
	request := groupRequest(t, 0, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, plan.Actions(), 1)
	assert.Equal(t, service.InspectGroupAction(), plan.Actions()[0].Kind())
	facts := executeGroup(t, controller, request, plan)
	require.Len(t, facts, 3)
	for _, fact := range facts {
		assert.Equal(t, apply.Completed(), fact.Row().Status())
		assert.Equal(t, registry.ObservationUnknown, fact.Row().Observation())
		_, recorded := fact.Record()
		assert.False(t, recorded)
	}
	assert.Empty(t, host.mutations)

	required := groupRequest(t, 1, nil)
	_, err = controller.PlanSelection(context.Background(), required)
	require.Error(t, err)
	assert.Empty(t, host.mutations)
}

func TestExactExternalApplyCellReturnsCoherentUnmanagedRecordForEveryCell(t *testing.T) {
	t.Parallel()
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			host := newHost(true, exactSplit(packageFor(extension)))
			controller := mustController(t, host)
			cc, _ := cell.New(ir.HarnessClaudeCode, extension)
			key, _ := registry.GlobalKey(cc)
			out, err := controller.Activator().Ensure(context.Background(), apply.InstallerSource(), key, activations(t)[cc], nil)
			require.NoError(t, err)
			require.NotNil(t, out.Record)
			assert.False(t, out.Record.Managed())
			assert.Equal(t, registry.ObservationInstalled, out.Record.Observation())
			assert.Equal(t, controller.versions[cc], out.Record.Version())
			assert.Empty(t, host.mutations)
		})
	}
}

func TestManagedAbsentTombstoneThenExternalReinstallNeverAuthorizesMutation(t *testing.T) {
	t.Parallel()
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			host := newHost(true, exactSplit(packageFor(extension)))
			controller := mustController(t, host)
			cc, _ := cell.New(ir.HarnessClaudeCode, extension)
			prior := exactRecord(t, controller, cc, true, registry.ObservationAbsent, registry.SourceInstaller)
			request := groupRequest(t, 0, map[cell.Cell]registry.Record{cc: prior})
			plan, err := controller.PlanSelection(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, service.InspectGroupAction(), plan.Actions()[0].Kind())
			facts := executeGroup(t, controller, request, plan)
			record, ok := facts[indexOfExtension(extension)].Record()
			require.True(t, ok)
			assert.False(t, record.Managed())
			assert.Empty(t, host.mutations)
		})
	}
}

func TestStrictPriorAuthorityRejectsEveryIdentityMismatchBeforeMutation(t *testing.T) {
	t.Parallel()
	cc, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	cases := []struct {
		name string
		make func(*testing.T, *Controller) registry.Record
	}{
		{"source", func(t *testing.T, c *Controller) registry.Record {
			return exactRecord(t, c, cc, true, registry.ObservationInstalled, registry.SourceHomeManager)
		}},
		{"version", func(t *testing.T, c *Controller) registry.Record {
			return recordWith(t, c, cc, registry.SourceInstaller, true, registry.ObservationInstalled, "9.9.9", selector("pasture-skills"), c.artifacts[cc], registry.TrustNotApplicable)
		}},
		{"selector", func(t *testing.T, c *Controller) registry.Record {
			return recordWith(t, c, cc, registry.SourceInstaller, true, registry.ObservationInstalled, c.versions[cc].String(), selector("pasture-agents"), c.artifacts[cc], registry.TrustNotApplicable)
		}},
		{"bundle", func(t *testing.T, c *Controller) registry.Record {
			var zero artifact.BundleID
			return recordWith(t, c, cc, registry.SourceInstaller, true, registry.ObservationInstalled, c.versions[cc].String(), selector("pasture-skills"), zero, registry.TrustNotApplicable)
		}},
		{"trust", func(t *testing.T, c *Controller) registry.Record {
			return recordWith(t, c, cc, registry.SourceInstaller, true, registry.ObservationInstalled, c.versions[cc].String(), selector("pasture-skills"), c.artifacts[cc], registry.TrustPending)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newHost(true, exactSplit("pasture-skills"))
			controller := mustController(t, host)
			_, err := controller.PlanSelection(context.Background(), groupRequest(t, 1, map[cell.Cell]registry.Record{cc: tc.make(t, controller)}))
			require.Error(t, err)
			assert.Empty(t, host.mutations)
		})
	}
}

func TestRegistrySaveFailureLeavesNoAuthorityAndFreshRetryPreservesExternal(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	controller := mustController(t, host)
	store := &memoryRegistry{store: registry.New(), failSaves: 1}
	sut := serviceWith(t, controller, store)
	request := service.SelectionRequest{Selection: groupRequest(t, 1, nil).Selection, Scope: apply.GlobalScope(), Source: apply.InstallerSource()}
	first, err := sut.ApplySelection(context.Background(), request)
	require.NoError(t, err)
	assert.False(t, first.OK())
	assert.Contains(t, host.plugins, selector("pasture-skills"))
	key, _ := registry.GlobalKey(first.Rows()[0].Cell())
	_, exists := store.store.Lookup(key)
	assert.False(t, exists)

	second, err := sut.ApplySelection(context.Background(), request)
	require.NoError(t, err)
	require.True(t, second.OK(), "%#v", second.Rows())
	record, exists := store.store.Lookup(key)
	require.True(t, exists)
	assert.False(t, record.Managed())
	assert.Equal(t, []string{"marketplace add", "install " + selector("pasture-skills")}, host.mutations)
}

func TestMutationThenErrorReturnsStrongestLiveFactsAndPersistsFailedRecord(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	host.mutateThenError = "install " + selector("pasture-skills")
	controller := mustController(t, host)
	request := groupRequest(t, 1, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	step := plan.Actions()[0]
	executeErr := controller.ExecuteAction(context.Background(), request, plan, step)
	require.Error(t, executeErr)
	facts, err := controller.InspectAction(context.Background(), request, plan, step, executeErr)
	require.NoError(t, err)
	skills := facts.Actions()[0]
	assert.Equal(t, apply.Failed(), skills.Row().Status())
	assert.Equal(t, registry.ObservationInstalled, skills.Row().Observation())
	record, ok := skills.Record()
	require.True(t, ok)
	assert.True(t, record.Managed())
	assert.Equal(t, registry.OutcomeFailed, record.LastOutcome())
	assert.Contains(t, host.plugins, selector("pasture-skills"))
}

func TestSuccessThenPostProbeFailureEmitsNoStaleFactsAndCombinesCause(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	host.failNextProbeAfterMutation = true
	controller := mustController(t, host)
	request := groupRequest(t, 1, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	step := plan.Actions()[0]
	require.NoError(t, controller.ExecuteAction(context.Background(), request, plan, step))
	facts, err := controller.InspectAction(context.Background(), request, plan, step, nil)
	require.Error(t, err)
	for _, fact := range facts.Actions() {
		assert.False(t, fact.Row().Cell().IsValid())
	}
	assert.Contains(t, err.Error(), "post-action")
	assert.Contains(t, err.Error(), "injected post-mutation probe failure")
}

func TestClosePlanSupportsEveryTerminalPathWithoutNativeIO(t *testing.T) {
	t.Parallel()
	host := newHost(false)
	controller := mustController(t, host)
	request := groupRequest(t, 0, nil)
	plan, err := controller.PlanSelection(context.Background(), request)
	require.NoError(t, err)
	before := append([]string(nil), host.mutations...)
	for stage := service.GroupTerminalPlanInvalid; stage <= service.GroupTerminalSucceeded; stage++ {
		require.NoError(t, controller.ClosePlan(context.Background(), request, plan, stage))
	}
	assert.Equal(t, before, host.mutations)
}

func TestOSManifestReaderIsolatedSafety(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, ".claude-plugin")
	require.NoError(t, os.Mkdir(dir, 0o700))
	want := []byte(`{"name":"pasture-skills","version":"0.0.4"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), want, 0o600))
	got, err := (OSManifestReader{}).ReadPluginManifest(root)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	_, err = (OSManifestReader{}).ReadPluginManifest(root + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(root))
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "plugin.json")))
	require.NoError(t, os.Symlink("target", filepath.Join(dir, "plugin.json")))
	_, err = (OSManifestReader{}).ReadPluginManifest(root)
	require.Error(t, err)
}

func TestOSRunnerIsolatedSuccessCancellationAndOutputBound(t *testing.T) {
	t.Parallel()
	runner := OSRunner{}
	result, err := runner.Run(context.Background(), command("sh", "-c", "printf isolated"))
	require.NoError(t, err)
	assert.Equal(t, "isolated", string(result.Stdout))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.Run(ctx, command("sh", "-c", "sleep 10"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = runner.Run(context.Background(), command("sh", "-c", "dd if=/dev/zero bs=1048576 count=9 2>/dev/null"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

func indexOfExtension(extension artifact.Extension) int {
	for i, candidate := range cell.CanonicalExtensions() {
		if candidate == extension {
			return i
		}
	}
	return -1
}

func exactRecord(t *testing.T, controller *Controller, cc cell.Cell, managed bool, observation registry.Observation, source registry.Source) registry.Record {
	t.Helper()
	return recordWith(t, controller, cc, source, managed, observation, controller.versions[cc].String(), selector(packageFor(cc.Extension())), controller.artifacts[cc], registry.TrustNotApplicable)
}

func recordWith(t *testing.T, controller *Controller, cc cell.Cell, source registry.Source, managed bool, observation registry.Observation, versionText, selectorText string, bundle artifact.BundleID, trust registry.Trust) registry.Record {
	t.Helper()
	key, err := registry.GlobalKey(cc)
	require.NoError(t, err)
	version, err := registry.NewVersion(versionText)
	require.NoError(t, err)
	selectorValue, err := registry.NewSelector(selectorText)
	require.NoError(t, err)
	record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: source, Strategy: activation.NativePluginKindValue(), Managed: managed, ArtifactID: bundle, Version: version, Selector: selectorValue, Observation: observation, Trust: trust, LastOperation: registry.OperationInspect, LastOutcome: registry.OutcomeCompleted})
	require.NoError(t, err)
	_ = controller
	return record
}

func serviceWith(t *testing.T, controller *Controller, store *memoryRegistry) *service.Service {
	t.Helper()
	contracts := map[ir.HarnessID]activation.ActivationContract{}
	descriptor, err := target.Descriptor()
	require.NoError(t, err)
	contracts[ir.HarnessClaudeCode], err = Contract(descriptor)
	require.NoError(t, err)
	contracts[ir.HarnessOpenCode] = fixtureDirectContract(t, ir.HarnessOpenCode, descriptor.Skills().Bundle())
	contracts[ir.HarnessCodex] = fixtureDirectContract(t, ir.HarnessCodex, descriptor.Skills().Bundle())
	sut, err := service.New(service.Config{Registry: store, Contracts: contracts, Activators: []apply.Activator{controller.Activator(), fixtureDirectActivator(t)}, Group: controller})
	require.NoError(t, err)
	return sut
}
