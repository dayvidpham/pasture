// Package registry owns the first-shipped installation registry shared by
// global and project-local installation.
package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/install/cell"
)

// SchemaID is the only accepted registry schema. There are no legacy codecs.
const SchemaID = "pasture.install.registry/v1"

// Scope is the closed installation scope set.
type Scope uint8

const (
	ScopeInvalid Scope = iota
	ScopeGlobal
	ScopeProject
)

func (s Scope) String() string {
	switch s {
	case ScopeGlobal:
		return "global"
	case ScopeProject:
		return "project"
	default:
		return "invalid"
	}
}

// ParseScope validates a scope at a text boundary.
func ParseScope(value string) (Scope, error) {
	switch value {
	case "global":
		return ScopeGlobal, nil
	case "project":
		return ScopeProject, nil
	default:
		return ScopeInvalid, fault("scope decode", "global or project scope", fmt.Sprintf("scope %q is unknown", value), "internal/install/registry.ParseScope", "decoding registry input", "the row cannot be assigned to a logical table", "use exactly global or project", nil)
	}
}

// ProjectRoot is an absolute, clean canonical project identity. Its zero value
// is invalid. Stored roots remain valid when a previously registered project is
// later absent.
type ProjectRoot struct{ path string }

// CanonicalProjectRoot resolves an existing directory through symlinks.
func CanonicalProjectRoot(path string) (ProjectRoot, error) {
	if !filepath.IsAbs(path) {
		return ProjectRoot{}, fault("project root canonicalization", "absolute project path", fmt.Sprintf("project root %q is relative", path), path, "resolving project identity", "the same project could receive working-directory-dependent keys", "provide an absolute project directory path", nil)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ProjectRoot{}, fault("project root canonicalization", "absolute project path", fmt.Sprintf("cannot make %q absolute: %v", path, err), path, "resolving project identity", "the registry key would not be stable", "provide an accessible project directory", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ProjectRoot{}, fault("project root canonicalization", "resolvable project path", fmt.Sprintf("cannot resolve %q: %v", abs, err), abs, "resolving project identity", "symlink aliases could create duplicate projects", "repair the path and retry", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return ProjectRoot{}, fault("project root canonicalization", "existing directory", fmt.Sprintf("resolved project root %q is not an accessible directory", resolved), resolved, "validating project identity", "a non-directory cannot own project installations", "provide an existing project directory", err)
	}
	return parseStoredProjectRoot(resolved)
}

func parseStoredProjectRoot(path string) (ProjectRoot, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ProjectRoot{}, fault("project root decode", "clean absolute canonical root", fmt.Sprintf("stored project root %q is not clean and absolute", path), "internal/install/registry.parseStoredProjectRoot", "decoding a project installation key", "the project identity would be ambiguous", "replace it with the symlink-resolved absolute project root", nil)
	}
	if _, err := os.Lstat(path); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return ProjectRoot{}, fault("project root decode", "resolvable canonical root", fmt.Sprintf("stored project root %q cannot be resolved: %v", path, resolveErr), path, "validating an existing project identity", "a symlink alias could create duplicate project rows", "replace it with its symlink-resolved absolute path", resolveErr)
		}
		if resolved != path {
			return ProjectRoot{}, fault("project root decode", "symlink-resolved canonical root", fmt.Sprintf("stored project root %q resolves to %q", path, resolved), path, "validating an existing project identity", "the alias could create a second key for one project", fmt.Sprintf("replace the root with %q", resolved), nil)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return ProjectRoot{}, fault("project root decode", "inspectable retained project directory", fmt.Sprintf("stored project root %q cannot be inspected: %v", path, statErr), path, "validating an existing project identity", "the stored identity type cannot be established", "repair path permissions and retry", statErr)
		}
		if !info.IsDir() {
			return ProjectRoot{}, fault("project root decode", "existing directory or absent retained root", fmt.Sprintf("stored project root %q is an existing non-directory", path), path, "validating a retained project identity", "a file cannot own project installations", "remove the conflicting entry or replace the root with the canonical project directory", nil)
		}
	} else if !os.IsNotExist(err) {
		return ProjectRoot{}, fault("project root decode", "inspectable canonical root", fmt.Sprintf("stored project root %q cannot be inspected: %v", path, err), path, "validating an existing project identity", "canonical identity cannot be established", "repair path permissions and retry", err)
	}
	return ProjectRoot{path: path}, nil
}

func (r ProjectRoot) String() string { return r.path }
func (r ProjectRoot) IsValid() bool {
	return r.path != "" && filepath.IsAbs(r.path) && filepath.Clean(r.path) == r.path
}

// Key is the closed union of global[cell] and project[root,cell].
type Key struct {
	scope Scope
	root  ProjectRoot
	cell  cell.Cell
}

func GlobalKey(c cell.Cell) (Key, error)                    { return newKey(ScopeGlobal, ProjectRoot{}, c) }
func ProjectKey(root ProjectRoot, c cell.Cell) (Key, error) { return newKey(ScopeProject, root, c) }

func newKey(scope Scope, root ProjectRoot, c cell.Cell) (Key, error) {
	if !c.IsValid() {
		return Key{}, fault("registry key construction", "valid installation cell", "the cell is invalid", "internal/install/registry.newKey", "constructing a registry key", "the row cannot be ordered", "construct the cell with cell.New", nil)
	}
	if scope == ScopeGlobal && root.IsValid() {
		return Key{}, fault("registry key construction", "global key without project root", "a global key contains a project root", "internal/install/registry.newKey", "constructing a global key", "global and project tables would be conflated", "use GlobalKey without a root", nil)
	}
	if scope == ScopeProject && !root.IsValid() {
		return Key{}, fault("registry key construction", "project key with canonical root", "a project key has no canonical root", "internal/install/registry.newKey", "constructing a project key", "same-cell projects could collide", "construct the root with CanonicalProjectRoot", nil)
	}
	if scope != ScopeGlobal && scope != ScopeProject {
		return Key{}, fault("registry key construction", "known scope", "the scope is invalid", "internal/install/registry.newKey", "constructing a registry key", "the row has no logical table", "use ScopeGlobal or ScopeProject", nil)
	}
	return Key{scope: scope, root: root, cell: c}, nil
}

func (k Key) Scope() Scope             { return k.scope }
func (k Key) ProjectRoot() ProjectRoot { return k.root }
func (k Key) Cell() cell.Cell          { return k.cell }
func (k Key) IsValid() bool {
	return k.cell.IsValid() && ((k.scope == ScopeGlobal && !k.root.IsValid()) || (k.scope == ScopeProject && k.root.IsValid()))
}
func (k Key) identity() string {
	return fmt.Sprintf("%d\x00%s\x00%s", k.scope, k.root.path, k.cell.String())
}

func fault(operation, expected, why, where, when, impact, fix string, cause error) error {
	return cell.NewFault(operation, expected, why, where, when, impact, fix, cause)
}
