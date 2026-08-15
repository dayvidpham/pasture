// Package service is the application boundary shared by installer frontends.
// It owns registry load/save around the canonical apply engine; frontends never
// compose a second apply path or persist transient results as authority.
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
)

// Registry is the injected atomic persistence boundary. Implementations must
// make Save replace one complete registry or leave the prior registry intact.
type Registry interface {
	Load(context.Context) (registry.Store, error)
	Save(context.Context, registry.Store) error
}

// FileRegistry is the production registry implementation.
type FileRegistry struct{ path string }

func NewFileRegistry(path string) (FileRegistry, error) {
	if path == "" || !filepath.IsAbs(path) {
		return FileRegistry{}, cell.NewFault("file registry construction", "absolute registry path", fmt.Sprintf("registry path %q is empty or relative", path), "internal/install/service.NewFileRegistry", "wiring installer persistence", "working-directory changes could select another authority file", "provide the absolute installations.yaml path", nil)
	}
	return FileRegistry{path: filepath.Clean(path)}, nil
}
func (r FileRegistry) Path() string { return r.path }
func (r FileRegistry) Load(ctx context.Context) (registry.Store, error) {
	if err := ctx.Err(); err != nil {
		return registry.Store{}, cell.NewFault("registry load", "live caller context", err.Error(), r.path, "before loading installation facts", "no registry was read and no action can start", "retry with a live context", err)
	}
	if _, err := os.Lstat(filepath.Dir(r.path)); os.IsNotExist(err) {
		return registry.New(), nil
	}
	return registry.Load(r.path)
}
func (r FileRegistry) Save(ctx context.Context, store registry.Store) error {
	if err := ctx.Err(); err != nil {
		return cell.NewFault("registry save", "live caller context", err.Error(), r.path, "before saving a confirmed installation fact", "the previous registry remains authoritative", "retry with a live context after inspecting the cell", err)
	}
	parent := filepath.Dir(r.path)
	info, err := os.Lstat(parent)
	if err != nil {
		return cell.NewFault("registry save", "pre-created private state directory", err.Error(), parent, "validating the registry parent before atomic save", "no path was created and the confirmed fact cannot be persisted", "create this directory as a real private directory without symlinked components, then retry", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return cell.NewFault("registry save", "real non-symlink private state directory", fmt.Sprintf("registry parent has unsafe mode %s", info.Mode()), parent, "validating the registry parent before atomic save", "the accepted no-follow writer was not entered and no external path was changed", "replace the parent with a real private directory and retry", nil)
	}
	return registry.Save(r.path, store)
}

// Config supplies the complete static activation graph and real side-effect
// seams. Contracts are copied and validated exhaustively by every operation.
type Config struct {
	Registry   Registry
	Contracts  map[ir.HarnessID]activation.ActivationContract
	Activators []apply.Activator
	// Group is the one statically selected selection-wide native transition.
	// It never receives registry persistence and cannot create another authority.
	Group GroupReconciler
}

type Service struct {
	registry  Registry
	contracts map[ir.HarnessID]activation.ActivationContract
	engine    *engine
}

func New(config Config) (*Service, error) {
	if config.Registry == nil || isNilInterface(config.Registry) {
		return nil, cell.NewFault("installer service construction", "registry persistence", "the registry dependency is nil", "internal/install/service.New", "wiring the installer application service", "confirmed facts could not be loaded or saved", "provide FileRegistry or an atomic Registry implementation", nil)
	}
	engine, err := newEngine(config.Activators, config.Group)
	if err != nil {
		return nil, err
	}
	contracts := make(map[ir.HarnessID]activation.ActivationContract, len(config.Contracts))
	for key, contract := range config.Contracts {
		contracts[key] = contract
	}
	if err := validateDirectFilePolicies(contracts, engine); err != nil {
		return nil, err
	}
	return &Service{registry: config.Registry, contracts: contracts, engine: engine}, nil
}

func validateDirectFilePolicies(contracts map[ir.HarnessID]activation.ActivationContract, engine *engine) error {
	bindings := make([]activation.ComponentActivation, 0, len(cell.CanonicalCells()))
	for _, c := range cell.CanonicalCells() {
		contract, ok := contracts[c.Harness()]
		if !ok || !contract.IsValid() || contract.Harness() != c.Harness() {
			continue
		}
		descriptor, err := activation.NewComponentDescriptor(c)
		if err != nil {
			return err
		}
		binding, err := activation.LookupComponentActivation(contract, descriptor)
		if err != nil {
			return err
		}
		if binding.Strategy().Kind() == activation.DirectFileKindValue() {
			bindings = append(bindings, binding)
		}
	}
	activator, exists := engine.activators[activation.DirectFileKindValue()]
	if len(bindings) == 0 {
		if exists {
			return cell.NewFault("installer service construction", "no DirectFile activator without DirectFile bindings", "a DirectFile activator was registered but no cell uses that strategy", "internal/install/service.validateDirectFilePolicies", "validating strategy dispatch", "foreign policy configuration would be silently retained", "remove the unused DirectFile activator", nil)
		}
		return nil
	}
	direct, ok := activator.(*apply.DirectFileActivator)
	if !exists || !ok {
		return cell.NewFault("installer service construction", "the central DirectFile activator", "DirectFile bindings are not served by *apply.DirectFileActivator", "internal/install/service.validateDirectFilePolicies", "validating strategy dispatch", "cell policies could be bypassed by another activator", "register exactly one activator returned by apply.NewDirectFileActivator", nil)
	}
	return direct.ValidateBindings(bindings)
}

type SelectionRequest struct {
	Selection selection.Selection
	Scope     apply.Scope
	Source    apply.Source
}
type CellRequest struct {
	Cell    cell.Cell
	Enabled bool
	Scope   apply.Scope
	Source  apply.Source
}

// ApplySelection and ApplyCell deliberately share load, canonical binding,
// execution, and per-fact atomic persistence.
func (s *Service) ApplySelection(ctx context.Context, request SelectionRequest) (apply.Result, error) {
	if s == nil {
		return apply.Result{}, preplan(request.Source, "service receiver validation", "the installer service receiver is nil", "internal/install/service.Service.ApplySelection", "no registry was loaded and no action was attempted", "construct the service with service.New before applying a selection", apply.RemediationManualRepair)
	}
	store, err := s.load(ctx, request.Source)
	if err != nil {
		return apply.Result{}, err
	}
	return s.engine.applySelection(ctx, request.Selection, request.Scope, request.Source, s.contracts, &store, s.registry.Save)
}
func (s *Service) ApplyCell(ctx context.Context, request CellRequest) (apply.Result, error) {
	if s == nil {
		return apply.Result{}, preplan(request.Source, "service receiver validation", "the installer service receiver is nil", "internal/install/service.Service.ApplyCell", "no registry was loaded and no action was attempted", "construct the service with service.New before applying a cell", apply.RemediationManualRepair)
	}
	store, err := s.load(ctx, request.Source)
	if err != nil {
		return apply.Result{}, err
	}
	return s.engine.applyCell(ctx, request.Cell, request.Enabled, request.Scope, request.Source, s.contracts, &store, s.registry.Save)
}
func (s *Service) load(ctx context.Context, source apply.Source) (registry.Store, error) {
	store, err := s.registry.Load(ctx)
	if err != nil {
		return registry.Store{}, apply.NewApplyError(source, "registry-load", err.Error(), "internal/install/service.Service.load", "no activation was attempted because authoritative ownership facts could not be trusted", "repair the registry path, type, permissions, or contents, then retry", apply.RemediationManualRepair)
	}
	return store, nil
}
