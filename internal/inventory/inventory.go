// Package inventory is Pasture's authoring-side capability table: one typed,
// uniqueness-validated projection that unifies the three capability axes a
// harness exposes — commands, lifecycle events, and native functions.
//
// Every Row is DERIVED at code-generation time from its already-pinned source
// and committed as a generated projection file:
//
//   - command rows        — the command-skill authoring surface
//     (internal/codegen commandSkillDirs), fanned out per enabled harness,
//     emitted into commands.gen.go by the codegen generation root;
//   - lifecycle-event rows — the pinned hostcontract lifecycle contracts,
//     emitted into lifecycle_events.gen.go by hostcontractgen in the SAME walk
//     that renders the registration manifests (so the table and registration
//     cannot drift from each other or from the contracts, by construction);
//   - native-function rows — the pinned runtime native-operation contracts,
//     emitted into native_functions.gen.go by the codegen generation root.
//
// The table is a meeting point, never a second authority. A Row may never
// override contract-derived truth: the agreement tests re-derive each axis from
// its pinned source and fail the build on any missing, extra, or contradicting
// entry.
//
// Stage-2 scope is authoring-side ONLY. No runtime/production package imports
// this package; only the generators emit rows into it (through the generated
// *.gen.go files) and tests validate it against the pinned sources.
package inventory

import (
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// Kind is the closed capability axis of a Row. It is a distinct string-typed
// enum (mirroring ir.HarnessID's own string-enum idiom in this codebase), so an
// axis value can never be confused with an arbitrary label. Disambiguation
// across axes is STRUCTURAL — carried by this typed field — never string-
// prefixed onto the Row ID.
type Kind string

const (
	// KindCommand is a command-skill capability (e.g. "cmd-work-impl").
	KindCommand Kind = "command"
	// KindLifecycleEvent is a lifecycle-event capability, identified by its
	// native event name (e.g. "PreToolUse", "tool.execute.before").
	KindLifecycleEvent Kind = "lifecycle-event"
	// KindNativeFunction is a native-function/tool capability derived from the
	// pinned runtime contract (e.g. "request-input").
	KindNativeFunction Kind = "native-function"
)

// IsValid reports whether k is one of the closed, known axes. It is the closed-
// set guard used when validating generated rows.
func (k Kind) IsValid() bool {
	switch k {
	case KindCommand, KindLifecycleEvent, KindNativeFunction:
		return true
	default:
		return false
	}
}

// Key is the unique table coordinate: which harness exposes which capability,
// on which axis. The ID keeps its existing source spelling, unmodified.
type Key struct {
	Harness ir.HarnessID
	Kind    Kind
	ID      string
}

// Row is one capability fact. Attribute payloads deliberately do NOT live here
// this stage (trimmed at UAT): consumers keep their authored attribute maps
// keyed by table IDs and validated exhaustive against the table. A Row gains
// fields only when a real consumer cutover needs them.
type Row struct {
	Key Key
}

// commandRows, lifecycleEventRows and nativeFunctionRows hold the generated
// capability projections — one slice per Kind, so each generator owns exactly
// one generated file and table assembly stays a static concatenation.
//
// They are populated by the generated projection files (commands.gen.go,
// lifecycle_events.gen.go, native_functions.gen.go) and are empty until code
// generation has run. Table() is the only reader.
var (
	commandRows        []Row
	lifecycleEventRows []Row
	nativeFunctionRows []Row
)

// Table returns the merged, deterministically-sorted, uniqueness-validated
// inventory assembled from every generated projection.
//
// Ordering is (Harness, Kind, ID). A duplicate Key is an authoring/derivation
// defect — two generated sources contributed the same coordinate — and fails
// with an actionable error naming the collided coordinate so the offending
// generator source can be de-duplicated. An out-of-range Kind fails the same
// way (a generated file drifted from the closed axis set).
func Table() ([]Row, error) {
	rows := make([]Row, 0, len(commandRows)+len(lifecycleEventRows)+len(nativeFunctionRows))
	rows = append(rows, commandRows...)
	rows = append(rows, lifecycleEventRows...)
	rows = append(rows, nativeFunctionRows...)

	sort.Slice(rows, func(i, j int) bool { return keyLess(rows[i].Key, rows[j].Key) })

	for i, r := range rows {
		if !r.Key.Kind.IsValid() {
			return nil, fmt.Errorf(
				"inventory.Table: generated row %d has out-of-range Kind %q for (harness=%q, id=%q) — "+
					"a generated projection file drifted from the closed Kind set {command, lifecycle-event, native-function}; "+
					"regenerate the inventory rows from the pinned sources (make generate)",
				i, string(r.Key.Kind), string(r.Key.Harness), r.Key.ID,
			)
		}
		if i > 0 && rows[i].Key == rows[i-1].Key {
			return nil, fmt.Errorf(
				"inventory.Table: duplicate row key (harness=%q, kind=%q, id=%q) — "+
					"two generated sources contributed the same coordinate; "+
					"de-duplicate the offending generator source (commandSkillDirs, the hostcontract lifecycle walk, or the runtime native-operation derivation) so each (harness, kind, id) is emitted exactly once",
				string(rows[i].Key.Harness), string(rows[i].Key.Kind), rows[i].Key.ID,
			)
		}
	}
	return rows, nil
}

// keyLess orders keys by (Harness, Kind, ID) for a stable, deterministic table.
func keyLess(a, b Key) bool {
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}
