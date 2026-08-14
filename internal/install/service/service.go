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
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return cell.NewFault("registry save", "private state directory", err.Error(), parent, "creating the registry parent before atomic save", "the confirmed fact cannot be persisted and later cells will not run", "create a private writable directory and retry", err)
	}
	return registry.Save(r.path, store)
}

// Config supplies the complete static activation graph and real side-effect
// seams. Contracts are copied and validated exhaustively by every operation.
type Config struct {
	Registry   Registry
	Contracts  map[ir.HarnessID]activation.ActivationContract
	Activators []apply.Activator
}

type Service struct {
	registry  Registry
	contracts map[ir.HarnessID]activation.ActivationContract
	engine    *apply.Engine
}

func New(config Config) (*Service, error) {
	if config.Registry == nil {
		return nil, cell.NewFault("installer service construction", "registry persistence", "the registry dependency is nil", "internal/install/service.New", "wiring the installer application service", "confirmed facts could not be loaded or saved", "provide FileRegistry or an atomic Registry implementation", nil)
	}
	engine, err := apply.NewEngine(config.Activators...)
	if err != nil {
		return nil, err
	}
	contracts := make(map[ir.HarnessID]activation.ActivationContract, len(config.Contracts))
	for key, contract := range config.Contracts {
		contracts[key] = contract
	}
	return &Service{registry: config.Registry, contracts: contracts, engine: engine}, nil
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
	store, err := s.load(ctx)
	if err != nil {
		return apply.Result{}, err
	}
	return s.engine.ApplySelection(ctx, request.Selection, request.Scope, request.Source, s.contracts, &store, s.registry.Save)
}
func (s *Service) ApplyCell(ctx context.Context, request CellRequest) (apply.Result, error) {
	store, err := s.load(ctx)
	if err != nil {
		return apply.Result{}, err
	}
	return s.engine.ApplyCell(ctx, request.Cell, request.Enabled, request.Scope, request.Source, s.contracts, &store, s.registry.Save)
}
func (s *Service) load(ctx context.Context) (registry.Store, error) {
	store, err := s.registry.Load(ctx)
	if err != nil {
		return registry.Store{}, cell.NewFault("installer service load", "readable authoritative installation registry", err.Error(), "internal/install/service.Service.load", "before planning an apply operation", "no activation was attempted because ownership facts could not be trusted", "repair the registry path, type, permissions, or contents, then retry", err)
	}
	return store, nil
}
