package gateauthority_test

// eventclass_test.go guards the event-to-action-class table and the published
// reachability column.
//
// Two populations are walked here and neither is written down: every registered
// event of every harness comes from the registration manifests, and every
// denied cell comes from the legality tables themselves.
//
// The enabled-ness cross-check imports the activation package. That import is
// possible ONLY because this is an external test package: activation reaches
// pasture's task store, which imports the package under test, so production
// code here may never read it.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func manifests() []registration.Manifest {
	return []registration.Manifest{
		registration.ClaudeCode2_1_261(),
		registration.Codex0_153_0(),
		registration.OpenCode1_18_29(),
	}
}

// ─── the mapping is total ────────────────────────────────────────────────────

// TestEveryRegisteredEventCarriesAnActionClass walks every registered event of
// every harness and requires a class for each.
//
// The production package refuses an unclassified event at process start, so the
// mutation for this guard turns the WHOLE package red before a test runs. This
// case states the same fact positively and counts what it walked, so a mapping
// that passed because nothing was walked cannot hide.
//
// RED when: a registered event has no class, or the walk sees no event at all.
func TestEveryRegisteredEventCarriesAnActionClass(t *testing.T) {
	all := manifests()
	if len(all) == 0 {
		t.Fatalf("no harness manifest was walked, so this check proves nothing")
	}

	var unclassified []string
	perHarness := map[ir.HarnessID]int{}
	total := 0
	for _, manifest := range all {
		for _, event := range manifest.Entries() {
			total++
			perHarness[manifest.Harness]++
			class, ok := gateauthority.ClassForEvent(manifest.Harness, event.NativeName)
			if !ok || !class.IsValid() {
				unclassified = append(unclassified, fmt.Sprintf("%s %s", manifest.Harness, event.NativeName))
			}
		}
	}
	if total == 0 {
		t.Fatalf("the manifests registered no event at all, so the class walk covered nothing")
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("%d registered event(s) carry no action class: %v; every registered event needs one, because the gate reads the class of the event that fired", len(unclassified), unclassified)
	}
	t.Logf("classified %d registered events: %v", total, perHarness)
}

// TestEveryV2EventCarriesAnActionClass walks the OpenCode 2.0.20 manifest and
// requires a class for each of its rows, including the fourteen coordinates
// the 1.18.29 catalogue does not share. The shared three keep the classes the
// 1.18.29 rows above assert; the new rows carry provisional classes that the
// committed capture sitting re-derives, all on never-denied actions so a
// misclassified provisional row can only proceed.
func TestEveryV2EventCarriesAnActionClass(t *testing.T) {
	manifest := registration.OpenCode2_0_20()
	var unclassified []string
	for _, event := range manifest.Entries() {
		class, ok := gateauthority.ClassForEvent(manifest.Harness, event.NativeName)
		if !ok || !class.IsValid() {
			unclassified = append(unclassified, event.NativeName)
		}
	}
	if len(manifest.Entries()) == 0 {
		t.Fatal("the 2.0.20 manifest registered no event at all, so the class walk covered nothing")
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("%d 2.0.20 event(s) carry no action class: %v", len(unclassified), unclassified)
	}
	provisional := map[string]gateauthority.ActionClass{
		"session.prompt":      gateauthority.ActionPromptSubmit,
		"session.compaction":  gateauthority.ActionCompact,
		"permission.evaluate": gateauthority.ActionPermission,
	}
	for name, want := range provisional {
		if class, ok := gateauthority.ClassForEvent(manifest.Harness, name); !ok || class != want {
			t.Errorf("2.0.20 event %q reads (%s, %t); want (%s, true)", name, class, ok, want)
		}
	}
	for _, name := range []string{
		"session.context", "session.generate", "session.title",
		"session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "shell.create.before",
	} {
		if class, ok := gateauthority.ClassForEvent(manifest.Harness, name); !ok || class != gateauthority.ActionObservation {
			t.Errorf("2.0.20 event %q reads (%s, %t); want (observation, true)", name, class, ok)
		}
	}
}

// TestAnUnregisteredEventNameHasNoClass proves the mapping refuses a name that
// did not come from a manifest, rather than answering with some class.
//
// RED when: an unknown name is given a class, or a known name loses one.
func TestAnUnregisteredEventNameHasNoClass(t *testing.T) {
	if class, ok := gateauthority.ClassForEvent(ir.HarnessClaudeCode, "NoSuchEventName"); ok || class != gateauthority.ActionUnset {
		t.Errorf("an unregistered event name reads (%s, %t); want (action-unset, false), because a name from outside the manifests must be refused, not read as some class", class, ok)
	}
	// A name registered on ONE harness is not thereby a name on another.
	if class, ok := gateauthority.ClassForEvent(ir.HarnessCodex, "WorktreeCreate"); ok || class != gateauthority.ActionUnset {
		t.Errorf("a Claude-only event read on Codex gives (%s, %t); want (action-unset, false), because the table is keyed by harness and event", class, ok)
	}
	// The control: a real pair does answer.
	if class, ok := gateauthority.ClassForEvent(ir.HarnessClaudeCode, "PreToolUse"); !ok || class != gateauthority.ActionToolUse {
		t.Errorf("Claude PreToolUse reads (%s, %t); want (tool-use, true), or the two refusals above passed because nothing answers at all", class, ok)
	}
}

// ─── the reachability column ─────────────────────────────────────────────────

// TestTheReachabilityColumnCoversExactlyTheDeniedCells proves the published
// column has one entry per denied cell and no other.
//
// The count is not written down: it is recomputed here from the tables.
//
// RED when: the column is empty, or it misses a denied cell, or it carries an
// entry for a cell that is not denied. The empty case is the one that already
// happened once: an eagerly built column read the tables before they existed
// and published nothing at all.
func TestTheReachabilityColumnCoversExactlyTheDeniedCells(t *testing.T) {
	want := map[string]bool{}
	for _, role := range gateauthority.AssignmentRoles() {
		for _, action := range gateauthority.ActionClasses() {
			if gateauthority.RoleVerdict(role, action) == gateauthority.VerdictDeny {
				want["role "+role.String()+" x "+action.String()] = true
			}
		}
	}
	for _, phase := range gateauthority.TaskPhases() {
		for _, action := range gateauthority.ActionClasses() {
			if gateauthority.PhaseVerdict(phase, action) == gateauthority.VerdictDeny {
				want["phase "+phase.String()+" x "+action.String()] = true
			}
		}
	}
	if len(want) == 0 {
		t.Fatalf("the tables deny nothing, so this check covers nothing; the policy has at least one denial")
	}

	got := map[string]bool{}
	for _, entry := range gateauthority.DeniedCellReachability() {
		got[cellKey(entry)] = true
		if !entry.Table.IsValid() {
			t.Errorf("a reachability entry carries table %s; want a real table name", entry.Table)
		}
		if len(entry.Harnesses) != len(manifests()) {
			t.Errorf("entry %s lists %d harnesses; want %d, one per registered harness, so an absent harness is stated rather than omitted", cellKey(entry), len(entry.Harnesses), len(manifests()))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the reachability column covers %d cell(s) and the tables deny %d; want one entry per denied cell, because a denied cell with no entry is a rule the user approved without being told whether anything can apply it", len(got), len(want))
	}
	for key := range want {
		if !got[key] {
			t.Errorf("denied cell %q has no reachability entry", key)
		}
	}
}

func cellKey(entry gateauthority.CellReachability) string {
	if entry.Table == gateauthority.TablePhase {
		return "phase " + entry.Phase.String() + " x " + entry.Action.String()
	}
	return "role " + entry.Role.String() + " x " + entry.Action.String()
}

// TestTheMeasuredReachabilityFactsHold pins the four facts the policy was
// approved on. They are stated per harness with their exact native events, so a
// host change that makes one of them false is visible here and not only in a
// document.
//
// RED when: a subagent cell becomes reachable, or one of the three Claude
// classes stops being reachable through the named events.
func TestTheMeasuredReachabilityFactsHold(t *testing.T) {
	byCell := map[string]gateauthority.CellReachability{}
	for _, entry := range gateauthority.DeniedCellReachability() {
		byCell[cellKey(entry)] = entry
	}

	// 1. Every subagent deny cell is UNREACHABLE and is KEPT. Claude and Codex
	// consult on subagent stop only while the session is inactive, their
	// subagent start is not blocking, and OpenCode registers no subagent event.
	subagentCells := 0
	for key, entry := range byCell {
		if entry.Action != gateauthority.ActionSubagent {
			continue
		}
		subagentCells++
		if entry.Reachable {
			t.Errorf("%s is reachable; want unreachable, because every registered subagent event is either not blocking or carries the stop-loop guard: %v", key, harnessEvents(entry))
		}
	}
	if subagentCells == 0 {
		t.Fatalf("no subagent deny cell is published; want the cells KEPT and labelled unreachable, because deleting them would remove the rule the day a host makes the event blocking")
	}

	// 2, 3 and 4. Worktree, config change and task lifecycle ARE reachable on
	// Claude, through these exact events.
	for _, want := range []struct {
		cell   string
		events []string
	}{
		{"role axis-reviewer x worktree", []string{"WorktreeCreate"}},
		{"role axis-reviewer x config-change", []string{"ConfigChange"}},
		{"role axis-reviewer x task-lifecycle", []string{"TaskCompleted", "TaskCreated"}},
	} {
		entry, ok := byCell[want.cell]
		if !ok {
			t.Errorf("cell %q is not published; want it denied and published", want.cell)
			continue
		}
		if !entry.Reachable {
			t.Errorf("cell %q reads unreachable; want reachable on Claude through %v", want.cell, want.events)
			continue
		}
		got := eventsFor(entry, ir.HarnessClaudeCode)
		if strings.Join(got, ",") != strings.Join(want.events, ",") {
			t.Errorf("cell %q is reached on Claude through %v; want exactly %v", want.cell, got, want.events)
		}
	}
}

func harnessEvents(entry gateauthority.CellReachability) map[ir.HarnessID][]string {
	out := map[ir.HarnessID][]string{}
	for _, reach := range entry.Harnesses {
		out[reach.Harness] = reach.Events
	}
	return out
}

func eventsFor(entry gateauthority.CellReachability, harness ir.HarnessID) []string {
	for _, reach := range entry.Harnesses {
		if reach.Harness == harness {
			return reach.Events
		}
	}
	return nil
}

// ─── registered against enabled ──────────────────────────────────────────────

// TestReachableCellsStateWhetherTheirEventsAreEnabledToday cross-checks the
// published REGISTERED reachability against what is ENABLED today.
//
// The two words mean different things and the report the user reads carries
// both. Registered reachability is a property of the manifests: the host has
// the event and it can block. Enabled says pasture actually asks for it today.
// A cell can be registered-reachable and not enabled; that is not a defect, it
// is work not yet done, and this case names it rather than hiding it.
//
// RED when: the enabled set cannot be read, or a reachable cell's events cannot
// be resolved at all.
func TestReachableCellsStateWhetherTheirEventsAreEnabledToday(t *testing.T) {
	enabled := enabledEventNames(t)
	if len(enabled) == 0 {
		t.Fatalf("no enabled event was resolved on any harness, so the cross-check compares against nothing")
	}

	reachable := 0
	for _, entry := range gateauthority.DeniedCellReachability() {
		if !entry.Reachable {
			continue
		}
		reachable++
		for _, reach := range entry.Harnesses {
			for _, name := range reach.Events {
				state := "not enabled today"
				if enabled[reach.Harness][name] {
					state = "enabled today"
				}
				t.Logf("%s: %s %s is registered-reachable and %s", cellKey(entry), reach.Harness, name, state)
			}
		}
	}
	if reachable == 0 {
		t.Fatalf("no denied cell is registered-reachable, so the cross-check ran over nothing")
	}
}

// enabledEventNames resolves, per harness, the native names of the events
// pasture asks for today. It joins the activation evaluator's target events to
// the registration manifest, because the evaluator speaks in event kinds and
// the reachability column speaks in native names.
func enabledEventNames(t *testing.T) map[ir.HarnessID]map[string]bool {
	t.Helper()
	evaluators := []activation.Evaluator{
		activation.ClaudeCodeEvaluator(),
		activation.CodexEvaluator(),
		activation.OpenCodeEvaluator(),
	}
	byHarness := map[ir.HarnessID]map[string]bool{}
	for i, manifest := range manifests() {
		evaluator := evaluators[i]
		if !evaluator.IsValid() {
			t.Fatalf("the activation evaluator for %s is not constructed", manifest.Harness)
		}
		names := map[model.ContractEventKind]string{}
		for _, event := range manifest.Entries() {
			names[event.Kind] = event.NativeName
		}
		byHarness[manifest.Harness] = map[string]bool{}
		for _, kind := range evaluator.TargetEvents() {
			name, ok := names[kind]
			if !ok {
				t.Fatalf("the activation evaluator for %s targets event kind %d, which its own manifest does not register", manifest.Harness, kind)
			}
			byHarness[manifest.Harness][name] = true
		}
	}
	return byHarness
}
