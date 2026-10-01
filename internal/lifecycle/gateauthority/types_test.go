package gateauthority_test

// types_test.go is the sync guard between this package's mirror enums and the
// enums they mirror, plus the guard on the import edge that lets the mirror
// exist at all.
//
// The file is an EXTERNAL test package on purpose. It imports the task store,
// which imports this package, and only an external test package may close that
// loop. A test inside the package would make the loop a real import cycle the
// day the store starts importing this package.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// ─── zero values ─────────────────────────────────────────────────────────────

// TestEveryEnumZeroValueIsInvalid proves that no enum in this package can be
// left unset and still pass for a real value.
//
// RED when: an enum's zero value starts reporting itself valid, or an arm list
// comes back empty, which would make every derived check below vacuous.
func TestEveryEnumZeroValueIsInvalid(t *testing.T) {
	if gateauthority.ActionUnset.IsValid() {
		t.Errorf("ActionUnset reports itself valid; want invalid, because a class nobody set must not pass for a real action")
	}
	if gateauthority.TaskPhaseUnset.IsValid() {
		t.Errorf("TaskPhaseUnset reports itself valid; want invalid, because a phase nobody set must not pass for a real phase")
	}
	if gateauthority.RoleUnset.IsValid() {
		t.Errorf("RoleUnset reports itself valid; want invalid, because a role nobody set must not pass for a real slot")
	}

	// NON-VACUITY: each derived arm list must hold arms, and every arm in it
	// must be valid and carry a token of its own.
	actions := gateauthority.ActionClasses()
	phases := gateauthority.TaskPhases()
	roles := gateauthority.AssignmentRoles()
	if len(actions) == 0 || len(phases) == 0 || len(roles) == 0 {
		t.Fatalf("derived arm lists are %d action classes, %d phases, %d roles; want each non-empty, or every check derived from them proves nothing", len(actions), len(phases), len(roles))
	}
	for _, arm := range actions {
		if !arm.IsValid() || strings.HasPrefix(arm.String(), "ActionClass(") {
			t.Errorf("derived action class %d renders %q and reports valid=%t; want a real token and valid, because the list is derived from the sentinel and must hold only real arms", uint8(arm), arm.String(), arm.IsValid())
		}
	}
	for _, arm := range phases {
		if !arm.IsValid() || strings.HasPrefix(arm.String(), "TaskPhase(") {
			t.Errorf("derived phase %d renders %q and reports valid=%t; want a real token and valid", uint8(arm), arm.String(), arm.IsValid())
		}
	}
	for _, arm := range roles {
		if !arm.IsValid() || strings.HasPrefix(arm.String(), "AssignmentRole(") {
			t.Errorf("derived role %d renders %q and reports valid=%t; want a real token and valid", uint8(arm), arm.String(), arm.IsValid())
		}
	}
}

// ─── PhaseFromProvenance ─────────────────────────────────────────────────────

// TestPhaseFromProvenanceMapsEveryArmAndRefusesOutOfRange proves the phase
// mapping is total over the task store's own valid range and refuses anything
// outside it.
//
// RED when: a store phase has no gate phase, two store phases collapse onto one
// gate phase, or a value outside the store's range is mapped instead of
// refused.
func TestPhaseFromProvenanceMapsEveryArmAndRefusesOutOfRange(t *testing.T) {
	arms := provenanceePhaseArms(t)

	seen := make(map[gateauthority.TaskPhase]provenance.Phase, len(arms))
	for _, arm := range arms {
		mapped, ok := gateauthority.PhaseFromProvenance(arm)
		if !ok {
			t.Errorf("PhaseFromProvenance(%s) refused a phase the task store calls valid; want a mapping, because every stored phase must be readable by the gate", arm)
			continue
		}
		if !mapped.IsValid() {
			t.Errorf("PhaseFromProvenance(%s) mapped to %s, which reports itself invalid; want a real arm", arm, mapped)
			continue
		}
		if first, taken := seen[mapped]; taken {
			t.Errorf("PhaseFromProvenance maps both %s and %s to %s; want one gate phase per stored phase", first, arm, mapped)
			continue
		}
		seen[mapped] = arm
		if mapped.String() != arm.String() {
			t.Errorf("PhaseFromProvenance(%s) mapped to a phase that renders %q; want the same token %q, because the two vocabularies are one vocabulary spelled twice", arm, mapped.String(), arm.String())
		}
	}

	// OUT OF RANGE, both directions.
	if mapped, ok := gateauthority.PhaseFromProvenance(provenance.Phase(-1)); ok || mapped != gateauthority.TaskPhaseUnset {
		t.Errorf("PhaseFromProvenance(-1) = (%s, %t); want (phase-unset, false), because a negative phase is outside the store's range", mapped, ok)
	}
	above := provenance.Phase(len(arms))
	if mapped, ok := gateauthority.PhaseFromProvenance(above); ok || mapped != gateauthority.TaskPhaseUnset {
		t.Errorf("PhaseFromProvenance(%d) = (%s, %t); want (phase-unset, false), because a phase one above the store's top arm was written by a newer build and must be refused, not renamed", int(above), mapped, ok)
	}
}

// provenanceePhaseArms derives every valid task-store phase from the store's own
// validity predicate, walking up from zero until the predicate refuses. The
// population is therefore the store's, not a list written here.
func provenanceePhaseArms(t *testing.T) []provenance.Phase {
	t.Helper()
	// The ceiling only stops a runaway walk; it is not the arm count.
	const walkCeiling = 1024
	var arms []provenance.Phase
	for i := 0; i < walkCeiling; i++ {
		arm := provenance.Phase(i)
		if !arm.IsValid() {
			break
		}
		arms = append(arms, arm)
	}
	if len(arms) < 2 {
		t.Fatalf("derived %d task-store phases; want at least 2, or this check proves nothing about a vocabulary that has many", len(arms))
	}
	if len(arms) >= walkCeiling {
		t.Fatalf("the phase walk hit its runaway ceiling of %d; the store's validity predicate no longer bounds the enum", walkCeiling)
	}
	return arms
}

// ─── mirror sync ─────────────────────────────────────────────────────────────

// TestAssignmentRoleMirrorsTheTaskStoreRoles proves every role the task store
// can write into an assignment-start event is readable by the gate.
//
// The source arms are PARSED FROM THE STORE'S OWN SOURCE, not listed here, so
// an arm added there is covered with no edit to this file. The bridge under
// test is the canonical payload token, which is what the store actually writes.
//
// RED when: a role arm is added to the store and the gate cannot read its
// token.
func TestAssignmentRoleMirrorsTheTaskStoreRoles(t *testing.T) {
	arms := taskStoreRoleArms(t)
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[tasks.AssignmentRole]{
		Subject: "task-store AssignmentRole -> gateauthority.RoleFromToken",
		Arms:    arms,
		Mirror: func(arm tasks.AssignmentRole) (string, bool) {
			role, ok := gateauthority.RoleFromToken(arm.String())
			if !ok {
				return "", false
			}
			return role.String(), true
		},
		Describe: func(arm tasks.AssignmentRole) string {
			return arm.String()
		},
	})
}

// TestTaskPhaseMirrorsTheTaskStorePhases proves every stored phase has a gate
// phase, through the same helper, with the population derived from the store's
// own validity predicate.
//
// RED when: a phase arm is added to the store and the gate has no arm for it.
func TestTaskPhaseMirrorsTheTaskStorePhases(t *testing.T) {
	testutil.RequireEnumMirrorComplete(t, testutil.EnumMirror[provenance.Phase]{
		Subject: "task-store Phase -> gateauthority.PhaseFromProvenance",
		Arms:    provenanceePhaseArms(t),
		Mirror: func(arm provenance.Phase) (string, bool) {
			mapped, ok := gateauthority.PhaseFromProvenance(arm)
			if !ok {
				return "", false
			}
			return mapped.String(), true
		},
		Describe: func(arm provenance.Phase) string { return arm.String() },
	})
}

// taskStoreRoleArms parses the task store's own source and returns every
// AssignmentRole arm above the invalid zero, in ordinal order. Parsing rather
// than listing is what makes the mirror catch an arm added after this file was
// written, including one added with no rendering of its own.
func taskStoreRoleArms(t *testing.T) []tasks.AssignmentRole {
	t.Helper()
	const roleTypeName = "AssignmentRole"
	dir := filepath.Join("..", "..", "tasks")
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, dir, func(entry fs.FileInfo) bool {
		return !strings.HasSuffix(entry.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the task store source at %s: %v", dir, err)
	}

	var names []string
	filesVisited := 0
	for _, pkg := range packages {
		for range pkg.Files {
			filesVisited++
		}
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				general, ok := decl.(*ast.GenDecl)
				if !ok || general.Tok != token.CONST {
					continue
				}
				armType := ""
				for _, spec := range general.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if ident, ok := value.Type.(*ast.Ident); ok {
						armType = ident.Name
					}
					if armType != roleTypeName {
						continue
					}
					for _, name := range value.Names {
						names = append(names, name.Name)
					}
				}
			}
		}
	}

	if filesVisited == 0 {
		t.Fatalf("parsed %s and visited no source file; the derivation read nothing, so any result it produced is vacuous", dir)
	}
	if len(names) < 2 {
		t.Fatalf("parsed %s and found %d %s arm(s) (%v); want at least 2, because the store declares an invalid zero plus real arms, and a shorter list means the parse missed the declaration", dir, len(names), roleTypeName, names)
	}

	// The first declared arm is the invalid zero; it mirrors nothing.
	arms := make([]tasks.AssignmentRole, 0, len(names)-1)
	for ordinal := 1; ordinal < len(names); ordinal++ {
		arms = append(arms, tasks.AssignmentRole(ordinal))
	}
	return arms
}

// ─── import direction ────────────────────────────────────────────────────────

// TestGateAuthorityDoesNotImportTheTaskStore proves the edge points one way.
// This package sits at the bottom of the import graph; the task store imports
// it and implements its Reader. An import the other way would be an import
// cycle the moment the store does so.
//
// The check PARSES this package's own production files, so it covers a file
// added later with no edit here.
//
// RED when: a production file of this package imports the task store.
func TestGateAuthorityDoesNotImportTheTaskStore(t *testing.T) {
	const forbidden = "github.com/dayvidpham/pasture/internal/tasks"
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(entry fs.FileInfo) bool {
		return !strings.HasSuffix(entry.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse this package's own source: %v", err)
	}

	filesVisited := 0
	var imports []string
	var offenders []string
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			filesVisited++
			for _, spec := range file.Imports {
				path := strings.Trim(spec.Path.Value, `"`)
				imports = append(imports, path)
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					offenders = append(offenders, filepath.Base(name)+" imports "+path)
				}
			}
		}
	}

	// NON-VACUITY: a parse that read no file, or a package with no import at
	// all, would pass this check while proving nothing.
	if filesVisited == 0 {
		t.Fatalf("parsed this package and visited no production file; the check read nothing")
	}
	if len(imports) == 0 {
		t.Fatalf("parsed %d production file(s) and found no import at all; the check read nothing about the import edge", filesVisited)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("this package imports the task store: %v; want no such import, because the store imports this package and the edge must point one way only", offenders)
	}
}

// ─── contract boundary ───────────────────────────────────────────────────────

// TestActorAuthorityHasNoEpisodeLimitField pins the read-side authority value
// to the complete episode list. The compiler also covers every composite literal
// in the tree; this reflection check makes the removed field's absence an
// explicit contract assertion.
//
// RED when: a partial-episode marker is added back to ActorAuthority.
func TestActorAuthorityHasNoEpisodeLimitField(t *testing.T) {
	if _, ok := reflect.TypeOf(gateauthority.ActorAuthority{}).FieldByName("Trunc" + "ated"); ok {
		t.Fatal("ActorAuthority exposes the retired partial-list marker; want the complete episode list without one")
	}
}
