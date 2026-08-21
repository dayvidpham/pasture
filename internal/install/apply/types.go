package apply

import (
	"context"

	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// Source identifies the controller applying desired state.
type Source uint8

const (
	SourceInvalid Source = iota
	SourceInstaller
	SourceHomeManager
)

func InstallerSource() Source   { return SourceInstaller }
func HomeManagerSource() Source { return SourceHomeManager }
func (s Source) String() string {
	switch s {
	case SourceInstaller:
		return "installer"
	case SourceHomeManager:
		return "home-manager"
	default:
		return "invalid"
	}
}
func (s Source) IsValid() bool { return s == SourceInstaller || s == SourceHomeManager }

// Scope selects one logical registry table. Project scope carries one canonical
// root, allowing the same service to be reused by project reconciliation.
type Scope struct {
	kind registry.Scope
	root registry.ProjectRoot
}

func GlobalScope() Scope { return Scope{kind: registry.ScopeGlobal} }
func ProjectScope(root registry.ProjectRoot) (Scope, error) {
	if !root.IsValid() {
		return Scope{}, cell.NewFault("apply scope construction", "canonical project root", "the project root is invalid", "internal/install/apply.ProjectScope", "constructing an apply scope", "project records could collide or enter the global table", "construct the root with registry.CanonicalProjectRoot", nil)
	}
	return Scope{kind: registry.ScopeProject, root: root}, nil
}
func (s Scope) Kind() registry.Scope              { return s.kind }
func (s Scope) ProjectRoot() registry.ProjectRoot { return s.root }
func (s Scope) IsValid() bool {
	return s.kind == registry.ScopeGlobal && !s.root.IsValid() || s.kind == registry.ScopeProject && s.root.IsValid()
}
func (s Scope) key(c cell.Cell) (registry.Key, error) {
	if s.kind == registry.ScopeProject {
		return registry.ProjectKey(s.root, c)
	}
	if s.kind == registry.ScopeGlobal {
		return registry.GlobalKey(c)
	}
	return registry.Key{}, cell.NewFault("apply scope resolution", "global or project scope", "the scope is invalid", "internal/install/apply.Scope.key", "planning a scoped cell", "the confirmed fact has no logical registry table", "use GlobalScope or ProjectScope", nil)
}

// Key resolves a validated cell into this scope's registry key.
func (s Scope) Key(c cell.Cell) (registry.Key, error) { return s.key(c) }

// Outcome is one live-inspected activator result. Record is the strongest
// confirmed fact and is persisted before execution advances to another cell.
type Outcome struct {
	Status      Status
	Observation registry.Observation
	Record      *registry.Record
	Diagnostic  string
}

// Activator is the production boundary implemented by each closed activation
// strategy. Inspect must be read-only. Ensure and Remove must return facts based
// on a live postcondition check, including when they return an action error.
type Activator interface {
	StrategyKind() activation.StrategyKind
	Inspect(context.Context, Source, registry.Key, activation.ComponentActivation, *registry.Record) (Outcome, error)
	Ensure(context.Context, Source, registry.Key, activation.ComponentActivation, *registry.Record) (Outcome, error)
	Remove(context.Context, Source, registry.Key, activation.ComponentActivation, registry.Record) (Outcome, error)
}
